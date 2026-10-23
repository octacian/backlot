//go:build !windows

package daemon

import (
	"context"
	"sync"
	"testing"
	"time"

	v1 "github.com/octacian/backlot/api/v1"
	"github.com/octacian/backlot/internal/plan"
)

// Authority races need the real coordinator/store, not application subprocesses.
func attachedCoordinator(t *testing.T) (*service, v1.RunRequest) {
	t.Helper()
	s, root, config := sceneFixture(t)
	s.directory = s.store.directory
	s.resolve = func(source plan.Source, request v1.PlanRequest) (plan.Snapshot, error) {
		snapshot, err := plan.Prepare(source, request)
		snapshot.Plan.Components = nil
		return snapshot, err
	}
	t.Cleanup(func() {
		if err := s.stop(); err != nil {
			t.Error(err)
		}
	})
	return s, v1.RunRequest{APIVersion: v1.Version, Attached: true, Plan: request(root, config, "dev").Plan}
}

func ownerRequest(response v1.InstanceResponse) v1.AttachedRequest {
	return v1.AttachedRequest{APIVersion: v1.Version, InstanceID: response.Instance.ID, Owner: response.Attachment.Owner}
}

func TestAttachedAcceptanceAndStaleAuthority(t *testing.T) {
	s, request := attachedCoordinator(t)
	results := make(chan v1.InstanceResponse, 2)
	failures := make(chan error, 2)
	var workers sync.WaitGroup
	for range 2 {
		workers.Go(func() { response, err := s.run(context.Background(), request); results <- response; failures <- err })
	}
	workers.Wait()
	close(results)
	close(failures)
	var accepted v1.InstanceResponse
	for response := range results {
		if response.Attachment != nil {
			if accepted.Attachment != nil {
				t.Fatal("two owners")
			}
			accepted = response
		}
	}
	rejected := 0
	for err := range failures {
		if err != nil {
			code(t, err, "conflict")
			rejected++
		}
	}
	if accepted.Attachment == nil || rejected != 1 {
		t.Fatal("exclusive acceptance failed")
	}
	owner := ownerRequest(accepted)
	ordinary := request
	ordinary.Attached = false
	joined, err := s.run(context.Background(), ordinary)
	if err != nil || joined.Attachment != nil || joined.Instance.ID != accepted.Instance.ID {
		t.Fatal("ordinary join gained authority", err)
	}
	if _, err = s.attached(owner, false); err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	old := s.executions[owner.InstanceID]
	old.attachment.expires = time.Now().Add(-time.Second)
	s.mu.Unlock()
	if _, err = s.attached(owner, false); err == nil {
		t.Fatal("expired authority revived")
	}
	if err = s.expireOwners(); err != nil {
		t.Fatal(err)
	}
	awaitControlled(t, old.done, "expired attached owner did not join")
	// Replace under the same logical ID, then simulate an already queued old expiry.
	replacement, err := s.run(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if replacement.Instance.ID != owner.InstanceID || replacement.Attachment.Owner.Attempt == owner.Owner.Attempt {
		t.Fatal("replacement identity")
	}
	if err = s.expireRuntime(owner.InstanceID, old); err != nil {
		t.Fatal(err)
	}
	if _, err = s.attached(owner, true); err == nil {
		t.Fatal("stale cancellation accepted")
	}
	if _, err = s.attached(ownerRequest(replacement), false); err != nil {
		t.Fatal("stale operation affected replacement", err)
	}
	for _, mutate := range []func(*v1.AttachedRequest){
		func(r *v1.AttachedRequest) { r.Owner.Token = newID() },
		func(r *v1.AttachedRequest) { r.Owner.DaemonGeneration = newID() },
		func(r *v1.AttachedRequest) { r.Owner.Attempt = newID() },
	} {
		invalid := ownerRequest(replacement)
		mutate(&invalid)
		if _, err = s.attached(invalid, true); err == nil {
			t.Fatal("invalid authority accepted")
		}
	}
	stored, err := s.store.inspect(owner.InstanceID)
	if err != nil || stored.LeaseExpiresAt != "" {
		t.Fatal("persistent lease was stored", err)
	}
}

func TestAttachedOperatorOverrides(t *testing.T) {
	for _, action := range []string{"stop", "scene-stop", "cancel", "restart", "reset", "destroy"} {
		t.Run(action, func(t *testing.T) {
			s, request := attachedCoordinator(t)
			response, err := s.run(context.Background(), request)
			if err != nil {
				t.Fatal(err)
			}
			owner := ownerRequest(response)
			s.mu.Lock()
			old := s.executions[owner.InstanceID]
			s.mu.Unlock()
			switch action {
			case "stop":
				_, err = s.stopRuntimeRequest(context.Background(), v1.StopRequest{InstanceID: owner.InstanceID})
			case "scene-stop":
				_, err = s.scene(v1.SceneTarget{InstanceID: owner.InstanceID}, true)
			case "cancel":
				_, err = s.cancelRuntime(owner.InstanceID)
			case "destroy":
				_, err = s.destroy(context.Background(), owner.InstanceID)
			default:
				request.Attached, request.InstanceID = false, owner.InstanceID
				_, err = s.runMode(context.Background(), request, action == "reset")
			}
			if err != nil {
				t.Fatal(err)
			}
			s.mu.Lock()
			ended := old.attachment.ended
			s.mu.Unlock()
			if !ended {
				t.Fatal("operator did not revoke attachment")
			}
			if _, err = s.attached(owner, true); (action == "restart" || action == "reset") && err == nil {
				t.Fatal("old owner accepted after replacement")
			}
		})
	}
}

func TestAttachedRejectsUnsupportedBeforeIntent(t *testing.T) {
	for _, variant := range []string{"disposable", "keep", "args", "restart"} {
		t.Run(variant, func(t *testing.T) {
			s, request := attachedCoordinator(t)
			switch variant {
			case "disposable":
				request.Plan.Scene = "test"
			case "keep":
				request.Options.KeepOnFailure = true
			case "args":
				request.Plan.TerminalArgs = []string{"extra"}
			case "restart":
				request.InstanceID = newID()
			}
			if _, err := s.run(context.Background(), request); err == nil {
				t.Fatal("unsupported attached request accepted")
			}
			listed, err := s.store.list(v1.ListRequest{})
			if err != nil || len(listed.Instances) != 0 {
				t.Fatal("rejection allocated state", err)
			}
		})
	}
}

func TestAttachedRevocationReadableDuringCleanup(t *testing.T) {
	s, request := attachedCoordinator(t)
	response, err := s.run(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	owner := ownerRequest(response)
	unlock := s.lockMutation(owner.InstanceID)
	defer unlock()
	s.mu.Lock()
	a := s.executions[owner.InstanceID].attachment
	a.ended = true
	expiry := a.expires
	s.mu.Unlock()
	done := make(chan error, 1)
	go func() { _, err := s.attached(owner, false); done <- err }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("renewal/status blocked behind operator cleanup")
	}
	s.mu.Lock()
	unchanged := a.expires.Equal(expiry)
	s.mu.Unlock()
	if !unchanged {
		t.Fatal("revoked authority renewed")
	}
}
