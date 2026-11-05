//go:build darwin || linux

package daemon

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	v1 "github.com/octacian/backlot/api/v1"
	bolt "go.etcd.io/bbolt"
)

func TestTaskHistoryParentBudgetAndPrivateRetirement(t *testing.T) {
	f := newControlledExpiry(t)
	parent, first := taskRecordFixture(t, f)
	initial, err := f.s.store.inspect(first)
	if err != nil {
		t.Fatal(err)
	}
	ids := []string{first}
	for range 3 {
		child := initial
		child.ID = newID()
		child.Execution = &v1.ExecutionResult{Attempt: newID()}
		ids = append(ids, child.ID)
		if err := f.s.store.db.Update(func(tx *bolt.Tx) error {
			if err := put(tx, "runtime", child.ID, runtimeRecord{Attempt: child.Execution.Attempt, StartedAt: time.Now().UTC().Format(time.RFC3339Nano)}); err != nil {
				return err
			}
			return put(tx, "instances", child.ID, record{Instance: child})
		}); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range ids {
		c, err := f.s.store.inspect(id)
		if err != nil {
			t.Fatal(err)
		}
		c.Execution.CompletedAt = time.Now().UTC().Format(time.RFC3339Nano)
		zero := 0
		c.Execution.TerminalExitCode = &zero
		if _, err := f.s.store.execution(id, v1.Succeeded, c.Execution); err != nil {
			t.Fatal(err)
		}
		if err := f.s.store.retireTaskInputs(id); err != nil {
			t.Fatal(err)
		}
	}
	p, err := f.s.store.inspect(parent)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.store.execution(parent, v1.Stopped, p.Execution); err != nil {
		t.Fatal(err)
	}
	f.s.evidence.attempts = 2
	if err := f.s.pruneEvidence(); err != nil {
		t.Fatal(err)
	}
	scope, err := f.s.store.retentionScope(parent)
	if err != nil {
		t.Fatal(err)
	}
	completed := 0
	for _, a := range scope {
		if a.Completed() {
			completed++
		}
	}
	if completed != 2 {
		t.Fatal("tasks did not share parent completed budget", completed)
	}
	for _, id := range ids {
		if data := storedBytes(t, f.s.store, "secrets", id); len(data) != 0 {
			t.Fatal("private task archive retained")
		}
	}
	if _, err := f.s.eraseDestroyed(parent); err != nil {
		t.Fatal(err)
	}
	for _, id := range ids {
		history, err := f.s.store.attempts(id)
		if err != nil || len(history) != 0 {
			t.Fatal("task history survived parent destroy", err)
		}
	}
}

func TestTaskDestroyDeletionRetryRemainsParentObligation(t *testing.T) {
	f := newControlledExpiry(t)
	parent, child := taskRecordFixture(t, f)
	for _, id := range []string{parent, child} {
		i, err := f.s.store.inspect(id)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.s.store.execution(id, v1.Stopped, i.Execution); err != nil {
			t.Fatal(err)
		}
	}
	artifacts := filepath.Join(f.s.directory, "artifacts")
	if err := os.MkdirAll(artifacts, 0700); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	marker := filepath.Join(outside, "keep")
	if err := os.WriteFile(marker, []byte("application data"), 0600); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(artifacts, child)
	if err := os.Symlink(outside, alias); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.eraseDestroyed(parent); err == nil {
		t.Fatal("unsafe evidence erasure succeeded")
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatal("external data touched", err)
	}
	if err := os.Remove(alias); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.eraseDestroyed(parent); err != nil {
		t.Fatal("retry lost child deletion obligation", err)
	}
	if err := f.s.store.db.View(func(tx *bolt.Tx) error {
		if tx.Bucket([]byte("evidence_deletions")).Stats().KeyN != 0 {
			t.Fatal("deletion retry incomplete")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.store.inspect(child); err != nil {
		t.Fatal(err)
	}
}
