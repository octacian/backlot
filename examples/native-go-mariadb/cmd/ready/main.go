// Readiness runs natively with the service's resolved application environment.
package main

import (
	"context"
	"database/sql"
	"net"
	"os"
	"time"

	"github.com/go-sql-driver/mysql"
)

func main() {
	if !ready() {
		// Driver errors can contain connection details; readiness needs only an exit.
		os.Exit(1)
	}
}

func ready() bool {
	config := mysql.NewConfig()
	config.Net = "tcp"
	config.Addr = net.JoinHostPort(os.Getenv("DB_HOST"), os.Getenv("DB_PORT"))
	config.User, config.Passwd = os.Getenv("DB_USERNAME"), os.Getenv("DB_PASSWORD")
	config.DBName = os.Getenv("DB_DATABASE")
	config.Timeout = time.Second
	if config.User == "" || config.Passwd == "" || config.DBName == "" {
		return false
	}
	connector, err := mysql.NewConnector(config)
	if err != nil {
		return false
	}
	db := sql.OpenDB(connector)
	defer func() { _ = db.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var database string
	var tables int
	err = db.QueryRowContext(ctx, "SELECT DATABASE(), COUNT(*) FROM information_schema.TABLES WHERE TABLE_SCHEMA = DATABASE()").Scan(&database, &tables)
	return err == nil && database == config.DBName
}
