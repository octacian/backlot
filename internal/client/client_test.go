package client

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	v1 "github.com/octacian/backlot/api/v1"
	"github.com/octacian/backlot/internal/manifest"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestClientEncodesNamedRequestsAndDecodesResponses(t *testing.T) {
	id := strings.Repeat("a", 64)
	token := strings.Repeat("b", 64)
	instance := v1.InstanceResponse{APIVersion: v1.Version, Instance: v1.Instance{ID: id, Checkout: v1.CheckoutIdentity{ID: id, Path: "/tmp/fixture"}, Status: v1.Prepared, CreatedAt: "2026-10-07T12:00:00Z", LeaseExpiresAt: "2026-10-07T12:00:30Z", Operation: v1.Operation{ID: id, Status: v1.Prepared, Allocations: []v1.AllocationRecord{{ID: id, OwnershipToken: token, Kind: "private_snapshot", Phase: "effect"}}}, Plan: v1.PlanResponse{APIVersion: v1.Version, Project: "fixture", Scene: "test", Lifetime: v1.Disposable, Components: []v1.PlannedComponent{}}}}
	status := v1.DaemonStatusResponse{APIVersion: v1.Version, StateVersion: v1.StateVersion, Status: "running", PID: 10, LeaseDuration: "30s"}
	cli := New("unused")
	defer cli.Close()
	cli.http.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Header.Get("X-Backlot-API-Version") != v1.Version || r.URL.Host != "backlot" {
			t.Fatal("missing transport compatibility header")
		}
		var response any = instance
		if r.URL.Path == "/v1/status" {
			if r.Method != http.MethodGet {
				t.Fatal("status method")
			}
			response = status
		} else {
			data, err := io.ReadAll(r.Body)
			if err != nil {
				t.Fatal(err)
			}
			if r.Header.Get("Content-Type") != "application/json" || r.Method != http.MethodPost {
				t.Fatal("missing JSON request contract")
			}
			switch r.URL.Path {
			case "/v1/prepare":
				var request v1.PrepareRequest
				if err := manifest.Decode(data, ".json", &request); err != nil || request.APIVersion != v1.Version || request.Plan.Scene != "test" {
					t.Fatalf("prepare request: %+v %v", request, err)
				}
			case "/v1/inspect", "/v1/cancel":
				var request v1.InstanceRequest
				if err := manifest.Decode(data, ".json", &request); err != nil || request.InstanceID != id {
					t.Fatalf("target request: %+v %v", request, err)
				}
			case "/v1/renew":
				var request v1.LeaseRequest
				if err := manifest.Decode(data, ".json", &request); err != nil || request.LeaseToken != token || request.InstanceID != id {
					t.Fatalf("lease request: %+v %v", request, err)
				}
			case "/v1/stop":
				var request v1.ControlRequest
				if err := manifest.Decode(data, ".json", &request); err != nil || request.APIVersion != v1.Version {
					t.Fatalf("control request: %+v %v", request, err)
				}
				response = status
			default:
				t.Fatal("unexpected endpoint")
			}
		}
		data, err := json.Marshal(response)
		if err != nil {
			t.Fatal(err)
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}, "X-Backlot-Api-Version": []string{v1.Version}}, Body: io.NopCloser(strings.NewReader(string(data)))}, nil
	})
	ctx := context.Background()
	if _, err := cli.Status(ctx); err != nil {
		t.Fatal(err)
	}
	if r, err := cli.Prepare(ctx, v1.PrepareRequest{APIVersion: v1.Version, Plan: v1.PlanRequest{Scene: "test", ProjectPath: "/tmp/fixture"}}); err != nil || r.Instance.ID != id {
		t.Fatal(err)
	}
	if _, err := cli.Inspect(ctx, id); err != nil {
		t.Fatal(err)
	}
	if _, err := cli.Cancel(ctx, id); err != nil {
		t.Fatal(err)
	}
	if _, err := cli.Renew(ctx, id, token); err != nil {
		t.Fatal(err)
	}
	if _, err := cli.Stop(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestClientRejectsIncompatibleMalformedAndOversizeResponses(t *testing.T) {
	valid := `{"api_version":"v1","state_version":2,"status":"running","pid":1,"lease_duration":"30s"}`
	for _, test := range []struct {
		name, body, header, code string
		status                   int
	}{
		{"header", valid, "v2", "api_version", 200},
		{"body version", strings.Replace(valid, `"v1"`, `"v2"`, 1), "v1", "api_version", 200},
		{"state version", strings.Replace(valid, `"state_version":2`, `"state_version":99`, 1), "v1", "api_version", 200},
		{"unknown", strings.Replace(valid, `"pid":1`, `"extra":1`, 1), "v1", "invalid_response", 200},
		{"duplicate", strings.Replace(valid, `"pid":1`, `"pid":1,"pid":2`, 1), "v1", "invalid_response", 200},
		{"trailing", valid + ` {}`, "v1", "invalid_response", 200},
		{"missing fields", `{"api_version":"v1","state_version":2}`, "v1", "invalid_response", 200},
		{"unknown status", strings.Replace(valid, `"running"`, `"ready"`, 1), "v1", "invalid_response", 200},
		{"oversize", strings.Repeat(" ", manifest.MaxBytes+1), "v1", "invalid_response", 200},
		{"typed error", `{"api_version":"v1","error":{"code":"state_version","message":"use compatible binary"}}`, "v1", "state_version", 409},
		{"invalid error", `{"api_version":"v1","error":{}}`, "v1", "invalid_response", 500},
	} {
		t.Run(test.name, func(t *testing.T) {
			cli := New("unused")
			defer cli.Close()
			cli.http.Transport = roundTripFunc(func(_ *http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: test.status, Header: http.Header{"Content-Type": []string{"application/json"}, "X-Backlot-Api-Version": []string{test.header}}, Body: io.NopCloser(strings.NewReader(test.body))}, nil
			})
			_, err := cli.Status(context.Background())
			var detail *v1.PlanError
			if !errors.As(err, &detail) || detail.Code != test.code {
				t.Fatalf("error %v, want %s", err, test.code)
			}
		})
	}
}
