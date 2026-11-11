package client

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	v1 "github.com/octacian/backlot/api/v1"
	"github.com/octacian/backlot/internal/manifest"
)

func TestSharedNamedWireContract(t *testing.T) {
	c := New("unused")
	defer c.Close()
	request := v1.SharedRequest{APIVersion: v1.Version, ServiceID: strings.Repeat("a", 64)}
	response := v1.SharedResponse{APIVersion: v1.Version, Services: []v1.SharedStatus{{ID: request.ServiceID, ProjectID: strings.Repeat("b", 64), Name: "sql", Configuration: strings.Repeat("c", 64), Status: "running", Allocations: 2}}}
	c.http.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/shared/inspect" {
			t.Fatal("wrong shared operation")
		}
		data, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		var got v1.SharedRequest
		if err = manifest.Decode(data, ".json", &got); err != nil || got != request {
			t.Fatal("named shared request drift", err)
		}
		data, err = json.Marshal(response)
		if err != nil {
			t.Fatal(err)
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}, "X-Backlot-Api-Version": []string{v1.Version}}, Body: io.NopCloser(strings.NewReader(string(data)))}, nil
	})
	if _, err := c.Shared(context.Background(), "inspect", request); err != nil {
		t.Fatal(err)
	}
	response.Services[0].LastTransition = &v1.SharedTransition{From: "stopped", To: "running", Cause: "explicit_shared_start", At: "2026-11-06T23:00:00Z"}
	if _, err := c.Shared(context.Background(), "inspect", request); err != nil {
		t.Fatal(err)
	}
	for _, transition := range []v1.SharedTransition{
		{From: "running", To: "running", Cause: "consumer_start", At: "2026-11-06T23:00:00Z"},
		{From: "", To: "running", Cause: "consumer_start", At: "2026-11-06T23:00:00Z"},
		{From: "stopped", To: "running", Cause: "private-detail", At: "2026-11-06T23:00:00Z"},
		{From: "stopped", To: "running", Cause: "consumer_start", At: "unknown"},
		{From: "running", To: "stopped", Cause: "explicit_shared_stop", At: "2026-11-06T23:00:00Z"},
	} {
		response.Services[0].LastTransition = &transition
		if _, err := c.Shared(context.Background(), "inspect", request); err == nil {
			t.Fatal("invalid/mismatched transition accepted")
		}
	}
	response.Services[0].LastTransition = nil
	response.Services[0].Status = "guessing"
	if _, err := c.Shared(context.Background(), "inspect", request); err == nil {
		t.Fatal("unvalidated shared state")
	}
	response.Services[0].Status = "running"
	for _, pending := range []int{-1, 3} {
		response.Services[0].PendingReleases = pending
		if _, err := c.Shared(context.Background(), "inspect", request); err == nil {
			t.Fatal("invalid pending release count accepted")
		}
	}
	response.Services[0].PendingReleases = 0
	response.Services[0].ID = strings.Repeat("d", 64)
	if _, err := c.Shared(context.Background(), "inspect", request); err == nil {
		t.Fatal("wrong shared identity accepted")
	}
}

func TestSharedLifecycleTransportBudget(t *testing.T) {
	id := strings.Repeat("a", 64)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(80 * time.Millisecond):
		case <-r.Context().Done():
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Backlot-API-Version", v1.Version)
		_ = json.NewEncoder(w).Encode(v1.SharedResponse{APIVersion: v1.Version, Services: []v1.SharedStatus{{ID: id, ProjectID: id, Configuration: id, Name: "sql", Status: "stopped"}}})
	}))
	defer server.Close()
	c := New("unused")
	defer c.Close()
	c.http.Timeout = 10 * time.Millisecond
	c.http.Transport = &http.Transport{Proxy: func(_ *http.Request) (*url.URL, error) { return url.Parse(server.URL) }}
	defer c.http.CloseIdleConnections()
	for _, action := range []string{"start", "stop", "restart", "destroy", "inspect", "list"} {
		t.Run(action, func(t *testing.T) {
			request := v1.SharedRequest{APIVersion: v1.Version, ServiceID: id}
			if action == "list" {
				request.ServiceID = ""
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			_, err := c.Shared(ctx, action, request)
			read := action == "inspect" || action == "list"
			if (err != nil) != read {
				t.Fatalf("shared %s transport budget: %v", action, err)
			}
			if !read {
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
				defer cancel()
				if _, err := c.Shared(ctx, action, request); err == nil || ctx.Err() == nil {
					t.Fatal("caller deadline ignored", err)
				}
			}
		})
	}
}

