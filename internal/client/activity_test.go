package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
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
				w.Header().Set("Content-Type", "application/x-ndjson")
				accepted := v1.StopAllRecord{APIVersion: v1.Version, Type: "accepted", DaemonGeneration: id, OperationID: id}
				_ = json.NewEncoder(w).Encode(accepted)
				out := v1.StopAllResponse{APIVersion: v1.Version, DaemonGeneration: id, OperationID: id, AdmissionsJoined: true, SelectionComplete: true, Targets: []v1.StopAllTarget{}}
				if mode == "partial-bulk" {
					out.AdmissionsJoined = false

					out.Error = &v1.PlanError{Code: "partial_failure", Message: "bulk stop incomplete; inspect activity and selected targets before explicit recovery"}
				}
				summary := out.Summary()
				if mode == "bad-bulk" {
					summary.Stopped = 1
				}
				accepted.Type, accepted.Summary = "summary", &summary
				_ = json.NewEncoder(w).Encode(accepted)
			}))
			defer server.Close()
			c := &Client{http: server.Client()}
			c.http.Transport = activityTransport{base: server.Client().Transport, url: server.URL}
			if mode == "activity" {
				if _, err := c.Activity(context.Background(), v1.ActivityRequest{APIVersion: v1.Version}); err != nil {
					t.Fatal(err)
				}
			} else {
				out, err := c.StopAll(context.Background(), v1.StopAllRequest{APIVersion: v1.Version, All: true, Contract: v1.StopAllContract}, nil)
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
			if _, err := c.StopAll(context.Background(), v1.StopAllRequest{APIVersion: v1.Version}, nil); err == nil || calls != 1 {
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

func TestBulkStreamLargeResultsAndPartialObservations(t *testing.T) {
	for _, mode := range []string{"large", "large-summary", "lost", "duplicate", "count", "identity", "oversize", "oversize-target", "after-summary", "duplicate-field", "null-field", "unknown-field", "rejected", "old-peer"} {
		t.Run(mode, func(t *testing.T) {
			id := strings.Repeat("a", 64)
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				w.Header().Set("X-Backlot-API-Version", v1.Version)
				if mode == "rejected" || mode == "old-peer" {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusBadRequest)
					detail := v1.PlanError{Code: "invalid_request", Message: "unsupported contract"}
					if mode == "old-peer" {
						detail.Code, detail.Message = "invalid_document", "unknown field or declaration"
					}
					_ = json.NewEncoder(w).Encode(v1.ErrorResponse{APIVersion: v1.Version, Error: detail})
					return
				}
				w.Header().Set("Content-Type", "application/x-ndjson")
				encoder := json.NewEncoder(w)
				event := v1.StopAllRecord{APIVersion: v1.Version, Type: "accepted", DaemonGeneration: id, OperationID: id}
				if mode == "duplicate-field" || mode == "null-field" || mode == "unknown-field" {
					data, _ := json.Marshal(event)
					switch mode {
					case "duplicate-field":
						data = bytes.Replace(data, []byte(`"api_version":"v1"`), []byte(`"api_version":"v1","api_version":"v1"`), 1)
					case "null-field":
						data = append(data[:len(data)-1], []byte(`,"target":null}`)...)
					case "unknown-field":
						data = append(data[:len(data)-1], []byte(`,"private":true}`)...)
					}
					_, _ = w.Write(append(data, '\n'))
					return
				}
				_ = encoder.Encode(event)
				out := v1.StopAllResponse{APIVersion: v1.Version, DaemonGeneration: id, OperationID: id, Targets: []v1.StopAllTarget{}, SelectionComplete: true, AdmissionsJoined: true}
				count := 1
				if mode == "large" || mode == "oversize-target" {
					count = 3000
				}
				if mode == "large-summary" {
					count = 16000
				}
				for i := range count {
					target := v1.StopAllTarget{ID: fmt.Sprintf("%064x", i+1), Outcome: "unresolved", Reason: "stop_timeout", Settlement: v1.ConsumerUnknown}
					out.Targets = append(out.Targets, target)
					event.Type, event.Target = "target", &target
					_ = encoder.Encode(event)
				}
				if mode == "lost" {
					return
				}
				if mode == "duplicate" {
					_ = encoder.Encode(event)
					return
				}
				if mode == "oversize" {
					_, _ = w.Write(bytes.Repeat([]byte(" "), 5000))
					return
				}
				if mode == "oversize-target" {
					_, _ = w.Write(bytes.Repeat([]byte(" "), 5000))
					_ = encoder.Encode(event)
					return
				}

				out.Error = &v1.PlanError{Code: "partial_failure", Message: "bulk stop incomplete; inspect activity and selected targets before explicit recovery"}
				summary := out.Summary()
				if mode == "count" {
					summary.Unresolved = 0
					summary.UnresolvedIDs = []string{}
				}
				if mode == "identity" {
					summary.UnresolvedIDs[0] = id
				}
				event.Type, event.Target, event.Summary = "summary", nil, &summary
				_ = encoder.Encode(event)
				if mode == "after-summary" {
					_ = encoder.Encode(event)
				}
			}))
			defer server.Close()
			c := &Client{http: server.Client()}
			c.http.Transport = activityTransport{base: server.Client().Transport, url: server.URL}
			emitted := 0
			out, err := c.StopAll(context.Background(), v1.StopAllRequest{APIVersion: v1.Version, All: true, Contract: v1.StopAllContract}, func(v1.StopAllRecord) error { emitted++; return nil })
			if err == nil || calls != 1 {
				t.Fatal("false success or retry", err, calls)
			}
			if mode == "large" || mode == "large-summary" {
				count := 3000
				if mode == "large-summary" {
					count = 16000
				}
				if out.Validate() != nil || len(out.Targets) != count || emitted != count+2 || err.Error() != out.Error.Error() {
					t.Fatal("large stream rejected", len(out.Targets), emitted, err)
				}
			} else if mode == "rejected" || mode == "old-peer" {
				if mode == "old-peer" && !strings.Contains(err.Error(), "no mutation was accepted") {
					t.Fatal("old peer lacked matching-binary guidance", err)
				}
				if emitted != 0 || strings.Contains(err.Error(), "unknown") {
					t.Fatal("definite rejection became unknown", err)
				}
			} else if mode == "duplicate-field" || mode == "null-field" || mode == "unknown-field" {
				if emitted != 0 || !strings.Contains(err.Error(), "do not automatically retry") {
					t.Fatal("malformed acceptance escaped strict decoding", emitted, err)
				}
			} else if !strings.Contains(err.Error(), "do not automatically retry") || emitted < 2 {
				t.Fatal("lost valid observations or unsafe retry advice", emitted, err)
			}
		})
	}
}

