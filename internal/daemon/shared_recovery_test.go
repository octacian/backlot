//go:build !windows

package daemon

import (
	"bytes"
	"context"
	"errors"
	bolt "go.etcd.io/bbolt"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	v1 "github.com/octacian/backlot/api/v1"
	"github.com/octacian/backlot/internal/plan"
)

func TestSharedReleaseIntentSurvivesPartial(t *testing.T) {
	for _, ready := range []bool{false, true} {
		t.Run(map[bool]string{false: "partial", true: "ready"}[ready], func(t *testing.T) {
			state, err := openStore(shortTemp(t))
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = state.db.Close() }()
			root := shortTemp(t)
			source := plan.Source{ProjectPath: root, CheckoutPath: root}
			identity, err := identifyShared(source, "fixture")
			if err != nil {
				t.Fatal(err)
			}
			serviceID := hash(identity.ID + "/sql")
			id := newID()
			allocation := sharedAllocation{ServiceID: serviceID, Request: v1.ManagerRequest{Version: v1.ManagerProtocolVersion, Operation: "allocate", AllocationID: newID(), Ownership: newID(), Database: "bl" + newID()[:32], Username: "bl" + newID()[:32], Password: newID()}}
			if ready {
				allocation.Result = v1.ManagerResult{Version: v1.ManagerProtocolVersion, AllocationID: allocation.Request.AllocationID, Ownership: "owned", Effect: "ready"}
			}
			if err = state.saveSharedAllocations(id, map[string]sharedAllocation{"db": allocation, "second": allocation}); err != nil {
				t.Fatal(err)
			}
			interrupted := errors.New("fixture interrupted before provider")
			state.sharedCheckpoint = func(point string) error {
				if point == "release-intent" {
					return interrupted
				}
				return nil
			}
			if err = state.releaseSharedAllocations(context.Background(), id); !errors.Is(err, interrupted) {
				t.Fatal("release did not reach durable intent", err)
			}
			observed, err := state.sharedAllocations(id)
			if err != nil {
				t.Fatal(err)
			}
			for _, a := range observed {
				expected := allocation.Request
				expected.Operation = "release"
				if a.Request != expected || a.Released {
					t.Fatal("release intent/paired credentials lost")
				}
			}
			// Invalid provider config cannot mask the release fence or receive contacts.
			svc := &service{store: state}
			snap := plan.Snapshot{Plan: v1.PlanResponse{Project: "fixture", SharedServices: map[string]v1.SharedService{"sql": {}}, Resources: map[string]v1.Resource{"db": {Kind: "allocation", SharedService: "sql"}}}}
			code(t, svc.ensureSharedAllocations(context.Background(), id, snap, &execution{source: capturedSource{source: source}}), "allocation_conflict")
		})
	}
}

func TestSharedControlShutdownJoinsAcceptedOwner(t *testing.T) {
	svc := &service{}
	ctx, finish, err := svc.beginMutation(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	finished := false
	defer func() {
		if !finished {
			finish()
		}
	}()
	if svc.ownersJoined() {
		t.Fatal("accepted shared effect not counted as storage owner")
	}
	svc.mu.Lock()
	svc.closing = true
	svc.mu.Unlock()
	done := make(chan error, 1)
	go func() { done <- svc.stopMutations() }()
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("shutdown did not cancel control")
	}
	select {
	case <-done:
		t.Fatal("shutdown returned without joining effect owner")
	default:
	}
	_, _, err = svc.beginMutation(context.Background())
	code(t, err, "shutting_down")
	finish()
	finished = true
	select {
	case err = <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("shared owner join did not finish")
	}
	if !svc.ownersJoined() {
		t.Fatal("settled shared owner retained storage")
	}
}

