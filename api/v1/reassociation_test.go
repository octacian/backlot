package v1

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestReassociationContracts(t *testing.T) {
	id := strings.Repeat("a", 64)
	request := ReassociationRequest{APIVersion: Version, ProjectPath: "/fixture/project", RegistryRoot: "/fixture/registry", ConfigPath: "/fixture/machine", ProjectID: id, CheckoutID: id}
	identity := ProjectIdentity{ID: id, Anchor: "/fixture/project/.git", Filesystem: "1:2", Relative: ".", Project: "fixture"}
	checkout := CheckoutIdentity{ID: id, Path: "/fixture/project", GitDirectory: identity.Anchor}
	association := ProjectAssociation{CheckoutParent: id, Checkout: checkout, ProjectPath: checkout.Path}
	response := ReassociationResponse{APIVersion: Version, DaemonGeneration: id, StateRevision: id, Request: request, Current: identity, Checkout: checkout, Filesystems: []FilesystemObservation{}, Projects: []ProjectRegistration{{Origin: identity, Current: identity}}, Instances: []ReassociationInstance{{ID: id, Scene: "dev", Status: Stopped, Recorded: checkout, Association: association, RecordedFilesystemState: "unrecorded"}}, Services: []SharedStatus{}, Allocations: []ReassociationAllocation{{AllocationID: id, InstanceID: id, ServiceID: id, Resource: "db"}}, Private: ReassociationPrivate{Revision: id}, Changes: []string{}, Preserved: []string{}, Blockers: []string{}, Preview: id}
	if err := response.Validate(); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	var decoded ReassociationResponse
	if err := json.Unmarshal(raw, &decoded); err != nil || decoded.Validate() != nil {
		t.Fatal("typed response roundtrip", err)
	}
	for _, damage := range []func(*ReassociationRequest){func(r *ReassociationRequest) { r.Apply = true }, func(r *ReassociationRequest) { r.Preview = id }, func(r *ReassociationRequest) { r.ProjectPath = "relative" }, func(r *ReassociationRequest) { r.RegistryRoot = "/bad\x00root" }, func(r *ReassociationRequest) { r.CheckoutID = "bad" }, func(r *ReassociationRequest) { r.APIVersion = "v2" }} {
		bad := request
		damage(&bad)
		if bad.Validate() == nil {
			t.Fatal("invalid request accepted")
		}
	}
	for _, damage := range []func(*ReassociationResponse){func(r *ReassociationResponse) { r.Allocations[0].AllocationID = "bad" }, func(r *ReassociationResponse) { r.Preview = "bad" }, func(r *ReassociationResponse) { r.Projects = nil }, func(r *ReassociationResponse) { r.Private.Revision = "" }, func(r *ReassociationResponse) { r.Request.RegistryRoot = "relative" }, func(r *ReassociationResponse) { r.Instances[0].Status = "invented" }, func(r *ReassociationResponse) { r.Instances[0].Association.CheckoutParent = "bad" }} {
		var bad ReassociationResponse
		if err := json.Unmarshal(raw, &bad); err != nil {
			t.Fatal(err)
		}
		damage(&bad)
		if bad.Validate() == nil {
			t.Fatal("invalid daemon response accepted")
		}
	}
}
