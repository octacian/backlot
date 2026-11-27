//go:build darwin || linux

package daemon

import (
	"context"
	"errors"
	"fmt"
	"net"
	"path/filepath"
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
	f := &controlledExpiry{s: &service{store: store, directory: store.directory, lease: time.Minute, executions: map[string]*execution{}}, root: root, config: config}
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
	if err := f.s.store.saveRuntime(owner.id, runtimeRecord{Attempt: result.Attempt}); err != nil {
		t.Fatal(err)
	}
	owner.entry = &execution{attempt: result.Attempt, done: owner.done, grace: grace, cancel: func() {
		cancelled.Do(func() {
			close(owner.cancelled)
			go func() {
				_, err := f.s.store.execution(owner.id, v1.Stopping, result)
				if err != nil {
					panic(err)
				}
				<-owner.gate
				result.CompletedAt = time.Now().UTC().Format(time.RFC3339Nano)
				_, err = f.s.store.execution(owner.id, v1.Cancelled, result)
				if err != nil {
					panic(err)
				}
				// This joined synthetic owner never launched work.
				if err := f.s.store.recordConsumerRelease(context.Background(), owner.id, runtimeRecord{Attempt: result.Attempt}, v1.SettlementExecutionCleanup, true); err != nil {
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

func TestExecutionAcceptanceRechecksCancellationAndLease(t *testing.T) {
	for _, action := range []string{"cancel", "expire", "expired-without-reconcile"} {
		t.Run(action, func(t *testing.T) {
			root := shortTemp(t)
			config := writeFixture(t, root)
			store, err := openStore(shortTemp(t))
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = store.db.Close() }()
			reached := make(chan string, 1)
			release := make(chan struct{})
			s := &service{store: store, lease: time.Minute, beforeExecution: func(id string) { reached <- id; <-release }}
			result := make(chan error, 1)
			go func() {
				_, err := s.run(context.Background(), v1.RunRequest{APIVersion: v1.Version, Plan: request(root, config, "test").Plan, Options: v1.ExecutionOptions{StartupTimeout: "0s", JobTimeout: "0s"}})
				result <- err
			}()
			id := <-reached
			if action == "cancel" {
				if _, err := s.cancelRuntime(id); err != nil {
					t.Fatal(err)
				}
			} else {
				err := store.db.Update(func(tx *bolt.Tx) error {
					r, err := load(tx, id)
					if err != nil {
						return err
					}
					r.Instance.LeaseExpiresAt = time.Now().Add(-time.Second).Format(time.RFC3339Nano)
					return put(tx, "instances", id, r)
				})
				if err != nil {
					t.Fatal(err)
				}
				if action == "expire" {
					if err := s.expire(); err != nil {
						t.Fatal(err)
					}
				}
			}
			close(release)
			if err := <-result; err == nil {
				t.Fatal("stale prepared response resurrected rejected work")
			}
			instance, err := store.inspect(id)
			if err != nil {
				t.Fatal(err)
			}
			if instance.Execution != nil || len(s.executions) != 0 {
				t.Fatal("rejected acceptance launched an execution", instance)
			}
			journal, err := store.runtime(id)
			if err != nil || len(journal.Groups) != 0 || journal.LaunchPending {
				t.Fatal("rejected acceptance recorded launch", journal, err)
			}
			if action == "cancel" && instance.Status != v1.Cancelled {
				t.Fatal(instance.Status)
			}
		})
	}
}

func TestCancellationRechecksRegisteredOwner(t *testing.T) {
	root := shortTemp(t)
	config := writeFixture(t, root)
	store, err := openStore(shortTemp(t))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.db.Close() }()
	s := &service{store: store, lease: time.Minute}
	response, err := s.prepare(context.Background(), request(root, config, "test"))
	if err != nil {
		t.Fatal(err)
	}
	id := response.Instance.ID
	// Hold the same instance mutation lock used by run acceptance. Cancellation
	// must examine the execution owner only after acquiring it.
	unlock := s.lockMutation(id)
	done := make(chan struct{})
	cancelled := make(chan struct{})
	entry := &execution{done: done, cancel: func() { close(cancelled); close(done) }}
	result := make(chan error, 1)
	go func() { _, err := s.cancelRuntime(id); result <- err }()
	select {
	case err := <-result:
		unlock()
		t.Fatalf("cancellation bypassed acceptance mutation lock: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	s.mu.Lock()
	s.executions = map[string]*execution{id: entry}
	s.mu.Unlock()
	unlock()
	if err := <-result; err != nil {
		t.Fatal(err)
	}
	select {
	case <-cancelled:
	default:
		t.Fatal("registered owner missed by cancellation")
	}
}

func TestLeaseCollectionFailureIsInstanceLocal(t *testing.T) {
	binary := buildFixtureBinary(t, t.TempDir(), "../../cmd/backlot")
	h := newNativeHarness(t, binary, "500ms")
	// Separate witnesses/control paths keep cleanup attribution exact for both owners.
	other := h.manifest.Components["server"]
	other.Environment.Assign = map[string]v1.Value{}
	for key, value := range h.manifest.Components["server"].Environment.Assign {
		other.Environment.Assign[key] = value
	}
	otherPIDs := filepath.Join(filepath.Dir(h.dir), "other-pids")
	other.Environment.Assign["PIDFILE"] = fixtureLiteral(otherPIDs)
	other.Environment.Assign["FIXTURE_CONTROL"] = fixtureLiteral(filepath.Join(filepath.Dir(h.dir), "other.sock"))
	h.extraPIDFiles = append(h.extraPIDFiles, otherPIDs)
	h.manifest.Components["other"] = other
	dev := h.manifest.Scenes["dev"]
	dev.Components = []string{"other"}
	h.manifest.Scenes["dev"] = dev
	h.manifest.Tools["sh"] = "/bin/sh"
	job := h.manifest.Components["job"]
	job.Command = &v1.Command{Tool: "sh", Args: v1.StringArguments("-c", "echo waiting; exec sleep 60")}
	job.Artifacts = map[string]v1.ArtifactSource{"missing": {Path: "not-produced"}}
	h.manifest.Components["job"] = job
	h.write()
	unrelated := h.run("dev")
	h.state(unrelated.Instance.ID, v1.RuntimeReady)
	request := h.request("test")
	request.Options.KeepOnFailure = true
	request.Options.JobTimeout = "0"
	r, err := h.cli.Run(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	waitComponentLog(t, h, r.Instance.ID, "job", "waiting", r.LeaseToken)
	deadline := time.Now().Add(30 * time.Second)
	var final v1.Instance
	for time.Now().Before(deadline) {
		if _, err := h.cli.Status(context.Background()); err != nil {
			t.Fatal("unrelated lease", err)
		}
		got, err := h.cli.Inspect(context.Background(), r.Instance.ID)
		if err != nil {
			t.Fatal("daemon unavailable", err)
		}
		final = got.Instance
		if final.Execution != nil && final.Execution.CompletedAt != "" {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if final.Execution == nil || !final.Execution.Cancelled || !final.Execution.Kept || final.Execution.CollectionFailure == "" || final.Execution.CleanupFailure != "" {
		t.Fatal("lease/collection outcome missing")
	}
	// Continue through subsequent sweeps, the point where the prior fatal error escaped.
	for range 10 {
		time.Sleep(50 * time.Millisecond)
		if _, err := h.cli.Status(context.Background()); err != nil {
			t.Fatal("daemon or unrelated owner lost", err)
		}
	}

	for _, id := range []string{r.Instance.ID, unrelated.Instance.ID} {
		got, err := h.cli.Inspect(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", got.Instance.Execution.Ports["port"]), time.Second)
		if err != nil {
			t.Fatal("healthy service lost", err)
		}
		_ = conn.Close()
		if _, err := h.cli.Destroy(context.Background(), id); err != nil {
			t.Fatal(err)
		}
	}
	h.absent()
}

func TestLeaseCollectorOutcomeDoesNotPoisonSweeper(t *testing.T) {
	f := newControlledExpiry(t)
	f.allowError = true // explicit shutdown still reports the retained per-instance gap.
	owner := f.owner(t, true, time.Millisecond)
	observed, _ := f.s.store.inspect(owner.id)
	result := &v1.ExecutionResult{Attempt: observed.Execution.Attempt, CollectionFailure: "application output collection failed", CompletedAt: time.Now().UTC().Format(time.RFC3339Nano), Cancelled: true}
	var cancelled sync.Once
	owner.entry.cancel = func() {
		cancelled.Do(func() {
			if _, err := f.s.store.execution(owner.id, v1.Failed, result); err != nil {
				panic(err)
			}
			close(owner.done)
		})
	}
	if err := f.s.expireOwners(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(15 * time.Second)
	for {
		f.s.mu.Lock()
		joined := len(f.s.expiryWorkers) == 0
		f.s.mu.Unlock()
		if joined {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("expiry worker did not join")
		}
		time.Sleep(time.Millisecond)
	}
	if err := f.s.expireOwners(); err != nil {
		t.Fatal("persisted collector failure became fatal", err)
	}
	got, err := f.s.store.inspect(owner.id)
	if err != nil || got.Execution.CollectionFailure != result.CollectionFailure {
		t.Fatal("collector outcome lost", err)
	}
}
