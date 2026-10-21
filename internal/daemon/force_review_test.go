package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	v1 "github.com/octacian/backlot/api/v1"
	"github.com/octacian/backlot/internal/manifest"
	bolt "go.etcd.io/bbolt"
)

func storedBytes(t *testing.T, s *store, bucket, key string) []byte {
	t.Helper()
	var data []byte
	if err := s.db.View(func(tx *bolt.Tx) error { data = bytes.Clone(tx.Bucket([]byte(bucket)).Get([]byte(key))); return nil }); err != nil {
		t.Fatal(err)
	}
	return data
}

func TestForceHTTPRejectsReplacedClaimBeforeCleanup(t *testing.T) {
	for _, managed := range []bool{false, true} {
		name := "scene"
		if managed {
			name = "managed"
		}
		t.Run(name, func(t *testing.T) {
			s, instance, _ := exportFixture(t)
			path := "/v1/runtime/stop"
			bucket, key := "instances", instance.ID
			var payload any = v1.StopRequest{APIVersion: v1.Version, InstanceID: instance.ID, Force: true, ExpectedAttempt: instance.Execution.Attempt}
			if managed {
				// A pending legacy claim also needs a stable public revision before any save.
				a := managedRecord{Scope: newID(), Config: manifest.CaddyDefaults(v1.CaddyConfig{Mode: "managed", Executable: "/explicit/caddy"}), Pending: true, Status: "uncertain"}
				if err := s.store.saveManaged(a); err != nil {
					t.Fatal(err)
				}
				selected, err := s.store.managed()
				if err != nil {
					t.Fatal(err)
				}
				again, err := s.store.managed()
				if err != nil || again.ClaimRevision != selected.ClaimRevision {
					t.Fatal("legacy revision unstable", err)
				}
				payload = v1.GatewayRequest{APIVersion: v1.Version, Force: true, ExpectedClaimRevision: selected.ClaimRevision}
				path, bucket, key = "/v1/gateway/stop", "meta", "managed_gateway"
				// Replacement B has authenticated launch intent provenance of its own.
				a.ClaimRevision = newID()
				if err := s.store.saveManaged(a); err != nil {
					t.Fatal(err)
				}
			} else {
				b := &v1.ExecutionResult{Attempt: newID()}
				if _, err := s.store.execution(instance.ID, v1.RuntimeReady, b); err != nil {
					t.Fatal(err)
				}
				if err := s.store.saveRuntime(instance.ID, runtimeRecord{Attempt: b.Attempt, LaunchPending: true}); err != nil {
					t.Fatal(err)
				}
			}
			before := storedBytes(t, s.store, bucket, key)
			journal := storedBytes(t, s.store, "runtime", instance.ID)
			handler := handler(s, v1.DaemonStatusResponse{APIVersion: v1.Version, StateVersion: v1.StateVersion}, func() {})
			data, err := json.Marshal(payload)
			if err != nil {
				t.Fatal(err)
			}
			for range 2 {
				req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(data))
				req.Header.Set("X-Backlot-API-Version", v1.Version)
				req.Header.Set("Content-Type", "application/json")
				rec := httptest.NewRecorder()
				handler.ServeHTTP(rec, req)
				if rec.Code == http.StatusOK || !bytes.Contains(rec.Body.Bytes(), []byte(`"conflict"`)) {
					t.Fatal(rec.Code, rec.Body.String())
				}
				if !bytes.Equal(before, storedBytes(t, s.store, bucket, key)) || !bytes.Equal(journal, storedBytes(t, s.store, "runtime", instance.ID)) {
					t.Fatal("stale public payload stopped or waived replacement")
				}
			}
		})
	}
}

