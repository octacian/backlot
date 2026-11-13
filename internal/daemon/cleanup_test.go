//go:build darwin || linux

package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	v1 "github.com/octacian/backlot/api/v1"
	"github.com/octacian/backlot/internal/native"
	bolt "go.etcd.io/bbolt"
)

func cleanupRequest(parent string) v1.CleanupRequest {
	return v1.CleanupRequest{APIVersion: v1.Version, Contract: v1.CleanupContract, CheckoutID: parent}
}
func cleanupPrepared(t *testing.T, s *service, root, config string, disposable bool) v1.Instance {
	t.Helper()
	scene := "dev"
	if disposable {
		scene = "test"
	}
	out, err := s.prepare(context.Background(), request(root, config, scene))
	if err != nil {
		t.Fatal(err)
	}
	return out.Instance
}
func awaitCleanup(t *testing.T, o *cleanupOperation, timeout ...time.Duration) []v1.CleanupRecord {
	t.Helper()
	budget := 10 * time.Second
	if len(timeout) > 0 {
		budget = timeout[0]
	}
	ctx, cancel := context.WithTimeout(context.Background(), budget)
	defer cancel()
	records := []v1.CleanupRecord{}
	for i := 0; ; i++ {
		r, err := o.next(ctx, i)
		if err != nil {
			t.Fatal("unfinished cleanup", err)
		}
		if err = r.Validate(); err != nil {
			t.Fatal("invalid emitted record", r, err)
		}
		records = append(records, r)
		if r.Type == "summary" {
			return records
		}
	}
}
func TestCleanupPreviewRevalidationAndScope(t *testing.T) {
	s, root, config := sceneFixture(t)
	first := cleanupPrepared(t, s, root, config, false)
	otherRoot := shortTemp(t)
	git(t, otherRoot, "init", "-q")
	otherConfig := writeFixture(t, otherRoot)
	other := cleanupPrepared(t, s, otherRoot, otherConfig, false)
	external := filepath.Join(t.TempDir(), "export")
	if err := os.WriteFile(external, []byte("caller export"), 0600); err != nil {
		t.Fatal(err)
	}
	q := cleanupRequest(checkoutParent(first))
	before, err := s.cleanupPreview(context.Background(), q)
	if err != nil || len(before.targets) != 1 {
		t.Fatal(before, err)
	}
	again, err := s.cleanupPreview(context.Background(), q)
	if err != nil || again.digest != before.digest {
		t.Fatal("preview has timer/random drift", err)
	}
	cleanupPrepared(t, s, root, config, true)
	q.ApplyPreview = before.digest
	_, err = s.startCleanup(context.Background(), q)
	code(t, err, "stale_preview")
	current, err := s.store.inspect(first.ID)
	if err != nil || current.Status != v1.Prepared {
		t.Fatal("stale preview had effects", err, current.Status)
	}
	preview, err := s.cleanupPreview(context.Background(), q)
	if err != nil {
		t.Fatal(err)
	}
	q.ApplyPreview = preview.digest
	op, err := s.startCleanup(context.Background(), q)
	if err != nil {
		t.Fatal(err)
	}
	records := awaitCleanup(t, op)
	summary := records[len(records)-1].Summary
	if summary.Done != 2 || summary.Failed+summary.Unresolved+summary.Unattempted != 0 {
		t.Fatal(summary)
	}
	current, err = s.store.inspect(other.ID)
	if err != nil || current.Status != v1.Prepared {
		t.Fatal("other checkout modified", err, current.Status)
	}
	data, err := os.ReadFile(external)
	if err != nil || string(data) != "caller export" {
		t.Fatal("caller export modified", err)
	}
	s.releaseCleanup(op.id, op, true)
	s.mu.Lock()
	remaining := len(s.cleanupOperations)
	s.mu.Unlock()
	if remaining != 0 {
		t.Fatal("finished operation leaked", remaining)
	}
}
func TestCleanupLateOrphanReappearanceAndCancellation(t *testing.T) {
	for _, cancelled := range []bool{false, true} {
		t.Run(map[bool]string{false: "reappeared", true: "cancelled"}[cancelled], func(t *testing.T) {
			s, root, config := sceneFixture(t)
			cleanupPrepared(t, s, root, config, true)
			cleanupPrepared(t, s, root, config, true)
			if err := os.RemoveAll(root); err != nil {
				t.Fatal(err)
			}
			q := v1.CleanupRequest{APIVersion: v1.Version, Contract: v1.CleanupContract, Orphans: true}
			preview, err := s.cleanupPreview(context.Background(), q)
			if err != nil || len(preview.targets) != 2 {
				t.Fatal(preview, err)
			}
			first := preview.targets[0].target.ID
			second := preview.targets[1].target.ID
			lock := s.mutationLock(first)
			lock.Lock()
			q.ApplyPreview = preview.digest
			op, err := s.startCleanup(context.Background(), q)
			if err != nil {
				lock.Unlock()
				t.Fatal(err)
			}
			ctx, stop := context.WithTimeout(context.Background(), 5*time.Second)
			defer stop()
			if _, err = op.next(ctx, 1); err != nil {
				lock.Unlock()
				t.Fatal(err)
			}
			if cancelled {
				ack, err := s.cancelCleanup(v1.CleanupCancelRequest{APIVersion: v1.Version, Contract: v1.CleanupContract, DaemonGeneration: s.generation, OperationID: op.id})
				if err != nil || !ack.Cancelled {
					lock.Unlock()
					t.Fatal(ack, err)
				}
			} else if err = os.Mkdir(root, 0700); err != nil {
				lock.Unlock()
				t.Fatal(err)
			}
			lock.Unlock()
			records := awaitCleanup(t, op)
			summary := records[len(records)-1].Summary
			if cancelled {
				if !summary.Cancelled || summary.Unattempted != 1 || summary.Unresolved != 1 {
					t.Fatal(summary)
				}
			} else if summary.Unresolved != 2 || summary.Done != 0 {
				t.Fatal("reappeared checkout deleted", summary)
			}
			for _, id := range []string{first, second} {
				current, err := s.store.inspect(id)
				if err != nil || current.Status != v1.Prepared {
					t.Fatal("unreviewed deletion", err, current.Status)
				}
			}
			s.mu.Lock()
			fences := len(s.cleanupFences)
			s.mu.Unlock()
			if fences != 0 {
				t.Fatal("fence not released")
			}
		})
	}
}
func TestCleanupScopedTicketsAndRepairFence(t *testing.T) {
	s, root, config := sceneFixture(t)
	instance := cleanupPrepared(t, s, root, config, false)
	parent := checkoutParent(instance)
	selected, finish, err := s.beginWorkload(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err = s.bindCheckout(selected, parent); err != nil {
		t.Fatal(err)
	}
	unrelated, otherFinish, err := s.beginWorkload(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer otherFinish()
	if err = s.bindCheckout(unrelated, newID()); err != nil {
		t.Fatal(err)
	}
	q := cleanupRequest(parent)
	preview, err := s.cleanupPreview(context.Background(), q)
	if err != nil {
		t.Fatal(err)
	}
	q.ApplyPreview = preview.digest
	op, err := s.startCleanup(context.Background(), q)
	if err != nil {
		t.Fatal(err)
	}
	code(t, s.checkWorkload(context.WithoutCancel(selected)), "admission_closed")
	if err = s.checkWorkload(unrelated); err != nil {
		t.Fatal("unrelated ticket invalidated", err)
	}
	code(t, s.checkCleanupBindings([]string{parent}), "conflict")
	code(t, s.checkCleanupRegistration(parent, associatedCheckout(instance).Registration), "conflict")
	if err = s.checkCleanupBindings([]string{newID()}); err != nil {
		t.Fatal(err)
	}
	finish()
	awaitCleanup(t, op)
	code(t, s.checkWorkload(context.WithoutCancel(selected)), "admission_closed")
	if err = s.checkWorkload(unrelated); err != nil {
		t.Fatal("unrelated ticket invalidated after reopening", err)
	}
}
func TestCleanupTombstoneEvidenceRetry(t *testing.T) {
	s, root, config := sceneFixture(t)
	instance := cleanupPrepared(t, s, root, config, false)
	if _, err := s.destroy(context.Background(), instance.ID); err != nil {
		t.Fatal(err)
	}
	directory := filepath.Join(s.directory, "artifacts", instance.ID)
	if err := os.MkdirAll(directory, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "residual"), []byte("evidence"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := s.store.db.Update(func(tx *bolt.Tx) error {
		return put(tx, "evidence_deletions", instance.ID+"/", evidenceDeletion{InstanceID: instance.ID})
	}); err != nil {
		t.Fatal(err)
	}
	q := cleanupRequest(checkoutParent(instance))
	preview, err := s.cleanupPreview(context.Background(), q)
	if err != nil || len(preview.targets) != 1 || preview.targets[0].target.Status != v1.Destroyed {
		t.Fatal("residual tombstone omitted", preview, err)
	}
	q.ApplyPreview = preview.digest
	op, err := s.startCleanup(context.Background(), q)
	if err != nil {
		t.Fatal(err)
	}
	records := awaitCleanup(t, op)
	if records[len(records)-1].Summary.Done != 1 {
		t.Fatal(records)
	}
	if _, err := os.Stat(directory); !os.IsNotExist(err) {
		t.Fatal("residual evidence retained", err)
	}
	preview, err = s.cleanupPreview(context.Background(), q)
	if err != nil || len(preview.targets) != 0 {
		t.Fatal("completed erasure still selected", err)
	}
}
func TestCleanupOrphanBlockersAndPrivateOutput(t *testing.T) {
	for _, mode := range []string{"present", "live", "uncertain", "binding"} {
		t.Run(mode, func(t *testing.T) {
			s, root, config := sceneFixture(t)
			instance := cleanupPrepared(t, s, root, config, false)
			parent := checkoutParent(instance)
			if mode != "present" {
				if err := os.RemoveAll(root); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "live" {
				ctx, finish, err := s.beginWorkload(context.Background())
				if err != nil {
					t.Fatal(err)
				}
				defer finish()
				if err = s.bindCheckout(ctx, parent); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "uncertain" {
				acceptHistory(t, s, instance.ID, v1.Interrupted, time.Hour)
			}
			if mode == "binding" {
				if err := s.store.db.Update(func(tx *bolt.Tx) error {
					b, err := readCheckoutBinding(tx, parent)
					if err != nil {
						return err
					}
					b.PendingPath = b.Current.Path
					return put(tx, "checkout_bindings", parent, b)
				}); err != nil {
					t.Fatal(err)
				}
			}
			q := v1.CleanupRequest{APIVersion: v1.Version, Contract: v1.CleanupContract, Orphans: true}
			preview, err := s.cleanupPreview(context.Background(), q)
			if err != nil {
				t.Fatal(err)
			}
			if mode == "present" {
				if len(preview.targets) != 0 {
					t.Fatal("present checkout selected")
				}
				return
			}
			if len(preview.targets) != 1 || len(preview.targets[0].target.Blockers) == 0 {
				t.Fatal("unsafe orphan eligible", preview)
			}
			data, _ := json.Marshal(preview.targetsPublic())
			for _, secret := range []string{"resolved-fixture-secret", "seed-fixture-secret", "private-config-sentinel", "ownership_token"} {
				if strings.Contains(string(data), secret) {
					t.Fatal("private output leaked")
				}
			}
			q.ApplyPreview = preview.digest
			op, err := s.startCleanup(context.Background(), q)
			if err != nil {
				t.Fatal(err)
			}
			records := awaitCleanup(t, op)
			if records[len(records)-1].Summary.Unresolved != 1 {
				t.Fatal(records)
			}
			current, err := s.store.inspect(instance.ID)
			if err != nil || current.Status == v1.Destroyed {
				t.Fatal("blocked orphan destroyed", err)
			}
		})
	}
}

func TestCleanupDirectoryWitnessSurvivesStoreReopen(t *testing.T) {
	s, id := historyFixture(t)
	acceptHistory(t, s, id, v1.Stopped, 0)
	token := newID()
	directory := filepath.Join(s.directory, "data", token)
	if err := os.MkdirAll(directory, 0700); err != nil {
		t.Fatal(err)
	}
	for name, value := range map[string]string{".backlot-owner": token, "data": "retained data"} {
		if err := os.WriteFile(filepath.Join(directory, name), []byte(value), 0600); err != nil {
			t.Fatal(err)
		}
	}
	g := resourceGeneration{ID: newID(), Resources: map[string]ownedResource{"data": {Kind: "directory", Name: directory, Token: token, ID: token}}, Initialized: map[string]bool{}}
	if err := s.store.saveResources(id, g); err != nil {
		t.Fatal(err)
	}
	releaseZeroLaunchTaskFixture(t, s.store, id)
	s.store.sharedCheckpoint = func(point string) error {
		if point == "directory-marker-removed" {
			return context.Canceled
		}
		return nil
	}
	_, err := s.destroy(context.Background(), id)
	code(t, err, "cleanup_failed")
	if _, err = os.Stat(filepath.Join(s.directory, "data", ".backlot-delete-"+token)); err != nil {
		t.Fatal("retry authority missing", err)
	}
	if err = s.store.db.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := openStore(s.directory)
	if err != nil {
		t.Fatal(err)
	}
	s.store.db = reopened.db
	s.store.sharedCheckpoint = nil
	result, err := s.destroy(context.Background(), id)
	if err != nil || result.Status != v1.Destroyed {
		t.Fatal("exact-ID retry after interruption failed", err, result.Status)
	}
	if _, err = os.Stat(directory); !os.IsNotExist(err) {
		t.Fatal("directory root remains", err)
	}
	if _, err = os.Stat(filepath.Join(s.directory, "data", ".backlot-delete-"+token)); !os.IsNotExist(err) {
		t.Fatal("witness remains", err)
	}
}

func TestCleanupLegacyRegistrationIsolation(t *testing.T) {
	s, root, config := sceneFixture(t)
	one := cleanupPrepared(t, s, root, config, false)
	otherRoot := shortTemp(t)
	git(t, otherRoot, "init", "-q")
	otherConfig := writeFixture(t, otherRoot)
	two := cleanupPrepared(t, s, otherRoot, otherConfig, false)
	selected, other := checkoutParent(one), checkoutParent(two)
	if err := s.store.db.Update(func(tx *bolt.Tx) error {
		for _, parent := range []string{selected, other} {
			b, err := readCheckoutBinding(tx, parent)
			if err != nil {
				return err
			}
			b.Current.Registration = v1.RegistrationIdentity{}
			b.Revision = newID()
			if err = put(tx, "checkout_bindings", parent, b); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	s.cleanupFences = map[string]bool{other: true}
	if err := s.checkCleanupRegistration(selected, v1.RegistrationIdentity{}); err != nil {
		t.Fatal("unknown common paths related unrelated legacy checkouts", err)
	}
	code(t, s.checkCleanupRegistration(other, v1.RegistrationIdentity{}), "conflict")
}
func TestCleanupNativeAbandonmentProofParity(t *testing.T) {
	current, err := native.CurrentBoot()
	if err != nil {
		t.Fatal(err)
	}
	old := settlementOldBoot
	if old == current {
		old = settlementBoot
	}
	for _, scenario := range []struct {
		name, boot string
		allowed    bool
	}{{"earlier", old, true}, {"current", current, false}, {"unknown", "", false}} {
		t.Run(scenario.name, func(t *testing.T) {
			s, id := historyFixture(t)
			acceptHistory(t, s, id, v1.Stopped, 0)
			if err := s.store.db.Update(func(tx *bolt.Tx) error {
				r, err := load(tx, id)
				if err != nil {
					return err
				}
				r.NativeAbandonments = []nativeAbandonment{{Attempt: r.Instance.Execution.Attempt, Boot: scenario.boot, Pending: true, PendingBoot: scenario.boot, RecordedAt: time.Now().UTC().Format(time.RFC3339Nano), Reason: "explicit_operator_acknowledgement"}}
				return put(tx, "instances", id, r)
			}); err != nil {
				t.Fatal(err)
			}
			if err := s.store.guardAbandonedData(id); (err == nil) != scenario.allowed {
				t.Fatal("existing absence proof differs", err)
			}
			instance, err := s.store.inspect(id)
			if err != nil {
				t.Fatal(err)
			}
			preview, err := s.cleanupPreview(context.Background(), cleanupRequest(checkoutParent(instance)))
			if err != nil || len(preview.targets) != 1 {
				t.Fatal(preview, err)
			}
			if (len(preview.targets[0].target.Blockers) == 0) != scenario.allowed {
				t.Fatal("preview is stricter than the existing independent absence proof", preview.targets[0].target.Blockers)
			}
		})
	}
}

func TestCleanupLateDanglingCheckoutPreservesData(t *testing.T) {
	s, root, config := sceneFixture(t)
	instance := cleanupPrepared(t, s, root, config, true)
	token := newID()
	resource := ownedResource{Kind: "directory", Token: token, ID: token}
	resource.Name = filepath.Join(s.directory, "data", token)
	if err := os.MkdirAll(resource.Name, 0700); err != nil {
		t.Fatal(err)
	}
	for name, value := range map[string]string{".backlot-owner": token, "retained": "keep"} {
		if err := os.WriteFile(filepath.Join(resource.Name, name), []byte(value), 0600); err != nil {
			t.Fatal(err)
		}
	}
	g := resourceGeneration{ID: newID(), Resources: map[string]ownedResource{"data": resource}, Initialized: map[string]bool{}}
	if err := s.store.saveResources(instance.ID, g); err != nil {
		t.Fatal(err)
	}
	acceptHistory(t, s, instance.ID, v1.Stopped, 0)
	releaseZeroLaunchTaskFixture(t, s.store, instance.ID)
	if err := os.RemoveAll(root); err != nil {
		t.Fatal(err)
	}
	restored := false
	s.store.sharedCheckpoint = func(point string) error {
		if point == "directory-authority-durable" {
			restored = true
			return os.Symlink(filepath.Join(t.TempDir(), "absent"), root)
		}
		return nil
	}
	q := v1.CleanupRequest{APIVersion: v1.Version, Contract: v1.CleanupContract, Orphans: true}
	preview, err := s.cleanupPreview(context.Background(), q)
	if err != nil || len(preview.targets) != 1 {
		t.Fatal(preview, err)
	}
	q.ApplyPreview = preview.digest
	op, err := s.startCleanup(context.Background(), q)
	if err != nil {
		t.Fatal(err)
	}
	records := awaitCleanup(t, op)
	if !restored || records[len(records)-1].Summary.Unresolved != 1 {
		t.Fatal("late restoration was not fenced", records)
	}
	if data, err := os.ReadFile(filepath.Join(resource.Name, "retained")); err != nil || string(data) != "keep" {
		t.Fatal("remaining data deleted", err)
	}
	if _, err := os.Lstat(filepath.Join(resource.Name, ".backlot-owner")); err != nil {
		t.Fatal("authority deleted", err)
	}
	current, err := s.store.inspect(instance.ID)
	if err != nil || current.Status == v1.Destroyed {
		t.Fatal("evidence erased", err)
	}
	// The same presence predicate also excludes the dangling path at preview time.
	next, err := s.cleanupPreview(context.Background(), q)
	if err != nil || len(next.targets) != 0 {
		t.Fatal("dangling checkout treated as absent", next, err)
	}
}

func TestCleanupCommittedStopOutcomes(t *testing.T) {
	for _, scenario := range []struct{ failure, queued bool }{{}, {true, false}, {false, true}, {true, true}} {
		failure := scenario.failure
		name := map[bool]string{false: "success", true: "later-data-failure"}[failure]
		if scenario.queued {
			name = "queued-" + name
		}
		t.Run(name, func(t *testing.T) {
			s, root, config := sceneFixture(t)
			engine := forceEngine(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet {
					t.Error("unexpected provider mutation", r.Method, r.URL.Path)
					w.WriteHeader(403)
					return
				}
				if r.URL.Path == "/containers/json" {
					_, _ = w.Write([]byte("[]"))
					return
				}
				http.NotFound(w, r)
			})
			instance := cleanupPrepared(t, s, root, config, false)
			token := newID()
			r := ownedResource{Kind: "directory", Token: token, ID: token}
			r.Name = filepath.Join(s.directory, "data", r.Token)
			if err := os.MkdirAll(r.Name, 0700); err != nil {
				t.Fatal(err)
			}
			proof := r.Token
			if failure {
				proof = "invalid"
			}
			if err := os.WriteFile(filepath.Join(r.Name, ".backlot-owner"), []byte(proof), 0600); err != nil {
				t.Fatal(err)
			}
			attempt := acceptHistory(t, s, instance.ID, v1.Stopped, 0)
			g := resourceGeneration{ID: newID(), Endpoint: engine.DaemonHost(), Resources: map[string]ownedResource{"data": r, "net": {Kind: "network", Name: "net", Token: newID(), ID: newID()}}, Containers: []ownedResource{{Kind: "container", Name: "owned", Component: "app", Token: newID(), Attempt: attempt.Result.Attempt, ID: newID()}}, Initialized: map[string]bool{}}
			g.Resources["prior"] = ownedResource{Kind: "network", Name: "prior", Token: newID(), ID: newID(), Removed: true}
			if err := s.store.saveResources(instance.ID, g); err != nil {
				t.Fatal(err)
			}
			entry := &execution{done: make(chan struct{}), attempt: attempt.Result.Attempt}
			var stopped sync.Once
			entry.cancel = func() {
				stopped.Do(func() {
					network := g.Resources["net"]
					network.Removed = true
					g.Resources["net"] = network
					g.Containers[0].Removed = true
					entry.err = s.store.saveResources(instance.ID, g)
					if entry.err == nil {
						j, e := s.store.runtime(instance.ID)
						if e == nil {
							e = s.store.recordConsumerRelease(context.Background(), instance.ID, j, v1.SettlementExecutionCleanup, true)
						}
						entry.err = e
					}
					close(entry.done)
				})
			}
			s.executions = map[string]*execution{instance.ID: entry}
			q := cleanupRequest(checkoutParent(instance))
			preview, err := s.cleanupPreview(context.Background(), q)
			if err != nil {
				t.Fatal(err)
			}
			q.ApplyPreview = preview.digest
			lock := s.mutationLock(instance.ID)
			if scenario.queued {
				lock.Lock()
			}
			op, err := s.startCleanup(context.Background(), q)
			if err != nil {
				if scenario.queued {
					lock.Unlock()
				}
				t.Fatal(err)
			}
			if scenario.queued {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				_, err = op.next(ctx, 1)
				cancel()
				if err == nil {
					entry.cancel()
				}
				lock.Unlock()
				if err != nil {
					t.Fatal(err)
				}
			}
			records := awaitCleanup(t, op)
			requireCleanupRuntimeOutcomes(t, records, preview.targets[0].target.Resources)
			for _, record := range records {
				if record.Resource != nil && record.Resource.Name == "prior" {
					t.Fatal("reported resource absent from reviewed loss", record)
				}
			}
			summary := records[len(records)-1].Summary
			if failure && summary.Failed != 1 || !failure && summary.Done != 1 {
				current, _ := s.store.inspect(instance.ID)
				raw, _ := json.Marshal(records)
				t.Fatal(summary, string(raw), current.Execution, entry.err)
			}
		})
	}
}

func requireCleanupRuntimeOutcomes(t *testing.T, records []v1.CleanupRecord, reviewed []v1.CleanupResource) {
	t.Helper()
	for _, resource := range reviewed {
		if resource.Kind != "container" && resource.Kind != "network" {
			continue
		}
		count := 0
		for _, record := range records {
			if record.Resource != nil && record.Resource.Name == resource.Name && record.Resource.Kind == resource.Kind {
				count++
				// Exercise the same typed records serialized as NDJSON by the transport.
				raw, err := json.Marshal(record)
				var decoded v1.CleanupRecord
				if err != nil || json.Unmarshal(raw, &decoded) != nil || decoded.Validate() != nil || decoded.Resource.Outcome != "removed" {
					t.Fatal("committed removal misreported", string(raw), err)
				}
			}
		}
		if count != 1 {
			t.Fatal("missing or duplicate runtime removal", resource, count)
		}
	}
}

func TestCleanupDelayedDiscoveryAdmission(t *testing.T) {
	s, root, config := sceneFixture(t)
	instance := cleanupPrepared(t, s, root, config, false)
	otherRoot := shortTemp(t)
	git(t, otherRoot, "init", "-q")
	otherConfig := writeFixture(t, otherRoot)
	cleanupPrepared(t, s, otherRoot, otherConfig, false)
	// All workload entry points acquire this ticket before locating their checkout.
	selected, finish, err := s.beginWorkload(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer finish()
	unrelated, otherFinish, err := s.beginWorkload(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer otherFinish()
	q := cleanupRequest(checkoutParent(instance))
	preview, err := s.cleanupPreview(context.Background(), q)
	if err != nil {
		t.Fatal(err)
	}
	q.ApplyPreview = preview.digest
	op, err := s.startCleanup(context.Background(), q)
	if err != nil {
		t.Fatal(err)
	}
	awaitCleanup(t, op)
	// Resume the real discovery-to-publication path only after cleanup reopens.
	_, err = s.prepareSelected(selected, request(root, config, "dev"), false)
	code(t, err, "admission_closed")
	if _, err = s.prepareSelected(unrelated, request(otherRoot, otherConfig, "test"), false); err != nil {
		t.Fatal("unrelated pre-discovery ticket invalidated", err)
	}
	fresh, done, err := s.beginWorkload(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer done()
	if _, err = s.prepareSelected(fresh, request(root, config, "test"), false); err != nil {
		t.Fatal("fresh admission after reopening rejected", err)
	}
}

func TestCleanupBusyTargetDoesNotStarveNext(t *testing.T) {
	for _, runtime := range []bool{false, true} {
		t.Run(map[bool]string{false: "instance-lock", true: "runtime-lock"}[runtime], func(t *testing.T) {
			s, root, config := sceneFixture(t)
			cleanupPrepared(t, s, root, config, true)
			cleanupPrepared(t, s, root, config, true)
			q := v1.CleanupRequest{APIVersion: v1.Version, Contract: v1.CleanupContract, CheckoutPath: root}
			preview, err := s.cleanupPreview(context.Background(), q)
			if err != nil || len(preview.targets) != 2 {
				t.Fatal(preview, err)
			}
			first, second := preview.targets[0].target.ID, preview.targets[1].target.ID
			lock := s.mutationLock(first)
			if runtime {
				entry := &execution{done: make(chan struct{}), cancel: func() {}}
				close(entry.done)
				s.executions = map[string]*execution{first: entry}
				lock = &entry.mu
			}
			lock.Lock()
			defer lock.Unlock()
			q.ApplyPreview = preview.digest
			op, err := s.startCleanup(context.Background(), q)
			if err != nil {
				t.Fatal(err)
			}
			defer op.cancel()
			records := awaitCleanup(t, op, 35*time.Second)
			summary := records[len(records)-1].Summary
			if summary.Failed != 1 || summary.Done != 1 || summary.Cancelled {
				t.Fatal(summary)
			}
			for id, want := range map[string]v1.InstanceStatus{first: v1.Prepared, second: v1.Destroyed} {
				current, err := s.store.inspect(id)
				if err != nil || current.Status != want {
					t.Fatal("busy target affected independent cleanup", id, current.Status, err)
				}
			}
		})
	}
}

func TestCleanupStalledProviderBodyDoesNotStarveNext(t *testing.T) {
	s, root, config := sceneFixture(t)
	cleanupPrepared(t, s, root, config, true)
	cleanupPrepared(t, s, root, config, true)
	q := v1.CleanupRequest{APIVersion: v1.Version, Contract: v1.CleanupContract, CheckoutPath: root}
	preview, err := s.cleanupPreview(context.Background(), q)
	if err != nil || len(preview.targets) != 2 {
		t.Fatal(preview, err)
	}
	first, second := preview.targets[0].target.ID, preview.targets[1].target.ID
	entered, finished := make(chan struct{}), make(chan struct{})
	engine := forceEngine(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Error("unauthenticated destructive provider call", r.Method)
			w.WriteHeader(403)
			return
		}
		if r.URL.Path == "/containers/json" {
			_, _ = w.Write([]byte("[]"))
			return
		}
		if strings.HasPrefix(r.URL.Path, "/volumes/") {
			// Headers arrive immediately; only a bounded body read can terminate this call.
			_, _ = w.Write([]byte(`{"Name":"owned","Labels":`))
			w.(http.Flusher).Flush()
			close(entered)
			<-r.Context().Done()
			close(finished)
			return
		}
		t.Error("unexpected provider target", r.URL.Path)
		http.NotFound(w, r)
	})
	resource := ownedResource{Kind: "volume", Name: "owned", ID: "owned", Token: newID()}
	g := resourceGeneration{ID: newID(), Endpoint: engine.DaemonHost(), Resources: map[string]ownedResource{"data": resource}, Initialized: map[string]bool{}}
	if err = s.store.saveResources(first, g); err != nil {
		t.Fatal(err)
	}
	preview, err = s.cleanupPreview(context.Background(), q)
	if err != nil {
		t.Fatal(err)
	}
	q.ApplyPreview = preview.digest
	op, err := s.startCleanup(context.Background(), q)
	if err != nil {
		t.Fatal(err)
	}
	defer op.cancel()
	records := awaitCleanup(t, op, 45*time.Second)
	summary := records[len(records)-1].Summary
	if summary.Failed != 1 || summary.Done != 1 || summary.Cancelled {
		t.Fatal(summary)
	}
	select {
	case <-entered:
	default:
		t.Fatal("provider body did not stall")
	}
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("provider call retained in-flight ownership after reporting completion")
	}
	remaining, err := s.store.resources(first)
	if err != nil || remaining.Resources["data"].Removed {
		t.Fatal("unverified provider effect committed", remaining, err)
	}
	current, err := s.store.inspect(second)
	if err != nil || current.Status != v1.Destroyed {
		t.Fatal("later target not attempted", current.Status, err)
	}
	found := false
	for _, record := range records {
		if record.Resource != nil && record.InstanceID == first && record.Resource.Name == "data" {
			found = true
			if record.Resource.Outcome != "unresolved" {
				t.Fatal(record)
			}
		}
	}
	if !found {
		t.Fatal("unresolved effect missing")
	}
}

func TestCleanupUnjoinedAdmissionPreservesParent(t *testing.T) {
	s, root, config := sceneFixture(t)
	instance := cleanupPrepared(t, s, root, config, false)
	parent := checkoutParent(instance)
	selected, finish, err := s.beginWorkload(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer finish()
	if err = s.bindCheckout(selected, parent); err != nil {
		t.Fatal(err)
	}
	otherRoot := shortTemp(t)
	git(t, otherRoot, "init", "-q")
	otherConfig := writeFixture(t, otherRoot)
	unrelated, otherFinish, err := s.beginWorkload(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer otherFinish()
	q := cleanupRequest(parent)
	preview, err := s.cleanupPreview(context.Background(), q)
	if err != nil {
		t.Fatal(err)
	}
	q.ApplyPreview = preview.digest
	op, err := s.startCleanup(context.Background(), q)
	if err != nil {
		t.Fatal(err)
	}
	defer op.cancel()
	records := awaitCleanup(t, op, 35*time.Second)
	summary := records[len(records)-1].Summary
	if summary.Unresolved != 1 || summary.Done != 0 || summary.Cancelled {
		t.Fatal(summary)
	}
	current, err := s.store.inspect(instance.ID)
	if err != nil || current.Status != v1.Prepared {
		t.Fatal("unjoined owner lost parent", current.Status, err)
	}
	code(t, s.checkWorkload(context.WithoutCancel(selected)), "admission_closed")
	if _, err = s.prepareSelected(unrelated, request(otherRoot, otherConfig, "test"), false); err != nil {
		t.Fatal("independent target blocked after timed-out join", err)
	}
}

func TestCleanupBusyDependentPreservesParent(t *testing.T) {
	s, root, config := sceneFixture(t)
	f := &controlledExpiry{s: s, root: root, config: config}
	parent, child := taskRecordFixture(t, f)
	instance, err := f.s.store.inspect(parent)
	if err != nil {
		t.Fatal(err)
	}
	q := cleanupRequest(checkoutParent(instance))
	preview, err := f.s.cleanupPreview(context.Background(), q)
	if err != nil {
		t.Fatal(err)
	}
	var selected cleanupSelection
	for _, target := range preview.targets {
		if target.target.ID == parent {
			selected = target
		}
	}
	if selected.target.ID == "" || len(selected.dependents[parent]) != 1 {
		t.Fatal("dependent not captured", preview)
	}
	entry := &execution{done: make(chan struct{}), cancel: func() {}}
	close(entry.done)
	f.s.executions = map[string]*execution{child: entry}
	entry.mu.Lock()
	var unlock sync.Once
	defer unlock.Do(entry.mu.Unlock)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	ctx = context.WithValue(ctx, cleanupStepsKey{}, &cleanupSteps{reviewed: selected.resources, before: func() error { return nil }, emit: func(string, string, string) {}})
	joined := make(chan error, 1)
	go func() { joined <- f.s.destroyCleanupTarget(ctx, preview.scope, selected) }()
	select {
	case err = <-joined:
	case <-time.After(2 * time.Second):
		unlock.Do(entry.mu.Unlock)
		<-joined
		t.Fatal("parent teardown bypassed bounded dependent-owner wait")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("busy dependent not preserved", err)
	}
	current, err := f.s.store.inspect(parent)
	if err != nil || current.Status != instance.Status || current.Execution.Attempt != instance.Execution.Attempt {
		t.Fatal("parent changed behind unresolved dependent", current, err)
	}
}
