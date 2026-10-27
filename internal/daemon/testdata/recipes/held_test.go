package mariadb

import (
	"encoding/json"
	"os"
	"testing"
	"time"
)

func TestHeld(t *testing.T) {
	TestDatabase(t)
	data, err := json.Marshal(map[string]any{"instance": os.Getenv("RUN_ID"), "pid": os.Getpid()})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("reports/holding.tmp", data, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename("reports/holding.tmp", "reports/holding.json"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Minute)
}
