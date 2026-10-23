package manager

import (
	v1 "github.com/octacian/backlot/api/v1"
	"strings"
	"testing"
)

func TestOwnershipProtocol(t *testing.T) {
	request := v1.ManagerRequest{Version: v1.ManagerProtocolVersion, Operation: "allocate", AllocationID: strings.Repeat("a", 64), Ownership: strings.Repeat("b", 64), Database: "bl" + strings.Repeat("c", 32), Username: "bl" + strings.Repeat("d", 32), Password: strings.Repeat("e", 64)}
	if err := ValidateRequest(request); err != nil {
		t.Fatal(err)
	}
	for _, operation := range []string{"allocate", "inspect", "release"} {
		next := request
		next.Operation = operation
		if err := ValidateRequest(next); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"blpeer%", "blpeer_", "bl'; DROP USER x", "bl`"} {
		next := request
		next.Database = name
		if ValidateRequest(next) == nil {
			t.Fatal("SQL identifier/wildcard accepted")
		}
	}
	for _, result := range []v1.ManagerResult{
		{Version: v1.ManagerProtocolVersion, AllocationID: request.AllocationID, Ownership: "uncertain", Effect: "ready"},
		{Version: v1.ManagerProtocolVersion, AllocationID: request.AllocationID, Ownership: "owned", Effect: "released"},
		{Version: v1.ManagerProtocolVersion, AllocationID: strings.Repeat("f", 64), Ownership: "owned", Effect: "ready"},
		{Version: v1.ManagerProtocolVersion, AllocationID: request.AllocationID, Ownership: "unknown", Effect: "partial"},
	} {
		if ValidateResult(request, result) == nil {
			t.Fatal("invalid structured ownership/effect accepted")
		}
	}
	for _, pair := range [][2]string{{"owned", "ready"}, {"absent", "released"}, {"uncertain", "partial"}, {"owned", "partial"}} {
		if err := ValidateResult(request, v1.ManagerResult{Version: v1.ManagerProtocolVersion, AllocationID: request.AllocationID, Ownership: pair[0], Effect: pair[1]}); err != nil {
			t.Fatal(err)
		}
	}
	if strings.Contains(fingerprint(request.Ownership), request.Ownership) {
		t.Fatal("raw authority in public database comment")
	}
}

func TestStructuredCommandResults(t *testing.T) {
	request := v1.ManagerRequest{AllocationID: strings.Repeat("a", 64)}
	valid := `{"version":"backlot.manager/v1","allocation_id":"` + request.AllocationID + `","ownership":"owned","effect":"ready"}`
	if _, err := DecodeResult(request, []byte(valid)); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{
		valid + valid,
		strings.Replace(valid, `"ownership":"owned"`, `"ownership":"owned","ownership":"absent"`, 1),
		strings.Replace(valid, `"effect":"ready"`, `"effect":"ready","unknown_secret":"private"`, 1),
		strings.Repeat(" ", MaxDocumentBytes+1),
		`{"version":"backlot.manager/v1","allocation_id":null,"ownership":"owned","effect":"ready"}`,
	} {
		if _, err := DecodeResult(request, []byte(bad)); err == nil {
			t.Fatal("malformed/ambiguous/unbounded manager result accepted")
		}
	}
}

func TestForceReleaseAuthorization(t *testing.T) {
	request := v1.ManagerRequest{Version: v1.ManagerProtocolVersion, AllocationID: strings.Repeat("a", 64), Ownership: strings.Repeat("b", 64), Database: "bl" + strings.Repeat("c", 32), Username: "bl" + strings.Repeat("d", 32), Password: strings.Repeat("e", 64), Force: true}
	for _, operation := range []string{"allocate", "inspect", "release", "destroy"} {
		request.Operation = operation
		if err := ValidateRequest(request); (err == nil) != (operation == "release") {
			t.Fatal("force accepted outside release or rejected for release", operation, err)
		}
	}
	request.Operation = "release"
	if err := ValidateResult(request, v1.ManagerResult{Version: request.Version, AllocationID: request.AllocationID, Ownership: "uncertain", Effect: "released"}); err == nil {
		t.Fatal("force accepted unverified absence")
	}
}
