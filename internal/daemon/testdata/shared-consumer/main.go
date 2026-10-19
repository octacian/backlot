// The application fixture consumes only explicitly mapped application values.
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/go-sql-driver/mysql"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "shared consumer check failed")
		os.Exit(1)
	}
	fmt.Println("application DDL/DML and admin denial verified")
	if path := os.Getenv("DB_STARTED"); path != "" {
		if err := os.WriteFile(path, []byte(os.Getenv("DB_DATABASE")), 0600); err != nil {
			os.Exit(1)
		}
	}
	if os.Getenv("DB_ACTION") == "fail" {
		os.Exit(17)
	}
	if os.Getenv("DB_ACTION") == "wait" {
		for {
			if _, err := os.Stat(os.Getenv("DB_GATE")); err == nil {
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
	if port := os.Getenv("DB_LISTEN"); port != "" {
		if err := serve(port); err != nil {
			fmt.Fprintln(os.Stderr, "shared live consumer failed")
			os.Exit(1)
		}
	}
}
func run() error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	config := mysql.NewConfig()
	config.User = os.Getenv("DB_USERNAME")
	config.Passwd = os.Getenv("DB_PASSWORD")
	config.DBName = os.Getenv("DB_DATABASE")
	config.Net = "tcp"
	config.Addr = net.JoinHostPort(os.Getenv("DB_HOST"), os.Getenv("DB_PORT"))
	config.Timeout = 3 * time.Second
	connector, err := mysql.NewConnector(config)
	if err != nil {
		return err
	}
	db := sql.OpenDB(connector)
	defer func() { _ = db.Close() }()
	var tables int
	if err = db.QueryRowContext(ctx, "SELECT COUNT(*) FROM information_schema.TABLES WHERE TABLE_SCHEMA = DATABASE()").Scan(&tables); err != nil {
		return err
	}
	if tables != 0 && os.Getenv("DB_PERSIST") != "1" {
		return fmt.Errorf("allocation not empty")
	}
	if tables == 0 {
		for _, query := range []string{"CREATE TABLE witness (id INT PRIMARY KEY, value VARCHAR(40))", "INSERT INTO witness VALUES (1,'application-data')", "UPDATE witness SET value='updated' WHERE id=1", "ALTER TABLE witness ADD COLUMN extra INT", "CREATE INDEX by_value ON witness(value)", "CREATE VIEW app_view AS SELECT value FROM witness", "CREATE TEMPORARY TABLE temp_fixture (id INT)"} {
			if _, err = db.ExecContext(ctx, query); err != nil {
				return err
			}
		}
	}
	var value string
	if err = db.QueryRowContext(ctx, "SELECT value FROM app_view WHERE value='updated'").Scan(&value); err != nil || value != "updated" {
		return fmt.Errorf("application read failed")
	}
	for index, query := range []string{"SELECT * FROM mysql.user", "CREATE USER 'forbidden_fixture'@'%' IDENTIFIED BY 'forbidden'", "GRANT ALL PRIVILEGES ON *.* TO '" + config.User + "'@'%'", "SET GLOBAL max_connections = 200"} {
		_, err = db.ExecContext(ctx, query)
		var driver *mysql.MySQLError
		if !errors.As(err, &driver) || (driver.Number != 1044 && driver.Number != 1045 && driver.Number != 1142 && driver.Number != 1227) {
			if driver != nil {
				fmt.Printf("administration check %d returned SQL code %d\n", index, driver.Number)
			}
			return fmt.Errorf("administration did not prove privilege denial")
		}
	}
	if err = db.QueryRowContext(ctx, "SELECT value FROM witness WHERE id=1").Scan(&value); err != nil || value != "updated" {
		return fmt.Errorf("same-user positive control failed after admin denials")
	}
	return nil
}

func serve(port string) error {
	config := mysql.NewConfig()
	config.User, config.Passwd, config.DBName = os.Getenv("DB_USERNAME"), os.Getenv("DB_PASSWORD"), os.Getenv("DB_DATABASE")
	config.Net, config.Addr = "tcp", net.JoinHostPort(os.Getenv("DB_HOST"), os.Getenv("DB_PORT"))
	config.Timeout, config.ReadTimeout, config.WriteTimeout = time.Second, time.Second, time.Second
	connector, err := mysql.NewConnector(config)
	if err != nil {
		return err
	}
	db := sql.OpenDB(connector)
	defer func() { _ = db.Close() }()
	listener, err := net.Listen("tcp", net.JoinHostPort("0.0.0.0", port))
	if err != nil {
		return err
	}
	defer func() { _ = listener.Close() }()
	server := &http.Server{ReadHeaderTimeout: time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), time.Second)
		defer cancel()
		var value string
		if err := db.QueryRowContext(ctx, "SELECT value FROM witness WHERE id=1").Scan(&value); err != nil {
			http.Error(w, "database unavailable", 503)
			return
		}
		_, _ = w.Write([]byte(value))
	})}
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGTERM, os.Interrupt)
	defer signal.Stop(signals)
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	select {
	case err = <-done:
		return err
	case <-signals:
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		var value string
		alive := db.QueryRowContext(ctx, "SELECT value FROM witness WHERE id=1").Scan(&value) == nil && value == "updated"
		if path := os.Getenv("DB_STOP_FILE"); path != "" {
			out, openErr := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0600)
			if openErr != nil {
				return openErr
			}
			proof, _ := json.Marshal(struct {
				Database string
				Alive    bool
			}{config.DBName, alive})
			_, writeErr := out.Write(append(proof, '\n'))
			closeErr := out.Close()
			if writeErr != nil {
				return writeErr
			}
			if closeErr != nil {
				return closeErr
			}
		}
		if !alive {
			return errors.New("consumer stopped after database became unavailable")
		}
		if err = server.Shutdown(ctx); err != nil {
			return err
		}
		err = <-done
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}
