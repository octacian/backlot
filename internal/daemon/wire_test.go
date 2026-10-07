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
	"github.com/octacian/backlot/internal/manifest"
)

func TestStrictServerWireContracts(t *testing.T) {
	root := shortTemp(t)
	config := writeFixture(t, root)
	state, err := openStore(shortTemp(t))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := state.db.Close(); err != nil {
			t.Error(err)
		}
	}()
	svc := &service{store: state, lease: time.Second}
	status := v1.DaemonStatusResponse{APIVersion: v1.Version, StateVersion: v1.StateVersion, Status: "running", PID: 1, LeaseDuration: "1s"}
	handler := handler(svc, status, func() {})
	perform := func(path, version, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
		req.Header.Set("X-Backlot-API-Version", version)
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec
	}
	data, err := json.Marshal(request(root, config, "test"))
	if err != nil {
		t.Fatal(err)
	}
	rec := perform("/v1/prepare", v1.Version, string(data))
	if rec.Code != 200 {
		t.Fatalf("prepare: %s", rec.Body.String())
	}
	var prepared v1.InstanceResponse
	if err := manifest.Decode(rec.Body.Bytes(), ".json", &prepared); err != nil {
		t.Fatal(err)
	}
	if prepared.APIVersion != v1.Version || prepared.Instance.Status != v1.Prepared {
		t.Fatalf("invalid response: %+v", prepared)
	}
	for _, secret := range []string{"resolved-fixture-secret", "seed-fixture-secret", "literal-fixture-secret"} {
		if strings.Contains(rec.Body.String(), secret) {
			t.Fatal("wire secret leak")
		}
	}
	cases := []struct{ name, path, header, body, code string }{
		{"missing header", "/v1/prepare", "", string(data), "api_version"},
		{"new header", "/v1/prepare", "v2", string(data), "api_version"},
		{"new body", "/v1/stop", "v1", `{"api_version":"v2"}`, "api_version"},
		{"unknown field", "/v1/stop", "v1", `{"api_version":"v1","untrusted":"fixture-secret"}`, "invalid_document"},
		{"duplicate field", "/v1/stop", "v1", `{"api_version":"v1","api_version":"v1"}`, "invalid_document"},
		{"case mismatch", "/v1/stop", "v1", `{"APIVersion":"v1"}`, "invalid_document"},
		{"null", "/v1/stop", "v1", `{"api_version":null}`, "invalid_document"},
		{"scalar type", "/v1/stop", "v1", `{"api_version":1}`, "invalid_document"},
		{"trailing", "/v1/stop", "v1", `{"api_version":"v1"} {}`, "invalid_document"},
		{"oversize", "/v1/stop", "v1", strings.Repeat(" ", manifest.MaxBytes+1), "invalid_request"},
		{"invalid target", "/v1/inspect", "v1", `{"api_version":"v1","instance_id":"fixture-secret"}`, "invalid_request"},
		{"missing scene", "/v1/prepare", "v1", `{"api_version":"v1","plan":{"project_path":"/tmp"}}`, "invalid_request"},
		{"relative project", "/v1/prepare", "v1", `{"api_version":"v1","plan":{"scene":"dev","project_path":"relative"}}`, "invalid_request"},
		{"unknown namespace", "/v2/prepare", "v1", string(data), "not_found"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			rec := perform(test.path, test.header, test.body)
			if rec.Code < 400 {
				t.Fatalf("invalid input succeeded: %s", rec.Body.String())
			}
			var envelope v1.ErrorResponse
			if err := manifest.Decode(rec.Body.Bytes(), ".json", &envelope); err != nil {
				t.Fatal(err)
			}
			if envelope.APIVersion != v1.Version || envelope.Error.Code != test.code || envelope.Error.Message == "" {
				t.Fatalf("bad envelope: %+v", envelope)
			}
			if strings.Contains(rec.Body.String(), "fixture-secret") {
				t.Fatal("error echoed untrusted secret")
			}
		})
	}
	// The server consumes the same named target contract it emits in the preparation response.
	target, err := json.Marshal(v1.InstanceRequest{APIVersion: v1.Version, InstanceID: prepared.Instance.ID})
	if err != nil {
		t.Fatal(err)
	}
	rec = perform("/v1/inspect", v1.Version, string(target))
	var inspected v1.InstanceResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &inspected); err != nil {
		t.Fatal(err)
	}
	if inspected.LeaseToken != "" || inspected.Instance.Operation.ID != prepared.Instance.Operation.ID {
		t.Fatal("inspect contract lost stable operation or leaked capability")
	}
	lease, err := json.Marshal(v1.LeaseRequest{APIVersion: v1.Version, InstanceID: prepared.Instance.ID, LeaseToken: prepared.LeaseToken})
	if err != nil {
		t.Fatal(err)
	}
	if rec := perform("/v1/renew", v1.Version, string(lease)); rec.Code != 200 {
		t.Fatal(rec.Body.String())
	}
	if rec := perform("/v1/cancel", v1.Version, string(target)); rec.Code != 200 {
		t.Fatal(rec.Body.String())
	}
}

func TestInterruptedAndCancelledAcceptanceIsDaemonOwned(t *testing.T) {
	root := shortTemp(t)
	config := writeFixture(t, root)
	state, err := openStore(shortTemp(t))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := state.db.Close(); err != nil {
			t.Error(err)
		}
	}()
	ctx, cancel := context.WithCancel(context.Background())
	svc := &service{store: state, lease: time.Second, checkpoint: func(_ string) error { cancel(); return nil }}
	prepared, err := svc.prepare(ctx, request(root, config, "dev"))
	if err != nil || prepared.Instance.Status != v1.Prepared {
		t.Fatalf("disconnect cancelled accepted persistent preparation: %+v %v", prepared, err)
	}
	_, err = svc.prepare(ctx, request(root, config, "test"))
	code(t, err, "cancelled")
	if err := svc.stop(); err != nil {
		t.Fatal(err)
	}
	_, err = svc.prepare(context.Background(), request(root, config, "test"))
	code(t, err, "shutting_down")
}
