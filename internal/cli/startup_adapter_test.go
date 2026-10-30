package cli

import (
	"bytes"
	"context"
	"encoding/json"
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
	"github.com/octacian/backlot/internal/client"
	"github.com/octacian/backlot/internal/daemon"
	urfave "github.com/urfave/cli/v3"
)

func TestStartupAdapterAttemptAndSignalBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name, adapter string
		signal        os.Signal
	}{
		{"run replacement", "run", nil},
		{"named replacement", "scene", nil},
		{"named SIGINT retrieval", "scene", os.Interrupt},
		{"named SIGTERM retrieval", "scene", syscall.SIGTERM},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root, err := os.MkdirTemp("/tmp", "bl-start-adapter-")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.RemoveAll(root) })
			manifest := `{"version":"backlot/v1","project":"adapter","tools":{"fixture":"/bin/sh"},"components":{"seed":{"kind":"job","runtime":"native","policy":"each-start","command":{"tool":"fixture","args":["-c","true"]},"fixtures":{"password":{"value":{"literal":"late-private-canary","secret":true}}}}},"scenes":{"dev":{"lifetime":"persistent","components":["seed"],"startup_details":[{"component":"seed","fixture":"password","label":"Password"}]}}}`
			if err := os.WriteFile(filepath.Join(root, "backlot.json"), []byte(manifest), 0600); err != nil {
				t.Fatal(err)
			}
			socket := daemon.SocketPath(root)
			listener, err := net.Listen("unix", socket)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(socket, 0600); err != nil {
				t.Fatal(err)
			}
			id, a, b := strings.Repeat("a", 64), strings.Repeat("b", 64), strings.Repeat("c", 64)
			checkout := v1.CheckoutIdentity{ID: id, Path: root}
			status := v1.Starting
			if tc.signal != nil {
				status = v1.RuntimeReady
			}
			instance := v1.Instance{ID: id, Checkout: checkout, Status: status, CreatedAt: time.Now().UTC().Format(time.RFC3339Nano), Operation: v1.Operation{ID: id, Status: status, Allocations: []v1.AllocationRecord{{ID: id, Kind: "private_snapshot", Phase: "effect"}}}, Plan: v1.PlanResponse{APIVersion: v1.Version, Project: "adapter", Scene: "dev", Lifetime: v1.Persistent, Components: []v1.PlannedComponent{}, Checkout: root, ManifestPath: filepath.Join(root, "backlot.json"), StartupDetails: []v1.StartupDetailSelection{{Component: "seed", Fixture: "password", Label: "Password"}}}, Execution: &v1.ExecutionResult{Attempt: a, Components: []v1.ComponentResult{}, Fixtures: []v1.FixtureRecord{{Component: "seed", Name: "password", Sensitive: true}}}}
			signals := make(chan os.Signal, 1)
			cancelled, handlerDone := make(chan struct{}, 1), make(chan struct{}, 1)
			var secretRequests, polls atomic.Int32
			server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("X-Backlot-API-Version", v1.Version)
				response := v1.InstanceResponse{APIVersion: v1.Version, Instance: instance}
				if r.URL.Path == "/v1/inspect" || r.URL.Path == "/v1/scenes/status" {
					polls.Add(1)
					response.Instance.Status = v1.RuntimeReady
					response.Instance.Operation.Status = v1.RuntimeReady
					result := *instance.Execution
					result.Attempt = b
					response.Instance.Execution = &result
				}
				switch r.URL.Path {
				case "/v1/status":
					_ = json.NewEncoder(w).Encode(v1.DaemonStatusResponse{APIVersion: v1.Version, StateVersion: v1.StateVersion, Status: "running", PID: os.Getpid(), LeaseDuration: "1m"})
				case "/v1/run", "/v1/inspect":
					_ = json.NewEncoder(w).Encode(response)
				case "/v1/scenes/start":
					_ = json.NewEncoder(w).Encode(v1.SceneResponse{APIVersion: v1.Version, Outcome: v1.SceneFound, DaemonGeneration: id, Checkout: &checkout, CanonicalTarget: &v1.SceneTarget{Scene: "dev", CWD: root}, Result: &response})
				case "/v1/scenes/status":
					_ = json.NewEncoder(w).Encode(v1.SceneResponse{APIVersion: v1.Version, Outcome: v1.SceneFound, DaemonGeneration: id, Result: &response})
				case "/v1/fixtures/secret":
					if _, err := io.Copy(io.Discard, r.Body); err != nil {
						t.Error(err)
						return
					}
					secretRequests.Add(1)
					if tc.signal != nil {
						signals <- tc.signal
						select {
						case <-r.Context().Done():
							cancelled <- struct{}{}
						case <-time.After(time.Second):
						}
					}
					// Even a late successful reply must never become startup output after interruption.
					_ = json.NewEncoder(w).Encode(v1.FixturesResponse{APIVersion: v1.Version, InstanceID: id, Attempt: a, Fixtures: []v1.FixtureRecord{{Component: "seed", Name: "password", Sensitive: true, Value: "late-private-canary"}}})
					handlerDone <- struct{}{}
				default:
					t.Errorf("unexpected adapter request %s", r.URL.Path)
				}
			})}
			serverDone := make(chan struct{})
			go func() { defer close(serverDone); _ = server.Serve(listener) }()
			t.Cleanup(func() { _ = server.Close(); <-serverDone })
			cli := client.New(socket)
			t.Cleanup(cli.Close)
			var out, stderr bytes.Buffer
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			if tc.adapter == "run" {
				err = NewCommand(v1.VersionResponse{APIVersion: v1.Version}, &out, &stderr).Run(ctx, []string{"backlot", "run", "dev", "--project", root, "--state-dir", root, "--json"})
			} else {
				command := &urfave.Command{Name: "scene-start", Writer: &out, ErrWriter: &stderr, Flags: []urfave.Flag{&urfave.BoolFlag{Name: "json"}}, Action: func(ctx context.Context, c *urfave.Command) error {
					return startScene(ctx, c, cli, v1.SceneTarget{Scene: "dev", CWD: root}, signals)
				}}
				err = command.Run(ctx, []string{"scene-start", "--json"})
			}
			if strings.Contains(out.String(), "late-private-canary") {
				t.Fatal("late private value rendered")
			}
			if tc.signal == nil {
				var result v1.InstanceResponse
				if tc.adapter == "scene" {
					var scene v1.SceneResponse
					if json.Unmarshal(out.Bytes(), &scene) != nil || scene.Result == nil {
						t.Fatal("missing retained scene", out.String())
					}
					result = *scene.Result
					result.Error = scene.Error
				} else if json.Unmarshal(out.Bytes(), &result) != nil {
					t.Fatal("invalid retained instance", out.String())
				}
				if err == nil || result.Error == nil || result.Error.Code != "startup_conflict" || result.Instance.ID != id || result.Instance.Execution.Attempt != a || result.Instance.Status != v1.Starting || !strings.Contains(result.Error.Message, "Last observed") {
					t.Fatal("replacement did not preserve accepted snapshot and conflict", err, out.String())
				}
				if polls.Load() != 1 || secretRequests.Load() != 0 || result.StartupDetails != nil {
					t.Fatal("replacement readiness reached successful summary/retrieval")
				}
			} else {
				if err != nil {
					t.Fatal("summary interruption changed successful startup", err)
				}
				select {
				case <-cancelled:
				case <-time.After(time.Second):
					t.Fatal("signal did not cancel pending sensitive retrieval")
				}
				select {
				case <-handlerDone:
				case <-time.After(time.Second):
					t.Fatal("private request handler not joined")
				}
				var result v1.SceneResponse
				if json.Unmarshal(out.Bytes(), &result) != nil || result.Result == nil || result.Result.Instance.ID != id || result.Result.Instance.Status != v1.RuntimeReady || result.Result.StartupDetails == nil || result.Result.StartupDetails.Warning == nil || result.Result.StartupDetails.Warning.Code != "startup_details_unavailable" || len(result.Result.StartupDetails.Details) != 0 {
					t.Fatal("interrupted retrieval lost ready identity or sanitized warning")
				}
			}
		})
	}
}

