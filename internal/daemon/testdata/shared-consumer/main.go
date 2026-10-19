// The application fixture consumes only explicitly mapped application values.
package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"os"
	"time"

	"github.com/go-sql-driver/mysql"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "shared consumer check failed")
		os.Exit(1)
	}
	fmt.Println("empty allocation, application DDL/DML and admin denial verified")
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
	if tables != 0 {
		return fmt.Errorf("allocation not empty")
	}
	for _, query := range []string{"CREATE TABLE witness (id INT PRIMARY KEY, value VARCHAR(40))", "INSERT INTO witness VALUES (1,'application-data')", "UPDATE witness SET value='updated' WHERE id=1", "ALTER TABLE witness ADD COLUMN extra INT", "CREATE INDEX by_value ON witness(value)", "CREATE VIEW app_view AS SELECT value FROM witness", "CREATE TEMPORARY TABLE temp_fixture (id INT)"} {
		if _, err = db.ExecContext(ctx, query); err != nil {
			return err
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