func TestSharedDestroyIntentFencesStart(t *testing.T) {
	state, err := openStore(shortTemp(t))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = state.db.Close() }()
	record := sharedLockRecord(newID())
	record.Server.Removed = false
	record.Destroying = true
	if err = state.saveShared(record); err != nil {
		t.Fatal(err)
	}
	code(t, state.startShared(context.Background(), &record), "shared_cleanup_pending")
	observed, err := state.sharedService(record.ID)
	if err != nil || !observed.Destroying || observed.Status != "destroying" || observed.Failure == "" {
		t.Fatal("destructive obligation/status lost", err)
	}
}

func TestSharedDestroyTracksReleaseOwner(t *testing.T) {
	state, err := openStore(shortTemp(t))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = state.db.Close() }()
	root := shortTemp(t)
	config := writeFixture(t, root)
	svc := &service{store: state, lease: time.Second}
	prepared, err := svc.prepare(context.Background(), request(root, config, "dev"))
	if err != nil {
		t.Fatal(err)
	}
	id := prepared.Instance.ID
	if err = state.saveResources(id, resourceGeneration{ID: newID(), Resources: map[string]ownedResource{}, Initialized: map[string]bool{}}); err != nil {
		t.Fatal(err)
	}
	allocation := sharedAllocation{ServiceID: newID(), Request: v1.ManagerRequest{Version: v1.ManagerProtocolVersion, Operation: "allocate", AllocationID: newID(), Ownership: newID(), Database: "bl" + newID()[:32], Username: "bl" + newID()[:32], Password: newID()}}
	if err = state.saveSharedAllocations(id, map[string]sharedAllocation{"db": allocation}); err != nil {
		t.Fatal(err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	interrupted := errors.New("owned fixture release interruption")
	state.sharedCheckpoint = func(at string) error {
		if at == "release-intent" {
			close(entered)
			<-release
			return interrupted
		}
		return nil
	}
	done := make(chan error, 1)
	go func() { _, err := svc.destroy(context.Background(), id); done <- err }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("destroy did not reach durable release")
	}
	if svc.ownersJoined() {
		t.Fatal("accepted destroy release owner omitted from storage lifetime")
	}
	svc.mu.Lock()
	svc.closing = true
	svc.mu.Unlock()
	stop := make(chan error, 1)
	go func() { stop <- svc.stopMutations() }()
	select {
	case <-stop:
		t.Fatal("shutdown lost unjoined release owner")
	default:
	}
	close(release)
	if err = <-done; err == nil {
		t.Fatal("interrupted cleanup reported success")
	}
	if err = <-stop; err != nil {
		t.Fatal(err)
	}
	if !svc.ownersJoined() {
		t.Fatal("completed destroy owner was not joined")
	}
	observed, err := state.sharedAllocations(id)
	if err != nil || observed["db"].Request.Operation != "release" {
		t.Fatal("release obligation lost", err)
	}
}

func TestSharedCorruptReleaseCannotBypassFence(t *testing.T) {
	state, err := openStore(shortTemp(t))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = state.db.Close() }()
	record := sharedLockRecord(newID())
	if err = state.saveShared(record); err != nil {
		t.Fatal(err)
	}
	allocation := sharedAllocation{ServiceID: record.ID, Request: v1.ManagerRequest{Version: v1.ManagerProtocolVersion, Operation: "release", AllocationID: newID(), Ownership: newID(), Database: "bl" + newID()[:32], Username: "bl" + newID()[:32], Password: newID()}, Released: true}
	if err = state.saveSharedAllocations(newID(), map[string]sharedAllocation{"db": allocation}); err != nil {
		t.Fatal(err)
	}
	svc := &service{store: state}
	_, err = svc.sharedControl(context.Background(), "destroy", v1.SharedRequest{APIVersion: v1.Version, ServiceID: record.ID})
	code(t, err, "state_corrupt")
	after, err := state.sharedService(record.ID)
	if err != nil || after.Destroying || after.Status != record.Status {
		t.Fatal("corrupt release bypassed destructive dependency fence", err)
	}
}

