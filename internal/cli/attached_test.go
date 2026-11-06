package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/octacian/backlot/internal/client"
	"github.com/octacian/backlot/internal/daemon"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
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

func TestAttachedStartupSummary(t *testing.T) {
	for _, mode := range []string{"success", "unavailable", "partial", "replacement", "stopped", "cancel", "SIGINT", "SIGTERM", "timeout", "output"} {
		for _, machine := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/json=%t", mode, machine), func(t *testing.T) {
				root, err := os.MkdirTemp("/tmp", "bl-attached-summary-")
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
				instance := v1.Instance{ID: id, Checkout: v1.CheckoutIdentity{ID: id, Path: root}, CreatedAt: time.Now().UTC().Format(time.RFC3339Nano), Status: v1.RuntimeReady, Operation: v1.Operation{ID: id, Status: v1.RuntimeReady, Allocations: []v1.AllocationRecord{{ID: id, Kind: "private_snapshot", Phase: "effect"}}}, Plan: v1.PlanResponse{APIVersion: v1.Version, Project: "adapter", Scene: "dev", ManifestPath: filepath.Join(root, "backlot.json"), Checkout: root, Lifetime: v1.Persistent, Components: []v1.PlannedComponent{}, StartupDetails: []v1.StartupDetailSelection{{Component: "seed", Fixture: "password", Label: "Password"}, {Component: "seed", Fixture: "email", Label: "Email"}}}, Execution: &v1.ExecutionResult{Attempt: attempt, Components: []v1.ComponentResult{}, Fixtures: []v1.FixtureRecord{{Component: "seed", Name: "password", Sensitive: true}, {Component: "seed", Name: "email", Sensitive: true}, {Component: "seed", Name: "unselected", Sensitive: true}}}}
				accepted := v1.InstanceResponse{APIVersion: v1.Version, Instance: instance, Attachment: &v1.Attachment{Owner: v1.AttachedOwner{Attempt: attempt, DaemonGeneration: id, Token: strings.Repeat("c", 64)}, LeaseDuration: "1s"}}
				ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
				defer cancel()
				var reads, renewals, stops atomic.Int32
				server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					w.Header().Set("X-Backlot-API-Version", v1.Version)
					switch r.URL.Path {
					case "/v1/fixtures/secret":
						n := reads.Add(1)
						var request v1.FixturesRequest
						if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
							t.Error(err)
						}
						if request.ExpectedAttempt != attempt || request.InstanceID != id || request.SecretName == "unselected" {
							t.Error("unfenced or unselected retrieval")
						}
						if mode == "cancel" || mode == "SIGINT" || mode == "SIGTERM" || mode == "timeout" {
							switch mode {
							case "cancel":
								cancel()
							case "SIGINT", "SIGTERM":
								sig := syscall.SIGINT
								if mode == "SIGTERM" {
									sig = syscall.SIGTERM
								}
								if err := syscall.Kill(os.Getpid(), sig); err != nil {
									t.Error(err)
								}
							}
							<-r.Context().Done()
							return
						}
						if mode == "unavailable" || mode == "partial" && n == 2 {
							w.WriteHeader(503)
							_ = json.NewEncoder(w).Encode(v1.ErrorResponse{APIVersion: v1.Version, Error: v1.PlanError{Code: "unavailable", Message: "raw-private-error-canary"}})
							return
						}
						_ = json.NewEncoder(w).Encode(v1.FixturesResponse{APIVersion: v1.Version, InstanceID: id, Attempt: attempt, Fixtures: []v1.FixtureRecord{{Component: "seed", Name: request.SecretName, Sensitive: true, Value: "selected-" + request.SecretName + "-canary"}}})
					case "/v1/attached/renew", "/v1/attached/cancel":
						next := instance
						if r.URL.Path == "/v1/attached/cancel" {
							stops.Add(1)
							next.Status, next.Operation.Status = v1.Stopped, v1.Stopped
						} else if n := renewals.Add(1); mode == "replacement" {
							execution := *next.Execution
							execution.Attempt = strings.Repeat("d", 64)
							next.Execution = &execution
						} else if n >= 3 || mode == "stopped" {
							next.Status, next.Operation.Status = v1.Stopped, v1.Stopped
						}
						_ = json.NewEncoder(w).Encode(v1.SceneResponse{APIVersion: v1.Version, DaemonGeneration: id, Outcome: v1.SceneFound, Result: &v1.InstanceResponse{APIVersion: v1.Version, Instance: next}})
					case "/v1/logs":
						_ = json.NewEncoder(w).Encode(v1.LogsResponse{APIVersion: v1.Version, Records: []v1.LogRecord{}})
					default:
						t.Error("unexpected operation", r.URL.Path)
					}
				})}
				done := make(chan struct{})
				go func() { defer close(done); _ = server.Serve(listener) }()
				t.Cleanup(func() { _ = server.Close(); <-done })
				api := client.New(daemon.SocketPath(root))
				defer api.Close()
				var out, diag bytes.Buffer
				var writer io.Writer = &out
				if mode == "output" {
					writer = failingWriter{}
				}
				command := &urfave.Command{Writer: writer, ErrWriter: &diag, Flags: []urfave.Flag{&urfave.BoolFlag{Name: "json"}}, Action: func(ctx context.Context, c *urfave.Command) error {
					return runAttached(ctx, c, api, "0s", func(context.Context) (v1.InstanceResponse, error) { return accepted, nil })
				}}
				args := []string{"attached"}
				if machine {
					args = append(args, "--json")
				}
				err = command.Run(ctx, args)
				wantError := mode == "replacement" || mode == "cancel" || mode == "SIGINT" || mode == "SIGTERM" || mode == "output"
				if (err != nil) != wantError {
					t.Fatal("unexpected outcome", err)
				}
				if mode == "SIGINT" || mode == "SIGTERM" || mode == "cancel" {
					var exit *ExitError
					want := 130
					if mode == "SIGTERM" {
						want = 143
					}
					if !errors.As(err, &exit) || exit.Code != want {
						t.Fatal("lost interruption exit", err)
					}
				}
				if wantError && stops.Load() != 1 {
					t.Fatal("cleanup not preserved", stops.Load())
				}
				visible := out.String() + diag.String()
				if strings.Contains(visible, "raw-private-error-canary") || strings.Contains(visible, accepted.Attachment.Owner.Token) {
					t.Fatal("private data leaked")
				}
				wantSummary := mode == "success"
				if count := strings.Count(visible, "selected-password-canary"); count != map[bool]int{true: 1, false: 0}[wantSummary] {
					t.Fatal("wrong disclosure count", count)
				}
				warning := mode == "unavailable" || mode == "partial" || mode == "timeout"
				if strings.Contains(visible, "Startup details unavailable") != warning {
					t.Fatal("missing or unexpected warning")
				}
				if accepted.StartupDetails != nil || accepted.Instance.Execution.Fixtures[0].Value != "" {
					t.Fatal("mutated retained response")
				}
				if machine && mode != "output" {
					decoder := json.NewDecoder(&out)
					ready := 0
					for {
						var record v1.AttachedRecord
						if err := decoder.Decode(&record); err == io.EOF {
							break
						} else if err != nil {
							t.Fatal(err)
						}
						if record.Type == "ready" {
							ready++
						} else if record.Result != nil && record.Result.StartupDetails != nil {
							t.Fatal("summary escaped ready record")
						}
					}
					if ready > 1 || (wantSummary || warning) && ready != 1 {
						t.Fatal("wrong ready count", ready)
					}
				} else if wantSummary && !strings.Contains(out.String(), "Password: selected-password-canary\nEmail: selected-email-canary") {
					t.Fatal("human labels/order lost")
				}
			})
		}
	}
}
