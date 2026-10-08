//go:build darwin || linux

package daemon

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	v1 "github.com/octacian/backlot/api/v1"
	bolt "go.etcd.io/bbolt"
)

type controlledExpiry struct {
	s            *service
	root, config string
	gates        []func()
	allowError   bool
}
type controlledOwner struct {
	id                    string
	gate, cancelled, done chan struct{}
	release               func()
	entry                 *execution
}

func newControlledExpiry(t *testing.T) *controlledExpiry {
	t.Helper()
	root := shortTemp(t)
	config := writeFixture(t, root)
	store, err := openStore(shortTemp(t))
	if err != nil {
		t.Fatal(err)
	}
	f := &controlledExpiry{s: &service{store: store, lease: time.Minute, executions: map[string]*execution{}}, root: root, config: config}
	t.Cleanup(func() {
		for _, release := range f.gates {
			release()
		}
		if err := f.s.stop(); err != nil && !f.allowError {
			t.Error(err)
		}
		if !f.s.ownersJoined() {
			t.Fatal("controlled expiry owners unjoined")
		}
		if err := store.db.Close(); err != nil {
			t.Error(err)
		}
	})
	return f
}
func (f *controlledExpiry) owner(t *testing.T, expired bool, grace time.Duration) *controlledOwner {
	t.Helper()
	response, err := f.s.prepare(context.Background(), request(f.root, f.config, "test"))
	if err != nil {
		t.Fatal(err)
	}
	result := &v1.ExecutionResult{Attempt: newID()}
	if _, err := f.s.store.execution(response.Instance.ID, v1.Starting, result); err != nil {
		t.Fatal(err)
	}
	owner := &controlledOwner{id: response.Instance.ID, gate: make(chan struct{}), cancelled: make(chan struct{}), done: make(chan struct{})}
	var released, cancelled sync.Once
	owner.release = func() { released.Do(func() { close(owner.gate) }) }
	f.gates = append(f.gates, owner.release)
	owner.entry = &execution{done: owner.done, grace: grace, cancel: func() {
		cancelled.Do(func() {
			close(owner.cancelled)
			go func() {
				_, err := f.s.store.execution(owner.id, v1.Stopping, result)
				if err != nil {
					panic(err)
				}
				<-owner.gate
				_, err = f.s.store.execution(owner.id, v1.Cancelled, result)
				if err != nil {
					panic(err)
				}
				close(owner.done)
			}()
		})
	}}
	f.s.mu.Lock()
	f.s.executions[owner.id] = owner.entry
	f.s.mu.Unlock()
	if expired {
		f.deadline(t, owner.id, time.Now().Add(-time.Second))
	}
	return owner
}
func (f *controlledExpiry) deadline(t *testing.T, id string, deadline time.Time) {
	t.Helper()
	f.s.mu.Lock()
	defer f.s.mu.Unlock()
	if err := f.s.store.db.Update(func(tx *bolt.Tx) error {
		r, err := load(tx, id)
		if err != nil {
			return err
		}
		r.Instance.LeaseExpiresAt = deadline.Format(time.RFC3339Nano)
		return put(tx, "instances", id, r)
	}); err != nil {
		t.Fatal(err)
	}
}
func awaitControlled(t *testing.T, done <-chan struct{}, message string) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal(message)
	}
}

func TestExpiryIndependentOfMutationAndCleanup(t *testing.T) {
	f := newControlledExpiry(t)
	locked := f.owner(t, true, 16*time.Second)
	short := f.owner(t, true, 0)
	short.release()
	unlock := f.s.lockMutation(locked.id)
	var unlocked sync.Once
	releaseLock := func() { unlocked.Do(unlock) }
	t.Cleanup(releaseLock)
	if err := f.s.expire(); err != nil {
		t.Fatal(err)
	}
	awaitControlled(t, short.cancelled, "other expired owner blocked by mutation lock")
	awaitControlled(t, short.done, "short cleanup blocked by other mutation")
	f.s.mu.Lock()
	pending := f.s.expiryWorkers[locked.id]
	f.s.mu.Unlock()
	if pending == nil {
		t.Fatal("locked expiry coordinator not tracked")
	}
	for range 3 {
		if err := f.s.expire(); err != nil {
			t.Fatal(err)
		}
	}
	f.s.mu.Lock()
	same := f.s.expiryWorkers[locked.id] == pending
	f.s.mu.Unlock()
	if !same {
		t.Fatal("duplicate sweeps replaced expiry coordinator")
	}
	later := f.owner(t, true, 0)
	later.release()
	if err := f.s.expire(); err != nil {
		t.Fatal(err)
	}
	awaitControlled(t, later.done, "later expiry blocked by earlier coordinator")
	// A lease renewal wins before the blocked coordinator obtains mutation authority.
	f.deadline(t, locked.id, time.Now().Add(time.Minute))
	releaseLock()
	awaitControlled(t, pending, "renewed expiry coordinator did not join")
	select {
	case <-locked.cancelled:
		t.Fatal("stale expiry cancelled renewed owner")
	default:
	}
	for _, id := range []string{short.id, later.id} {
		instance, err := f.s.store.inspect(id)
		if err != nil || instance.Status != v1.Cancelled || instance.LeaseExpiresAt != "" {
			t.Fatal("terminal state/lease not truthful", instance, err)
		}
	}
}

