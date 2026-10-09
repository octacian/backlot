//go:build darwin || linux

package daemon

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	v1 "github.com/octacian/backlot/api/v1"
	"github.com/octacian/backlot/internal/gateway/gatewaytest"
	"github.com/octacian/backlot/internal/native"
)

func reviewFixBinary(t *testing.T) string {
	t.Helper()
	binary := filepath.Join(t.TempDir(), "backlot")
	if out, err := exec.Command("go", "build", "-race", "-o", binary, "../../cmd/backlot").CombinedOutput(); err != nil {
		t.Fatalf("build: %v %s", err, out)
	}
	return binary
}

func TestDestroyActiveKeptNative(t *testing.T) {
	binary := reviewFixBinary(t)
	for _, complete := range []bool{false, true} {
		t.Run(fmt.Sprintf("completion-race-%v", complete), func(t *testing.T) {
			h := newNativeHarness(t, binary, "20s")
			h.manifest.Tools["sh"] = "/bin/sh"
			h.manifest.Resources["data"] = v1.Resource{Kind: "directory"}
			dev := h.manifest.Scenes["dev"]
			dev.Resources = append(dev.Resources, "data")
			h.manifest.Scenes["dev"] = dev
			scene := h.manifest.Scenes["test"]
			scene.Resources = append(scene.Resources, "data")
			h.manifest.Scenes["test"] = scene
			server := h.manifest.Components["server"]
			server.Resources = append(server.Resources, "data")
			h.manifest.Components["server"] = server
			job := h.manifest.Components["job"]
			job.Resources = append(job.Resources, "data")
			job.Fixtures = map[string]v1.Fixture{"data": {Value: fixtureRef("resource", "data", "path")}}
			job.Command = &v1.Command{Tool: "sh", Args: []string{"-c", "echo waiting; while [ ! -f release ]; do sleep 0.01; done; exit 23"}}
			h.manifest.Components["job"] = job
			h.write()
			request := h.request("test")
			request.Options.KeepOnFailure = true
			request.Options.JobTimeout = "0"
			response, err := h.cli.Run(context.Background(), request)
			if err != nil {
				t.Fatal(err)
			}
			id := response.Instance.ID
			waitComponentLog(t, h, id, "job", "waiting")
			fixtures, err := h.cli.Fixtures(context.Background(), v1.FixturesRequest{APIVersion: v1.Version, InstanceID: id})
			if err != nil || len(fixtures.Fixtures) != 1 {
				t.Fatal("fixture data", err)
			}
			data := fixtures.Fixtures[0].Value
			released := make(chan error, 1)
			if complete {
				go func() { released <- os.WriteFile(filepath.Join(h.project, "release"), nil, 0600) }()
			}
			destroyed, err := h.cli.Destroy(context.Background(), id)
			if err != nil {
				t.Fatal(err)
			}
			if complete {
				if err := <-released; err != nil {
					t.Fatal(err)
				}
			}
			result := destroyed.Instance.Execution
			if destroyed.Instance.Status != v1.Destroyed || result.Kept || result.CleanupFailure != "" {
				t.Fatal("destroy did not join retained owner")
			}
			conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", result.Ports["port"]), time.Second)
			if conn != nil {
				_ = conn.Close()
			}
			if err == nil {
				t.Fatal("service live after destroy")
			}
			if _, err := os.Stat(data); !os.IsNotExist(err) {
				t.Fatal("data retained after verified destroy", err)
			}
			h.absent()
			logs, err := h.cli.Logs(context.Background(), v1.LogsRequest{APIVersion: v1.Version, InstanceID: id})
			if err != nil || len(logs.Records) == 0 {
				t.Fatal("evidence absent", err)
			}
		})
	}
}