// The socket peer is a fault harness, not a substitute for the owned native gate.
func TestAcceptedCLIObservationFailuresKeepIdentity(t *testing.T) {
	for _, adapter := range []string{"run", "scene"} {
		for _, fault := range []string{"disconnect", "invalid-wire", "cleanup", "renewal"} {
			if adapter == "scene" && fault == "renewal" {
				continue
			}
			t.Run(adapter+"/"+fault, func(t *testing.T) {
				root, err := os.MkdirTemp("/tmp", "bl-result-")
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = os.RemoveAll(root) })
				if err := os.WriteFile(filepath.Join(root, "backlot.json"), []byte(`{"version":"backlot/v1","project":"adapter","tools":{"sh":"/bin/sh"},"components":{"job":{"kind":"job","runtime":"native","policy":"each-start","command":{"tool":"sh"}}},"scenes":{"dev":{"lifetime":"persistent","components":["job"]}}}`), 0600); err != nil {
					t.Fatal(err)
				}
				listener, err := net.Listen("unix", daemon.SocketPath(root))
				if err != nil {
					t.Fatal(err)
				}
				if err := os.Chmod(daemon.SocketPath(root), 0600); err != nil {
					t.Fatal(err)
				}
				id, attempt := strings.Repeat("a", 64), strings.Repeat("b", 64)
				ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				defer cancel()
				instance := v1.Instance{ID: id, Checkout: v1.CheckoutIdentity{ID: id, Path: root}, CreatedAt: time.Now().UTC().Format(time.RFC3339Nano), Status: v1.Starting, Operation: v1.Operation{ID: id, Status: v1.Starting, Allocations: []v1.AllocationRecord{{ID: id, Kind: "private_snapshot", Phase: "effect"}}}, Plan: v1.PlanResponse{APIVersion: v1.Version, Project: "adapter", Scene: "dev", ManifestPath: filepath.Join(root, "backlot.json"), Checkout: root, Lifetime: v1.Persistent, Components: []v1.PlannedComponent{}}, Execution: &v1.ExecutionResult{Attempt: attempt, Components: []v1.ComponentResult{}}}
				response := v1.InstanceResponse{APIVersion: v1.Version, Instance: instance, ManifestDrift: true, ConfigDrift: true}
				if fault == "renewal" {
					response.Instance.Plan.Lifetime = v1.Disposable
					response.Instance.LeaseExpiresAt = time.Now().Add(time.Second).UTC().Format(time.RFC3339Nano)
					response.LeaseToken = strings.Repeat("d", 64)
				}
				var polls, stops, renewals atomic.Int32
				server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					w.Header().Set("X-Backlot-API-Version", v1.Version)
					switch r.URL.Path {
					case "/v1/status":
						_ = json.NewEncoder(w).Encode(v1.DaemonStatusResponse{APIVersion: v1.Version, StateVersion: v1.StateVersion, Status: "running", PID: os.Getpid(), LeaseDuration: "30s"})
					case "/v1/run", "/v1/scenes/start":
						if fault == "cleanup" {
							cancel()
						}
						if adapter == "run" {
							_ = json.NewEncoder(w).Encode(response)
						} else {
							_ = json.NewEncoder(w).Encode(v1.SceneResponse{APIVersion: v1.Version, DaemonGeneration: id, Outcome: v1.SceneFound, Checkout: &instance.Checkout, CanonicalTarget: &v1.SceneTarget{Scene: "dev", CWD: root, ProjectPath: root}, Result: &response})
						}
					case "/v1/renew":
						renewals.Add(1)
						w.WriteHeader(503)
						_ = json.NewEncoder(w).Encode(v1.ErrorResponse{APIVersion: v1.Version, Error: v1.PlanError{Code: "lease_failed", Message: "lease renewal unavailable"}})
					case "/v1/runtime/stop":
						stops.Add(1)
						var stop v1.StopRequest
						if json.NewDecoder(r.Body).Decode(&stop) != nil || stop.InstanceID != id || stop.ExpectedAttempt != attempt || stop.Force {
							t.Error("startup cancellation lacked exact precondition")
						}
						w.WriteHeader(503)
						_ = json.NewEncoder(w).Encode(v1.ErrorResponse{APIVersion: v1.Version, Error: v1.PlanError{Code: "cleanup_uncertain", Message: "cleanup result unavailable"}})
					case "/v1/inspect", "/v1/scenes/status":
						polls.Add(1)
						if fault == "invalid-wire" {
							_, _ = io.WriteString(w, `{"api_version":"v1","instance":{"id":"invalid-private-canary"}}`)
							return
						}
						if fault == "renewal" {
							w.WriteHeader(503)
							_ = json.NewEncoder(w).Encode(v1.ErrorResponse{APIVersion: v1.Version, Error: v1.PlanError{Code: "observation_failed", Message: "cannot inspect after renewal"}})
							return
						}
						hijacker, ok := w.(http.Hijacker)
						if !ok {
							t.Error("missing hijacker")
							return
						}
						connection, _, err := hijacker.Hijack()
						if err != nil {
							t.Error(err)
							return
						}
						_ = connection.Close()
					default:
						t.Error("unexpected request", r.URL.Path)
					}
				})}
				done := make(chan struct{})
				go func() { defer close(done); _ = server.Serve(listener) }()
				t.Cleanup(func() { _ = server.Close(); <-done })
				var out, diag bytes.Buffer
				args := []string{"backlot", "--json"}
				if adapter == "scene" {
					args = append(args, "scene", "start")
				} else {
					args = append(args, "run")
				}
				args = append(args, "dev", "--project", root, "--state-dir", root)
				err = NewCommand(v1.VersionResponse{}, &out, &diag).Run(ctx, args)
				if err == nil {
					t.Fatal("fault reported success")
				}
				var retained v1.InstanceResponse
				var detail *v1.PlanError
				if adapter == "scene" {
					var scene v1.SceneResponse
					if json.Unmarshal(out.Bytes(), &scene) != nil || scene.Result == nil {
						t.Fatal("missing scene snapshot", out.String())
					}
					retained, detail = *scene.Result, scene.Error
				} else {
					if json.Unmarshal(out.Bytes(), &retained) != nil {
						t.Fatal("invalid single document", out.String())
					}
					detail = retained.Error
				}
				if detail == nil || !strings.Contains(detail.Message, "Last observed") || retained.Instance.ID != id || startupAttempt(retained.Instance) != attempt || retained.Instance.Status != v1.Starting || !retained.ManifestDrift || !retained.ConfigDrift || retained.LeaseToken != "" || retained.StartupDetails != nil || retained.Attachment != nil || strings.Contains(out.String(), "invalid-private-canary") || strings.Contains(out.String(), strings.Repeat("d", 64)) {
					t.Fatal("unsafe or lost accepted snapshot", out.String(), err)
				}
				if fault == "cleanup" && stops.Load() != 1 || fault == "renewal" && renewals.Load() != 1 || fault != "cleanup" && polls.Load() != 1 {
					t.Fatal("wrong fault exercised", stops.Load(), renewals.Load(), polls.Load())
				}
			})
		}
	}
}
