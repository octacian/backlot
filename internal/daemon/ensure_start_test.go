//go:build darwin || linux

package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	v1 "github.com/octacian/backlot/api/v1"
	"github.com/octacian/backlot/internal/native"
	"github.com/octacian/backlot/internal/plan"
	bolt "go.etcd.io/bbolt"
)

func TestEnsureStartQueuedAcceptance(t *testing.T) {
	for _, mode := range []string{"terminal-current", "pruned-current", "replaced", "attached-overturned"} {
		t.Run(mode, func(t *testing.T) {
			s, request := attachedCoordinator(t)
			request.Attached = false
			prepared, err := s.prepare(context.Background(), v1.PrepareRequest{APIVersion: v1.Version, Plan: request.Plan})
			if err != nil {
				t.Fatal(err)
			}
			id := prepared.Instance.ID
			a := acceptHistory(t, s, id, v1.Failed, time.Second)
			// An overlapping output owned by another scene makes the real executor fail
			// before any native launch. The existing coordinator fixture owns cleanup.
			blocker := prepared.Instance
			blocker.ID = newID()
			blocker.Plan.Scene = "blocker"
			blocker.Plan.Outputs = map[string]v1.Output{"output": {Path: "out", ConcurrencyGroup: "other"}}
			blocker.Status, blocker.Operation.Status = v1.RuntimeReady, v1.RuntimeReady
			blocker.Execution = &v1.ExecutionResult{Attempt: newID(), Components: []v1.ComponentResult{}}
			if err := s.store.db.Update(func(tx *bolt.Tx) error {
				if err := put(tx, "instances", blocker.ID, record{Instance: blocker}); err != nil {
					return err
				}
				return tx.Bucket([]byte("persistent")).Put([]byte(sceneSlot(blocker)), []byte(blocker.ID))
			}); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if _, err := s.store.execution(blocker.ID, v1.Stopped, blocker.Execution); err != nil {
					t.Error(err)
				}
			})
			s.resolve = func(source plan.Source, request v1.PlanRequest) (plan.Snapshot, error) {
				snap, err := plan.Prepare(source, request)
				snap.Plan.Components = nil
				snap.Plan.Outputs = map[string]v1.Output{"output": {Path: "out", ConcurrencyGroup: "selected"}}
				return snap, err
			}
			reached, release, joined := make(chan struct{}), make(chan struct{}), make(chan struct{})
			var once sync.Once
			var calls atomic.Int32
			s.beforeExecution = func(string) {
				if calls.Add(1) == 1 {
					close(reached)
					<-release
				}
			}
			var queued v1.InstanceResponse
			var queuedErr error
			queuedRequest := request
			queuedRequest.Attached = mode == "attached-overturned"
			go func() { defer close(joined); queued, queuedErr = s.run(context.Background(), queuedRequest) }()
			t.Cleanup(func() { once.Do(func() { close(release) }); awaitControlled(t, joined, "queued caller not joined") })
			select {
			case <-reached:
			case <-joined:
				t.Fatalf("queued caller rejected before observation: %v", queuedErr)
			case <-time.After(time.Second):
				t.Fatal("queued caller did not reach barrier")
			}
			b, err := s.run(context.Background(), request)
			if err != nil {
				t.Fatal(err)
			}
			s.mu.Lock()
			bOwner := s.executions[id]
			s.mu.Unlock()
			awaitControlled(t, bOwner.done, "B failure/cleanup not joined")
			b.Instance, err = s.store.inspect(id)
			if err != nil || b.Instance.Status != v1.Failed || b.Instance.Execution.CleanupFailure != "" || b.Instance.Execution.CompletedAt == "" {
				t.Fatal("B did not fail cleanly", err)
			}
			// Release the conflicting output owner before a genuine later invocation.
			blocker.Execution.CompletedAt = time.Now().UTC().Format(time.RFC3339Nano)
			if _, err := s.store.execution(blocker.ID, v1.Stopped, blocker.Execution); err != nil {
				t.Fatal(err)
			}
			if mode == "pruned-current" {
				keep := 0
				_, err := s.pruneHistory(context.Background(), v1.HistoryPruneRequest{APIVersion: v1.Version, InstanceID: id, Keep: &keep})
				if err != nil {
					t.Fatal(err)
				}
				_, err = s.store.attempt(v1.AttemptRequest{InstanceID: id, Attempt: b.Instance.Execution.Attempt})
				code(t, err, "attempt_unavailable")
			}
			var c v1.InstanceResponse
			if mode == "replaced" {
				c, err = s.run(context.Background(), request)
				if err != nil || c.Instance.Execution.Attempt == b.Instance.Execution.Attempt {
					t.Fatal("later call did not accept C", err)
				}
			}
			once.Do(func() { close(release) })
			awaitControlled(t, joined, "queued caller did not finish")
			if mode == "replaced" || mode == "attached-overturned" {
				if mode == "attached-overturned" {
					code(t, queuedErr, "conflict")
				} else {
					code(t, queuedErr, "startup_conflict")
				}
				if mode != "attached-overturned" && (!strings.Contains(queuedErr.Error(), id) || !strings.Contains(queuedErr.Error(), b.Instance.Execution.Attempt)) {
					t.Fatal("conflict lost exact public identity", queuedErr)
				}
			} else if queuedErr != nil || queued.Instance.Execution.Attempt != b.Instance.Execution.Attempt || queued.Instance.Status != v1.Failed {
				t.Fatal("queued A did not return coherent terminal B", queuedErr)
			}
			current, err := s.store.inspect(id)
			if err != nil {
				t.Fatal(err)
			}
			wantAttempt := b.Instance.Execution.Attempt
			if mode == "replaced" {
				wantAttempt = c.Instance.Execution.Attempt
			}
			if current.Execution.Attempt != wantAttempt {
				t.Fatal("queued caller replaced current attempt")
			}
			var observed startObservation
			if err := s.store.db.View(func(tx *bolt.Tx) error { var err error; observed, err = observeStart(tx, current); return err }); err != nil {
				t.Fatal(err)
			}
			want := a.Sequence + 1
			if mode == "replaced" {
				want++
			}
			if observed.sequence != want {
				t.Fatal("extra acceptance", observed.sequence, want)
			}
			if mode == "replaced" {
				s.mu.Lock()
				cOwner := s.executions[id]
				s.mu.Unlock()
				select {
				case <-cOwner.done:
					t.Fatal("queued A cancelled C")
				default:
				}
				t.Logf("A=%s B=%s C=%s queued=startup_conflict highwater=%d; no D, C owner untouched", a.Result.Attempt, b.Instance.Execution.Attempt, c.Instance.Execution.Attempt, observed.sequence)
			} else {
				t.Logf("A=%s B=%s queued=%s highwater=%d", a.Result.Attempt, b.Instance.Execution.Attempt, mode, observed.sequence)
			}
		})
	}
}

