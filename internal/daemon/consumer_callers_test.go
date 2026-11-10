//go:build darwin || linux

package daemon

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	v1 "github.com/octacian/backlot/api/v1"
	"github.com/octacian/backlot/internal/native"
	"github.com/octacian/backlot/internal/plan"
	bolt "go.etcd.io/bbolt"
)

func TestStopRetriesJoinedParentAfterDependentSettles(t *testing.T) {
	f, r, j, g := settledConsumerFixture(t)
	j.Groups = nil
	r.Instance.Execution.CompletedAt = time.Now().UTC().Format(time.RFC3339Nano)
	r.Instance.Execution.CleanupFailure = "dependent cleanup was unverified"
	saveConsumerFixture(t, f, r, j, g)
	child := r
	child.Instance.ID, child.Instance.Plan.Lifetime = newID(), v1.Disposable
	child.Instance.Task = &v1.TaskBinding{InstanceID: r.Instance.ID, Attempt: j.Attempt, Generation: g.ID, Name: "job"}
	child.Instance.Execution = &v1.ExecutionResult{Attempt: newID(), Outcome: v1.Succeeded, CompletedAt: r.Instance.Execution.CompletedAt}
	cj := runtimeRecord{Attempt: child.Instance.Execution.Attempt}
	saveConsumerFixture(t, f, child, cj, resourceGeneration{})
	done := make(chan struct{})
	close(done)
	retained := problem("cleanup_failed", "joined owner could not settle its dependent")
	f.s.executions = map[string]*execution{r.Instance.ID: {attempt: j.Attempt, done: done, cancel: func() {}, err: retained}}
	if _, err := f.s.stopExecution(r.Instance.ID); err == nil {
		t.Fatal("unverified dependent did not fence parent")
	}
	// The fixture's known zero-launch child owner now publishes its independent
	// proof. Parent retry must reload after its own retained teardown error.
	if err := f.s.store.recordConsumerRelease(context.Background(), child.Instance.ID, cj, v1.SettlementExplicitStop, true); err != nil {
		t.Fatal(err)
	}
	got, err := f.s.stopExecution(r.Instance.ID)
	if err != nil || got.ID != r.Instance.ID || got.Execution == nil || got.Execution.Attempt != j.Attempt || got.Execution.Settlement.State != v1.ConsumerSettled {
		t.Fatal("joined cleanup retry failed", got.ID, err)
	}
	if got.Execution.Outcome != r.Instance.Execution.Outcome || got.Execution.CompletedAt != r.Instance.Execution.CompletedAt || got.Execution.CollectionFailure != r.Instance.Execution.CollectionFailure {
		t.Fatal("retry rewrote parent history")
	}
	childResult, err := f.s.store.inspect(child.Instance.ID)
	if err != nil || childResult.Execution.Settlement.State != v1.ConsumerSettled {
		t.Fatal("parent bypassed dependent proof", err)
	}
}

func TestStopRetainsNonretryableJoinError(t *testing.T) {
	for _, mode := range []string{"missing-execution", "changed-attempt", "other-error"} {
		t.Run(mode, func(t *testing.T) {
			f, r, j, g := settledConsumerFixture(t)
			j.Groups = nil
			if mode == "missing-execution" {
				r.Instance.Execution = nil
				r.Instance.Status = v1.Prepared
				r.Instance.Operation.Status = v1.Prepared
			}
			saveConsumerFixture(t, f, r, j, g)
			done := make(chan struct{})
			close(done)
			attempt := j.Attempt
			if mode == "changed-attempt" {
				attempt = newID()
			}
			failure := problem("cleanup_failed", "retained owner failure")
			if mode == "other-error" {
				failure = problem("owned_failure", "nonretryable owner failure")
			}
			f.s.executions = map[string]*execution{r.Instance.ID: {attempt: attempt, done: done, cancel: func() {}, err: failure}}
			before := stateContents(t, f.s.store)
			got, err := f.s.stopExecution(r.Instance.ID)
			if !errors.Is(err, failure) {
				t.Fatal("original join error lost", err)
			}
			if mode != "other-error" && got.ID != r.Instance.ID {
				t.Fatal("selected identity lost")
			}
			if !reflect.DeepEqual(before, stateContents(t, f.s.store)) {
				t.Fatal("nonretryable join altered durable state")
			}
		})
	}
}

