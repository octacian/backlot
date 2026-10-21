package client

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	v1 "github.com/octacian/backlot/api/v1"
)

func TestForceRequestsRetainAcknowledgedPreconditions(t *testing.T) {
	for _, gateway := range []bool{false, true} {
		name := "scene"
		if gateway {
			name = "managed"
		}
		t.Run(name, func(t *testing.T) {
			selected := strings.Repeat("a", 64)
			cli := New("unused")
			defer cli.Close()
			calls := 0
			cli.http.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
				calls++
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Fatal(err)
				}
				key, path := "expected_attempt", "/v1/runtime/stop"
				if gateway {
					key, path = "expected_claim_revision", "/v1/gateway/stop"
				}
				if r.URL.Path != path || body[key] != selected || body["force"] != true {
					t.Fatal("acknowledged precondition changed", r.URL.Path, body)
				}
				return &http.Response{StatusCode: http.StatusConflict, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"api_version":"v1","error":{"code":"conflict","message":"target replaced"}}`))}, nil
			})
			for range 2 {
				var err error
				if gateway {
					_, err = cli.Gateway(context.Background(), "stop", v1.GatewayRequest{APIVersion: v1.Version, Force: true, ExpectedClaimRevision: selected})
				} else {
					_, err = cli.StopExecutionWithRequest(context.Background(), v1.StopRequest{APIVersion: v1.Version, InstanceID: selected, Force: true, ExpectedAttempt: selected})
				}
				if err == nil {
					t.Fatal("conflict hidden")
				}
			}
			if calls != 2 {
				t.Fatal("client refreshed/retried acknowledged target", calls)
			}
		})
	}
}