func TestBulkClientEmitsBeforeFinalSummary(t *testing.T) {
	observed := make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(observed) })
	id := strings.Repeat("a", 64)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/x-ndjson")
		w.Header().Set("X-Backlot-API-Version", v1.Version)
		event := v1.StopAllRecord{APIVersion: v1.Version, Type: "accepted", DaemonGeneration: id, OperationID: id}
		_ = json.NewEncoder(w).Encode(event)
		_ = http.NewResponseController(w).Flush()
		select {
		case <-observed:
		case <-r.Context().Done():
			return
		}
		summary := (v1.StopAllResponse{SelectionComplete: true, AdmissionsJoined: true}).Summary()
		event.Type, event.Summary = "summary", &summary
		_ = json.NewEncoder(w).Encode(event)
	}))
	defer server.Close()
	c := &Client{http: server.Client()}
	c.http.Transport = activityTransport{base: server.Client().Transport, url: server.URL}
	out, err := c.StopAll(context.Background(), v1.StopAllRequest{APIVersion: v1.Version, All: true, Contract: v1.StopAllContract}, func(event v1.StopAllRecord) error {
		if event.Type == "accepted" {
			once.Do(func() { close(observed) })
		}
		return nil
	})
	if err != nil || out.Validate() != nil {
		t.Fatal(out, err)
	}
}