func TestWaiverAfterWritesRollsBack(t *testing.T) {
	for _, tc := range []struct {
		name            string
		managed, cancel bool
	}{
		{name: "scene-error"},
		{name: "scene-cancel", cancel: true},
		{name: "managed-error", managed: true},
		{name: "managed-cancel", managed: true, cancel: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, instance, _ := exportFixture(t)
			j := runtimeRecord{Attempt: instance.Execution.Attempt, LaunchPending: true}
			if err := s.store.saveRuntime(instance.ID, j); err != nil {
				t.Fatal(err)
			}
			r := managedRecord{Scope: newID(), Config: manifest.CaddyDefaults(v1.CaddyConfig{Mode: "managed", Executable: "/explicit/caddy"}), Pending: true, Status: "uncertain"}
			if err := s.store.saveManaged(r); err != nil {
				t.Fatal(err)
			}
			r, err := s.store.managed()
			if err != nil {
				t.Fatal(err)
			}
			beforeInstance := storedBytes(t, s.store, "instances", instance.ID)
			beforeRuntime := storedBytes(t, s.store, "runtime", instance.ID)
			beforeManaged := storedBytes(t, s.store, "meta", "managed_gateway")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			injected := errors.New("waiver commit failure")
			// Runs after actual bbolt Put calls; nil must not hide cancellation.
			s.store.waiverCheckpoint = func() error {
				if tc.cancel {
					cancel()
					return nil
				}
				return injected
			}
			waive := func(ctx context.Context) error {
				if tc.managed {
					return s.store.abandonManaged(ctx, &r, r, nil, errors.New("missing authority"))
				}
				_, err := s.store.abandonNative(ctx, instance.ID, j.Attempt)
				return err
			}
			err = waive(ctx)
			if tc.cancel {
				injected = context.Canceled
			}
			if !errors.Is(err, injected) {
				t.Fatal("failed transaction claimed success", err)
			}
			if !bytes.Equal(beforeInstance, storedBytes(t, s.store, "instances", instance.ID)) || !bytes.Equal(beforeRuntime, storedBytes(t, s.store, "runtime", instance.ID)) || !bytes.Equal(beforeManaged, storedBytes(t, s.store, "meta", "managed_gateway")) {
				t.Fatal("failed transaction released claim/history")
			}
			s.store.waiverCheckpoint = nil
			if err := waive(context.Background()); err != nil {
				t.Fatal("retry after rollback failed", err)
			}
		})
	}
}

func TestStateSixRejectsMalformedLifecycleUnchanged(t *testing.T) {
	cases := []struct{ name, bucket, key, value string }{
		{"managed syntax", "meta", "managed_gateway", "{"},
		{"managed shape", "meta", "managed_gateway", "[]"},
		{"probe", "meta", "doctor_probe/invalid", `{"resource":{}}`},
		{"runtime array", "runtime", "bad", "[]"},
		{"runtime null", "runtime", "bad", "null"},
		{"runtime fields", "runtime", "bad", `{"groups":{}}`},
		{"resources fields", "resources", "bad", `{"resources":[]}`},
		{"resources ownership", "resources", "bad", `{"id":"invalid","resources":{},"initialized":{}}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s, instance, _ := exportFixture(t)
			dir := s.store.directory
			sentinel := filepath.Join(dir, "retained-ca-config")
			if err := os.WriteFile(sentinel, []byte("retained private state"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := s.store.db.Update(func(tx *bolt.Tx) error {
				if err := tx.Bucket([]byte("meta")).Put([]byte("version"), []byte("6")); err != nil {
					return err
				}
				if err := tx.Bucket([]byte("meta")).Put([]byte("unknown_metadata"), []byte("opaque preserved")); err != nil {
					return err
				}
				key := c.key
				if c.bucket == "runtime" || c.bucket == "resources" {
					key = instance.ID // Exercise the typed decoder, not orphan-parent rejection.
				}
				return tx.Bucket([]byte(c.bucket)).Put([]byte(key), []byte(c.value))
			}); err != nil {
				t.Fatal(err)
			}
			if err := s.store.db.Close(); err != nil {
				t.Fatal(err)
			}
			before, err := os.ReadFile(filepath.Join(dir, "state.db"))
			if err != nil {
				t.Fatal(err)
			}
			opened, err := openStore(dir)
			if err == nil {
				_ = opened.db.Close()
				t.Fatal("corrupt v6 advanced format")
			}
			after, readErr := os.ReadFile(filepath.Join(dir, "state.db"))
			if readErr != nil || !bytes.Equal(before, after) {
				t.Fatal("rejected v6 bytes changed", readErr)
			}
			retained, err := os.ReadFile(sentinel)
			if err != nil || string(retained) != "retained private state" {
				t.Fatal("retained files changed", err)
			}
		})
	}
}
