package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	v1 "github.com/octacian/backlot/api/v1"
)

func TestCleanupStreamingValidationAndUnknown(t *testing.T) {
	id := strings.Repeat("a", 64)
	for _, mode := range []string{"preview", "apply", "partial", "duplicate", "missing-summary", "identity", "counts", "trailing", "header", "rejected", "late"} {
		t.Run(mode, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				w.Header().Set("X-Backlot-API-Version", v1.Version)
				w.Header().Set("Content-Type", "application/x-ndjson")
				if mode == "rejected" {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(400)
					_ = json.NewEncoder(w).Encode(v1.ErrorResponse{APIVersion: v1.Version, Error: v1.PlanError{Code: "stale_preview", Message: "changed"}})
					return
				}
				if mode == "header" {
					w.Header().Set("X-Backlot-API-Version", "other")
				}
				if mode == "late" {
					time.Sleep(30 * time.Millisecond)
				}
				record := v1.CleanupRecord{APIVersion: v1.Version, Type: "accepted", DaemonGeneration: id, OperationID: id}
				if mode == "preview" {
					record.Type = "preview"
					record.OperationID = ""
					record.Summary = &v1.CleanupSummary{Preview: id}
					_ = json.NewEncoder(w).Encode(record)
					return
				}
				_ = json.NewEncoder(w).Encode(record)
				record.Type = "target"
				record.InstanceID = id
				record.Outcome = "done"
				if mode == "partial" {
					record.Outcome = "unresolved"
					record.Reason = "blocked"
				}
				_ = json.NewEncoder(w).Encode(record)
				if mode == "duplicate" {
					_ = json.NewEncoder(w).Encode(record)
				}
				if mode == "missing-summary" {
					return
				}
				record.Type = "summary"
				record.InstanceID = ""
				record.Outcome = ""
				record.Reason = ""
				record.Summary = &v1.CleanupSummary{Preview: id, Targets: 1, Done: 1}
				if mode == "identity" {
					record.OperationID = strings.Repeat("b", 64)
				}
				if mode == "counts" {
					record.Summary.Targets = 2
					record.Summary.Done = 2
				}
				if mode == "partial" {
					record.Summary.Done = 0
					record.Summary.Unresolved = 1
				}
				_ = json.NewEncoder(w).Encode(record)
				if mode == "trailing" {
					_, _ = fmt.Fprintln(w, `{}`)
				}
			}))
			defer server.Close()
			c := New("unused")
			defer c.Close()
			c.http.Timeout = time.Millisecond
			c.http.Transport = activityTransport{base: server.Client().Transport, url: server.URL}
			q := v1.CleanupRequest{APIVersion: v1.Version, Contract: v1.CleanupContract, Orphans: true, ApplyPreview: id}
			if mode == "preview" {
				q.ApplyPreview = ""
			}
			_, err := c.Cleanup(context.Background(), q, nil)
			var typed *v1.PlanError
			switch mode {
			case "preview", "apply", "late":
				if err != nil {
					t.Fatal(err)
				}
			case "rejected":
				if !errors.As(err, &typed) || typed.Code != "stale_preview" {
					t.Fatal(err)
				}
			case "partial":
				if !errors.As(err, &typed) || typed.Code != "partial_failure" {
					t.Fatal(err)
				}
			default:
				if !errors.As(err, &typed) || typed.Code != "result_unknown" {
					t.Fatal("unknown completion disguised", err)
				}
			}
			if calls != 1 {
				t.Fatal("mutation retried", calls)
			}
		})
	}
}
func TestCleanupCancellationAcknowledgement(t *testing.T) {
	id := strings.Repeat("a", 64)
	for _, wrong := range []bool{false, true} {
		t.Run(fmt.Sprint(wrong), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("X-Backlot-API-Version", v1.Version)
				w.Header().Set("Content-Type", "application/json")
				out := v1.CleanupCancelResponse{APIVersion: v1.Version, DaemonGeneration: id, OperationID: id, Cancelled: true}
				if wrong {
					out.OperationID = strings.Repeat("b", 64)
				}
				_ = json.NewEncoder(w).Encode(out)
			}))
			defer server.Close()
			c := New("unused")
			defer c.Close()
			c.http.Transport = activityTransport{base: server.Client().Transport, url: server.URL}
			_, err := c.CancelCleanup(context.Background(), v1.CleanupCancelRequest{APIVersion: v1.Version, Contract: v1.CleanupContract, DaemonGeneration: id, OperationID: id})
			if (err != nil) != wrong {
				t.Fatal(err)
			}
		})
	}
}