func TestSharedCompletedDestroyClearsFailure(t *testing.T) {
	state, err := openStore(shortTemp(t))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = state.db.Close() }()
	record := sharedLockRecord(newID())
	record.Destroying = true
	record.Server.Removed = true
	record.Volume.Removed = true
	record.Network.Removed = true
	if err := state.saveShared(record); err != nil {
		t.Fatal(err)
	}
	svc := &service{store: state}
	request := v1.SharedRequest{APIVersion: v1.Version, ServiceID: record.ID}
	_, err = svc.sharedControl(context.Background(), "start", request)
	code(t, err, "shared_cleanup_pending")
	prior, err := state.sharedService(record.ID)
	if err != nil || prior.Failure == "" {
		t.Fatal("rejected start did not retain controlled failure", err)
	}
	result, err := svc.sharedControl(context.Background(), "destroy", request)
	if err != nil || len(result.Services) != 1 {
		t.Fatal("completed explicit cleanup failed", err)
	}
	if result.Services[0].Status != "destroyed" || result.Services[0].Failure != "" {
		t.Fatal("completed cleanup reported stale failure")
	}
	ids, err := state.sharedIDs()
	if err != nil || len(ids) != 0 {
		t.Fatal("completed cleanup retained shared obligation", err)
	}
}