func TestUnknownTerminalConsumerFencesOutputClearing(t *testing.T) {
	for _, kind := range []string{"scene", "task"} {
		t.Run(kind, func(t *testing.T) {
			f, r, j, g := settledConsumerFixture(t)
			boot, err := native.CurrentBoot()
			if err != nil {
				t.Fatal(err)
			}
			j.Groups = nil
			j.LaunchPending = true
			j.PendingBoot = boot
			r.Instance.Status, r.Instance.Operation.Status = v1.Succeeded, v1.Succeeded
			r.Instance.Execution.NativeCleanup[0].Confidence = v1.CleanupAuthorityLost
			r.Instance.Execution.NativeCleanup[0].Reason = "guardian_authority_lost"
			r.Instance.Plan.Outputs = map[string]v1.Output{"held": {Path: "out", ConcurrencyGroup: "held", EmptyBeforeExecution: true}}
			if kind == "task" {
				var parent record
				if err := f.s.store.db.View(func(tx *bolt.Tx) error { var err error; parent, err = load(tx, f.instance.ID); return err }); err != nil {
					t.Fatal(err)
				}
				parent.Instance.Execution = &v1.ExecutionResult{Attempt: newID()}
				parent.Instance.Status, parent.Instance.Operation.Status = v1.RuntimeReady, v1.RuntimeReady
				saveConsumerFixture(t, f, parent, runtimeRecord{Attempt: parent.Instance.Execution.Attempt}, resourceGeneration{})
				r.Instance.ID, r.Instance.Plan.Lifetime = newID(), v1.Disposable
				r.Instance.Task = &v1.TaskBinding{InstanceID: parent.Instance.ID, Attempt: parent.Instance.Execution.Attempt, Generation: parent.Instance.Execution.Attempt, Name: "job"}
				g = resourceGeneration{}
			}
			j.OutputPaths, err = outputPaths(r.Instance.Plan)
			if err != nil {
				t.Fatal(err)
			}
			saveConsumerFixture(t, f, r, j, g)
			sentinel := filepath.Join(f.project, "out", "keep")
			if err := os.MkdirAll(filepath.Dir(sentinel), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(sentinel, []byte("retained output"), 0600); err != nil {
				t.Fatal(err)
			}
			source, err := plan.Locate(f.project)
			if err != nil {
				t.Fatal(err)
			}
			identity, err := captureSource(source)
			if err != nil {
				t.Fatal(err)
			}
			next := plan.Snapshot{Plan: r.Instance.Plan, Manifest: f.manifest}
			next.Plan.Outputs = map[string]v1.Output{"new": {Path: "out", ConcurrencyGroup: "held", EmptyBeforeExecution: true}}
			next.Manifest.Outputs = next.Plan.Outputs
			before := stateContents(t, f.s.store)
			// Same group and changed-group aliases both retain the unknown reservation.
			for _, group := range []string{"held", "renamed"} {
				target := next.Plan
				target.Outputs = map[string]v1.Output{"new": {Path: "out", ConcurrencyGroup: group, EmptyBeforeExecution: true}}
				paths, err := outputPaths(target)
				if err != nil {
					t.Fatal(err)
				}
				err = f.s.store.db.View(func(tx *bolt.Tx) error { return outputConflictTx(tx, target, paths, "", true, boot) })
				code(t, err, "output_conflict")
			}
			code(t, f.s.prepareOutputDirectories(context.Background(), next, identity, ""), "output_conflict")
			if data, err := os.ReadFile(sentinel); err != nil || string(data) != "retained output" {
				t.Fatal("unknown consumer output erased", err)
			}
			if !reflect.DeepEqual(before, stateContents(t, f.s.store)) {
				t.Fatal("reservation read changed proof")
			}
			unrelated := next
			unrelated.Plan.Outputs = map[string]v1.Output{"other": {Path: "other", ConcurrencyGroup: "other", EmptyBeforeExecution: true}}
			unrelated.Manifest.Outputs = unrelated.Plan.Outputs
			if err := f.s.prepareOutputDirectories(context.Background(), unrelated, identity, ""); err != nil {
				t.Fatal("unrelated output fenced", err)
			}
			// This fixture owns an unactivated launch intent. Its joined zero-launch
			// owner can clear that intent and publish real current evidence; history stays.
			j.LaunchPending, j.PendingBoot = false, ""
			if err := f.s.store.saveRuntime(r.Instance.ID, j); err != nil {
				t.Fatal(err)
			}
			if err := f.s.store.recordConsumerRelease(context.Background(), r.Instance.ID, j, v1.SettlementExplicitStop, true); err != nil {
				t.Fatal(err)
			}
			current, err := f.s.store.inspect(r.Instance.ID)
			if err != nil || current.Execution.Settlement.State != v1.ConsumerSettled || current.Execution.NativeCleanup[0].Confidence != v1.CleanupAuthorityLost {
				t.Fatal("current proof failed to preserve/release historical confidence", err)
			}
			if err := f.s.prepareOutputDirectories(context.Background(), next, identity, ""); err != nil {
				t.Fatal("settled output remained fenced", err)
			}
			if _, err := os.Stat(sentinel); !os.IsNotExist(err) {
				t.Fatal("verified release did not permit selected clearing", err)
			}
		})
	}
}

func TestAcceptedExecutionOutputSelfExclusion(t *testing.T) {
	for _, tc := range []struct{ kind, other string }{{"scene", ""}, {"scene", "unknown"}, {"task", ""}, {"task", "sibling"}, {"task", "parent"}} {
		t.Run(tc.kind+"/"+tc.other, func(t *testing.T) {
			f, r, j, g := settledConsumerFixture(t)
			j.Groups = nil
			r.Instance.Status, r.Instance.Operation.Status = v1.Stopped, v1.Stopped
			r.Instance.Execution = &v1.ExecutionResult{Attempt: j.Attempt}
			outputs := map[string]v1.Output{"out": {Path: "out", ConcurrencyGroup: "out", EmptyBeforeExecution: true}}
			var parent record
			if tc.kind == "task" {
				parent = r
				parent.Instance.Status, parent.Instance.Operation.Status = v1.RuntimeReady, v1.RuntimeReady
				saveConsumerFixture(t, f, parent, j, g)
				r.Instance.ID, r.Instance.Plan.Lifetime = newID(), v1.Disposable
				r.Instance.Task = &v1.TaskBinding{InstanceID: parent.Instance.ID, Attempt: j.Attempt, Generation: g.ID, Name: "job"}
				g = resourceGeneration{}
			}
			r.Instance.Plan.Outputs = outputs
			var err error
			j.OutputPaths, err = outputPaths(r.Instance.Plan)
			if err != nil {
				t.Fatal(err)
			}
			if tc.kind == "scene" {
				saveConsumerFixture(t, f, r, j, g)
				j.Attempt = newID()
				r.Instance.Execution = &v1.ExecutionResult{Attempt: j.Attempt}
				accepted, err := f.s.store.acceptExecutionSnapshot(r.Instance.ID, j, r.Instance.Execution, true, nil, nil)
				if err != nil || accepted.Status != v1.Starting {
					t.Fatal("actual scene acceptance failed", err)
				}
				if err := f.s.store.outputConflict(accepted.Plan); err != nil {
					t.Fatal("pre-lock self preflight failed", err)
				}
			} else {
				// Borrowed tasks are accepted by runTask's transaction, not scene restart.
				r.Instance.Status, r.Instance.Operation.Status = v1.Starting, v1.Starting
				r.Instance.Execution = &v1.ExecutionResult{Attempt: newID()}
				j.Attempt = r.Instance.Execution.Attempt
				saveConsumerFixture(t, f, r, j, g)
			}
			if tc.other != "" {
				if tc.other == "parent" {
					parent.Instance.Plan.Outputs = outputs
					pj, err := f.s.store.runtime(parent.Instance.ID)
					if err != nil {
						t.Fatal(err)
					}
					pg, err := f.s.store.resources(parent.Instance.ID)
					if err != nil {
						t.Fatal(err)
					}
					saveConsumerFixture(t, f, parent, pj, pg)
				} else {
					other := r
					other.Instance.ID, other.Instance.Operation.ID = newID(), newID()
					other.Instance.Status, other.Instance.Operation.Status = v1.Interrupted, v1.Interrupted
					other.Instance.Execution = &v1.ExecutionResult{Attempt: newID()}
					boot, err := native.CurrentBoot()
					if err != nil {
						t.Fatal(err)
					}
					saveConsumerFixture(t, f, other, runtimeRecord{Attempt: other.Instance.Execution.Attempt, LaunchPending: true, PendingBoot: boot, OutputPaths: j.OutputPaths}, resourceGeneration{})
					inspected, err := f.s.store.inspect(other.Instance.ID)
					if err != nil || inspected.Execution.Settlement.State != v1.ConsumerUnknown {
						t.Fatal("other reservation must remain unknown", err)
					}
				}
			}
			source, err := plan.Locate(f.project)
			if err != nil {
				t.Fatal(err)
			}
			identity, err := captureSource(source)
			if err != nil {
				t.Fatal(err)
			}
			snapshot := plan.Snapshot{Plan: r.Instance.Plan, Manifest: f.manifest}
			snapshot.Manifest.Outputs = outputs
			sentinel := filepath.Join(f.project, "out", "old")
			if err := os.MkdirAll(filepath.Dir(sentinel), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(sentinel, []byte("owned output"), 0600); err != nil {
				t.Fatal(err)
			}
			before := stateContents(t, f.s.store)
			// This is execute's corrected contract for scenes AND tasks: exact accepted ID.
			err = f.s.prepareOutputDirectories(context.Background(), snapshot, identity, r.Instance.ID)
			if tc.other != "" {
				code(t, err, "output_conflict")
				if data, err := os.ReadFile(sentinel); err != nil || string(data) != "owned output" {
					t.Fatal("other owner's fence was bypassed", err)
				}
			} else {
				if err != nil {
					t.Fatal("accepted execution rejected its own reservation", err)
				}
				if _, err := os.Stat(sentinel); !os.IsNotExist(err) {
					t.Fatal("selected output was not cleared", err)
				}
			}
			if !reflect.DeepEqual(before, stateContents(t, f.s.store)) {
				t.Fatal("output clearing altered execution/proof records")
			}
		})
	}
}

func TestStopJoinsOwnerThatBecomesKept(t *testing.T) {
	s, instance, _ := exportFixture(t)
	result := *instance.Execution
	result.CompletedAt, result.Outcome = "", ""
	result.CollectionFailure, result.TerminalExitCode = "", nil
	if _, err := s.store.execution(instance.ID, v1.Starting, &result); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	entry := &execution{attempt: result.Attempt, done: done}
	entry.cancel = func() {
		go func() {
			result.Cancelled, result.Kept = true, true
			result.CompletedAt = time.Now().UTC().Format(time.RFC3339Nano)
			if _, err := s.store.execution(instance.ID, v1.Cancelled, &result); err != nil {
				panic(err)
			}
			entry.mu.Lock()
			entry.kept = true
			entry.mu.Unlock()
			close(done)
		}()
	}
	s.executions = map[string]*execution{instance.ID: entry}
	got, err := s.stopExecution(instance.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != v1.Cancelled || got.Execution.Outcome != v1.Cancelled || !got.Execution.Cancelled || !got.Execution.Kept || got.Execution.CollectionFailure != "" || got.Execution.CompletedAt != result.CompletedAt || got.Execution.Settlement.State != v1.ConsumerUnsettled {
		t.Fatal("joined cancellation lost investigation retention or fabricated interruption", got.Execution)
	}
	got, err = s.stopExecution(instance.ID)
	if err != nil || got.Execution.Kept || got.Status != v1.Cancelled || got.Execution.Outcome != v1.Cancelled || got.Execution.CollectionFailure != "" || got.Execution.CompletedAt != result.CompletedAt || got.Execution.Settlement.State != v1.ConsumerSettled {
		t.Fatal("subsequent stop failed to release retained cancellation", err)
	}
}
