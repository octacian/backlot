package client

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	v1 "github.com/octacian/backlot/api/v1"
	"github.com/octacian/backlot/internal/manifest"
)

func TestStopValidatesAcknowledgment(t *testing.T) {
	valid := `{"api_version":"v1","state_version":9,"status":"stopping","pid":10,"lease_duration":"30s"}`
	for _, tc := range []struct {
		name, body, header, content, code string
		status, version                   int
	}{
		{"current", valid, "v1", "application/json", "", 200, 9},
		{"six", strings.Replace(valid, `"state_version":9`, `"state_version":6`, 1), "v1", "application/json", "api_version", 200, 0},
		{"eight", strings.Replace(valid, `"state_version":9`, `"state_version":8`, 1), "v1", "application/json", "api_version", 200, 0},
		{"five", strings.Replace(valid, `"state_version":9`, `"state_version":5`, 1), "v1", "application/json", "api_version", 200, 0},
		{"seven", strings.Replace(valid, `"state_version":9`, `"state_version":7`, 1), "v1", "application/json", "api_version", 200, 0},
		{"future", strings.Replace(valid, `"state_version":9`, `"state_version":99`, 1), "v1", "application/json", "api_version", 200, 0},
		{"version type", strings.Replace(valid, `"state_version":9`, `"state_version":"6"`, 1), "v1", "application/json", "invalid_response", 200, 0},
		{"body API", strings.Replace(valid, `"api_version":"v1"`, `"api_version":"v2"`, 1), "v1", "application/json", "api_version", 200, 0},
		{"header", valid, "v2", "application/json", "api_version", 200, 0},
		{"content", valid, "v1", "text/plain", "invalid_response", 200, 0},
		{"PID", strings.Replace(valid, `"pid":10`, `"pid":0`, 1), "v1", "application/json", "invalid_response", 200, 0},
		{"lease", strings.Replace(valid, `"30s"`, `"99h"`, 1), "v1", "application/json", "invalid_response", 200, 0},
		{"lease minimum", strings.Replace(valid, `"30s"`, `"1ms"`, 1), "v1", "application/json", "invalid_response", 200, 0},
		{"missing", `{"api_version":"v1","state_version":9,"status":"stopping"}`, "v1", "application/json", "invalid_response", 200, 0},
		{"syntax", `{`, "v1", "application/json", "invalid_response", 200, 0},
		{"unknown", strings.Replace(valid, `"pid":10`, `"pid":10,"unknown":true`, 1), "v1", "application/json", "invalid_response", 200, 0},
		{"duplicate", strings.Replace(valid, `"pid":10`, `"pid":10,"pid":11`, 1), "v1", "application/json", "invalid_response", 200, 0},
		{"trailing", valid + ` {}`, "v1", "application/json", "invalid_response", 200, 0},
		{"oversize", strings.Repeat(" ", manifest.MaxBytes+1), "v1", "application/json", "invalid_response", 200, 0},
		{"cleanup error", `{"api_version":"v1","error":{"code":"cleanup_failed","message":"owned cleanup remains uncertain"}}`, "v1", "application/json", "cleanup_failed", 409, 0},
		{"invalid error", valid, "v1", "application/json", "invalid_response", 500, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cli := New("unused")
			defer cli.Close()
			calls := 0
			cli.http.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
				calls++
				if req.Method != http.MethodPost || req.URL.Path != "/v1/stop" || req.Header.Get("X-Backlot-API-Version") != "v1" || req.Header.Get("Content-Type") != "application/json" {
					t.Fatal("unexpected stop request")
				}
				var control v1.ControlRequest
				data, err := io.ReadAll(req.Body)
				if err != nil {
					t.Fatal(err)
				}
				if err := manifest.Decode(data, ".json", &control); err != nil || control.APIVersion != "v1" {
					t.Fatal("invalid control body", err)
				}
				return &http.Response{StatusCode: tc.status, Header: http.Header{"Content-Type": {tc.content}, "X-Backlot-Api-Version": {tc.header}}, Body: io.NopCloser(strings.NewReader(tc.body))}, nil
			})
			result, err := cli.Stop(context.Background())
			if calls != 1 {
				t.Fatal("shutdown retried or preflighted")
			}
			if tc.code == "" {
				if err != nil || result.StateVersion != tc.version || result.Status != "stopping" {
					t.Fatal("invalid acknowledgment", result, err)
				}
			} else {
				var detail *v1.PlanError
				if !errors.As(err, &detail) || detail.Code != tc.code {
					t.Fatalf("error %v, want %s", err, tc.code)
				}
			}
		})
	}
}

func TestPreV9DaemonRejected(t *testing.T) {
	cli := New("unused")
	defer cli.Close()
	stop := `{"api_version":"v1","state_version":6,"status":"stopping","pid":10,"lease_duration":"30s"}`
	stopCalls, statusCalls, mutations := 0, 0, 0
	cli.http.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		body := stop
		if req.Method == http.MethodPost && req.URL.Path == "/v1/stop" {
			stopCalls++
		} else if req.URL.Path == "/v1/status" {
			statusCalls++
			body = strings.Replace(stop, `"stopping"`, `"running"`, 1)
		} else {
			mutations++
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}, "X-Backlot-Api-Version": {"v1"}}, Body: io.NopCloser(strings.NewReader(body))}, nil
	})
	if _, err := cli.Stop(context.Background()); err == nil {
		t.Fatal("pre-v9 shutdown acknowledgment accepted")
	}
	if _, err := cli.Status(context.Background()); err == nil {
		t.Fatal("pre-v9 Status accepted")
	}
	for _, path := range []string{"/v1/prepare", "/v1/run", "/v1/restart", "/v1/reset", "/v1/scenes/start", "/v1/gateway/start", "/v1/gateway/restart"} {
		if err := cli.call(context.Background(), http.MethodPost, path, nil, &v1.InstanceResponse{}); err == nil {
			t.Fatal("pre-v9 mutation accepted", path)
		}
	}
	// The same result type cannot admit an incompatible daemon through another path.
	for _, target := range []struct{ method, path string }{{http.MethodGet, "/v1/stop"}, {http.MethodPost, "/other"}} {
		if err := cli.call(context.Background(), target.method, target.path, nil, &v1.DaemonStatusResponse{}); err == nil {
			t.Fatal("unrelated call accepted")
		}
	}
	if stopCalls != 1 || statusCalls != 8 || mutations != 2 {
		t.Fatal("unexpected transport calls", stopCalls, statusCalls, mutations)
	}
}

func TestStopPreservesTransportAndCancellationErrors(t *testing.T) {
	for _, cancelled := range []bool{false, true} {
		t.Run(map[bool]string{false: "transport", true: "cancellation"}[cancelled], func(t *testing.T) {
			cli := New("unused")
			defer cli.Close()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if cancelled {
				cancel()
			}
			cli.http.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
				if cancelled {
					return nil, req.Context().Err()
				}
				return nil, io.ErrUnexpectedEOF
			})
			if _, err := cli.Stop(ctx); err == nil {
				t.Fatal("shutdown failure became success")
			}
		})
	}
}
