//go:build darwin || linux

package daemon

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	v1 "github.com/octacian/backlot/api/v1"
	bolt "go.etcd.io/bbolt"
)

// Fixture journals have never launched work; only this known owner may assert zero effects.
func releaseZeroLaunchTaskFixture(t *testing.T, s *store, id string) {
	t.Helper()
	i, err := s.inspect(id)
	if err != nil {
		t.Fatal(err)
	}
	j, err := s.runtime(id)
	if err != nil {
		t.Fatal(err)
	}
	if j.Attempt == "" {
		j.Attempt = i.Execution.Attempt
		if err := s.saveRuntime(id, j); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.recordConsumerRelease(context.Background(), id, j, v1.SettlementExecutionCleanup, true); err != nil {
		t.Fatal(err)
	}
}

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
		releaseZeroLaunchTaskFixture(t, f.s.store, id)
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
	for _, id := range []string{child, parent} {
		i, err := f.s.store.inspect(id)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.s.store.execution(id, v1.Stopped, i.Execution); err != nil {
			t.Fatal(err)
		}
		releaseZeroLaunchTaskFixture(t, f.s.store, id)
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

func TestTaskHistoryPreservesUnverifiedInputs(t *testing.T) {
	f := newControlledExpiry(t)
	parent, child := taskRecordFixture(t, f)
	c, err := f.s.store.inspect(child)
	if err != nil {
		t.Fatal(err)
	}
	c.Execution.CompletedAt = time.Now().Add(-time.Hour).UTC().Format(time.RFC3339Nano)
	c.Execution.Outcome = v1.Interrupted
	if _, err := f.s.store.execution(child, v1.Interrupted, c.Execution); err != nil {
		t.Fatal(err)
	}
	entries, err := f.s.store.attempts(child)
	if err != nil || len(entries) != 1 {
		t.Fatal(entries, err)
	}
	a := entries[0]
	writeAttemptLog(t, f.s, a, false)
	before := map[string][]byte{}
	for _, bucket := range []string{"runtime", "snapshots", "secrets"} {
		before[bucket] = storedBytes(t, f.s.store, bucket, child)
	}
	history, err := f.s.store.history(v1.HistoryRequest{InstanceID: child})
	if err != nil || len(history.Attempts) != 1 || !history.Attempts[0].Protected {
		t.Fatal(history, err)
	}
	keep := 0
	q := v1.HistoryPruneRequest{APIVersion: v1.Version, InstanceID: parent, Keep: &keep, DryRun: true}
	preview, err := f.s.pruneHistory(context.Background(), q)
	if err != nil || len(preview.Attempts) != 1 || preview.Attempts[0].Protection != "cleanup_unresolved" {
		t.Fatal(preview, err)
	}
	q.DryRun = false
	if _, err := f.s.pruneHistory(context.Background(), q); err != nil {
		t.Fatal(err)
	}
	// The deletion boundary rechecks current authority even if a caller bypasses preview.
	if err := f.s.deleteAttempt(a, true); err != nil {
		t.Fatal(err)
	}
	if err := f.s.pruneEvidence(); err != nil {
		t.Fatal(err)
	}
	for bucket, want := range before {
		if !bytes.Equal(want, storedBytes(t, f.s.store, bucket, child)) {
			t.Fatalf("%s erased without verified release", bucket)
		}
	}
	if entries, err := f.s.store.attempts(child); err != nil || len(entries) != 1 {
		t.Fatal("unverified task history pruned", entries, err)
	}
	if _, err := os.Stat(filepath.Join(f.s.directory, "logs", child+"-"+a.Result.Attempt+"-output.ndjson")); err != nil {
		t.Fatal("unverified task evidence erased", err)
	}
	var original record
	if err := f.s.store.db.Update(func(tx *bolt.Tx) error {
		var err error
		original, err = load(tx, child)
		if err != nil {
			return err
		}
		malformed := original
		malformed.Instance.Execution = nil
		malformed.Instance.Status, malformed.Instance.Operation.Status = v1.Destroyed, v1.Destroyed
		return put(tx, "instances", child, malformed)
	}); err != nil {
		t.Fatal(err)
	}
	if err := f.s.deleteAttempt(a, true); err == nil {
		t.Fatal("incomplete task tombstone bypassed deletion fence")
	}
	if err := f.s.store.db.Update(func(tx *bolt.Tx) error { return put(tx, "instances", child, original) }); err != nil {
		t.Fatal(err)
	}
	// The same completed attempt becomes eligible only after a current owner
	// publishes this fixture's known zero-launch fact through the real producer.
	j, err := f.s.store.runtime(child)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.s.store.recordConsumerRelease(context.Background(), child, j, v1.SettlementExecutionCleanup, true); err != nil {
		t.Fatal(err)
	}
	p, err := f.s.store.inspect(parent)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.store.execution(parent, v1.Stopped, p.Execution); err != nil {
		t.Fatal(err)
	}
	pj := runtimeRecord{Attempt: p.Execution.Attempt}
	if err := f.s.store.saveRuntime(parent, pj); err != nil {
		t.Fatal(err)
	}
	if err := f.s.store.recordConsumerRelease(context.Background(), parent, pj, v1.SettlementExecutionCleanup, true); err != nil {
		t.Fatal(err)
	}
	protection, err := f.s.store.attemptProtection(a)
	if err != nil || protection != "" {
		t.Fatal("verified task remained protected", protection, err)
	}
}