func TestSharedStateAuthority(t *testing.T) {
	for _, kind := range []string{"intent", "partial", "released", "settled-service-deleted", "tampered-release", "project", "path", "service", "orphan", "force", "nested"} {
		t.Run(kind, func(t *testing.T) {
			svc, instance, _ := exportFixture(t)
			state := svc.store
			root := filepath.Dir(instance.Plan.ManifestPath)
			project, err := identifyShared(plan.Source{ProjectPath: root, CheckoutPath: root}, instance.Plan.Project)
			if err != nil {
				t.Fatal(err)
			}
			r := sharedLockRecord(hash(project.ID + "/sql"))
			r.Project = project
			r.Server = ownedResource{Kind: "container", Name: "server", Token: newID()}
			r.Volume = ownedResource{Kind: "volume", Name: "volume", Token: newID()}
			r.Network = ownedResource{Kind: "network", Name: "network", Token: newID()}
			a := sharedAllocation{ServiceID: r.ID, Request: v1.ManagerRequest{Version: v1.ManagerProtocolVersion, Operation: "allocate", AllocationID: newID(), Ownership: newID(), Database: "bl" + newID()[:32], Username: "bl" + newID()[:32], Password: newID()}}
			if kind == "partial" {
				a.Result = v1.ManagerResult{Version: v1.ManagerProtocolVersion, AllocationID: a.Request.AllocationID, Ownership: "uncertain", Effect: "partial"}
			}
			if kind == "released" || kind == "settled-service-deleted" || kind == "tampered-release" {
				a.Request.Operation = "release"
				a.Released = true
				a.Result = v1.ManagerResult{Version: v1.ManagerProtocolVersion, AllocationID: a.Request.AllocationID, Ownership: "absent", Effect: "released"}
			}
			if err := state.db.Update(func(tx *bolt.Tx) error {
				if err := registerSharedProject(tx, project); err != nil {
					return err
				}
				parent, err := load(tx, instance.ID)
				if err != nil {
					return err
				}
				parent.Instance.Plan.Resources = map[string]v1.Resource{"db": {Kind: "allocation", SharedService: "sql"}}
				if err = put(tx, "instances", instance.ID, parent); err != nil {
					return err
				}
				if err = put(tx, "shared_services", r.ID, r); err != nil {
					return err
				}
				if err = put(tx, "shared_allocations", instance.ID, map[string]sharedAllocation{"db": a}); err != nil {
					return err
				}
				switch kind {
				case "project":
					return tx.Bucket([]byte("shared_projects")).Put([]byte(project.ID), []byte(`null`))
				case "path":
					return tx.Bucket([]byte("shared_paths")).Put([]byte("foreign"), []byte(project.ID))
				case "service":
					return tx.Bucket([]byte("shared_services")).Delete([]byte(r.ID))
				case "orphan":
					return tx.Bucket([]byte("instances")).Delete([]byte(instance.ID))
				case "force":
					a.Request.Operation = "release"
					a.Request.Force = true
					return put(tx, "shared_allocations", instance.ID, map[string]sharedAllocation{"db": a})
				case "nested":
					_, err := tx.Bucket([]byte("shared_services")).CreateBucket([]byte("nested"))
					return err
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			if kind == "settled-service-deleted" || kind == "tampered-release" {
				peer := r
				peer.Name = "peer"
				peer.ID = hash(project.ID + "/peer")
				peer.Server = ownedResource{Kind: "container", Name: "peer-server", Token: newID()}
				peer.Volume = ownedResource{Kind: "volume", Name: "peer-volume", Token: newID()}
				peer.Network = ownedResource{Kind: "network", Name: "peer-network", Token: newID()}
				sibling := sharedAllocation{ServiceID: peer.ID, Request: v1.ManagerRequest{Version: v1.ManagerProtocolVersion, Operation: "allocate", AllocationID: newID(), Ownership: newID(), Database: "bl" + newID()[:32], Username: "bl" + newID()[:32], Password: newID()}}
				if err := state.db.Update(func(tx *bolt.Tx) error {
					parent, err := load(tx, instance.ID)
					if err != nil {
						return err
					}
					parent.Instance.Plan.Resources["pending"] = v1.Resource{Kind: "allocation", SharedService: "peer"}
					if err = put(tx, "instances", instance.ID, parent); err != nil {
						return err
					}
					if err = put(tx, "shared_services", peer.ID, peer); err != nil {
						return err
					}
					return put(tx, "shared_allocations", instance.ID, map[string]sharedAllocation{"db": a, "pending": sibling})
				}); err != nil {
					t.Fatal(err)
				}
				if count, err := state.sharedDependents(r.ID, true); err != nil || count != 0 {
					t.Fatal("settled acknowledgment still blocks service deletion", count, err)
				}
				if count, err := state.sharedDependents(peer.ID, true); err != nil || count != 1 {
					t.Fatal("unresolved sibling lost its service fence", count, err)
				}
				// This is shared destroy's final storage transaction after exact effects settle.
				if err := state.db.Update(func(tx *bolt.Tx) error {
					if err := tx.Bucket([]byte("shared_services")).Delete([]byte(r.ID)); err != nil {
						return err
					}
					if kind == "tampered-release" {
						a.Result.AllocationID = newID()
						return put(tx, "shared_allocations", instance.ID, map[string]sharedAllocation{"db": a, "pending": sibling})
					}
					return nil
				}); err != nil {
					t.Fatal(err)
				}
			}
			retained := stateContents(t, state)
			if err := state.db.Close(); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(state.directory, "state.db")
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			reopened, err := openStore(state.directory)
			if kind == "intent" || kind == "partial" || kind == "released" || kind == "settled-service-deleted" {
				if err != nil {
					t.Fatal("valid interrupted journal refused", err)
				}
				if kind == "settled-service-deleted" {
					if !reflect.DeepEqual(retained, stateContents(t, reopened)) {
						t.Fatal("reopen changed settled or unresolved sibling bytes")
					}
					if _, err := reopened.inspect(instance.ID); err != nil {
						t.Fatal("typed parent read failed", err)
					}
					allocations, err := reopened.sharedAllocations(instance.ID)
					if err != nil || len(allocations) != 2 || allocations["db"] != a || allocations["pending"].Released {
						t.Fatal("settled tombstone or unresolved sibling changed", err)
					}
					if _, err := reopened.sharedService(r.ID); err == nil {
						t.Fatal("deleted service recreated")
					}
					if count, err := reopened.sharedDependents(allocations["pending"].ServiceID, true); err != nil || count != 1 {
						t.Fatal("unresolved sibling fence changed on reopen", err)
					}
				}
				if err = reopened.db.Close(); err != nil {
					t.Fatal(err)
				}
			} else {
				if err == nil {
					_ = reopened.db.Close()
					t.Fatal("malformed authority accepted")
				}
				code(t, err, "state_corrupt")
				after, readErr := os.ReadFile(path)
				if readErr != nil || !bytes.Equal(before, after) {
					t.Fatal("rejection changed DB bytes", readErr)
				}
			}
		})
	}
}

func TestSharedAbandonedDataBeforeRelease(t *testing.T) {
	for _, generation := range []bool{false, true} {
		for _, force := range []bool{false, true} {
			svc, instance, _ := exportFixture(t)
			state := svc.store
			if err := state.db.Update(func(tx *bolt.Tx) error {
				r, err := load(tx, instance.ID)
				if err != nil {
					return err
				}
				r.NativeAbandonments = []nativeAbandonment{{Pending: true, RecordedAt: time.Now().UTC().Format(time.RFC3339Nano), Reason: "explicit_force_stop"}}
				return put(tx, "instances", instance.ID, r)
			}); err != nil {
				t.Fatal(err)
			}
			allocation := sharedAllocation{ServiceID: newID(), Request: v1.ManagerRequest{Version: v1.ManagerProtocolVersion, Operation: "allocate", AllocationID: newID(), Ownership: newID(), Database: "bl" + newID()[:32], Username: "bl" + newID()[:32], Password: newID()}}
			if err := state.saveSharedAllocations(instance.ID, map[string]sharedAllocation{"db": allocation}); err != nil {
				t.Fatal(err)
			}
			if generation {
				if err := state.saveResources(instance.ID, resourceGeneration{ID: newID(), Resources: map[string]ownedResource{}, Initialized: map[string]bool{}}); err != nil {
					t.Fatal(err)
				}
			}
			before := stateContents(t, state)
			code(t, state.removeResourcesWithForce(context.Background(), instance.ID, force), "native_ownership_relinquished")
			if !reflect.DeepEqual(before, stateContents(t, state)) {
				t.Fatal("abandoned-data guard ran after destructive intent")
			}
		}
	}
}

func TestSharedShutdownAfterJoinedCollectionFailure(t *testing.T) {
	svc, instance, _ := exportFixture(t)
	result := instance.Execution
	result.CollectionFailure = "retained collection gap"
	if _, err := svc.store.execution(instance.ID, v1.Failed, result); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	close(done)
	svc.executions = map[string]*execution{instance.ID: {done: done, cancel: func() {}}}
	var inspections atomic.Int32
	engine := forceEngine(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Error("shutdown issued unexpected provider mutation")
		}
		inspections.Add(1)
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"No such container"}`))
	})
	server := sharedLockRecord(newID())
	server.Endpoint = engine.DaemonHost()
	server.Configuration = sharedDigest(server.Declaration, server.Endpoint)
	server.Server.Removed = false
	server.Server.Name = "missing-recorded-server"
	server.Server.Kind = "container"
	if err := svc.store.saveShared(server); err != nil {
		t.Fatal(err)
	}
	code(t, svc.stop(), "collection_failed")
	if !svc.ownersJoined() || inspections.Load() != 1 {
		t.Fatal("joined collection failure suppressed independent shared stop")
	}
	// A known prepared consumer still fences its own server, without a provider call.
	if _, err := svc.store.execution(instance.ID, v1.Prepared, result); err != nil {
		t.Fatal(err)
	}
	allocation := sharedAllocation{ServiceID: server.ID, Request: v1.ManagerRequest{Version: v1.ManagerProtocolVersion, Operation: "allocate", AllocationID: newID(), Ownership: newID(), Database: "bl" + newID()[:32], Username: "bl" + newID()[:32], Password: newID()}}
	if err := svc.store.saveSharedAllocations(instance.ID, map[string]sharedAllocation{"db": allocation}); err != nil {
		t.Fatal(err)
	}
	code(t, svc.store.stopSharedServices(context.Background()), "shared_in_use")
	if inspections.Load() != 1 {
		t.Fatal("known consumer fence contacted provider")
	}
}
