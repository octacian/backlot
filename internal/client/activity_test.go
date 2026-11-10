package client

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	v1 "github.com/octacian/backlot/api/v1"
)

func TestActivityAndBulkWireValidation(t *testing.T) {
	id := strings.Repeat("a", 64)
	for _, mode := range []string{"activity", "bulk", "lost-bulk", "bad-bulk", "partial-bulk"} {
		t.Run(mode, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("X-Backlot-API-Version", v1.Version)
				if mode == "activity" {
					if r.URL.Path != "/v1/activity" {
						t.Error("wrong path")
					}
					_ = json.NewEncoder(w).Encode(v1.ActivityResponse{APIVersion: v1.Version, DaemonGeneration: id, GatewayStatus: "unconfigured", Entries: []v1.ActivityEntry{}, Complete: true})
					return
				}
				var q v1.StopAllRequest
				if json.NewDecoder(r.Body).Decode(&q) != nil || q.Validate() != nil || r.URL.Path != "/v1/runtime/stop-all" {
					t.Error("invalid explicit intent")
				}
				if mode == "lost-bulk" {
					_, _ = w.Write([]byte("broken"))
					return
				}
				out := v1.StopAllResponse{APIVersion: v1.Version, DaemonGeneration: id, OperationID: id, AdmissionsJoined: true, SelectionComplete: true, Targets: []v1.StopAllTarget{}}
				if mode == "partial-bulk" {
					out.AdmissionsJoined = false
					out.Error = &v1.PlanError{Code: "partial_failure", Message: "bulk stop incomplete; inspect activity and selected targets before explicit recovery"}
				}
				if mode == "bad-bulk" {
					out.AdmissionsJoined = false
				}
				_ = json.NewEncoder(w).Encode(out)
			}))
			defer server.Close()
			c := &Client{http: server.Client()}
			c.http.Transport = activityTransport{base: server.Client().Transport, url: server.URL}
			if mode == "activity" {
				if _, err := c.Activity(context.Background(), v1.ActivityRequest{APIVersion: v1.Version}); err != nil {
					t.Fatal(err)
				}
			} else {
				out, err := c.StopAll(context.Background(), v1.StopAllRequest{APIVersion: v1.Version, All: true})
				if mode == "partial-bulk" {
					if err == nil || out.OperationID != id || out.Error == nil {
						t.Fatal("partial result discarded", err)
					}
					return
				}
				if mode == "bulk" && err != nil || mode != "bulk" && (err == nil || !strings.Contains(err.Error(), "do not automatically retry")) {
					t.Fatal("unknown acceptance handling", err)
				}
			}
			if calls != 1 {
				t.Fatal("unexpected compatibility call or retry", calls)
			}
			if _, err := c.StopAll(context.Background(), v1.StopAllRequest{APIVersion: v1.Version}); err == nil || calls != 1 {
				t.Fatal("implicit all reached transport")
			}
		})
	}
}

// Test-only adapter retains the real HTTP exchange and redirects its Unix-socket URL.
type activityTransport struct {
	base http.RoundTripper
	url  string
}

func (a activityTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r.URL.Scheme = "http"
	r.URL.Host = strings.TrimPrefix(a.url, "http://")
	return a.base.RoundTrip(r)
}
