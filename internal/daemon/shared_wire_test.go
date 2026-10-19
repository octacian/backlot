//go:build !windows

package daemon

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	v1 "github.com/octacian/backlot/api/v1"
)

func TestSharedWirePrivacyAndStrictness(t *testing.T) {
	state, err := openStore(shortTemp(t))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = state.db.Close() }()
	newResource := func(kind string) ownedResource {
		return ownedResource{Kind: kind, Name: "backlot-private-name", Token: newID()}
	}
	declaration := v1.SharedService{Manager: "mariadb", Image: "mariadb:11.4", Storage: "volume", Admin: "generated", Port: 3306, ReadinessTimeout: "1m"}
	record := sharedServiceRecord{ID: newID(), Project: sharedProjectIdentity{ID: newID()}, Name: "sql", Declaration: declaration, Endpoint: "unix:///unavailable-private-engine.sock", Configuration: sharedDigest(declaration, "unix:///unavailable-private-engine.sock"), AdminPassword: newID(), Server: newResource("container"), Volume: newResource("volume"), Network: newResource("network"), Status: "intent"}
	if err = state.saveShared(record); err != nil {
		t.Fatal(err)
	}
	handler := handler(&service{store: state}, v1.DaemonStatusResponse{}, func() {})
	perform := func(path, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
		req.Header.Set("X-Backlot-API-Version", v1.Version)
		req.Header.Set("Content-Type", "application/json")
		out := httptest.NewRecorder()
		handler.ServeHTTP(out, req)
		return out
	}
	out := perform("/v1/shared/list", `{"api_version":"v1"}`)
	if out.Code != 200 {
		t.Fatal(out.Body.String())
	}
	var response v1.SharedResponse
	if err = json.Unmarshal(out.Body.Bytes(), &response); err != nil || len(response.Services) != 1 || response.Services[0].ID != record.ID {
		t.Fatal("named public response invalid", err)
	}
	for _, private := range []string{record.AdminPassword, record.Server.Token, record.Network.Token, record.Volume.Token, record.Endpoint, record.Server.Name} {
		if strings.Contains(out.Body.String(), private) {
			t.Fatal("private provisioning authority exposed")
		}
	}
	for _, bad := range []string{`{"api_version":"v1","admin_password":"private"}`, `{"api_version":"v1","service_id":null}`, `{"api_version":"v1","service_id":"private"}`, `{"api_version":"v1","api_version":"v1"}`} {
		out = perform("/v1/shared/inspect", bad)
		if out.Code < 400 || strings.Contains(out.Body.String(), "private") {
			t.Fatal("invalid shared input succeeded or echoed sensitive data")
		}
	}
	// An unresolved consumer cleanup must fence shared stop/recovery even after
	// the instance itself is interrupted. The endpoint is intentionally absent.
	root := shortTemp(t)
	config := writeFixture(t, root)
	svc := &service{store: state, lease: time.Second}
	prepared, err := svc.prepare(context.Background(), request(root, config, "dev"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = state.execution(prepared.Instance.ID, v1.Interrupted, &v1.ExecutionResult{Attempt: newID(), CleanupFailure: "ownership unverified"}); err != nil {
		t.Fatal(err)
	}
	allocation := sharedAllocation{ServiceID: record.ID, Request: v1.ManagerRequest{Version: v1.ManagerProtocolVersion, Operation: "allocate", AllocationID: newID(), Ownership: newID(), Database: "bl" + newID()[:32], Username: "bl" + newID()[:32], Password: newID()}}
	if err = state.saveSharedAllocations(prepared.Instance.ID, map[string]sharedAllocation{"db": allocation}); err != nil {
		t.Fatal(err)
	}
	_, err = svc.sharedControl(context.Background(), "stop", v1.SharedRequest{APIVersion: v1.Version, ServiceID: record.ID})
	code(t, err, "shared_in_use")
	code(t, state.stopSharedServices(context.Background()), "shared_in_use")
}
