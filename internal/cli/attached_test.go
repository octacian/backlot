package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/octacian/backlot/internal/client"
	"github.com/octacian/backlot/internal/daemon"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	v1 "github.com/octacian/backlot/api/v1"
	urfave "github.com/urfave/cli/v3"
)

func TestAttachedFailureReasonsHumanAndJSON(t *testing.T) {
	result := &v1.ExecutionResult{Attempt: strings.Repeat("a", 64), Failure: "required service exited unexpectedly", CollectionFailure: "application output collection failed", CleanupFailure: "owned route cleanup uncertain"}
	record := v1.AttachedRecord{APIVersion: v1.Version, Type: "failure", Result: &v1.InstanceResponse{APIVersion: v1.Version, Instance: v1.Instance{ID: strings.Repeat("b", 64), Status: v1.Failed, Execution: result}}}
	for _, machine := range []bool{false, true} {
		t.Run(map[bool]string{false: "human", true: "json"}[machine], func(t *testing.T) {
			var out bytes.Buffer
			command := &urfave.Command{Name: "renderer", Writer: &out, ErrWriter: &out, Flags: []urfave.Flag{&urfave.BoolFlag{Name: "json"}}, Action: func(_ context.Context, c *urfave.Command) error { return attachedRecord(c, record) }}
			args := []string{"renderer"}
			if machine {
				args = append(args, "--json")
			}
			if err := command.Run(context.Background(), args); err != nil {
				t.Fatal(err)
			}
			for _, reason := range []string{result.Failure, result.CollectionFailure, result.CleanupFailure} {
				if !strings.Contains(out.String(), reason) {
					t.Fatal("failure reason omitted", reason, out.String())
				}
			}
			if machine {
				decoder := json.NewDecoder(&out)
				var decoded v1.AttachedRecord
				if err := decoder.Decode(&decoded); err != nil || decoded.Type != "failure" || decoded.Result == nil || decoded.Result.Attachment != nil || decoded.Result.LeaseToken != "" {
					t.Fatal("invalid/private JSON frame", err)
				}
				if err := decoder.Decode(&decoded); err != io.EOF {
					t.Fatal("extra JSON output", err)
				}
			}
		})
	}
}

