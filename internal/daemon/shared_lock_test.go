//go:build !windows

package daemon

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	v1 "github.com/octacian/backlot/api/v1"
	"github.com/octacian/backlot/internal/plan"
)

func TestSharedWaitingCancellation(t *testing.T) {
	for _, operation := range []string{"allocate", "release", "inspect", "list", "start", "restart", "stop", "destroy", "shutdown"} {
		for _, deadline := range []bool{false, true} {
			t.Run(operation+map[bool]string{false: "/cancel", true: "/deadline"}[deadline], func(t *testing.T) {
				state, err := openStore(shortTemp(t))
				if err != nil {
					t.Fatal(err)
				}
				defer func() { _ = state.db.Close() }()
				svc := &service{store: state}
				root := shortTemp(t)
				project, err := identifyShared(plan.Source{ProjectPath: root, CheckoutPath: root}, "fixture")
				if err != nil {
					t.Fatal(err)
				}
				serviceID := hash(project.ID + "/sql")
				id := newID()
				allocations := map[string]sharedAllocation{"db": {ServiceID: serviceID, Request: v1.ManagerRequest{Version: v1.ManagerProtocolVersion, Operation: "allocate", AllocationID: newID(), Ownership: newID(), Database: "bl" + newID()[:32], Username: "bl" + newID()[:32], Password: newID()}}}
				if err = state.saveSharedAllocations(id, allocations); err != nil {
					t.Fatal(err)
				}
				record := sharedLockRecord(serviceID)
				if err = state.saveShared(record); err != nil {
					t.Fatal(err)
				}
				unlock, err := state.lockShared(context.Background(), serviceID)
				if err != nil {
					t.Fatal(err)
				}
				held := true
				defer func() {
					if held {
						unlock()
					}
				}()
				ctx, cancel := context.WithCancel(context.Background())
				expected := context.Canceled
				if deadline {
					cancel()
					ctx, cancel = context.WithTimeout(context.Background(), 30*time.Millisecond)
					expected = context.DeadlineExceeded
				}
				defer cancel()
				done := make(chan error, 1)
				go func() {
					switch operation {
					case "allocate":
						snap := plan.Snapshot{Plan: v1.PlanResponse{Project: "fixture", SharedServices: map[string]v1.SharedService{"sql": {}}, Resources: map[string]v1.Resource{"db": {Kind: "allocation", SharedService: "sql"}}}}
						done <- svc.ensureSharedAllocations(ctx, id, snap, &execution{source: capturedSource{source: plan.Source{ProjectPath: root, CheckoutPath: root}}})
					case "shutdown":
						done <- state.stopSharedServices(ctx)
					case "release":
						done <- state.releaseSharedAllocations(ctx, id)
					default:
						request := v1.SharedRequest{ServiceID: serviceID}
						if operation == "list" {
							request.ServiceID = ""
						}
						_, err := svc.sharedControl(ctx, operation, request)
						done <- err
					}
				}()
				if !deadline {
					cancel()
				}
				select {
				case err = <-done:
					if !errors.Is(err, expected) {
						t.Errorf("wanted cancellation, got %v", err)
					}
				case <-time.After(time.Second):
					t.Error("canceled operation waited for serialization owner")
					unlock()
					held = false
					<-done
				}
				after, err := state.sharedAllocations(id)
				if err != nil || !reflect.DeepEqual(after, allocations) {
					t.Fatal("waiting changed durable allocation obligations", err)
				}
				afterRecord, err := state.sharedService(serviceID)
				if err != nil || !reflect.DeepEqual(record, afterRecord) {
					t.Fatal("waiting changed server ownership/effect intent", err)
				}
			})
		}
	}
}

func sharedLockRecord(id string) sharedServiceRecord {
	declaration := v1.SharedService{Manager: "mariadb", Image: "mariadb:11.4", Storage: "volume", Admin: "generated", Port: 3306, ReadinessTimeout: "1m"}
	endpoint := "unix:///unavailable-shared-lock-fixture.sock"
	return sharedServiceRecord{ID: id, Project: sharedProjectIdentity{ID: newID()}, Name: "sql", Declaration: declaration, Endpoint: endpoint, Configuration: sharedDigest(declaration, endpoint), AdminPassword: newID(), Server: ownedResource{Token: newID(), Removed: true}, Volume: ownedResource{Token: newID()}, Network: ownedResource{Token: newID()}, Status: "stopped"}
}

func TestSharedSerializationAndPrivateCleanup(t *testing.T) {
	state, err := openStore(shortTemp(t))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = state.db.Close() }()
	svc := &service{store: state}
	id := newID()
	record := sharedLockRecord(id)
	if err = state.saveShared(record); err != nil {
		t.Fatal(err)
	}
	unlock, err := state.lockShared(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	held := true
	defer func() {
		if held {
			unlock()
		}
	}()
	// The same-service inspection cannot observe an in-progress owner. Its
	// deadline must expire while that owner still holds serialization.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	_, err = svc.sharedControl(ctx, "inspect", v1.SharedRequest{ServiceID: id})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("same-service operation escaped exclusion", err)
	}
	// An unrelated service and private instance cleanup complete while held.
	peer := sharedLockRecord(newID())
	if err = state.saveShared(peer); err != nil {
		t.Fatal(err)
	}
	root := shortTemp(t)
	config := writeFixture(t, root)
	prepared, err := svc.prepare(context.Background(), request(root, config, "dev"))
	if err != nil {
		t.Fatal(err)
	}
	privateID := prepared.Instance.ID
	if err = state.saveResources(privateID, resourceGeneration{ID: newID(), Initialized: map[string]bool{}, Resources: map[string]ownedResource{"secret": {Kind: "secret", Name: "private-secret", Token: newID()}}}); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := svc.sharedControl(context.Background(), "inspect", v1.SharedRequest{ServiceID: peer.ID})
		if err == nil {
			err = state.removeResources(context.Background(), privateID)
		}
		done <- err
	}()
	select {
	case err = <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		unlock()
		held = false
		<-done
		t.Fatal("unrelated operation waited for shared owner")
	}
	generation, err := state.resources(privateID)
	if err != nil || generation.ID != "" {
		t.Fatal("private cleanup did not finish", err)
	}
	unlock()
	held = false
	response, err := svc.sharedControl(context.Background(), "inspect", v1.SharedRequest{ServiceID: id})
	if err != nil || len(response.Services) != 1 {
		t.Fatal("operation failed after owner release", err)
	}
	if err = state.stopSharedServices(context.Background()); err != nil {
		t.Fatal("settled shutdown failed", err)
	}
}
