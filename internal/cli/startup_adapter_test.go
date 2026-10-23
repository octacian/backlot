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
				var result v1.ErrorResponse
				if err == nil || json.Unmarshal(out.Bytes(), &result) != nil || result.Error.Code != "startup_conflict" {
					t.Fatal("replacement did not produce one conflict JSON document", err)
				}
				if polls.Load() != 1 || secretRequests.Load() != 0 || strings.Contains(out.String(), "startup_details") {
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
