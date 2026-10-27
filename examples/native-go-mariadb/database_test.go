package mariadb

import (
	"context"
	"database/sql"
	"encoding/json"
	"net"
	"os"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"
)

func TestMain(m *testing.M) {
	if os.Getenv("DB_PORT") == "" {
		os.Exit(m.Run())
	}
	// The output group is held through collection; discard earlier run reports.
	if err := os.RemoveAll("reports"); err != nil {
		os.Exit(1)
	}
	if err := os.MkdirAll("reports", 0700); err != nil {
		os.Exit(1)
	}
	code := m.Run()
	report, err := json.Marshal(struct {
		ExitCode int `json:"exit_code"`
	}{code})
	if err != nil || os.WriteFile("reports/result.json", report, 0600) != nil {
		os.Exit(1)
	}
	os.Exit(code)
}

func TestDatabase(t *testing.T) {
	if os.Getenv("DB_PORT") == "" {
		t.Skip("run through the disposable Backlot test scene")
	}
	config := mysql.NewConfig()
	config.Net = "tcp"
	config.Addr = net.JoinHostPort(os.Getenv("DB_HOST"), os.Getenv("DB_PORT"))
	config.User, config.Passwd = os.Getenv("DB_USERNAME"), os.Getenv("DB_PASSWORD")
	config.DBName = os.Getenv("DB_DATABASE")
	config.Timeout = time.Second
	connector, err := mysql.NewConnector(config)
	if err != nil {
		t.Fatal("invalid application database configuration")
	}
	db := sql.OpenDB(connector)
	t.Cleanup(func() { _ = db.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for _, query := range []string{
		"CREATE TABLE notes (id INT PRIMARY KEY, body VARCHAR(40))",
		"INSERT INTO notes VALUES (1, 'hello')",
	} {
		if _, err := db.ExecContext(ctx, query); err != nil {
			t.Fatal("application database write failed")
		}
	}
	var body string
	if err := db.QueryRowContext(ctx, "SELECT body FROM notes WHERE id = 1").Scan(&body); err != nil || body != "hello" {
		t.Fatal("application database read failed")
	}
}
