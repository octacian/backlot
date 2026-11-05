//go:build darwin || linux

package daemon

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	v1 "github.com/octacian/backlot/api/v1"
	"github.com/octacian/backlot/internal/gateway"
	bolt "go.etcd.io/bbolt"
)

// taskRecordFixture installs preactivation journals, never synthetic OS authority.
func taskRecordFixture(t *testing.T, f *controlledExpiry) (string, string) {
	t.Helper()
	parent, err := f.s.prepare(context.Background(), request(f.root, f.config, "dev"))
	if err != nil {
		t.Fatal(err)
	}
	attempt := newID()
	if _, err := f.s.store.execution(parent.Instance.ID, v1.RuntimeReady, &v1.ExecutionResult{Attempt: attempt}); err != nil {
		t.Fatal(err)
	}
	id := newID()
	child := parent.Instance
	child.ID = id
	child.Plan.Lifetime = v1.Disposable
	child.Status = v1.Starting
	child.Operation.Status = v1.Starting
	child.Execution = &v1.ExecutionResult{Attempt: newID()}
	child.Task = &v1.TaskBinding{InstanceID: parent.Instance.ID, Attempt: attempt, Generation: attempt, Name: "task"}
	if err := f.s.store.db.Update(func(tx *bolt.Tx) error {
		if err := put(tx, "runtime", id, runtimeRecord{Attempt: child.Execution.Attempt}); err != nil {
			return err
		}
		if err := put(tx, "snapshots", id, json.RawMessage(`{"private":"retire-me"}`)); err != nil {
			return err
		}
		if err := put(tx, "secrets", id, map[string]string{"private": "retire-me"}); err != nil {
			return err
		}
		return put(tx, "instances", id, record{Instance: child})
	}); err != nil {
		t.Fatal(err)
	}
	return parent.Instance.ID, id
}

func TestTaskRecoveryAuthorityAndReleaseFences(t *testing.T) {
	f := newControlledExpiry(t)
	parent, child := taskRecordFixture(t, f)
	if err := f.s.store.db.View(validateTaskAuthority); err != nil {
		t.Fatal(err)
	}
	if err := f.s.store.removeResources(context.Background(), parent); err == nil {
		t.Fatal("live task failed to fence resource release")
	}
	i, err := f.s.store.inspect(child)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.store.execution(child, v1.Starting, &v1.ExecutionResult{Attempt: newID()}); err == nil {
		t.Fatal("late writer replaced task attempt")
	}
	// A preactivation task has no launched effects to resume; recovery interrupts it first.
	if err := f.s.store.recoverExecution(child); err != nil {
		t.Fatal(err)
	}
	c, err := f.s.store.inspect(child)
	if err != nil || c.Status != v1.Interrupted || c.Execution.Attempt != i.Execution.Attempt {
		t.Fatal("task recovery", c, err)
	}
	if err := f.s.store.guardDependentTasks(parent); err != nil {
		t.Fatal("settled task retained live fence", err)
	}
	p, err := f.s.store.inspect(parent)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.store.execution(parent, v1.Stopped, p.Execution); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"snapshots", "secrets"} {
		if data := storedBytes(t, f.s.store, name, child); len(data) != 0 {
			t.Fatal("settled private launch data retained", name)
		}
	}
}

