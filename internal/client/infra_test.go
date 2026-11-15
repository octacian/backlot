package client

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	v1 "github.com/octacian/backlot/api/v1"
)

func TestInfraShutdownWire(t *testing.T) {
	id := strings.Repeat("a", 64)
	for _, mode := range []string{"success", "failure", "missing-terminal", "truncated", "generation", "operation", "duplicate-target", "missing-phase", "false-success", "output-failure", "rejected"} {
		t.Run(mode, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
				calls++
				var q v1.InfraRequest
				if json.NewDecoder(request.Body).Decode(&q) != nil || q.Validate("stop") != nil || q.DaemonGeneration != id || request.URL.Path != "/v1/infra/stop" {
					t.Error("invalid shutdown intent")
				}
				w.Header().Set("X-Backlot-API-Version", v1.Version)
				w.Header().Set("Content-Type", "application/x-ndjson")
				if mode == "rejected" {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusBadRequest)
					_ = json.NewEncoder(w).Encode(v1.ErrorResponse{APIVersion: v1.Version, Error: v1.PlanError{Code: "replacement_daemon", Message: "replacement was not stopped"}})
					return
				}
				base := v1.InfraStopRecord{APIVersion: v1.Version, DaemonGeneration: id, OperationID: id, Type: "accepted"}
				write := func(r v1.InfraStopRecord) { _ = json.NewEncoder(w).Encode(r) }
				write(base)
				if mode == "truncated" {
					_, _ = w.Write([]byte(`{"type":`))
					return
				}
				for _, phase := range []string{"owners", "expiry", "recovery", "gateway", "shared", "records"} {
					if mode == "missing-phase" && phase == "records" {
						continue
					}
					r := base
					r.Type, r.Phase, r.State = "phase", phase, "running"
					write(r)
					r.State = "succeeded"
					if (mode == "failure" || mode == "false-success") && phase == "gateway" {
						r.State = "failed"
						r.Error = &v1.PlanError{Code: "cleanup_failed", Message: "owned fixture failure"}
					}
					write(r)
				}
				r := base
				r.Type, r.Target = "target", &v1.InfraTarget{ID: id, Outcome: "stopped"}
				write(r)
				if mode == "duplicate-target" {
					write(r)
				}
				if mode == "missing-terminal" {
					return
				}
				r = base
				r.Type = "cleanup"
				if mode == "generation" {
					r.DaemonGeneration = strings.Repeat("b", 64)
				}
				if mode == "operation" {
					r.OperationID = strings.Repeat("b", 64)
				}
				if mode == "failure" {
					r.Error = &v1.PlanError{Code: "cleanup_failed", Message: "owned fixture failure"}
				}
				write(r)
			}))
			defer server.Close()
			c := &Client{http: server.Client()}
			c.http.Transport = activityTransport{base: server.Client().Transport, url: server.URL}
			observations := 0
			terminal, err := c.InfraShutdown(context.Background(), id, func(v1.InfraStopRecord) error {
				observations++
				if mode == "output-failure" {
					return errors.New("closed output")
				}
				return nil
			})
			if calls != 1 {
				t.Fatal("automatic mutation retry", calls)
			}
			switch mode {
			case "success", "failure":
				if err != nil || terminal.Type != "cleanup" || (terminal.Error != nil) != (mode == "failure") || observations != 15 {
					t.Fatal(terminal, err, observations)
				}
			case "rejected":
				var detail *v1.PlanError
				if !errors.As(err, &detail) || detail.Code != "replacement_daemon" || observations != 0 {
					t.Fatal(err, observations)
				}
			default:
				var detail *v1.PlanError
				if !errors.As(err, &detail) || detail.Code != "completion_unknown" {
					t.Fatal("unknown completion hidden", err)
				}
			}
		})
	}
}
