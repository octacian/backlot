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
		"scenes/start": func(c *Client) error {
			_, err := c.StartScene(context.Background(), v1.SceneStartRequest{CWD: "/tmp/scene", Plan: v1.PlanRequest{Scene: "dev"}})
			return err
		},
		"prepare": func(c *Client) error { _, err := c.Prepare(context.Background(), v1.PrepareRequest{}); return err },
		"run":     func(c *Client) error { _, err := c.Run(context.Background(), v1.RunRequest{}); return err },
		"restart": func(c *Client) error { _, err := c.Restart(context.Background(), v1.RunRequest{}); return err },
		"reset":   func(c *Client) error { _, err := c.Reset(context.Background(), v1.RunRequest{}); return err },
		"gateway/start": func(c *Client) error {
			_, err := c.Gateway(context.Background(), "start", v1.GatewayRequest{APIVersion: v1.Version})
			return err
		},
		"gateway/restart": func(c *Client) error {
			_, err := c.Gateway(context.Background(), "restart", v1.GatewayRequest{APIVersion: v1.Version})
			return err
		},
		"doctor": func(c *Client) error {
			_, err := c.Doctor(context.Background(), v1.DoctorRequest{APIVersion: v1.Version, InstanceID: id, ContainerProbeImage: "owned:diagnostic"})
			return err
		},
	}
	for name, call := range methods {
		t.Run(name, func(t *testing.T) {
			mutationResponse := encoded
			var result any
			switch name {
			case "scenes/start":
				scene := sceneResponse()
				scene.Outcome, scene.Result = v1.SceneFound, sceneInstance("e")
				result = scene
			case "gateway/start", "gateway/restart":
				result = v1.GatewayResponse{APIVersion: v1.Version, Mode: "managed", Status: "unconfigured"}
			case "doctor":
				result = v1.DoctorResponse{APIVersion: v1.Version, Scope: "instance", InstanceID: id, Healthy: true, Paths: []v1.DoctorPath{}, Checks: []v1.DoctorCheck{}}
			}
			if result != nil {
				mutationResponse, err = json.Marshal(result)
				if err != nil {
					t.Fatal(err)
				}
			}
			statusCalls, mutations := 0, 0
			statusBody, header := valid, v1.Version
			cli := New("unused")
			defer cli.Close()
			cli.http.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
				body := string(mutationResponse)
				if r.URL.Path == "/v1/status" {
					statusCalls++
					body = statusBody
				} else {
					if r.URL.Path != "/v1/"+name {
						t.Fatalf("unexpected mutation %s", r.URL.Path)
					}
					if r.Method != http.MethodPost {
						t.Fatal("mutation was not POST")
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
				{"v5", strings.Replace(valid, fmt.Sprintf(`"state_version":%d`, v1.StateVersion), `"state_version":5`, 1), "v1"},
				{"v6", strings.Replace(valid, fmt.Sprintf(`"state_version":%d`, v1.StateVersion), `"state_version":6`, 1), "v1"},
				{"v7", strings.Replace(valid, fmt.Sprintf(`"state_version":%d`, v1.StateVersion), `"state_version":7`, 1), "v1"},
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
					if err == nil || !strings.Contains(err.Error(), "matching binary") || !strings.Contains(err.Error(), "same --state-dir") || strings.Contains(err.Error(), "fresh --state-dir") || statusCalls != before+1 || mutations != 1 {
						t.Fatalf("not fenced: %v calls=%d mutations=%d", err, statusCalls, mutations)
					}
				})
			}
			statusBody, header = valid, "v1"
			before := statusCalls
			if err := call(cli); err != nil || mutations != 2 || statusCalls != before+1 {
				t.Fatalf("matching current recovery: %v mutations=%d", err, mutations)
			}
		})
	}
}

func TestHistoricalReadsAndRecordedCleanupDoNotRequireFreshStatus(t *testing.T) {
	ctx := context.Background()
	id := strings.Repeat("a", 64)
	gateway := v1.GatewayResponse{APIVersion: v1.Version, Mode: "managed", Status: "unconfigured"}
	for _, test := range []struct {
		name, path string
		call       func(*Client) error
		response   any
	}{
		{"scene status", "/v1/scenes/status", func(c *Client) error { _, err := c.SceneStatus(ctx, sceneRequest()); return err }, sceneResponse()},
		{"scene stop", "/v1/scenes/stop", func(c *Client) error { _, err := c.StopScene(ctx, sceneRequest()); return err }, sceneResponse()},
		{"gateway status", "/v1/gateway/status", func(c *Client) error {
			_, err := c.Gateway(ctx, "status", v1.GatewayRequest{APIVersion: v1.Version})
			return err
		}, gateway},
		{"gateway stop", "/v1/gateway/stop", func(c *Client) error {
			_, err := c.Gateway(ctx, "stop", v1.GatewayRequest{APIVersion: v1.Version})
			return err
		}, gateway},
		{"recorded list", "/v1/instances", func(c *Client) error { _, err := c.List(ctx, v1.ListRequest{}); return err }, v1.ListResponse{APIVersion: v1.Version, Instances: []v1.InstanceSummary{}}},
		{"doctor daemon", "/v1/doctor", func(c *Client) error { _, err := c.Doctor(ctx, v1.DoctorRequest{APIVersion: v1.Version}); return err }, v1.DoctorResponse{APIVersion: v1.Version, Scope: "daemon", Healthy: true, Paths: []v1.DoctorPath{}, Checks: []v1.DoctorCheck{}}},
		{"doctor instance", "/v1/doctor", func(c *Client) error {
			_, err := c.Doctor(ctx, v1.DoctorRequest{APIVersion: v1.Version, InstanceID: id})
			return err
		}, v1.DoctorResponse{APIVersion: v1.Version, Scope: "instance", InstanceID: id, Healthy: true, Paths: []v1.DoctorPath{}, Checks: []v1.DoctorCheck{}}},
		{"doctor scene", "/v1/doctor", func(c *Client) error {
			_, err := c.Doctor(ctx, v1.DoctorRequest{APIVersion: v1.Version, ProjectPath: "/tmp/project", Scene: "test", ConfigPath: "/tmp/machine.json"})
			return err
		}, v1.DoctorResponse{APIVersion: v1.Version, Scope: "scene", Healthy: true, Paths: []v1.DoctorPath{}, Checks: []v1.DoctorCheck{}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			cli := New("unused")
			defer cli.Close()
			calls := 0
			data, err := json.Marshal(test.response)
			if err != nil {
				t.Fatal(err)
			}
			cli.http.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
				if r.URL.Path != test.path {
					t.Fatalf("historical operation unexpectedly requested %s", r.URL.Path)
				}
				calls++
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}, "X-Backlot-Api-Version": {v1.Version}}, Body: io.NopCloser(strings.NewReader(string(data)))}, nil
			})
			for range 2 {
				if err := test.call(cli); err != nil {
					t.Fatal(err)
				}
			}
			if calls != 2 {
				t.Fatal("historical call refreshed or retried", calls)
			}
		})
	}
}