func TestTaskUncertainCleanupProtectsParentAndRetention(t *testing.T) {
	f := newControlledExpiry(t)
	parent, child := taskRecordFixture(t, f)
	c, err := f.s.store.inspect(child)
	if err != nil {
		t.Fatal(err)
	}
	c.Execution.CleanupFailure = "uncertain consumer"
	c.Execution.CompletedAt = time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := f.s.store.execution(child, v1.Failed, c.Execution); err != nil {
		t.Fatal(err)
	}
	if err := f.s.store.guardDependentTasks(parent); err == nil {
		t.Fatal("uncertain cleanup released parent")
	}
	if _, err := f.s.eraseDestroyed(parent); err == nil {
		t.Fatal("destroy erased uncertain task")
	}
	if err := f.s.store.db.Update(func(tx *bolt.Tx) error {
		r, err := load(tx, parent)
		if err != nil {
			return err
		}
		r.Instance.Plan.Publish = &v1.Publication{Resource: "origin"}
		r.Instance.Status = v1.Failed
		r.Instance.Operation.Status = v1.Failed
		r.Instance.Execution.CleanupFailure = "dependent cleanup incomplete"
		return put(tx, "instances", parent, r)
	}); err != nil {
		t.Fatal(err)
	}
	managed := managedRecord{Status: "running"}
	if err := f.s.store.stopManaged(&managed, nil); err == nil || managed.Status != "running" {
		t.Fatal("gateway released an uncertain dependent's parent publication", err)
	}
	// Even prior route-only parent proof cannot release a live or uncertain dependent.
	journal, err := f.s.store.runtime(parent)
	if err != nil {
		t.Fatal(err)
	}
	p, err := f.s.store.inspect(parent)
	if err != nil {
		t.Fatal(err)
	}
	journal.Attempt, journal.GatewayOnlyCleanup = p.Execution.Attempt, true
	journal.Gateway = &gateway.Intent{}
	if err := f.s.store.saveRuntime(parent, journal); err != nil {
		t.Fatal(err)
	}
	for _, status := range []v1.InstanceStatus{v1.Starting, v1.Failed} {
		if _, err := f.s.store.execution(child, status, c.Execution); err != nil {
			t.Fatal(err)
		}
		if err := f.s.store.stopManaged(&managed, nil); err == nil || managed.Status != "running" {
			t.Fatal("route-only parent proof bypassed dependent fence", status, err)
		}
	}

	entries, err := f.s.store.retentionScope(parent)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, a := range entries {
		if a.InstanceID == child {
			found = true
			if !attemptProtected(a) {
				t.Fatal("uncertain history unprotected")
			}
		}
	}
	if !found {
		t.Fatal("task not in parent budget")
	}
	c.Execution.CleanupFailure = ""
	c.Execution.CollectionFailure = "collection gap"
	if _, err := f.s.store.execution(child, v1.Failed, c.Execution); err != nil {
		t.Fatal(err)
	}
	if err := f.s.store.guardDependentTasks(parent); err != nil {
		t.Fatal("collection failure became live fence", err)
	}
	p, err = f.s.store.inspect(parent)
	if err != nil {
		t.Fatal(err)
	}
	p.Execution.CleanupFailure = ""
	if _, err := f.s.store.execution(parent, v1.Stopped, p.Execution); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.eraseDestroyed(parent); err != nil {
		t.Fatal(err)
	}
	c, err = f.s.store.inspect(child)
	if err != nil || c.Execution != nil || c.Status != v1.Destroyed {
		t.Fatal("child not erased", err)
	}
}

func TestTaskAuthorityRejectsGenerationAndLegacyCaptures(t *testing.T) {
	f := newControlledExpiry(t)
	parent, child := taskRecordFixture(t, f)
	original, err := f.s.store.inspect(child)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.s.store.db.Update(func(tx *bolt.Tx) error {
		r, err := load(tx, child)
		if err != nil {
			return err
		}
		r.Instance.Task.Generation = newID()
		return put(tx, "instances", child, r)
	}); err != nil {
		t.Fatal(err)
	}
	if err := f.s.store.db.View(validateTaskAuthority); err == nil {
		t.Fatal("mismatched parent generation accepted")
	}
	if _, err := f.s.store.execution(child, v1.Starting, original.Execution); err == nil {
		t.Fatal("task writer ignored replaced parent generation")
	}

	if err := f.s.store.db.Update(func(tx *bolt.Tx) error {
		r, err := load(tx, child)
		if err != nil {
			return err
		}
		r.Instance.Task = original.Task
		return put(tx, "instances", child, r)
	}); err != nil {
		t.Fatal(err)
	}
	// Historical manifest text never grants a task capture or retrofits a ready parent.
	_, err = f.s.runTask(context.Background(), v1.TaskRunRequest{APIVersion: v1.Version, Target: v1.SceneTarget{InstanceID: parent}, Task: "check"})
	code(t, err, "restart_required")
	for _, id := range []string{parent, child} {
		i, err := f.s.store.inspect(id)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.s.store.execution(id, v1.Stopped, i.Execution); err != nil {
			t.Fatal(err)
		}
	}
}