func TestAttachedFaultsRetainDriftWithoutRetry(t *testing.T) {
	for _, fault := range []string{"cleanup", "logs", "renewal", "cancel"} {
		t.Run(fault, func(t *testing.T) {
			root, err := os.MkdirTemp("/tmp", "bl-attached-error-")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.RemoveAll(root) })
			listener, err := net.Listen("unix", daemon.SocketPath(root))
			if err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(daemon.SocketPath(root), 0600); err != nil {
				t.Fatal(err)
			}
			id, attempt := strings.Repeat("a", 64), strings.Repeat("b", 64)
			instance := v1.Instance{ID: id, Checkout: v1.CheckoutIdentity{ID: id, Path: root}, CreatedAt: time.Now().UTC().Format(time.RFC3339Nano), Status: v1.Starting, Operation: v1.Operation{ID: id, Status: v1.Starting, Allocations: []v1.AllocationRecord{{ID: id, Kind: "private_snapshot", Phase: "effect"}}}, Plan: v1.PlanResponse{APIVersion: v1.Version, Project: "adapter", Scene: "dev", ManifestPath: filepath.Join(root, "backlot.json"), Checkout: root, Lifetime: v1.Persistent, Components: []v1.PlannedComponent{}}, Execution: &v1.ExecutionResult{Attempt: attempt, Components: []v1.ComponentResult{}}}
			accepted := v1.InstanceResponse{APIVersion: v1.Version, Instance: instance, ManifestDrift: true, ConfigDrift: true, Attachment: &v1.Attachment{Owner: v1.AttachedOwner{Attempt: attempt, DaemonGeneration: id, Token: "attachment-private-canary"}, LeaseDuration: "30s"}}
			var stops, renewals, logs atomic.Int32
			server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("X-Backlot-API-Version", v1.Version)
				switch r.URL.Path {
				case "/v1/attached/cancel", "/v1/attached/renew":
					if r.URL.Path == "/v1/attached/cancel" {
						stops.Add(1)
						if fault == "cleanup" {
							w.WriteHeader(503)
							_ = json.NewEncoder(w).Encode(v1.ErrorResponse{APIVersion: v1.Version, Error: v1.PlanError{Code: "cleanup_uncertain", Message: "cleanup result unknown"}})
							return
						}
					} else {
						renewals.Add(1)
					}
					next := instance
					if r.URL.Path == "/v1/attached/cancel" {
						next.Status, next.Operation.Status = v1.Stopped, v1.Stopped
					} else {
						next.Status, next.Operation.Status = v1.RuntimeReady, v1.RuntimeReady
					}
					// The daemon's matching attached snapshots carry no drift metadata.
					_ = json.NewEncoder(w).Encode(v1.SceneResponse{APIVersion: v1.Version, DaemonGeneration: id, Outcome: v1.SceneFound, Result: &v1.InstanceResponse{APIVersion: v1.Version, Instance: next}})
				case "/v1/logs":
					n := logs.Add(1)
					if fault == "logs" || fault == "renewal" && n > 1 {
						w.WriteHeader(503)
						_ = json.NewEncoder(w).Encode(v1.ErrorResponse{APIVersion: v1.Version, Error: v1.PlanError{Code: "logs_failed", Message: "logs unavailable"}})
						return
					}
					_ = json.NewEncoder(w).Encode(v1.LogsResponse{APIVersion: v1.Version, Records: []v1.LogRecord{}})
				default:
					t.Error("unexpected operation", r.URL.Path)
				}
			})}
			done := make(chan struct{})
			go func() { defer close(done); _ = server.Serve(listener) }()
			t.Cleanup(func() { _ = server.Close(); <-done })
			api := client.New(daemon.SocketPath(root))
			t.Cleanup(api.Close)
			var out, diag bytes.Buffer
			command := &urfave.Command{Writer: &out, ErrWriter: &diag, Flags: []urfave.Flag{&urfave.BoolFlag{Name: "json"}}, Action: func(ctx context.Context, c *urfave.Command) error {
				ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
				defer cancel()
				return runAttached(ctx, c, api, "0s", func(context.Context) (v1.InstanceResponse, error) {
					if fault == "cleanup" || fault == "cancel" {
						cancel()
					}
					return accepted, nil
				})
			}}
			err = command.Run(context.Background(), []string{"attached", "--json"})
			if err == nil || stops.Load() != 1 || strings.Contains(out.String(), "attachment-private-canary") {
				t.Fatal("lost failure, authority leak or mutation retry", err, stops.Load(), out.String())
			}
			decoder := json.NewDecoder(&out)
			var final v1.AttachedRecord
			for {
				var record v1.AttachedRecord
				if err := decoder.Decode(&record); err == io.EOF {
					break
				} else if err != nil {
					t.Fatal(err)
				}
				if record.Result != nil && (!record.Result.ManifestDrift || !record.Result.ConfigDrift || record.Result.Instance.ID != id || startupAttempt(record.Result.Instance) != attempt || record.Result.Attachment != nil) {
					t.Fatal("lost accepted drift/identity or authority leak", record)
				}
				final = record
			}
			if final.Result == nil {
				t.Fatal("missing final result")
			}
			if fault == "cancel" {
				var exit *ExitError
				if !errors.As(err, &exit) || exit.Code != 130 || final.Type != "stopped" {
					t.Fatal("lost cancellation exit", err, final)
				}
			} else if final.Type != "failure" || final.Error == nil {
				t.Fatal("missing failure context", final)
			}
			if fault == "cleanup" {
				if final.Error.Code != "cleanup_uncertain" || final.Result.Instance.Status != v1.Starting {
					t.Fatal("lost failed-cleanup snapshot", final)
				}
			} else if final.Result.Instance.Status != v1.Stopped {
				t.Fatal("matching cleanup result not retained", final)
			}
			if fault == "renewal" && renewals.Load() != 1 || fault != "renewal" && renewals.Load() != 0 {
				t.Fatal("wrong renewal path", renewals.Load())
			}
		})
	}
}