func TestShutdownCancelsWhileExpiryCleanupWaits(t *testing.T) {
	f := newControlledExpiry(t)
	long := f.owner(t, true, 16*time.Second)
	short := f.owner(t, false, 0)
	short.release()
	if err := f.s.expire(); err != nil {
		t.Fatal(err)
	}
	awaitControlled(t, long.cancelled, "expired long owner not cancelled")
	stopped := make(chan error, 1)
	go func() { stopped <- f.s.stop() }()
	// Registered before assertions: a failing independence assertion still releases
	// the held owner and joins the shutdown call before the private DB is closed.
	t.Cleanup(func() {
		long.release()
		if err := <-stopped; err != nil {
			t.Error(err)
		}
	})
	awaitControlled(t, short.done, "daemon shutdown delayed by expiry cleanup/lock")
	select {
	case <-long.done:
		t.Fatal("long join gate unexpectedly released")
	default:
	}
	long.release()
	// The cleanup owns the single receive/join above.
}

func TestExpiryWorkerFailureReachesDaemon(t *testing.T) {
	f := newControlledExpiry(t)
	f.allowError = true
	owner := f.owner(t, true, 0)
	owner.release()
	owner.entry.err = errors.New("controlled cleanup verification failure")
	if err := f.s.expire(); err != nil {
		t.Fatal(err)
	}
	awaitControlled(t, owner.done, "failed owner not joined")
	if err := f.s.joinExpiryWorkers(); err != nil {
		t.Fatal(err)
	}
	if err := f.s.expire(); err == nil {
		t.Fatal("asynchronous expiry failure hidden from daemon")
	}
	if err := f.s.stop(); err == nil {
		t.Fatal("asynchronous expiry failure hidden at shutdown")
	}
}

func TestVerifiedCleanupReleasesOutputs(t *testing.T) {
	f := newControlledExpiry(t)
	f.allowError = true // Recovery truthfully retains a collection gap.
	owner := f.owner(t, false, 0)
	owner.release()
	// End the controlled owner first, then simulate a durably failed cleanup with
	// valid ownership. The explicit stop must release its retained output token.
	owner.entry.cancel()
	<-owner.done
	instance, err := f.s.store.inspect(owner.id)
	if err != nil {
		t.Fatal(err)
	}
	result := instance.Execution
	result.CleanupFailure = "fixture transient cleanup failure"
	if _, err = f.s.store.execution(owner.id, v1.Failed, result); err != nil {
		t.Fatal(err)
	}
	if err = f.s.store.saveRuntime(owner.id, runtimeRecord{Attempt: result.Attempt}); err != nil {
		t.Fatal(err)
	}
	released := false
	owner.entry.outputRelease = func() { released = true }
	if _, err = f.s.stopExecution(owner.id); err != nil {
		t.Fatal(err)
	}
	if !released {
		t.Fatal("verified cleanup retained checkout output lock")
	}
}

func TestOutputGroupsFenceSnapshotDrift(t *testing.T) {
	f := newControlledExpiry(t)
	owner := f.owner(t, false, 0)
	snap, err := f.s.store.snapshot(owner.id)
	if err != nil {
		t.Fatal(err)
	}
	snap.Plan.Outputs = map[string]v1.Output{"build": {Path: "build", ConcurrencyGroup: "original"}}
	if err = f.s.store.replaceSnapshot(owner.id, snap); err != nil {
		t.Fatal(err)
	}
	next := snap.Plan
	next.Outputs = map[string]v1.Output{"build": {Path: "build/subdirectory", ConcurrencyGroup: "changed"}}
	if err = f.s.store.outputConflict(next); err == nil {
		t.Fatal("group rename bypassed live overlapping output")
	}
	next.Outputs = map[string]v1.Output{"build": {Path: "build/subdirectory", ConcurrencyGroup: "original"}}
	if err = f.s.store.outputConflict(next); err != nil {
		t.Fatal("compatible group could not join serialization", err)
	}
	owner.entry.cancel()
	owner.release()
	<-owner.done
	if err = f.s.store.outputConflict(next); err != nil {
		t.Fatal("joined consumer still blocks output", err)
	}
}

func TestDestroyedAcceptanceCannotResurrect(t *testing.T) {
	f := newControlledExpiry(t)
	prepared, err := f.s.prepare(context.Background(), request(f.root, f.config, "dev"))
	if err != nil {
		t.Fatal(err)
	}
	accepted := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	f.gates = append(f.gates, func() { once.Do(func() { close(release) }) })
	f.s.beforeExecution = func(string) { close(accepted); <-release }
	done := make(chan error, 1)
	go func() {
		_, err := f.s.run(context.Background(), v1.RunRequest{APIVersion: v1.Version, Plan: request(f.root, f.config, "dev").Plan})
		done <- err
	}()
	<-accepted
	if _, err = f.s.destroy(context.Background(), prepared.Instance.ID); err != nil {
		t.Fatal(err)
	}
	once.Do(func() { close(release) })
	if err = <-done; err == nil {
		t.Fatal("destroyed preparation accepted runtime")
	}
	current, err := f.s.store.inspect(prepared.Instance.ID)
	if err != nil || current.Status != v1.Destroyed {
		t.Fatal("destroyed instance resurrected", err)
	}
	next, err := f.s.prepare(context.Background(), request(f.root, f.config, "dev"))
	if err != nil || next.Instance.ID == prepared.Instance.ID {
		t.Fatal("fresh run did not get a new identity", err)
	}
}
