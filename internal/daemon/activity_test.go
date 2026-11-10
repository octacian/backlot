//go:build !windows

package daemon

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	v1 "github.com/octacian/backlot/api/v1"
	"github.com/octacian/backlot/internal/plan"
	bolt "go.etcd.io/bbolt"
)

func bulkRequest() v1.StopAllRequest { return v1.StopAllRequest{APIVersion: v1.Version, All: true} }
func checkBulk(t *testing.T, r v1.StopAllResponse, err error) {
	t.Helper()
	if err != nil || r.Validate() != nil {
		t.Fatal("invalid bulk result", r, err, r.Validate())
	}
}

func TestBulkAdmissionGeneration(t *testing.T) {
	s, _, _ := sceneFixture(t)
	ctx, finish, err := s.beginWorkload(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	joined := make(chan struct{})
	go func() { defer close(joined); <-ctx.Done(); finish() }()
	out, err := s.stopAll(context.Background(), bulkRequest())
	checkBulk(t, out, err)
	awaitControlled(t, joined, "admission owner not joined")
	if !out.AdmissionsJoined || !out.SelectionComplete || out.Error != nil {
		t.Fatal(out)
	}
	// Strip cancellation to prove that generation, independently of ctx.Err, is permanent.
	code(t, s.checkWorkload(context.WithoutCancel(ctx)), "admission_closed")
	next, done, err := s.beginWorkload(context.Background())
	if err != nil {
		t.Fatal("fence did not reopen", err)
	}
	defer done()
	if err := s.checkWorkload(next); err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	s.closing = true
	s.mu.Unlock()
	if _, _, err := s.beginWorkload(context.Background()); err == nil {
		t.Fatal("permanent shutdown reopened")
	}
}

func TestBulkRejectsWaitingWriters(t *testing.T) {
	for _, action := range []string{"prepare", "run", "attached", "scene", "restart", "reset"} {
		t.Run(action, func(t *testing.T) {
			s, q := attachedCoordinator(t)
			q.Attached = false
			if action == "restart" || action == "reset" {
				r, err := s.run(context.Background(), q)
				if err != nil {
					t.Fatal(err)
				}
				taskAdmissionReady(t, s, r.Instance.ID)
				q.InstanceID = r.Instance.ID
			}
			reached, release, joined := make(chan struct{}), make(chan struct{}), make(chan struct{})
			var once sync.Once
			s.resolve = func(source plan.Source, q v1.PlanRequest) (plan.Snapshot, error) {
				close(reached)
				<-release
				p, e := plan.Prepare(source, q)
				p.Plan.Components = nil
				return p, e
			}
			var writerErr error
			go func() {
				defer close(joined)
				switch action {
				case "prepare":
					_, writerErr = s.prepare(context.Background(), v1.PrepareRequest{APIVersion: v1.Version, Plan: q.Plan})
				case "attached":
					q.Attached = true
					_, writerErr = s.run(context.Background(), q)
				case "scene":
					_, writerErr = s.startScene(context.Background(), v1.SceneStartRequest{APIVersion: v1.Version, CWD: q.Plan.ProjectPath, Plan: q.Plan})
				default:
					_, writerErr = s.runMode(context.Background(), q, action == "reset")
				}
			}()
			t.Cleanup(func() { once.Do(func() { close(release) }); awaitControlled(t, joined, "writer not joined") })
			awaitControlled(t, reached, "writer did not resolve")
			// The owner callback releases input resolution only when the actual fence cancels it.
			s.mu.Lock()
			for owner := range s.mutationOwners {
				if owner.workload {
					original := owner.cancel
					owner.cancel = func() { original(); once.Do(func() { close(release) }) }
				}
			}
			s.mu.Unlock()
			out, err := s.stopAll(context.Background(), bulkRequest())
			checkBulk(t, out, err)
			awaitControlled(t, joined, "writer not joined")
			if writerErr == nil {
				t.Fatal("invalidated writer accepted")
			}
			code(t, writerErr, "admission_closed")
			if out.Error != nil {
				t.Fatal(out)
			}
		})
	}
}

func TestBulkBusyAndStaleTargets(t *testing.T) {
	s, q := attachedCoordinator(t)
	q.Attached = false
	first, err := s.run(context.Background(), q)
	if err != nil {
		t.Fatal(err)
	}
	taskAdmissionReady(t, s, first.Instance.ID)
	selected, err := s.selectBulk(context.Background())
	if err != nil || len(selected) != 1 {
		t.Fatal(selected, err)
	}
	lock := s.mutationLock(first.Instance.ID)
	lock.Lock()
	out, err := s.stopAll(context.Background(), bulkRequest())
	lock.Unlock()
	checkBulk(t, out, err)
	if out.Error == nil || out.Targets[0].Outcome != "unresolved" {
		t.Fatal("busy target claimed completed", out)
	}
	current, err := s.store.inspect(first.Instance.ID)
	if err != nil || current.Status != v1.RuntimeReady {
		t.Fatal("busy target mutated", err)
	}
	q.InstanceID = first.Instance.ID
	replacement, err := s.run(context.Background(), q)
	if err != nil {
		t.Fatal(err)
	}
	taskAdmissionReady(t, s, replacement.Instance.ID)
	old, budget := s.stopBulkTarget(context.Background(), selected[0].target)
	if budget || old.Outcome != "unresolved" {
		t.Fatal(old, budget)
	}
	current, err = s.store.inspect(first.Instance.ID)
	if err != nil || current.Execution.Attempt != replacement.Instance.Execution.Attempt || current.Status != v1.RuntimeReady {
		t.Fatal("stale cleanup touched successor", err)
	}
}

func TestBulkContinuingCleanupRetainsOwnerAndSerialization(t *testing.T) {
	s, q := attachedCoordinator(t)
	q.Attached = false
	r, err := s.run(context.Background(), q)
	if err != nil {
		t.Fatal(err)
	}
	taskAdmissionReady(t, s, r.Instance.ID)
	selected, err := s.selectBulk(context.Background())
	if err != nil || len(selected) != 1 {
		t.Fatal(err)
	}
	s.mu.Lock()
	entry := s.executions[r.Instance.ID]
	s.mu.Unlock()
	entry.mu.Lock()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	out, exhausted := s.stopBulkTarget(ctx, selected[0].target)
	cancel()
	if !exhausted || out.Outcome != "unresolved" {
		entry.mu.Unlock()
		t.Fatal(out, exhausted)
	}
	lock := s.mutationLock(r.Instance.ID)
	if lock.TryLock() {
		lock.Unlock()
		entry.mu.Unlock()
		t.Fatal("cleanup lost serialization")
	}
	s.mu.Lock()
	owners := len(s.mutationOwners)
	s.mu.Unlock()
	if owners != 1 {
		entry.mu.Unlock()
		t.Fatal("cleanup not registered", owners)
	}
	entry.mu.Unlock()
	s.waitOwners()
	if !lock.TryLock() {
		t.Fatal("completed cleanup retained lock")
	}
	lock.Unlock()
	final, err := s.store.inspect(r.Instance.ID)
	if err != nil || final.Execution.Settlement.State != v1.ConsumerSettled {
		t.Fatal(final, err)
	}
}

func TestBulkDisconnectedWaitingAndFence(t *testing.T) {
	s, q := attachedCoordinator(t)
	q.Attached = false
	r, err := s.run(context.Background(), q)
	if err != nil {
		t.Fatal(err)
	}
	taskAdmissionReady(t, s, r.Instance.ID)
	ctx, cancel := context.WithCancel(context.Background())
	// An admission owner retains coordination so client cancellation is deterministic.
	admission, finish, err := s.beginWorkload(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() { _, err := s.stopAll(ctx, bulkRequest()); result <- err }()
	<-admission.Done()
	s.mu.Lock()
	closed := s.bulkStopping
	s.mu.Unlock()
	if !closed {
		t.Fatal("missing fence")
	}
	if _, _, err := s.beginWorkload(context.Background()); err == nil {
		t.Fatal("new admission crossed fence")
	}
	if _, err := s.stopAll(context.Background(), bulkRequest()); err == nil {
		t.Fatal("overlapping bulk accepted")
	}
	cancel()
	code(t, <-result, "result_unknown")
	finish()
	s.waitOwners()
	final, err := s.store.inspect(r.Instance.ID)
	if err != nil || final.Execution.Settlement.State != v1.ConsumerSettled {
		t.Fatal("disconnect cancelled accepted cleanup", err)
	}
	s.mu.Lock()
	closed = s.bulkStopping
	s.mu.Unlock()
	if closed {
		t.Fatal("fence not released")
	}
}

func TestBulkCompleteSelectionBeforeStops(t *testing.T) {
	for _, failure := range []string{"overflow", "corrupt", "scan"} {
		t.Run(failure, func(t *testing.T) {
			s, q := attachedCoordinator(t)
			q.Attached = false
			r, err := s.run(context.Background(), q)
			if err != nil {
				t.Fatal(err)
			}
			taskAdmissionReady(t, s, r.Instance.ID)
			var added []string
			err = s.store.db.Update(func(tx *bolt.Tx) error {
				original, e := load(tx, r.Instance.ID)
				if e != nil {
					return e
				}
				count := v1.BulkStopTargetLimit
				if failure == "scan" {
					count = 10001
				}
				if failure == "corrupt" {
					count = 1
				}
				for range count {
					id := newID()
					added = append(added, id)
					if failure == "corrupt" {
						if err := tx.Bucket([]byte("instances")).Put([]byte(id), []byte("broken")); err != nil {
							return err
						}
						continue
					}
					clone := original
					clone.Instance.ID = id
					clone.Instance.Execution = nil
					clone.Instance.Status = v1.Preparing
					clone.Instance.Operation.Status = v1.Preparing
					if err := put(tx, "instances", id, clone); err != nil {
						return err
					}
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			// Fixture-only records authorize no external effects and are removed before ordinary shutdown.
			t.Cleanup(func() {
				if err := s.store.db.Update(func(tx *bolt.Tx) error {
					for _, id := range added {
						if err := tx.Bucket([]byte("instances")).Delete([]byte(id)); err != nil {
							return err
						}
					}
					return nil
				}); err != nil {
					t.Error(err)
				}
			})
			out, err := s.stopAll(context.Background(), bulkRequest())
			checkBulk(t, out, err)
			if out.SelectionComplete || len(out.Targets) != 0 || out.Error == nil {
				t.Fatal("partial-page all success", out)
			}
			current, err := s.store.inspect(r.Instance.ID)
			if failure == "corrupt" || failure == "scan" { // inspect itself scans all records; inspect after removing only fixture records.
				if err = s.store.db.Update(func(tx *bolt.Tx) error {
					for _, id := range added {
						if err := tx.Bucket([]byte("instances")).Delete([]byte(id)); err != nil {
							return err
						}
					}
					return nil
				}); err != nil {
					t.Fatal(err)
				}
				current, err = s.store.inspect(r.Instance.ID)
			}
			if err != nil || current.Status != v1.RuntimeReady {
				t.Fatal("selection refusal stopped earlier target", err)
			}
		})
	}
}

func TestActivityReadOnlyRecordedScopeAndPagination(t *testing.T) {
	s, root, config := sceneFixture(t)
	// Preparation without an execution is settled and skipped, but counts toward scans.
	for range 3 {
		if _, err := s.prepare(context.Background(), request(root, config, "test")); err != nil {
			t.Fatal(err)
		}
	}
	var ids []string
	for range 3 {
		r, err := s.prepare(context.Background(), request(root, config, "test"))
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, r.Instance.ID)
		if err := s.store.db.Update(func(tx *bolt.Tx) error {
			r, e := load(tx, r.Instance.ID)
			if e != nil {
				return e
			}
			r.Instance.Status = v1.Preparing
			r.Instance.Operation.Status = v1.Preparing
			return put(tx, "instances", r.Instance.ID, r)
		}); err != nil {
			t.Fatal(err)
		}
	}
	before := stateContents(t, s.store)
	if err := os.WriteFile(filepath.Join(root, "backlot.json"), []byte("broken manifest"), 0600); err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	after := ""
	for range 5 {
		page, err := s.activity(context.Background(), v1.ActivityRequest{APIVersion: v1.Version, After: after, Limit: 1})
		if err != nil || page.Validate() != nil {
			t.Fatal(page, err)
		}
		for _, e := range page.Entries {
			if seen[e.ID] || e.Owner != "absent" || e.Settlement.State != v1.ConsumerUnsettled {
				t.Fatal(e)
			}
			seen[e.ID] = true
		}
		if page.Complete {
			break
		}
		if page.NextAfter <= after {
			t.Fatal("pagination stalled")
		}
		after = page.NextAfter
	}
	if len(seen) != len(ids) || !reflect.DeepEqual(before, stateContents(t, s.store)) {
		t.Fatal("read skipped work or mutated state")
	}
	// Restore intentionally incomplete metadata, with no authority to launch or clean external work.
	if err := s.store.db.Update(func(tx *bolt.Tx) error {
		for _, id := range ids {
			r, e := load(tx, id)
			if e != nil {
				return e
			}
			r.Instance.Status = v1.Prepared
			r.Instance.Operation.Status = v1.Prepared
			if e := put(tx, "instances", id, r); e != nil {
				return e
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestActivityBudgetIncludesDependentScans(t *testing.T) {
	s, root, config := sceneFixture(t)
	prepared := scenePrepare(t, s, root, config)
	err := s.store.db.View(func(tx *bolt.Tx) error {
		r, err := load(tx, prepared.Instance.ID)
		if err != nil {
			return err
		}
		b := &scanBudget{ctx: context.Background(), scans: 9999}
		_, err = consumerEligibility(tx, r, "", b)
		if err == nil || b.err == nil {
			t.Fatal("dependent read escaped budget")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	b := &scanBudget{ctx: context.Background()}
	code(t, chargeScan([]*scanBudget{b}, 64<<20+1), "scope_too_large")
}

func TestBulkTaskAdmissionInvalidated(t *testing.T) {
	s, q, marker := taskAdmissionFixture(t)
	r, err := s.run(context.Background(), q)
	if err != nil {
		t.Fatal(err)
	}
	taskAdmissionReady(t, s, r.Instance.ID)
	reached, release, joined := make(chan struct{}), make(chan struct{}), make(chan struct{})
	s.beforeTaskMutation = func(string) { close(reached); <-release }
	var taskErr error
	go func() {
		defer close(joined)
		_, taskErr = s.runTask(context.Background(), v1.TaskRunRequest{APIVersion: v1.Version, Target: v1.SceneTarget{InstanceID: r.Instance.ID}, Task: "check"})
	}()
	awaitControlled(t, reached, "task not waiting")
	var once sync.Once
	t.Cleanup(func() { once.Do(func() { close(release) }); awaitControlled(t, joined, "task not joined") })
	s.mu.Lock()
	for owner := range s.mutationOwners {
		if owner.workload {
			original := owner.cancel
			owner.cancel = func() { original(); once.Do(func() { close(release) }) }
		}
	}
	s.mu.Unlock()
	out, err := s.stopAll(context.Background(), bulkRequest())
	checkBulk(t, out, err)
	awaitControlled(t, joined, "task not joined")
	if taskErr == nil {
		t.Fatal("task crossed bulk fence")
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("task launched", err)
	}
	tasks, err := s.store.dependentTasks(r.Instance.ID)
	if err != nil || len(tasks) != 0 {
		data, _ := json.Marshal(tasks)
		t.Fatal("task admitted", string(data), err)
	}
}

func TestBulkRegisteredOutputWaiterAndCrossProject(t *testing.T) {
	s, root, config := sceneFixture(t)
	t.Cleanup(func() {
		if err := s.stop(); err != nil {
			t.Error(err)
		}
	})
	// Hold an actual output admission lock before the registered execution can launch.
	path := filepath.Join(root, "backlot.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var m v1.Manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	m.Outputs = map[string]v1.Output{"report": {Path: "report", ConcurrencyGroup: "reports"}}
	for name, c := range m.Components {
		c.Outputs = []string{"report"}
		m.Components[name] = c
	}
	raw, err = json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	prepared := scenePrepare(t, s, root, config)
	held := make(chan struct{}, 1)
	held <- struct{}{}
	s.outputs = map[string]chan struct{}{prepared.Instance.Plan.Checkout + "/reports": held}
	first, err := s.run(context.Background(), v1.RunRequest{APIVersion: v1.Version, Plan: request(root, config, "dev").Plan})
	if err != nil {
		t.Fatal(err)
	}
	// Another recorded project is selected without rediscovering either source.
	other := shortTemp(t)
	git(t, other, "init", "-q")
	otherConfig := writeFixture(t, other)
	s.resolve = func(source plan.Source, q v1.PlanRequest) (plan.Snapshot, error) {
		p, e := plan.Prepare(source, q)
		p.Plan.Components = nil
		return p, e
	}
	second, err := s.run(context.Background(), v1.RunRequest{APIVersion: v1.Version, Plan: request(other, otherConfig, "dev").Plan})
	if err != nil {
		t.Fatal(err)
	}
	taskAdmissionReady(t, s, second.Instance.ID)
	activity, err := s.activity(context.Background(), v1.ActivityRequest{APIVersion: v1.Version})
	if err != nil || len(activity.Entries) != 2 {
		t.Fatal(activity, err)
	}
	out, err := s.stopAll(context.Background(), bulkRequest())
	checkBulk(t, out, err)
	if out.Error != nil || len(out.Targets) != 2 {
		t.Fatal(out)
	}
	queued, err := s.store.inspect(first.Instance.ID)
	if err != nil || queued.Status != v1.Stopped || !queued.Execution.Cancelled || len(queued.Execution.Components) != 0 || queued.Execution.TerminalExitCode != nil || queued.Execution.Settlement.State != v1.ConsumerSettled {
		t.Fatal("queued execution launched or lost cancellation", err)
	}
	if len(held) != 1 {
		t.Fatal("bulk released a peer's output lock")
	}
	<-held
	// Admission is open for a new attempt after this finite selection.
	q := v1.RunRequest{APIVersion: v1.Version, Plan: request(other, otherConfig, "dev").Plan}
	replacement, err := s.run(context.Background(), q)
	if err != nil {
		t.Fatal(err)
	}
	taskAdmissionReady(t, s, replacement.Instance.ID)
	if replacement.Instance.Execution.Attempt == second.Instance.Execution.Attempt {
		t.Fatal("old attempt revived")
	}
}

func TestActivityBulkBoundsAdmissionOwners(t *testing.T) {
	s, _, _ := sceneFixture(t)
	s.mutationOwners = map[*mutationOwner]struct{}{}
	// No goroutines or effects: these fixture registrations only exercise the acceptance cap.
	for range 10001 {
		s.mutationOwners[&mutationOwner{cancel: func() {}, done: make(chan struct{})}] = struct{}{}
	}
	before := s.admissionGeneration
	_, err := s.stopAll(context.Background(), bulkRequest())
	code(t, err, "scope_too_large")
	if s.bulkStopping || s.admissionGeneration != before || len(s.mutationOwners) != 10001 {
		t.Fatal("rejected bulk changed admission")
	}
	_, err = s.activity(context.Background(), v1.ActivityRequest{APIVersion: v1.Version})
	code(t, err, "scope_too_large")
	s.mutationOwners = nil
}

func TestBulkInvalidatesInstanceLockWaiterAfterReopen(t *testing.T) {
	s, q := attachedCoordinator(t)
	q.Attached = false
	first, err := s.run(context.Background(), q)
	if err != nil {
		t.Fatal(err)
	}
	taskAdmissionReady(t, s, first.Instance.ID)
	lock := s.mutationLock(first.Instance.ID)
	lock.Lock()
	var unlock sync.Once
	reached, joined := make(chan struct{}), make(chan struct{})
	s.beforeExecution = func(string) { close(reached) }
	var oldErr error
	t.Cleanup(func() { unlock.Do(lock.Unlock); awaitControlled(t, joined, "old admission caller not joined") })
	go func() { defer close(joined); _, oldErr = s.run(context.Background(), q) }()
	awaitControlled(t, reached, "old caller did not reach instance serialization")
	out, err := s.stopAll(context.Background(), bulkRequest())
	checkBulk(t, out, err)
	if out.AdmissionsJoined || out.Error == nil || len(out.Targets) != 1 || out.Targets[0].Outcome != "unresolved" {
		t.Fatal("busy admission was claimed joined", out)
	}
	s.mu.Lock()
	reopened := !s.bulkStopping
	s.mu.Unlock()
	if !reopened {
		t.Fatal("fence did not settle")
	}
	unlock.Do(lock.Unlock)
	awaitControlled(t, joined, "old caller revived or remained blocked")
	code(t, oldErr, "admission_closed")
	current, err := s.store.inspect(first.Instance.ID)
	if err != nil || current.Status != v1.RuntimeReady || current.Execution.Attempt != first.Instance.Execution.Attempt {
		t.Fatal("old waiter affected current runtime", err)
	}
}
