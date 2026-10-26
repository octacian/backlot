// The application fixture consumes only explicitly mapped application values.
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/lib/pq"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/go-sql-driver/mysql"
)

func main() {
	if len(os.Args) == 2 && os.Args[1] == "check-http" {
		if err := checkHTTP(os.Getenv("DB_CHECK_PORT")); err != nil {
			os.Exit(1)
		}
		return
	}
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

func checkHTTP(port string) error {
	client := &http.Client{Timeout: time.Second}
	response, err := client.Get("http://" + net.JoinHostPort("127.0.0.1", port))
	if err != nil {
		return err
	}
	defer func() { _ = response.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(response.Body, 8))
	if err != nil || response.StatusCode != http.StatusOK || string(body) != "updated" {
		return errors.New("application HTTP/SQL readiness failed")
	}
	return nil
}

func run() error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if os.Getenv("DB_MANAGER") == "postgresql" {
		return runPostgreSQL(ctx)
	}
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
	db, err := applicationDB()
	if err != nil {
		return err
	}
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
			}{os.Getenv("DB_DATABASE"), alive})
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

func applicationDB() (*sql.DB, error) {
	if os.Getenv("DB_MANAGER") == "postgresql" {
		port, err := strconv.Atoi(os.Getenv("DB_PORT"))
		if err != nil {
			return nil, err
		}
		c, err := pq.NewConnectorConfig(pq.Config{Host: os.Getenv("DB_HOST"), Port: uint16(port), Database: os.Getenv("DB_DATABASE"), User: os.Getenv("DB_USERNAME"), Password: os.Getenv("DB_PASSWORD"), SSLMode: pq.SSLModeDisable, ConnectTimeout: time.Second, TargetSessionAttrs: pq.TargetSessionAttrsAny})
		if err != nil {
			return nil, err
		}
		return sql.OpenDB(c), nil
	}
	config := mysql.NewConfig()
	config.User, config.Passwd, config.DBName = os.Getenv("DB_USERNAME"), os.Getenv("DB_PASSWORD"), os.Getenv("DB_DATABASE")
	config.Net, config.Addr = "tcp", net.JoinHostPort(os.Getenv("DB_HOST"), os.Getenv("DB_PORT"))
	config.Timeout, config.ReadTimeout, config.WriteTimeout = time.Second, time.Second, time.Second
	c, err := mysql.NewConnector(config)
	if err != nil {
		return nil, err
	}
	return sql.OpenDB(c), nil
}
func runPostgreSQL(ctx context.Context) error {
	port, err := strconv.Atoi(os.Getenv("DB_PORT"))
	if err != nil {
		return err
	}
	for _, user := range []string{os.Getenv("DB_USERNAME"), "postgres"} {
		database := os.Getenv("DB_DATABASE")
		if user == "postgres" {
			database = "postgres"
		}
		wrong, err := pq.NewConnectorConfig(pq.Config{Host: os.Getenv("DB_HOST"), Port: uint16(port), Database: database, User: user, Password: "wrong-fixture-password", SSLMode: pq.SSLModeDisable, ConnectTimeout: time.Second})
		if err != nil {
			return err
		}
		bad := sql.OpenDB(wrong)
		err = bad.PingContext(ctx)
		_ = bad.Close()
		var denied *pq.Error
		if !errors.As(err, &denied) || denied.Code != "28P01" {
			return errors.New("wrong application or administration password was not rejected")
		}
	}
	db, err := applicationDB()
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	var exists bool
	if err = db.QueryRowContext(ctx, `SELECT EXISTS(SELECT FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname NOT IN ('pg_catalog','information_schema') AND n.nspname NOT LIKE 'pg\_%' ESCAPE '\')`).Scan(&exists); err != nil {
		return err
	}
	if exists && os.Getenv("DB_PERSIST") != "1" {
		return errors.New("allocation not empty")
	}
	if !exists {
		for _, query := range []string{"CREATE SCHEMA application", "CREATE TABLE application.schema_witness(value int)", "CREATE EXTENSION hstore", "CREATE TABLE witness (id INT PRIMARY KEY,value VARCHAR(40))", "INSERT INTO witness VALUES (1,'application-data')", "UPDATE witness SET value='updated' WHERE id=1", "ALTER TABLE witness ADD COLUMN extra INT", "CREATE INDEX by_value ON witness(value)", "CREATE VIEW app_view AS SELECT value FROM witness", "CREATE TEMPORARY TABLE temp_fixture(id INT)"} {
			if _, err = db.ExecContext(ctx, query); err != nil {
				return err
			}
		}
	}
	for _, query := range []string{"SELECT * FROM pg_authid", "CREATE ROLE forbidden_fixture", "CREATE DATABASE forbidden_fixture", "SET ROLE postgres", "ALTER DATABASE " + pq.QuoteIdentifier(os.Getenv("DB_DATABASE")) + " OWNER TO " + pq.QuoteIdentifier(os.Getenv("DB_USERNAME"))} {
		_, err = db.ExecContext(ctx, query)
		var denied *pq.Error
		if !errors.As(err, &denied) || denied.Code != "42501" {
			return errors.New("PostgreSQL administration was not denied")
		}
	}
	var value string
	if err = db.QueryRowContext(ctx, "SELECT value FROM app_view WHERE value='updated'").Scan(&value); err != nil || value != "updated" {
		return errors.New("application read failed")
	}
	return nil
}
