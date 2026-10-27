//go:build darwin || linux

package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	v1 "github.com/octacian/backlot/api/v1"
)

// A private forwarding socket faults only the client exchange; the real daemon
// retains native ownership and supplies normal authenticated fixture cleanup.
func TestCLIAcceptedNativeFailure(t *testing.T) {
	binary := buildFixtureBinary(t, t.TempDir(), "../../cmd/backlot")
	for _, fault := range []string{"observation", "cleanup"} {
		t.Run(fault, func(t *testing.T) {
			h := newNativeHarness(t, binary)
			h.manifest.Tools["sh"] = "/bin/sh"
			component := h.manifest.Components["server"]
			marker := filepath.Join(h.project, "probe-started")
			component.Readiness = &v1.Probe{Kind: "command", Timeout: "0s", Command: &v1.Command{Tool: "sh", Args: []string{"-c", `: > "$1"; exec sleep 60`, "probe", marker}}}
			h.manifest.Components["server"] = component
			h.write()
			proxyRoot, err := os.MkdirTemp("/tmp", "bl-cli-proxy-")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.RemoveAll(proxyRoot) })
			listener, err := net.Listen("unix", SocketPath(proxyRoot))
			if err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(SocketPath(proxyRoot), 0600); err != nil {
				t.Fatal(err)
			}
			transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, "unix", SocketPath(h.dir))
			}}
			t.Cleanup(transport.CloseIdleConnections)
			proxy := httputil.NewSingleHostReverseProxy(&url.URL{Scheme: "http", Host: "backlot"})
			proxy.Transport = transport
			proxy.ErrorHandler = func(w http.ResponseWriter, _ *http.Request, _ error) { w.WriteHeader(502) }
			var out, diag bytes.Buffer
			command := exec.Command(binary, "--json", "run", "dev", "--project", h.project, "--state-dir", proxyRoot, "--stop-grace", "50ms", "--startup-timeout", "0s")
			command.Stdout, command.Stderr = &out, &diag
			started := make(chan struct{})
			server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/v1/inspect" {
					<-started
					deadline := time.Now().Add(5 * time.Second)
					for time.Now().Before(deadline) {
						if _, err := os.Stat(marker); err == nil {
							break
						}
						time.Sleep(10 * time.Millisecond)
					}
					if _, err := os.Stat(marker); err != nil {
						t.Error("native application/probe did not start", err)
					}
					if fault == "cleanup" {
						if err := command.Process.Signal(os.Interrupt); err != nil {
							t.Error(err)
						}
					}
				}
				if fault == "observation" && r.URL.Path == "/v1/inspect" || fault == "cleanup" && r.URL.Path == "/v1/runtime/stop" {
					w.Header().Set("Content-Type", "application/json")
					w.Header().Set("X-Backlot-API-Version", v1.Version)
					w.WriteHeader(503)
					_ = json.NewEncoder(w).Encode(v1.ErrorResponse{APIVersion: v1.Version, Error: v1.PlanError{Code: "fixture_exchange_failed", Message: "owned client exchange failed"}})
					return
				}
				proxy.ServeHTTP(w, r)
			})}
			serverDone := make(chan struct{})
			go func() { defer close(serverDone); _ = server.Serve(listener) }()
			t.Cleanup(func() { _ = server.Close(); <-serverDone })
			done := make(chan error, 1)
			if err := command.Start(); err != nil {
				close(started)
				t.Fatal(err)
			}
			close(started)
			go func() { done <- command.Wait() }()
			joined := false
			t.Cleanup(func() {
				if !joined {
					_ = command.Process.Kill()
					<-done
				}
			})
			select {
			case err = <-done:
				joined = true
			case <-time.After(15 * time.Second):
				t.Fatal("CLI did not return after exchange failure")
			}
			var result v1.InstanceResponse
			if err == nil || json.Unmarshal(out.Bytes(), &result) != nil || result.Error == nil || result.Error.Code != "fixture_exchange_failed" || result.Instance.ID == "" || result.Instance.Execution == nil || result.Instance.Status != v1.Starting || result.LeaseToken != "" || result.Attachment != nil || result.StartupDetails != nil || !strings.Contains(result.Error.Message, "Last observed") {
				t.Fatal("lost accepted native snapshot", err, out.String(), diag.String())
			}
			observed, err := h.cli.Inspect(context.Background(), result.Instance.ID)
			if err != nil || observed.Instance.Execution.Attempt != result.Instance.Execution.Attempt {
				t.Fatal("wrong accepted attempt", err)
			}
			if _, err := h.cli.StopExecution(context.Background(), result.Instance.ID); err != nil {
				t.Fatal(err)
			}
			h.absent()
		})
	}
}
