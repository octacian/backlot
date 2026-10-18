package v1

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestDoctorSelectionContract(t *testing.T) {
	id := strings.Repeat("a", 64)
	for _, r := range []DoctorRequest{{APIVersion: Version}, {APIVersion: Version, ProjectPath: "/project", Scene: "dev", ConfigPath: "/machine.yaml"}, {APIVersion: Version, InstanceID: id}, {APIVersion: Version, InstanceID: id, ContainerProbeImage: "diagnostic:local", ProbePublicCA: true}} {
		if err := r.Validate(); err != nil {
			t.Fatal(r, err)
		}
	}
	for _, r := range []DoctorRequest{{}, {APIVersion: Version, Scene: "dev"}, {APIVersion: Version, ProjectPath: "/project"}, {APIVersion: Version, ConfigPath: "/config"}, {APIVersion: Version, ProjectPath: "/project", Scene: "dev", ConfigPath: "relative"}, {APIVersion: Version, InstanceID: "not-an-id"}, {APIVersion: Version, InstanceID: strings.Repeat("A", 64)}, {APIVersion: Version, InstanceID: id, Scene: "dev"}, {APIVersion: Version, InstanceID: id, ConfigPath: "/config"}, {APIVersion: Version, ContainerProbeImage: "local"}, {APIVersion: Version, InstanceID: id, ProbePublicCA: true}, {APIVersion: Version, InstanceID: id, ContainerProbeImage: "secret\nvalue"}} {
		if r.Validate() == nil {
			t.Fatalf("accepted ambiguous/invalid request %+v", r)
		}
	}
}
func TestDoctorResponseContract(t *testing.T) {
	response := DoctorResponse{APIVersion: Version, Scope: "scene", Healthy: true, Paths: []DoctorPath{{Name: "machine", State: "absent", Path: "/config"}}, Checks: []DoctorCheck{{Name: "runtime", Status: DoctorDeferred, Context: "daemon/host", Message: "start explicitly"}, {Name: "external_ca", Status: DoctorUnsupported, Context: "operator", Message: "external files unknown"}}}
	data, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	var decoded DoctorResponse
	if err := json.Unmarshal(data, &decoded); err != nil || decoded.Validate() != nil {
		t.Fatal(err, decoded)
	}
	response.Checks[0].Status = DoctorFailed
	if response.Validate() == nil {
		t.Fatal("healthy accepted despite failed check")
	}
	response.Healthy = false
	if response.Validate() != nil {
		t.Fatal("failed check should validate")
	}
	response.Checks[0].Status = "maybe"
	if response.Validate() == nil {
		t.Fatal("unknown status accepted")
	}
	response.Checks[0].Status = DoctorFailed
	response.Paths[0].Path = "relative"
	if response.Validate() == nil {
		t.Fatal("relative diagnostic path accepted")
	}
}
