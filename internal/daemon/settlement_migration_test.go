//go:build darwin || linux

package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	v1 "github.com/octacian/backlot/api/v1"
	bolt "go.etcd.io/bbolt"
)

func TestSettlementMigrationFifteenPreservesUnknownProof(t *testing.T) {
	dir := copyStateFixture(t, "testdata/state/v15/state.db")
	db, err := bolt.Open(filepath.Join(dir, "state.db"), 0600, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	before := stateContents(t, &store{db: db})
	reached := false
	injected := errors.New("after version 16 source validation")
	steps := append([]stateMigration{}, stateMigrations...)
	last := steps[len(steps)-1].apply
	steps[len(steps)-1].apply = func(tx *bolt.Tx) error {
		if err := last(tx); err != nil {
			return err
		}
		reached = true
		return injected
	}
	if err := db.Update(func(tx *bolt.Tx) error { return openStateVersion(tx, 16, steps) }); err == nil {
		t.Fatal("injected migration accepted")
	} else {
		var detail *v1.PlanError
		if !reached || !errors.As(err, &detail) || detail.Code != "state_corrupt" {
			t.Fatal("migration failure not returned", err)
		}
	}
	if !reflect.DeepEqual(before, stateContents(t, &store{db: db})) {
		t.Fatal("failed migration changed source")
	}
	if err := db.Update(openStateTransaction); err != nil {
		t.Fatal(err)
	}
	before["meta"]["version"] = "16"
	if !reflect.DeepEqual(before, stateContents(t, &store{db: db})) {
		t.Fatal("semantic fence backfilled proof or rewrote history")
	}
	if err := db.View(func(tx *bolt.Tx) error {
		return tx.Bucket([]byte("runtime")).ForEach(func(key, raw []byte) error {
			j, err := decodeRuntime(raw)
			if err != nil {
				return err
			}
			if j.Release != nil {
				t.Fatal("upgrade invented exact release")
			}
			if len(j.Groups) == 0 {
				return nil
			}
			r, err := load(tx, string(key))
			if err != nil {
				return err
			}
			result, err := consumerEligibility(tx, r, j.Groups[0].Boot)
			if err != nil {
				return err
			}
			if result.State != v1.ConsumerUnknown || result.ObservedAt != "" {
				t.Fatal("same-boot component history fabricated exact proof", result)
			}
			return nil
		})
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := openStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := reopened.db.Close(); err != nil {
			t.Error(err)
		}
	}()
	if !reflect.DeepEqual(before, stateContents(t, reopened)) {
		t.Fatal("reopening changed migrated source")
	}
}

func TestSettlementMigrationRejectsFutureSourceUnchanged(t *testing.T) {
	for _, mode := range []string{"release", "transition", "current", "attempt", "abandonment", "managed-abandonment"} {
		t.Run(mode, func(t *testing.T) {
			dir := copyStateFixture(t, "testdata/state/v15/state.db")
			path := filepath.Join(dir, "state.db")
			db, err := bolt.Open(path, 0600, nil)
			if err != nil {
				t.Fatal(err)
			}
			if err := db.Update(func(tx *bolt.Tx) error {
				bucket, field := "runtime", "consumer_release"
				switch mode {
				case "transition":
					bucket, field = "shared_services", "last_transition"
				case "current", "abandonment":
					bucket = "instances"
				case "attempt":
					bucket = "attempts"
				case "managed-abandonment":
					bucket = "meta"
				}
				b := tx.Bucket([]byte(bucket))
				key, raw := b.Cursor().First()
				if mode == "managed-abandonment" {
					key = []byte("managed_gateway")
					raw = []byte(`{}`)
				}
				if key == nil {
					key = []byte(newID())
					raw = []byte(`{}`)
				}
				var fields map[string]json.RawMessage
				if err := json.Unmarshal(raw, &fields); err != nil {
					return err
				}
				switch mode {
				case "current":
					var instance map[string]json.RawMessage
					if err := json.Unmarshal(fields["instance"], &instance); err != nil {
						return err
					}
					instance["execution"] = json.RawMessage(`{"settlement":null}`)
					data, err := json.Marshal(instance)
					if err != nil {
						return err
					}
					fields["instance"] = data
				case "attempt":
					fields["result"] = json.RawMessage(`{"settlement":null}`)
				case "abandonment", "managed-abandonment":
					fields["native_abandonments"] = json.RawMessage(`[{"original_result":{"settlement":null}}]`)
				default:
					fields[field] = json.RawMessage(`null`)
				}
				return put(tx, bucket, string(key), fields)
			}); err != nil {
				t.Fatal(err)
			}
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if s, err := openStore(dir); err == nil {
				_ = s.db.Close()
				t.Fatal("future evidence accepted as version 15")
			} else if !strings.Contains(err.Error(), "version 15 source contains version 16 consumer evidence") {
				t.Fatal("source field was not independently rejected", err)
			}
			after, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(before, after) {
				t.Fatal("rejected source changed", err)
			}
		})
	}
}

func TestSettlementMigrationOldBinaryRefusal(t *testing.T) {
	binary := os.Getenv("BACKLOT_V15_PREDECESSOR")
	if binary == "" {
		t.Skip("set BACKLOT_V15_PREDECESSOR to exercise the actual v15 binary")
	}
	dir := shortTemp(t)
	data, err := os.ReadFile("testdata/state/v15/state.db")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "state.db"), data, 0600); err != nil {
		t.Fatal(err)
	}
	s, err := openStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.db.Close(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "state.db")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, binary, "daemon", "serve", "--state-dir", dir, "--json").Output()
	if err == nil || ctx.Err() != nil {
		t.Fatal("old binary did not promptly refuse new state", err)
	}
	var response v1.ErrorResponse
	if json.Unmarshal(output, &response) != nil || response.Error.Code != "state_version" {
		t.Fatal("old binary returned wrong refusal", string(output))
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("old binary changed version 16 database", err)
	}
	if _, err := os.Lstat(SocketPath(dir)); !os.IsNotExist(err) {
		t.Fatal("old binary opened daemon socket", err)
	}
}
