package client

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	v1 "github.com/octacian/backlot/api/v1"
)

func TestConfigurationMutationsRequireFreshCompatibleStatus(t *testing.T) {
	id := strings.Repeat("a", 64)
	instance := v1.InstanceResponse{APIVersion: v1.Version, Instance: v1.Instance{ID: id, Checkout: v1.CheckoutIdentity{ID: id, Path: "/tmp/fixture"}, Status: v1.Prepared, CreatedAt: "2026-10-07T12:00:00Z", LeaseExpiresAt: "2026-10-07T12:00:30Z", Operation: v1.Operation{ID: id, Status: v1.Prepared, Allocations: []v1.AllocationRecord{{ID: id, Kind: "private_snapshot", Phase: "effect"}}}, Plan: v1.PlanResponse{APIVersion: v1.Version, Project: "fixture", Scene: "test", Lifetime: v1.Disposable, Components: []v1.PlannedComponent{}}}}
	encoded, err := json.Marshal(instance)
	if err != nil {
		t.Fatal(err)
	}
	valid := fmt.Sprintf(`{"api_version":"v1","state_version":%d,"status":"running","pid":1,"lease_duration":"30s"}`, v1.StateVersion)
	methods := map[string]func(*Client) error{
		"prepare": func(c *Client) error { _, err := c.Prepare(context.Background(), v1.PrepareRequest{}); return err },
		"run":     func(c *Client) error { _, err := c.Run(context.Background(), v1.RunRequest{}); return err },
		"restart": func(c *Client) error { _, err := c.Restart(context.Background(), v1.RunRequest{}); return err },
		"reset":   func(c *Client) error { _, err := c.Reset(context.Background(), v1.RunRequest{}); return err },
	}
	for name, call := range methods {
		t.Run(name, func(t *testing.T) {
			statusCalls, mutations := 0, 0
			statusBody, header := valid, v1.Version
			cli := New("unused")
			defer cli.Close()
			cli.http.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
				body := string(encoded)
				if r.URL.Path == "/v1/status" {
					statusCalls++
					body = statusBody
				} else {
					if r.URL.Path != "/v1/"+name {
						t.Fatalf("unexpected mutation %s", r.URL.Path)
					}
					mutations++
				}
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}, "X-Backlot-Api-Version": {header}}, Body: io.NopCloser(strings.NewReader(body))}, nil
			})
			// A successful earlier check must never authorize later operations.
			if err := call(cli); err != nil || statusCalls != 1 || mutations != 1 {
				t.Fatalf("current status: %v calls=%d mutations=%d", err, statusCalls, mutations)
			}
			for _, test := range []struct{ name, body, header string }{
				{"old", strings.Replace(valid, fmt.Sprintf(`"state_version":%d`, v1.StateVersion), `"state_version":5`, 1), "v1"},
				{"future", strings.Replace(valid, fmt.Sprintf(`"state_version":%d`, v1.StateVersion), `"state_version":99`, 1), "v1"},
				{"malformed", `{"api_version":"v1"}`, "v1"},
				{"invalid", `not json`, "v1"},
				{"header", valid, "v2"},
				{"stopping", strings.Replace(valid, `"running"`, `"stopping"`, 1), "v1"},
			} {
				t.Run(test.name, func(t *testing.T) {
					statusBody, header = test.body, test.header
					before := statusCalls
					err := call(cli)
					if err == nil || !strings.Contains(err.Error(), "matching older binary") || !strings.Contains(err.Error(), "--state-dir") || statusCalls != before+1 || mutations != 1 {
						t.Fatalf("not fenced: %v calls=%d mutations=%d", err, statusCalls, mutations)
					}
				})
			}
			statusBody, header = valid, "v1"
			if err := call(cli); err != nil || mutations != 2 {
				t.Fatalf("matching current recovery: %v mutations=%d", err, mutations)
			}
		})
	}
}
