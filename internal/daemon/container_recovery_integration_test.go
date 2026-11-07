//go:build darwin || linux

package daemon

import (
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
	"sync/atomic"
	"testing"
	"time"

	"github.com/containerd/errdefs"
	"github.com/moby/moby/client"
	v1 "github.com/octacian/backlot/api/v1"
)

func TestContainerRecoveryEarly(t *testing.T) {
	endpoint := os.Getenv("BACKLOT_DOCKER_TEST_ENDPOINT")
	if endpoint == "" {
		t.Skip("requires explicit Docker provider and pre-pulled alpine:3.21; skip is not runtime evidence")
	}
	target, err := url.Parse(endpoint)
	if err != nil || target.Scheme != "unix" {
		t.Fatal("explicit local provider required")
	}
	listener, err := net.Listen("unix", filepath.Join(shortTemp(t), "engine.sock"))
	if err != nil {
		t.Fatal(err)
	}
	var unavailable, rejectLogs atomic.Bool
	var rejectedLogs atomic.Int32
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", target.Path)
	}}
	proxy := &httputil.ReverseProxy{Rewrite: func(r *httputil.ProxyRequest) {
		r.Out.URL.Scheme = "http"
		r.Out.URL.Host = "docker"
		r.Out.Host = "docker"
	}, Transport: transport}
	server := &http.Server{ReadHeaderTimeout: time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if unavailable.Load() {
			http.Error(w, "owned fixture provider unavailable", http.StatusServiceUnavailable)
			return
		}
		if rejectLogs.Load() && strings.HasSuffix(r.URL.Path, "/logs") {
			rejectedLogs.Add(1)
			http.Error(w, "owned fixture collection failure", http.StatusServiceUnavailable)
			return
		}
		proxy.ServeHTTP(w, r)
	})}
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	t.Cleanup(func() { _ = server.Close(); _ = listener.Close(); <-done; transport.CloseIdleConnections() })
	binary := buildFixtureBinary(t, t.TempDir(), "../../cmd/backlot")
	h := newDockerHarness(t, binary, endpoint)
	sentinel := newDockerHarness(t, binary, endpoint)
	// Restore only our proxy before either harness's registered journal cleanup.
	t.Cleanup(func() { unavailable.Store(false); rejectLogs.Store(false) })
	h.alpine()
	sentinel.alpine()
	config, err := json.Marshal(v1.MachineConfig{Version: v1.ManifestVersion, Docker: &v1.DockerConfig{Endpoint: "unix://" + listener.Addr().String()}})
	if err != nil || os.WriteFile(h.config, config, 0600) != nil {
		t.Fatal("private fixture configuration failed")
	}
	run := func(h *dockerHarness) string {
		t.Helper()
		out, err := exec.Command(binary, "run", "--project", h.project, "--config", h.config, "--state-dir", h.dir, "--stop-grace", "50ms", "--json", "dev").Output()
		if err != nil {
			t.Fatal("real CLI start failed", err)
		}
		var response v1.InstanceResponse
		if json.Unmarshal(out, &response) != nil {
			t.Fatal("invalid CLI start response")
		}
		h.state(response.Instance.ID, v1.RuntimeReady)
		return response.Instance.ID
	}
	id, peer := run(h), run(sentinel)
	h.crash()
	sentinel.crash()
	generation, peerGeneration := h.generation(id), sentinel.generation(peer)
	ownedServer := func(g resourceGeneration) ownedResource {
		t.Helper()
		for _, r := range g.Containers {
			if r.Component == "server" && !r.Removed {
				return r
			}
		}
		t.Fatal("exact server launch missing from owned journal")
		return ownedResource{}
	}
	consumer, peerConsumer := ownedServer(generation), ownedServer(peerGeneration)
	witness, peerWitness := newID(), newID()
	writeWitness := func(h *dockerHarness, r ownedResource, value string) {
		t.Helper()
		if _, code := h.exec(r, []string{"sh", "-c", "printf '%s' '" + value + "' > /data/recovery-witness"}); code != 0 {
			t.Fatal("owned witness write failed")
		}
	}
	verifyWitness := func(h *dockerHarness, r ownedResource, value string) {
		t.Helper()
		out, code := h.exec(r, []string{"cat", "/data/recovery-witness"})
		if code != 0 || out != value {
			t.Fatal("independent random data witness changed")
		}
	}
	writeWitness(h, consumer, witness)
	writeWitness(sentinel, peerConsumer, peerWitness)
	// Simulate an earlier daemon's terminal result write while retaining REAL launch
	// intent/effects. No test-created identity or capability authorizes cleanup.
	state, err := openStore(h.dir)
	if err != nil {
		t.Fatal(err)
	}
	original, err := state.inspect(id)
	if err != nil {
		t.Fatal(err)
	}
	result := *original.Execution
	result.Outcome = v1.Interrupted
	result.CompletedAt = time.Now().UTC().Format(time.RFC3339Nano)
	result.CollectionFailure = ""
	result.CleanupFailure = ""
	if _, err = state.execution(id, v1.Interrupted, &result); err != nil {
		t.Fatal(err)
	}
	if err = state.db.Close(); err != nil {
		t.Fatal(err)
	}
	unavailable.Store(true)
	h.start()
	failed, err := h.cli.Inspect(context.Background(), id)
	if err != nil || failed.Instance.Status != v1.Interrupted || failed.Instance.Execution.Attempt != result.Attempt || failed.Instance.Execution.CleanupFailure == "" {
		t.Fatal("ownerless interrupted effect was skipped or falsely released", err)
	}
	if _, err = h.cli.StopExecution(context.Background(), id); err == nil {
		t.Fatal("provider outage did not retain explicit-stop fence")
	}
	inspect, err := h.engine.ContainerInspect(context.Background(), consumer.ID, client.ContainerInspectOptions{})
	if err != nil || inspect.Container.State == nil || !inspect.Container.State.Running {
		t.Fatal("unavailable proxy changed owned live consumer", err)
	}
	verifyWitness(sentinel, peerConsumer, peerWitness)
	unavailable.Store(false)
	rejectLogs.Store(true)
	out, err := exec.Command(binary, "stop", "--state-dir", h.dir, "--json", id).Output()
	if err != nil {
		t.Fatal("collection-only failure prevented independently verified cleanup", err)
	}
	var stopped v1.InstanceResponse
	if json.Unmarshal(out, &stopped) != nil || stopped.Instance.Execution == nil {
		t.Fatal("invalid CLI stop result")
	}
	current := stopped.Instance.Execution
	if current.Settlement == nil || current.Settlement.State != v1.ConsumerSettled || current.Settlement.Cause != v1.SettlementExplicitStop || current.Settlement.ObservedAt == "" {
		t.Fatal("current exact container release not reported independently")
	}
	if stopped.Instance.Status != v1.Interrupted || current.Outcome != v1.Interrupted || current.Attempt != result.Attempt || current.CompletedAt != result.CompletedAt || current.CollectionFailure != "recovery container logs could not be retained" || current.CleanupFailure != "" || rejectedLogs.Load() == 0 {
		t.Fatalf("independent outcomes lost: status=%s outcome=%s same-attempt=%t same-completion=%t collection-match=%t cleanup-empty=%t log-faults=%d", stopped.Instance.Status, current.Outcome, current.Attempt == result.Attempt, current.CompletedAt == result.CompletedAt, current.CollectionFailure == "recovery container logs could not be retained", current.CleanupFailure == "", rejectedLogs.Load())
	}
	for _, r := range generation.Containers {
		if _, err := h.engine.ContainerInspect(context.Background(), r.ID, client.ContainerInspectOptions{}); !errdefs.IsNotFound(err) {
			t.Fatal("exact owned container absence unverified", err)
		}
	}
	h.crash()
	released := h.generation(id)
	if released.ID != generation.ID || released.Resources["secret"] != generation.Resources["secret"] {
		t.Fatal("consumer cleanup replaced generation or paired secret")
	}
	for _, r := range released.Containers {
		if !r.Removed {
			t.Fatal("verified release not acknowledged")
		}
	}
	for _, r := range released.Resources {
		if r.Kind == "network" && !r.Removed {
			t.Fatal("network obligation remained after verified consumer release")
		}
	}
	verifyWitness(sentinel, peerConsumer, peerWitness)
	rejectLogs.Store(false)
	h.start()
	out, err = exec.Command(binary, "restart", "--state-dir", h.dir, "--stop-grace", "50ms", "--json", id).Output()
	var restart v1.InstanceResponse
	if err != nil || json.Unmarshal(out, &restart) != nil || restart.Instance.ID != id {
		t.Fatal("explicit retained CLI restart failed", err)
	}
	h.state(id, v1.RuntimeReady)
	h.crash()
	next := h.generation(id)
	if next.ID != generation.ID || next.Resources["secret"] != generation.Resources["secret"] {
		t.Fatal("explicit restart changed data generation/paired secret")
	}
	verifyWitness(h, ownedServer(next), witness)
	verifyWitness(sentinel, peerConsumer, peerWitness)
	h.start()
	sentinel.start()
	for _, item := range []struct {
		h  *dockerHarness
		id string
		g  resourceGeneration
	}{{h, id, next}, {sentinel, peer, peerGeneration}} {
		if _, err := item.h.cli.Destroy(context.Background(), item.id); err != nil {
			t.Fatal("explicit fixture destroy failed", err)
		}
		for _, r := range item.g.Containers {
			if _, err := item.h.engine.ContainerInspect(context.Background(), r.ID, client.ContainerInspectOptions{}); !errdefs.IsNotFound(err) {
				t.Fatal("destroyed fixture container remains", err)
			}
		}
		for _, r := range item.g.Resources {
			switch r.Kind {
			case "volume":
				if _, err := item.h.engine.VolumeInspect(context.Background(), resourceHandle(r), client.VolumeInspectOptions{}); !errdefs.IsNotFound(err) {
					t.Fatal("destroyed fixture volume remains", err)
				}
			case "network":
				if _, err := item.h.engine.NetworkInspect(context.Background(), resourceHandle(r), client.NetworkInspectOptions{}); !errdefs.IsNotFound(err) {
					t.Fatal("destroyed fixture network remains", err)
				}
			case "directory":
				if _, err := os.Lstat(r.Name); !os.IsNotExist(err) {
					t.Fatal("destroyed fixture directory remains", err)
				}
			}
		}
	}
	t.Log("real no-SQL CLI/daemon/client: ownerless interrupted live effects retained under outage; collection failed independently of exact container/network release; original outcome/completion and paired data retained; foreign-state live sentinel preserved; explicit destroy verified exact fixture absence")
}
