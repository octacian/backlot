package manager

import (
	"context"
	v1 "github.com/octacian/backlot/api/v1"
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestPostgreSQLExplicitConfig(t *testing.T) {
	if os.Getenv("BACKLOT_PG_CONFIG_CHILD") == "1" {
		db, err := PostgreSQLDB("127.0.0.1", 1, "postgres", "postgres", "fixture-only")
		if os.Getenv("PQGO_DEBUG") == "1" {
			if err == nil {
				t.Fatal("protocol debugging accepted")
			}
			return
		}
		if err != nil {
			t.Fatal("ambient PG settings affected construction", err)
		}
		_ = db.Close()
		return
	}
	for _, debug := range []string{"0", "1"} {
		cmd := exec.Command(os.Args[0], "-test.run=^TestPostgreSQLExplicitConfig$")
		cmd.Env = append(os.Environ(), "BACKLOT_PG_CONFIG_CHILD=1", "PQGO_DEBUG="+debug, "PGSERVICE=hostile", "PGSERVICEFILE=/nonexistent/service", "PGPASSFILE=/nonexistent/password", "PGPASSWORD=hostile", "PGHOST=hostile,hostile", "PGPORT=invalid", "PGSSLMODE=invalid", "PGSSLROOTCERT=/nonexistent/tls", "PGOPTIONS=-c search_path=hostile", "PGTARGETSESSIONATTRS=invalid", "PGCONNECT_TIMEOUT=invalid", "PGCLIENTENCODING=invalid")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("explicit config/debug fence: %v %s", err, out)
		}
	}
	for _, port := range []int{-1, 0, 65536} {
		if _, err := PostgreSQLDB("127.0.0.1", port, "postgres", "postgres", "secret"); err == nil {
			t.Fatal("invalid port accepted")
		}
	}
	if _, err := PostgreSQLDB("host,peer", 5432, "postgres", "postgres", "secret"); err == nil {
		t.Fatal("alternate host accepted")
	}
}

func TestPostgreSQLRequestBoundary(t *testing.T) {
	r := v1.ManagerRequest{Version: v1.ManagerProtocolVersion, Operation: "allocate", AllocationID: strings.Repeat("a", 64), Ownership: strings.Repeat("b", 64), Database: "bl" + strings.Repeat("c", 32), Username: "bl" + strings.Repeat("d", 32), Password: strings.Repeat("e", 64)}
	r.Database = r.Username
	result, err := PostgreSQL(context.Background(), nil, "127.0.0.1", 5432, "secret", r, nil)
	if err == nil || result.Ownership != "uncertain" {
		t.Fatal("colliding roles accepted")
	}
	if postgresFingerprint("app", r.Ownership) == postgresFingerprint("owner", r.Ownership) || strings.Contains(postgresFingerprint("app", r.Ownership), r.Ownership) {
		t.Fatal("fingerprints lack separation")
	}
}