func TestLeaseCollectionFailureIsInstanceLocal(t *testing.T) {
	binary := reviewFixBinary(t)
	h := newNativeHarness(t, binary, "500ms")
	// Separate witnesses/control paths keep cleanup attribution exact for both owners.
	other := h.manifest.Components["server"]
	other.Environment.Assign = map[string]v1.Value{}
	for key, value := range h.manifest.Components["server"].Environment.Assign {
		other.Environment.Assign[key] = value
	}
	otherPIDs := filepath.Join(filepath.Dir(h.dir), "other-pids")
	other.Environment.Assign["PIDFILE"] = fixtureLiteral(otherPIDs)
	other.Environment.Assign["FIXTURE_CONTROL"] = fixtureLiteral(filepath.Join(filepath.Dir(h.dir), "other.sock"))
	h.extraPIDFiles = append(h.extraPIDFiles, otherPIDs)
	h.manifest.Components["other"] = other
	dev := h.manifest.Scenes["dev"]
	dev.Components = []string{"other"}
	h.manifest.Scenes["dev"] = dev
	h.manifest.Tools["sh"] = "/bin/sh"
	job := h.manifest.Components["job"]
	job.Command = &v1.Command{Tool: "sh", Args: []string{"-c", "echo waiting; exec sleep 60"}}
	job.Artifacts = map[string]v1.ArtifactSource{"missing": {Path: "not-produced"}}
	h.manifest.Components["job"] = job
	h.write()
	unrelated := h.run("dev")
	h.state(unrelated.Instance.ID, v1.RuntimeReady)
	request := h.request("test")
	request.Options.KeepOnFailure = true
	request.Options.JobTimeout = "0"
	r, err := h.cli.Run(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	waitComponentLog(t, h, r.Instance.ID, "job", "waiting")
	deadline := time.Now().Add(8 * time.Second)
	var final v1.Instance
	for time.Now().Before(deadline) {
		if _, err := h.cli.Status(context.Background()); err != nil {
			t.Fatal("unrelated lease", err)
		}
		got, err := h.cli.Inspect(context.Background(), r.Instance.ID)
		if err != nil {
			t.Fatal("daemon unavailable", err)
		}
		final = got.Instance
		if final.Execution != nil && final.Execution.CompletedAt != "" {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if final.Execution == nil || !final.Execution.Cancelled || !final.Execution.Kept || final.Execution.CollectionFailure == "" || final.Execution.CleanupFailure != "" {
		t.Fatal("lease/collection outcome missing")
	}
	// Continue through subsequent sweeps, the point where the prior fatal error escaped.
	for range 10 {
		time.Sleep(50 * time.Millisecond)
		if _, err := h.cli.Status(context.Background()); err != nil {
			t.Fatal("daemon or unrelated owner lost", err)
		}
	}
	for _, id := range []string{r.Instance.ID, unrelated.Instance.ID} {
		got, err := h.cli.Inspect(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", got.Instance.Execution.Ports["port"]), time.Second)
		if err != nil {
			t.Fatal("healthy service lost", err)
		}
		_ = conn.Close()
		if _, err := h.cli.Destroy(context.Background(), id); err != nil {
			t.Fatal(err)
		}
	}
	h.absent()
}

func TestDestroyActiveKeptDocker(t *testing.T) {
	endpoint := os.Getenv("BACKLOT_DOCKER_TEST_ENDPOINT")
	if endpoint == "" {
		t.Skip("requires explicit owned Docker fixture endpoint")
	}
	binary := reviewFixBinary(t)
	for _, complete := range []bool{false, true} {
		t.Run(fmt.Sprintf("completion-race-%v", complete), func(t *testing.T) {
			h := newDockerHarness(t, binary, endpoint)
			h.alpine()
			job := h.manifest.Components["each"]
			job.Args = []string{"sh", "-c", "echo waiting; while [ ! -f /private/release ]; do sleep 0.01; done; exit 23"}
			job.Fixtures = map[string]v1.Fixture{"data": {Value: fixtureRef("resource", "directory", "path")}}
			h.manifest.Components["each"] = job
			h.write()
			request := h.request("test")
			request.Options.KeepOnFailure = true
			request.Options.JobTimeout = "0"
			r, err := h.cli.Run(context.Background(), request)
			if err != nil {
				t.Fatal(err)
			}
			id := r.Instance.ID
			waitComponentLog(t, h.nativeHarness, id, "each", "waiting")
			server := h.container(id, "server")
			fixtures, err := h.cli.Fixtures(context.Background(), v1.FixturesRequest{APIVersion: v1.Version, InstanceID: id, Component: "each"})
			if err != nil || len(fixtures.Fixtures) != 1 {
				t.Fatal("data fixture", err)
			}
			data := fixtures.Fixtures[0].Value
			released := make(chan error, 1)
			if complete {
				go func() { released <- os.WriteFile(filepath.Join(data, "release"), nil, 0600) }()
			}
			destroyed, err := h.cli.Destroy(context.Background(), id)
			if err != nil {
				t.Fatal(err)
			}
			if complete {
				if err := <-released; err != nil {
					t.Fatal(err)
				}
			}
			if destroyed.Instance.Status != v1.Destroyed || destroyed.Instance.Execution.Kept || h.engineAlive(server) {
				t.Fatal("retained container owner survived destroy")
			}
			if _, err := os.Stat(data); !os.IsNotExist(err) {
				t.Fatal("destroy data", err)
			}
			logs, err := h.cli.Logs(context.Background(), v1.LogsRequest{APIVersion: v1.Version, InstanceID: id})
			if err != nil || len(logs.Records) == 0 {
				t.Fatal("historical evidence", err)
			}
		})
	}
}

func TestPublicationReadinessSignals(t *testing.T) {
	if os.Getenv("BACKLOT_CADDY_TEST_BINARY") == "" {
		t.Skip("requires explicit owned Caddy fixture binary")
	}
	binary := reviewFixBinary(t)
	for _, sig := range []os.Signal{os.Interrupt, syscall.SIGTERM} {
		t.Run(sig.String(), func(t *testing.T) {
			f := gatewaytest.Start(t, "fixture.localhost")
			t.Setenv("SSL_CERT_FILE", f.Cert)
			h := newNativeHarness(t, binary, "20s")
			server := h.manifest.Components["server"]
			server.Environment.Assign["HTTP_STATUS"] = fixtureLiteral("503")
			h.manifest.Components["server"] = server
			origin := fixtureRef("resource", "origin", "url")
			h.manifest.Resources["origin"] = v1.Resource{Kind: "origin"}
			scene := h.manifest.Scenes["dev"]
			scene.Resources = append(scene.Resources, "origin")
			scene.Publish = &v1.Publication{Resource: "origin", Routes: []v1.Route{{Path: "/", Service: "server", Port: "http", Prefix: "preserve"}}, Probe: v1.Probe{Kind: "http", Target: &origin, Timeout: "20s"}}
			h.manifest.Scenes["dev"] = scene
			h.write()
			config := filepath.Join(h.project, "machine.json")
			raw, err := json.Marshal(v1.MachineConfig{Version: v1.ManifestVersion, Caddy: &v1.CaddyConfig{Endpoint: f.Endpoint, Scope: "backlot", DomainSuffix: "fixture.localhost", HostAddress: "127.0.0.1", HTTPSPort: &f.Port}})
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(config, raw, 0600); err != nil {
				t.Fatal(err)
			}
			prepared, err := h.cli.Prepare(context.Background(), v1.PrepareRequest{APIVersion: v1.Version, Plan: v1.PlanRequest{ProjectPath: h.project, Scene: "dev", ConfigPath: config}})
			if err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command(binary, "run", "dev", "--project", h.project, "--config", config, "--state-dir", h.dir, "--startup-timeout", "20s", "--stop-grace", "50ms", "--json")
			var output strings.Builder
			cmd.Stdout = &output
			cmd.Stderr = &strings.Builder{}
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			joined := false
			t.Cleanup(func() {
				if !joined {
					_ = cmd.Process.Kill()
					_ = cmd.Wait()
				}
			})
			transport := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: f.Pool, MinVersion: tls.VersionTLS12}}
			defer transport.CloseIdleConnections()
			httpClient := &http.Client{Transport: transport, Timeout: time.Second}
			deadline := time.Now().Add(8 * time.Second)
			probing := false
			for time.Now().Before(deadline) {
				got, err := h.cli.Inspect(context.Background(), prepared.Instance.ID)
				if err != nil {
					t.Fatal(err)
				}
				if got.Instance.Execution != nil && got.Instance.Execution.Origin != "" {
					response, err := httpClient.Get(got.Instance.Execution.Origin)
					if err == nil {
						_ = response.Body.Close()
						if response.StatusCode == http.StatusServiceUnavailable && got.Instance.Status == v1.Starting && response.Header.Get("Backlot-Instance") == "bl-"+prepared.Instance.ID[:40]+".fixture.localhost" {
							probing = true
							break
						}
					}
				}
				time.Sleep(20 * time.Millisecond)
			}
			if !probing {
				t.Fatal("did not reach aggregate readiness")
			}
			if err := cmd.Process.Signal(sig); err != nil {
				t.Fatal(err)
			}
			err = cmd.Wait()
			joined = true
			want := 130
			if sig == syscall.SIGTERM {
				want = 143
			}
			exit, ok := err.(*exec.ExitError)
			if !ok || exit.ExitCode() != want {
				t.Fatalf("signal exit %v want %d", err, want)
			}
			var final v1.InstanceResponse
			if err := json.Unmarshal([]byte(output.String()), &final); err != nil {
				t.Fatal(err)
			}
			result := final.Instance.Execution
			if !result.Cancelled || result.Failure != "" || result.CleanupFailure != "" || result.CollectionFailure != "" {
				t.Fatal("publication cancellation misclassified")
			}
			if _, err := h.cli.Destroy(context.Background(), prepared.Instance.ID); err != nil {
				t.Fatal(err)
			}
			h.absent()
		})
	}
}

func TestLeaseCollectorOutcomeDoesNotPoisonSweeper(t *testing.T) {
	f := newControlledExpiry(t)
	f.allowError = true // explicit shutdown still reports the retained per-instance gap.
	owner := f.owner(t, true, time.Millisecond)
	result := &v1.ExecutionResult{Attempt: newID(), CollectionFailure: "application output collection failed", CompletedAt: time.Now().UTC().Format(time.RFC3339Nano), Cancelled: true}
	var cancelled sync.Once
	owner.entry.cancel = func() {
		cancelled.Do(func() {
			if _, err := f.s.store.execution(owner.id, v1.Failed, result); err != nil {
				panic(err)
			}
			close(owner.done)
		})
	}
	if err := f.s.expireOwners(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		f.s.mu.Lock()
		joined := len(f.s.expiryWorkers) == 0
		f.s.mu.Unlock()
		if joined {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if err := f.s.expireOwners(); err != nil {
		t.Fatal("persisted collector failure became fatal", err)
	}
	got, err := f.s.store.inspect(owner.id)
	if err != nil || got.Execution.CollectionFailure != result.CollectionFailure {
		t.Fatal("collector outcome lost", err)
	}
}

func TestKeptGuardianLossReportsEvidenceGap(t *testing.T) {
	binary := reviewFixBinary(t)
	h := newNativeFaultHarness(t, binary)
	job := h.manifest.Components["job"]
	job.Command.Args[len(job.Command.Args)-1] = "job-fail"
	h.manifest.Components["job"] = job
	h.write()
	request := h.request("test")
	request.Options.KeepOnFailure = true
	r, err := h.cli.Run(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	final := h.state(r.Instance.ID, v1.Failed).Instance.Execution
	if !final.Kept || final.TerminalExitCode == nil || *final.TerminalExitCode != 17 {
		t.Fatal("kept job result missing")
	}
	identity := h.guardianCapability(r.Instance.ID)
	h.retainFixtureCleanup()
	if err := native.CrashForTest(identity); err != nil {
		t.Fatal(err)
	}
	if _, err := h.cli.Destroy(context.Background(), r.Instance.ID); err == nil {
		t.Fatal("uncertain native cleanup succeeded")
	}
	got, err := h.cli.Inspect(context.Background(), r.Instance.ID)
	if err != nil {
		t.Fatal(err)
	}
	result := got.Instance.Execution
	if result.CollectionFailure == "" || result.CleanupFailure == "" || result.TerminalExitCode == nil || *result.TerminalExitCode != 17 {
		t.Fatal("kept collection gap or original job result lost")
	}
	logs, err := h.cli.Logs(context.Background(), v1.LogsRequest{APIVersion: v1.Version, InstanceID: r.Instance.ID})
	if err != nil || logs.Gap == "" {
		t.Fatal("kept historical gap omitted", err)
	}
}