func TestEnsureStartCancellationPreconditionBeforeEffects(t *testing.T) {
	s, request := attachedCoordinator(t)
	request.Attached = false
	b, err := s.run(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.stopRuntimeRequest(context.Background(), v1.StopRequest{APIVersion: v1.Version, InstanceID: b.Instance.ID}); err != nil {
		t.Fatal(err)
	}
	c, err := s.run(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	owner := s.executions[c.Instance.ID]
	s.mu.Unlock()
	var cancellations atomic.Int32
	s.store.sharedCheckpoint = func(point string) error {
		if point == "cancel-effect" {
			cancellations.Add(1)
		}
		return nil
	}
	_, err = s.stopRuntimeRequest(context.Background(), v1.StopRequest{APIVersion: v1.Version, InstanceID: b.Instance.ID, ExpectedAttempt: b.Instance.Execution.Attempt})
	code(t, err, "conflict")
	if cancellations.Load() != 0 {
		t.Fatal("stale pinned cancellation reached effects")
	}
	select {
	case <-owner.done:
		t.Fatal("C owner stopped")
	default:
	}
	current, err := s.store.inspect(c.Instance.ID)
	if err != nil || current.Execution.Attempt != c.Instance.Execution.Attempt {
		t.Fatal("C changed", err)
	}
	// The same ID-only operator stop remains intentionally authoritative.
	if _, err := s.stopRuntimeRequest(context.Background(), v1.StopRequest{APIVersion: v1.Version, InstanceID: c.Instance.ID}); err != nil {
		t.Fatal(err)
	}
	if cancellations.Load() != 1 {
		t.Fatal("operator stop semantics changed")
	}
}

func TestEnsureStartObservedBindingAndAtomicRejection(t *testing.T) {
	for _, mode := range []string{"starting-replaced-pruned", "starting-terminal-pruned", "destroyed-selected", "revision-rejected", "status-rejected"} {
		t.Run(mode, func(t *testing.T) {
			s, root, config := sceneFixture(t)
			response := scenePrepare(t, s, root, config)
			id := response.Instance.ID
			b := acceptHistory(t, s, id, v1.Failed, time.Second)
			if strings.HasPrefix(mode, "starting-") {
				if _, err := s.store.execution(id, v1.Starting, &b.Result); err != nil {
					t.Fatal(err)
				}
			}
			var c v1.Attempt
			var replacementID string
			s.beforeExecution = func(string) {
				if mode == "destroyed-selected" {
					if _, err := s.destroy(context.Background(), id); err != nil {
						t.Fatal(err)
					}
					next := scenePrepare(t, s, root, config)
					replacementID = next.Instance.ID
					return
				}
				if _, err := s.store.execution(id, v1.Failed, &b.Result); err != nil {
					t.Fatal(err)
				}
				if mode == "starting-replaced-pruned" {
					c = acceptHistory(t, s, id, v1.Failed, time.Second)
				}
				if err := s.store.db.Update(func(tx *bolt.Tx) error {
					return tx.Bucket([]byte("attempts")).Delete([]byte(attemptKey(id, b.Sequence)))
				}); err != nil {
					t.Fatal(err)
				}
			}
			request := v1.RunRequest{APIVersion: v1.Version, Plan: request(root, config, "dev").Plan}
			if mode == "revision-rejected" || mode == "status-rejected" {
				var observed startObservation
				current, _ := s.store.inspect(id)
				if err := s.store.db.View(func(tx *bolt.Tx) error { var err error; observed, err = observeStart(tx, current); return err }); err != nil {
					t.Fatal(err)
				}
				candidate, err := s.store.snapshot(id)
				if err != nil {
					t.Fatal(err)
				}
				candidate.Plan.ManifestDigest = "rejected-candidate"
				candidate.Secrets = map[string]v1.PlannedValue{"rejected": {}}
				if mode == "revision-rejected" {
					c = acceptHistory(t, s, id, v1.Failed, time.Second)
				} else {
					if _, err := s.store.execution(id, v1.Succeeded, &b.Result); err != nil {
						t.Fatal(err)
					}
				}
				candidateAttempt := newID()
				_, err = s.store.acceptExecutionSnapshot(id, runtimeRecord{Attempt: candidateAttempt}, &v1.ExecutionResult{Attempt: candidateAttempt}, true, &candidate, &observed)
				code(t, err, "startup_conflict")
				retained, err := s.store.snapshot(id)
				if err != nil || retained.Plan.ManifestDigest == candidate.Plan.ManifestDigest {
					t.Fatal("rejected acceptance published candidate provenance", err)
				}
				if _, ok := retained.Secrets["rejected"]; ok {
					t.Fatal("rejected acceptance published candidate secrets")
				}
				return
			}
			got, err := s.run(context.Background(), request)
			if mode == "starting-terminal-pruned" {
				if err != nil || got.Instance.Execution.Attempt != b.Result.Attempt || got.Instance.Status != v1.Failed {
					t.Fatal("lost terminal bound B", err)
				}
			} else {
				code(t, err, "startup_conflict")
				if mode == "starting-replaced-pruned" {
					if !strings.Contains(err.Error(), b.Result.Attempt) {
						t.Fatal("known B identity required a pruned history lookup", err)
					}
					current, _ := s.store.inspect(id)
					if current.Execution.Attempt != c.Result.Attempt {
						t.Fatal("redirected replacement")
					}
				} else {
					if replacementID == id || replacementID == "" {
						t.Fatal("new named identity missing")
					}
					current, _ := s.store.inspect(replacementID)
					if current.Execution != nil {
						t.Fatal("old selection redirected to new identity")
					}
				}
			}
		})
	}
}

func TestEnsureStartConflictWireClassification(t *testing.T) {
	id, attempt := newID(), newID()
	recorder := httptest.NewRecorder()
	writeError(recorder, startConflict(id, attempt))
	var response v1.ErrorResponse
	if recorder.Code != http.StatusConflict || json.Unmarshal(recorder.Body.Bytes(), &response) != nil || response.Error.Code != "startup_conflict" || !strings.Contains(response.Error.Message, id) || !strings.Contains(response.Error.Message, attempt) || !strings.Contains(response.Error.Message, "history/logs/export") {
		t.Fatal("startup conflict lost public binding or classification", recorder.Code, recorder.Body.String())
	}
}

func TestEnsureStartEligibleRecoveryAndFences(t *testing.T) {
	for _, condition := range []string{"cancelled", "interrupted", "collection-only", "uncertain-native", "unfinished-preparation"} {
		t.Run(condition, func(t *testing.T) {
			s, request := attachedCoordinator(t)
			request.Attached = false
			prepared, err := s.prepare(context.Background(), v1.PrepareRequest{APIVersion: v1.Version, Plan: request.Plan})
			if err != nil {
				t.Fatal(err)
			}
			id := prepared.Instance.ID
			if condition == "unfinished-preparation" {
				if _, err := s.store.execution(id, v1.Preparing, nil); err != nil {
					t.Fatal(err)
				}
				_, err := s.run(context.Background(), request)
				code(t, err, "conflict")
				current, _ := s.store.inspect(id)
				if current.Execution != nil || len(s.executions) != 0 {
					t.Fatal("unfinished preparation authorized runtime")
				}
				return
			}
			status := v1.Cancelled
			if condition == "interrupted" {
				status = v1.Interrupted
			}
			a := acceptHistory(t, s, id, status, time.Second)
			if condition == "collection-only" {
				a.Result.CollectionFailure = "fixture collection gap"
				if _, err := s.store.execution(id, v1.Failed, &a.Result); err != nil {
					t.Fatal(err)
				}
				done := make(chan struct{})
				close(done)
				s.executions = map[string]*execution{id: {attempt: a.Result.Attempt, done: done, cancel: func() {}}}
			}
			before := storedBytes(t, s.store, "snapshots", id)
			if condition == "uncertain-native" {
				boot, err := native.CurrentBoot()
				if err != nil {
					t.Fatal(err)
				}
				a.Result.CleanupFailure = "fixture unresolved launch intent"
				if _, err := s.store.execution(id, v1.Failed, &a.Result); err != nil {
					t.Fatal(err)
				}
				if err := s.store.saveRuntime(id, runtimeRecord{Attempt: a.Result.Attempt, LaunchPending: true, PendingBoot: boot}); err != nil {
					t.Fatal(err)
				}
				// This fixture records no OS effect; clear only its synthetic intent at teardown.
				t.Cleanup(func() {
					if err := s.store.saveRuntime(id, runtimeRecord{Attempt: a.Result.Attempt}); err != nil {
						t.Error(err)
					}
					a.Result.CleanupFailure = ""
					if _, err := s.store.execution(id, v1.Failed, &a.Result); err != nil {
						t.Error(err)
					}
				})
			}
			got, err := s.run(context.Background(), request)
			if condition == "uncertain-native" {
				code(t, err, "cleanup_failed")
				current, _ := s.store.inspect(id)
				if current.Execution.Attempt != a.Result.Attempt || !bytes.Equal(before, storedBytes(t, s.store, "snapshots", id)) {
					t.Fatal("uncertain cleanup replaced attempt/provenance")
				}
				return
			}
			if err != nil || got.Instance.ID != id || got.Instance.Execution.Attempt == a.Result.Attempt {
				t.Fatal("eligible explicit start did not accept next attempt", err)
			}
			prior, err := s.store.attempt(v1.AttemptRequest{InstanceID: id, Attempt: a.Result.Attempt})
			if err != nil || prior.Attempt.Result.CollectionFailure != a.Result.CollectionFailure || got.Instance.Execution.CollectionFailure != "" {
				t.Fatal("old diagnostic lost or copied into replacement", err)
			}
		})
	}
}

func TestEnsureStartJoinsStoppingBeforeCurrentConfiguration(t *testing.T) {
	s, request := attachedCoordinator(t)
	request.Attached = false
	prepared, err := s.prepare(context.Background(), v1.PrepareRequest{APIVersion: v1.Version, Plan: request.Plan})
	if err != nil {
		t.Fatal(err)
	}
	id := prepared.Instance.ID
	a := acceptHistory(t, s, id, v1.Stopping, -1)
	cancelled, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var cancelledOnce, releaseOnce sync.Once
	entry := &execution{attempt: a.Result.Attempt, done: done, cancel: func() { cancelledOnce.Do(func() { close(cancelled) }) }}
	s.executions = map[string]*execution{id: entry}
	go func() { defer close(done); <-release; _, _ = s.store.execution(id, v1.Stopped, &a.Result) }()
	t.Cleanup(func() {
		releaseOnce.Do(func() { close(release) })
		awaitControlled(t, done, "old stopping owner unfinished")
	})
	// A real new machine selection changes prospective tools while old work is joined.
	config := filepath.Join(shortTemp(t), "next.json")
	if err := os.WriteFile(config, []byte(`{"version":"backlot/v1","tools":{"go":"sh"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	request.Plan.ConfigPath = config
	var got v1.InstanceResponse
	var runErr error
	joined := make(chan struct{})
	go func() { defer close(joined); got, runErr = s.run(context.Background(), request) }()
	t.Cleanup(func() {
		releaseOnce.Do(func() { close(release) })
		awaitControlled(t, joined, "start owner unfinished")
	})
	awaitControlled(t, cancelled, "start did not join stopping work")
	current, _ := s.store.inspect(id)
	if current.Execution.Attempt != a.Result.Attempt {
		t.Fatal("accepted before stopping owner joined")
	}
	releaseOnce.Do(func() { close(release) })
	awaitControlled(t, joined, "start did not finish after old owner joined")
	if runErr != nil || got.Instance.Execution.Attempt == a.Result.Attempt || got.Instance.Plan.ConfigDigest == prepared.Instance.Plan.ConfigDigest || got.ConfigDrift {
		t.Fatal("current config was not accepted after stop", runErr)
	}
	journal, err := s.store.runtime(id)
	if err != nil || journal.ConfigPath != config {
		t.Fatal("current machine provenance lost", err)
	}
	request.Plan.ConfigPath = ""
	ready, err := s.run(context.Background(), request)
	if err != nil || ready.Instance.Execution.Attempt != got.Instance.Execution.Attempt || !ready.ConfigDrift {
		t.Fatal("ready join changed recorded config or hid drift", err)
	}
}

func TestEnsureStartProtectedDriftPreservesFailedSnapshot(t *testing.T) {
	for _, change := range []string{"provider", "fresh-only"} {
		t.Run(change, func(t *testing.T) {
			s, root, config := sceneFixture(t)
			path := filepath.Join(root, "backlot.json")
			var manifest v1.Manifest
			if err := json.Unmarshal([]byte(fixtureManifest), &manifest); err != nil {
				t.Fatal(err)
			}
			delete(manifest.Scenes, "test")
			manifest.Resources = map[string]v1.Resource{"data": {Kind: "directory"}}
			scene := manifest.Scenes["dev"]
			scene.Resources = []string{"data"}
			manifest.Scenes["dev"] = scene
			initializer := manifest.Components["check"]
			initializer.Policy, initializer.Initializes, initializer.Resources = v1.FreshOnly, []string{"data"}, []string{"data"}
			manifest.Components["check"] = initializer
			write := func() {
				t.Helper()
				data, err := json.Marshal(manifest)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, data, 0600); err != nil {
					t.Fatal(err)
				}
			}
			write()
			if err := os.WriteFile(config, []byte(`{"version":"backlot/v1","docker":{"endpoint":"unix:///fixture-old.sock"}}`), 0600); err != nil {
				t.Fatal(err)
			}
			prepared := scenePrepare(t, s, root, config)
			id := prepared.Instance.ID
			a := acceptHistory(t, s, id, v1.Failed, time.Second)
			before := storedBytes(t, s.store, "snapshots", id)
			if change == "provider" {
				if err := os.WriteFile(config, []byte(`{"version":"backlot/v1","docker":{"endpoint":"unix:///fixture-new.sock"}}`), 0600); err != nil {
					t.Fatal(err)
				}
			} else {
				initializer.Command.Args = append(initializer.Command.Args, "changed")
				manifest.Components["check"] = initializer
				write()
			}
			s.beforeExecution = func(string) { s.closing = true }
			_, err := s.run(context.Background(), v1.RunRequest{APIVersion: v1.Version, Plan: request(root, config, "dev").Plan})
			code(t, err, "unsafe_drift")
			current, _ := s.store.inspect(id)
			generation, err := s.store.resources(id)
			if err != nil || current.Execution.Attempt != a.Result.Attempt || generation.ID != "" || !bytes.Equal(before, storedBytes(t, s.store, "snapshots", id)) {
				t.Fatal("protected drift changed failed attempt/provenance or allocated resources", err)
			}
		})
	}
}
