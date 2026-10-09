//go:build darwin && arm64

package acceptance

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	v1 "github.com/octacian/backlot/api/v1"
	"go.yaml.in/yaml/v3"
)

type sentinels struct{ verify, cleanup func() }

func (h *harness) machine() v1.MachineConfig {
	h.t.Helper()
	data, err := os.ReadFile(h.config)
	if err != nil {
		h.t.Fatal(err)
	}
	var config v1.MachineConfig
	if yaml.Unmarshal(data, &config) != nil || config.Docker == nil || config.Caddy == nil {
		h.t.Fatal("acceptance machine requires approved Docker and gateway")
	}
	return config
}
func (h *harness) docker(args ...string) []byte {
	h.t.Helper()
	config := h.machine()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "docker", append([]string{"--host", config.Docker.Endpoint}, args...)...)
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		h.t.Fatalf("owned Docker fixture command %s failed (provider output retained only in memory): %v", args[0], err)
	}
	return bytes.TrimSpace(stdout.Bytes())
}
func gatewayRequest(t *testing.T, endpoint, method, path string, body []byte) []byte {
	t.Helper()
	request, err := http.NewRequest(method, endpoint+path, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := (&http.Client{Timeout: 5 * time.Second}).Do(request)
	if err != nil {
		t.Fatal("owned gateway fixture request", err)
	}
	defer func() { _ = response.Body.Close() }()
	data, err := io.ReadAll(response.Body)
	if err != nil || response.StatusCode < 200 || response.StatusCode >= 300 {
		t.Fatalf("owned gateway fixture %s %s status=%d", method, path, response.StatusCode)
	}
	return data
}
func (h *harness) sentinels() sentinels {
	t := h.t
	config := h.machine()
	label := "backlot.acceptance.run=" + filepath.Base(h.root)
	name := "backlot-acceptance-" + filepath.Base(h.root)
	volume := string(h.docker("volume", "create", "--label", label, name))
	t.Cleanup(func() {
		if string(h.docker("volume", "inspect", "--format", `{{index .Labels "backlot.acceptance.run"}}`, volume)) != filepath.Base(h.root) {
			t.Error("sentinel volume uncertain; preserved")
			return
		}
		h.docker("volume", "rm", volume)
	})
	network := string(h.docker("network", "create", "--label", label, name))
	t.Cleanup(func() {
		if string(h.docker("network", "inspect", "--format", `{{index .Labels "backlot.acceptance.run"}}`, network)) != filepath.Base(h.root) {
			t.Error("sentinel network uncertain; preserved")
			return
		}
		h.docker("network", "rm", network)
	})
	container := string(h.docker("run", "-d", "--label", label, "--name", name, "--network", network, "--mount", "type=volume,source="+volume+",target=/sentinel", "alpine:3.21", "sh", "-c", "echo preserve >/sentinel/witness; exec sleep 600"))
	t.Cleanup(func() {
		if string(h.docker("inspect", "--format", `{{index .Config.Labels "backlot.acceptance.run"}}`, container)) != filepath.Base(h.root) {
			t.Error("sentinel container uncertain; preserved")
			return
		}
		h.docker("rm", "-f", container)
	})
	route := []byte(`{"@id":"` + name + `","match":[{"host":["` + name + `.` + config.Caddy.DomainSuffix + `"]}],"handle":[{"handler":"static_response","body":"acceptance-sentinel"}],"terminal":true}`)
	gatewayRequest(t, config.Caddy.Endpoint, http.MethodPost, "/id/"+config.Caddy.Scope+"/routes", route)
	t.Cleanup(func() {
		actual := gatewayRequest(t, config.Caddy.Endpoint, http.MethodGet, "/id/"+name, nil)
		var want, got any
		if json.Unmarshal(route, &want) != nil || json.Unmarshal(actual, &got) != nil || !reflect.DeepEqual(want, got) {
			t.Error("sentinel route uncertain; preserved")
			return
		}
		gatewayRequest(t, config.Caddy.Endpoint, http.MethodDelete, "/id/"+name, nil)
	})
	check := func() {
		if string(h.docker("inspect", "--format", `{{index .Config.Labels "backlot.acceptance.run"}}`, container)) != filepath.Base(h.root) {
			t.Fatal("sentinel container ownership changed; preserve")
		}
		for _, kind := range []string{"volume", "network"} {
			if string(h.docker(kind, "inspect", "--format", `{{index .Labels "backlot.acceptance.run"}}`, name)) != filepath.Base(h.root) {
				t.Fatal("sentinel allocation ownership changed; preserve")
			}
		}
		if string(h.docker("exec", container, "cat", "/sentinel/witness")) != "preserve" {
			t.Fatal("unrelated Docker sentinel data changed")
		}
		actual := gatewayRequest(t, config.Caddy.Endpoint, http.MethodGet, "/id/"+name, nil)
		var want, got any
		if json.Unmarshal(route, &want) != nil || json.Unmarshal(actual, &got) != nil || !reflect.DeepEqual(want, got) {
			t.Fatal("unrelated scoped Caddy sentinel changed; preserve")
		}
	}
	return sentinels{verify: check, cleanup: check}
}

// Docker's public HTTP API is forwarded over a private owned Unix socket. One
// selected inspection is delayed after readiness; logs, stop and data operations
// remain real. No journal edits or internal runtime fault hooks are involved.
func (h *harness) inspectionFailure() {
	t := h.t
	config := h.machine()
	upstream, err := url.Parse(config.Docker.Endpoint)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(h.root, "docker-observation.sock")
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Chmod(path, 0600); err != nil {
		t.Fatal(err)
	}
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", upstream.Path)
	}}
	target, _ := url.Parse("http://docker")
	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.Transport = transport
	proxy.ErrorHandler = func(w http.ResponseWriter, _ *http.Request, _ error) { w.WriteHeader(http.StatusBadGateway) }
	var armed atomic.Bool
	var faults atomic.Int32
	server := &http.Server{ReadHeaderTimeout: 5 * time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/containers/") && strings.HasSuffix(r.URL.Path, "/json") && armed.CompareAndSwap(true, false) {
			faults.Add(1)
			select {
			case <-time.After(3 * time.Second):
			case <-r.Context().Done():
				return
			}
		}
		proxy.ServeHTTP(w, r)
	})}
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(ctx); err != nil {
			t.Error("owned observation proxy shutdown", err)
		}
		select {
		case <-done:
		case <-ctx.Done():
			t.Error("owned observation proxy unjoined")
		}
		transport.CloseIdleConnections()
	}()
	config.Docker.Endpoint = "unix://" + path
	data, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(h.root, "observation-machine.json")
	if err = os.WriteFile(configPath, data, 0600); err != nil {
		t.Fatal(err)
	}
	old := h.config
	h.config = configPath
	defer func() { h.config = old }()
	i := h.require("run", "dev")
	// The engine proxy must outlive explicit public destruction even after Fatal.
	defer func() {
		if _, err := h.instance("destroy", i.ID); err != nil {
			t.Error("observation fixture cleanup failed; proxy/state preserved evidence", h.root)
		}
	}()
	httpValue(t, i.Execution.Origin, "observation-retained")
	armed.Store(true)
	failed := h.await(i.ID, v1.Failed)
	if faults.Load() != 1 || !strings.Contains(failed.Execution.Failure, "Docker inspection failed") || !strings.Contains(failed.Execution.Failure, "supervision failed") || strings.Contains(failed.Execution.Failure, "exited unexpectedly") || failed.Execution.CleanupFailure != "" {
		t.Fatalf("observation loss not discriminated: failure=%s cleanup=%s", detail(failed, "failure"), detail(failed, "cleanup"))
	}
	logs, err := h.command("logs", "--state-dir", h.state, "--json", "--component", "database", i.ID)
	if err != nil || !bytes.Contains(logs, []byte("Shutdown complete")) {
		t.Fatal("normal database stop witness absent")
	}
	ready := h.require("restart", i.ID)
	if httpValue(t, ready.Execution.Origin, "") != "observation-retained" {
		t.Fatal("inspection failure restart lost retained data")
	}
	h.require("destroy", i.ID)
	t.Log("one controlled post-ready Docker inspection timeout caused supervision failure and normal DB shutdown; retained-data restart passed; original historical cause remains unproven")
}