func TestOrdinaryStartTransportBudget(t *testing.T) {
	for _, path := range []string{"/v1/run", "/v1/scenes/start"} {
		for _, mode := range []string{"conflict", "cancel", "incompatible"} {
			t.Run(path+"/"+mode, func(t *testing.T) {
				c := New("unused")
				defer c.Close()
				// Inspect deadlines directly instead of racing localhost against 5ms.
				c.http.Timeout = time.Hour
				ctx, cancel := context.WithTimeout(context.Background(), 2*time.Hour)
				defer cancel()
				callerDeadline, _ := ctx.Deadline()
				started := time.Now()
				statusCalls, mutationCalls := 0, 0
				conflict := v1.PlanError{Code: "startup_conflict", Message: "bound attempt was replaced; inspect history"}
				c.http.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
					code := http.StatusOK
					var payload any
					if r.URL.Path == "/v1/status" {
						statusCalls++
						deadline, ok := r.Context().Deadline()
						if r.Method != http.MethodGet || !ok || deadline.Before(started.Add(c.http.Timeout)) || deadline.After(time.Now().Add(c.http.Timeout)) {
							t.Fatal("compatibility preflight lost its transport budget")
						}
						status := v1.DaemonStatusResponse{APIVersion: v1.Version, StateVersion: v1.StateVersion, Status: "running", PID: 1, LeaseDuration: "1m"}
						if mode == "incompatible" {
							status.StateVersion--
						}
						payload = status
					} else {
						mutationCalls++
						deadline, ok := r.Context().Deadline()
						if statusCalls != 1 || r.Method != http.MethodPost || r.URL.Path != path || r.Context() != ctx || !ok || !deadline.Equal(callerDeadline) {
							t.Fatal("start lost compatibility preflight or caller context/budget")
						}
						if mode == "cancel" {
							cancel()
							<-r.Context().Done()
							return nil, r.Context().Err()
						}
						code = http.StatusConflict
						payload = v1.ErrorResponse{APIVersion: v1.Version, Error: conflict}
					}
					data, err := json.Marshal(payload)
					if err != nil {
						t.Fatal(err)
					}
					return &http.Response{StatusCode: code, Header: http.Header{"Content-Type": []string{"application/json"}, "X-Backlot-Api-Version": []string{v1.Version}}, Body: io.NopCloser(strings.NewReader(string(data)))}, nil
				})
				var response v1.InstanceResponse
				err := c.call(ctx, http.MethodPost, path, v1.RunRequest{}, &response)
				var detail *v1.PlanError
				if statusCalls != 1 {
					t.Fatal("compatibility preflight bypassed", statusCalls)
				}
				switch mode {
				case "conflict":
					if mutationCalls != 1 || !errors.As(err, &detail) || *detail != conflict {
						t.Fatal("start lost cleanup budget or typed conflict", err)
					}
				case "cancel":
					if mutationCalls != 1 || err == nil || ctx.Err() != context.Canceled {
						t.Fatal("caller cancellation ignored", err)
					}
				case "incompatible":
					if mutationCalls != 0 || !errors.As(err, &detail) || detail.Code != "api_version" {
						t.Fatal("incompatible status reached mutation", err)
					}
				}
			})
		}
	}
}
