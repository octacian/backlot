package client

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	v1 "github.com/octacian/backlot/api/v1"
	"github.com/octacian/backlot/internal/manifest"
)

func TestForceDestroyWireRouting(t *testing.T) {
	for _, shared := range []bool{false, true} {
		t.Run(map[bool]string{false: "instance", true: "shared"}[shared], func(t *testing.T) {
			c := New("unused")
			defer c.Close()
			calls := 0
			id := strings.Repeat("a", 64)
			c.http.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
				calls++
				body, err := io.ReadAll(r.Body)
				if err != nil {
					t.Fatal(err)
				}
				if shared {
					var got v1.SharedDestroyRequest
					if r.URL.Path != "/v1/shared/destroy" || manifest.Decode(body, ".json", &got) != nil || got != (v1.SharedDestroyRequest{APIVersion: v1.Version, ServiceID: id, Force: true}) {
						t.Fatal("force shared request lost authority or identity")
					}
				} else {
					var got v1.DestroyRequest
					if r.URL.Path != "/v1/destroy" || manifest.Decode(body, ".json", &got) != nil || got != (v1.DestroyRequest{Contract: v1.DestroyContract, APIVersion: v1.Version, InstanceID: id, Force: true}) {
						t.Fatal("force instance request lost authority or identity")
					}
				}
				// Model an old strict daemon: never retry by dropping explicit authorization.
				return &http.Response{StatusCode: http.StatusBadRequest, Header: http.Header{"Content-Type": []string{"application/json"}, "X-Backlot-Api-Version": []string{v1.Version}}, Body: io.NopCloser(strings.NewReader(`{"api_version":"v1","error":{"code":"invalid_request","message":"unsupported force"}}`))}, nil
			})
			var err error
			if shared {
				_, err = c.DestroyShared(context.Background(), v1.SharedDestroyRequest{APIVersion: v1.Version, ServiceID: id, Force: true})
			} else {
				_, err = c.DestroyWithRequest(context.Background(), v1.DestroyRequest{APIVersion: v1.Version, InstanceID: id, Force: true})
			}
			if err == nil || calls != 1 {
				t.Fatal("unsupported force silently downgraded/retried", calls, err)
			}
			if shared {
				_, err = c.DestroyShared(context.Background(), v1.SharedDestroyRequest{APIVersion: "v2", ServiceID: id, Force: true})
			} else {
				_, err = c.DestroyWithRequest(context.Background(), v1.DestroyRequest{APIVersion: v1.Version, InstanceID: "bad", Force: true})
			}
			if err == nil || calls != 1 {
				t.Fatal("invalid force identity/version reached daemon")
			}
		})
	}
}

func TestForceLegacyDestroyRequestsRemainConservative(t *testing.T) {
	c := New("unused")
	defer c.Close()
	calls := 0
	id := strings.Repeat("a", 64)
	c.http.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(body), `"force"`) {
			t.Fatal("legacy destroy acquired force authority")
		}
		if r.URL.Path == "/v1/destroy" {
			var got v1.DestroyRequest
			if manifest.Decode(body, ".json", &got) != nil || got.InstanceID != id || got.Force {
				t.Fatal("legacy instance request incompatible")
			}
		} else {
			var got v1.SharedDestroyRequest
			if r.URL.Path != "/v1/shared/destroy" || manifest.Decode(body, ".json", &got) != nil || got.ServiceID != id || got.Force {
				t.Fatal("legacy shared request incompatible")
			}
		}
		return &http.Response{StatusCode: http.StatusBadRequest, Header: http.Header{"Content-Type": []string{"application/json"}, "X-Backlot-Api-Version": []string{v1.Version}}, Body: io.NopCloser(strings.NewReader(`{"api_version":"v1","error":{"code":"not_found","message":"fixture"}}`))}, nil
	})
	if _, err := c.Destroy(context.Background(), id); err == nil {
		t.Fatal("fixture unexpectedly succeeded")
	}
	if _, err := c.Shared(context.Background(), "destroy", v1.SharedRequest{APIVersion: v1.Version, ServiceID: id}); err == nil {
		t.Fatal("fixture unexpectedly succeeded")
	}
	if calls != 2 {
		t.Fatal("legacy request routing changed")
	}
}

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
