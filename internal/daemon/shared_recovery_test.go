//go:build !windows

package daemon

import (
	"context"
	"errors"
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
