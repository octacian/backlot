//go:build darwin || linux

package daemon

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	v1 "github.com/octacian/backlot/api/v1"
	"github.com/octacian/backlot/internal/gateway/gatewaytest"
)

func httpsHarness(t *testing.T, binary, app, image, endpoint string, f gatewaytest.Fixture, containerGateway bool) *dockerHarness {
	t.Helper()
	// Root trust is scoped to this new fixture daemon process, never installed.
	t.Setenv("SSL_CERT_FILE", f.Cert)
	h := newDockerHarness(t, binary, endpoint)
	trust := filepath.Join(h.project, "trust")
	if err := os.Mkdir(trust, 0700); err != nil {
		t.Fatal(err)
	}
	cert, err := os.ReadFile(f.Cert)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(trust, "cert.pem"), cert, 0600); err != nil {
		t.Fatal(err)
	}
	upstream := "127.0.0.1"
	if containerGateway {
		upstream = "host.docker.internal"
	}
	config := v1.MachineConfig{Version: v1.ManifestVersion, Docker: &v1.DockerConfig{Endpoint: endpoint, HostAddress: "host.docker.internal", PublishAddress: "0.0.0.0"}, Caddy: &v1.CaddyConfig{Endpoint: f.Endpoint, Scope: "backlot", DomainSuffix: "fixture.localhost", HostAddress: upstream, HTTPSPort: &f.Port}}
	data, _ := json.Marshal(config)
	if err := os.WriteFile(h.config, data, 0600); err != nil {
		t.Fatal(err)
	}
	port := func(name string) v1.Value { return fixtureRef("resource", name, "port") }
	origin := fixtureRef("resource", "origin", "url")
	api := v1.Component{Kind: v1.Service, Runtime: v1.Native, Command: &v1.Command{Tool: "fixture"}, Resources: []string{"api"}, Ports: map[string]v1.ServicePort{"http": {Resource: "api"}}, Environment: v1.Environment{Assign: map[string]v1.Value{"PORT": port("api"), "BIND": fixtureLiteral("0.0.0.0"), "ROLE": fixtureLiteral("api")}}, Readiness: &v1.Probe{Kind: "tcp", Target: ptrValue(port("api")), Timeout: "10s"}}
	web := v1.Component{Kind: v1.Service, Runtime: v1.Container, Image: ptrValue(fixtureLiteral(image)), Resources: []string{"web", "origin"}, Outputs: []string{"trust"}, Mounts: []v1.Mount{{Output: "trust", Target: "/trust", ReadOnly: true}}, Ports: map[string]v1.ServicePort{"http": {Resource: "web", ContainerPort: 8080}}, DependsOn: []v1.Dependency{{Component: "api", Condition: v1.Ready}}, Environment: v1.Environment{Assign: map[string]v1.Value{"PORT": fixtureLiteral("8080"), "ROLE": fixtureLiteral("web"), "CERT": fixtureLiteral("/trust/cert.pem"), "ORIGIN": origin, "GATEWAY_ADDRESS": fixtureLiteral("host.docker.internal:" + strconv.Itoa(f.Port))}}, Readiness: &v1.Probe{Kind: "tcp", Target: ptrValue(port("web")), Timeout: "10s"}}
	client := v1.Component{Kind: v1.Job, Runtime: v1.Container, Policy: v1.EachStart, Image: web.Image, Resources: []string{"origin", "api"}, Outputs: web.Outputs, Mounts: web.Mounts, DependsOn: []v1.Dependency{{Component: "web", Condition: v1.Ready}}, Environment: v1.Environment{Assign: map[string]v1.Value{"CLIENT": fixtureLiteral("1"), "CERT": fixtureLiteral("/trust/cert.pem"), "ORIGIN": origin, "GATEWAY_ADDRESS": fixtureLiteral("host.docker.internal:" + strconv.Itoa(f.Port)), "BACKEND_HOST": {Ref: &v1.Reference{Kind: "service", Name: "api", Field: "host"}}, "BACKEND_PORT": {Ref: &v1.Reference{Kind: "service", Name: "api", Field: "port", Port: "http"}}}}}
	publication := &v1.Publication{Resource: "origin", Routes: []v1.Route{{Path: "/", Service: "web", Port: "http", Prefix: "preserve"}, {Path: "/api", Service: "api", Port: "http", Prefix: "strip"}}, Probe: v1.Probe{Kind: "http", Target: &origin, Timeout: "10s"}}
	resources := []string{"api", "web", "origin"}
	h.manifest = v1.Manifest{Version: v1.ManifestVersion, Project: "https-fixture", Tools: map[string]string{"fixture": app}, Resources: map[string]v1.Resource{"api": {Kind: "port"}, "web": {Kind: "port"}, "origin": {Kind: "origin"}}, Outputs: map[string]v1.Output{"trust": {Path: "trust", ConcurrencyGroup: "fixture-trust"}}, Components: map[string]v1.Component{"api": api, "web": web, "client": client}, Scenes: map[string]v1.Scene{"dev": {Lifetime: v1.Persistent, Components: []string{"api", "web"}, Resources: resources, Publish: publication}, "test": {Lifetime: v1.Disposable, Components: []string{"api", "web", "client"}, Resources: resources, TerminalJob: "client", Publish: publication}}}
	h.write()
	return h
}

func httpsBody(t *testing.T, f gatewaytest.Fixture, origin, path string) string {
	t.Helper()
	transport := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: f.Pool, MinVersion: tls.VersionTLS12}}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport}
	response, err := client.Get(origin + path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	body, err := io.ReadAll(response.Body)
	if err != nil || response.StatusCode != 200 {
		t.Fatal(response.StatusCode, string(body), err)
	}
	return string(body)
}

// TestHTTPSRuntime uses the real daemon/guardian/Docker path and normal host DNS.
// .localhost DNS and explicit per-client fixture trust never alter machine trust.
func TestHTTPSRuntime(t *testing.T) {
	endpoint := os.Getenv("BACKLOT_DOCKER_TEST_ENDPOINT")
	if endpoint == "" || os.Getenv("BACKLOT_CADDY_TEST_BINARY") == "" {
		t.Skip("set BACKLOT_DOCKER_TEST_ENDPOINT and BACKLOT_CADDY_TEST_BINARY")
	}
	directory := t.TempDir()
	binary, app := filepath.Join(directory, "backlot"), filepath.Join(directory, "fixture")
	for _, build := range []struct{ path, source string }{{binary, "../../cmd/backlot"}, {app, "../gateway/testdata/fixture"}} {
		command := exec.Command("go", "build", "-race", "-o", build.path, build.source)
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatal(err, string(output))
		}
	}
	image := gatewaytest.FixtureImage(t, endpoint, "../gateway/testdata/fixture")
	for _, topology := range []string{"native-gateway", "container-gateway"} {
		t.Run(topology, func(t *testing.T) {
			var f gatewaytest.Fixture
			if topology == "container-gateway" {
				f = gatewaytest.StartContainer(t, "fixture.localhost")
			} else {
				f = gatewaytest.Start(t, "fixture.localhost")
			}
			h := httpsHarness(t, binary, app, image, endpoint, f, topology == "container-gateway")
			ready := h.state(h.run("dev").Instance.ID, v1.RuntimeReady)
			id, origin := ready.Instance.ID, ready.Instance.Execution.Origin
			if !strings.HasPrefix(origin, "https://bl-") || httpsBody(t, f, origin, "/ssr") != "ssr:api:/value" {
				t.Fatal("canonical origin/SSR failed")
			}
			originalConfig, err := os.ReadFile(h.config)
			if err != nil {
				t.Fatal(err)
			}
			var changed v1.MachineConfig
			if err := json.Unmarshal(originalConfig, &changed); err != nil {
				t.Fatal(err)
			}
			changed.Caddy.DomainSuffix = "different.localhost"
			changedConfig, _ := json.Marshal(changed)
			if err := os.WriteFile(h.config, changedConfig, 0600); err != nil {
				t.Fatal(err)
			}
			reset := h.request("dev")
			reset.InstanceID = id
			if _, err := h.cli.Reset(context.Background(), reset); err == nil {
				t.Fatal("domain drift reset must fail before stopping or deleting data")
			}
			if httpsBody(t, f, origin, "/ssr") != "ssr:api:/value" {
				t.Fatal("domain drift disturbed the retained running instance")
			}
			if err := os.WriteFile(h.config, originalConfig, 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := h.cli.StopExecution(context.Background(), id); err != nil {
				t.Fatal(err)
			}
			if httpsBody(t, f, origin, "/api/value") != "unrelated sentinel" {
				t.Fatal("stop did not remove route")
			}
			for _, operation := range []string{"start", "restart", "reset"} {
				var response v1.InstanceResponse
				if operation == "start" {
					response = h.run("dev")
				} else {
					response = h.restart(id, operation == "reset")
				}
				current := h.state(response.Instance.ID, v1.RuntimeReady)
				if current.Instance.ID != id || current.Instance.Execution.Origin != origin {
					t.Fatal("logical identity/hostname changed on", operation)
				}
			}
			// Register cleanup through h before faulting the retained direct daemon.
			if err := h.process.Process.Kill(); err != nil {
				t.Fatal(err)
			}
			_ = h.process.Wait()
			h.waited = true
			state, err := openStore(h.dir)
			if err != nil {
				t.Fatal(err)
			}
			journal, err := state.runtime(id)
			if err != nil || journal.Gateway == nil {
				t.Fatal("durable gateway effect missing", err)
			}
			// Recreate the narrow crash window: external effect exists, completion
			// flag absent. The durable exact intent must still authorize cleanup.
			journal.Gateway.Applied = false
			if err := state.saveRuntime(id, journal); err != nil {
				t.Fatal(err)
			}
			if err := state.db.Close(); err != nil {
				t.Fatal(err)
			}
			h.start()
			interrupted := h.state(id, v1.Interrupted)
			if interrupted.Instance.Execution.CleanupFailure != "" || interrupted.Instance.Execution.CollectionFailure == "" || httpsBody(t, f, origin, "/api/value") != "unrelated sentinel" {
				t.Fatal("crash recovery did not remove only owned route", interrupted.Instance.Execution)
			}
			if _, err := h.cli.Destroy(context.Background(), id); err != nil {
				t.Fatal(err)
			}
			disposable := h.state(h.run("test").Instance.ID, v1.Succeeded)
			if disposable.Instance.Execution.CleanupFailure != "" || httpsBody(t, f, disposable.Instance.Execution.Origin, "/api/value") != "unrelated sentinel" {
				t.Fatal("disposable terminal job/route cleanup failed", disposable.Instance.Execution)
			}
			for _, mode := range []string{"job-failure", "cancellation"} {
				t.Run("kept-"+mode, func(t *testing.T) {
					original := h.manifest.Components["client"]
					job := original
					job.Image = ptrValue(fixtureLiteral("alpine:3.21"))
					script := "echo kept-artifact >/tmp/report; echo kept-output; exit 17"
					if mode == "cancellation" {
						script = "echo kept-artifact >/tmp/report; echo kept-output; exec sleep 90"
					}
					job.Args = []string{"sh", "-c", script}
					job.Artifacts = map[string]v1.ArtifactSource{"report": {Path: "/tmp/report"}}
					h.manifest.Components["client"] = job
					h.write()
					request := h.request("test")
					request.Options.KeepOnFailure = true
					response, err := h.cli.Run(context.Background(), request)
					if err != nil {
						t.Fatal(err)
					}
					runID := response.Instance.ID
					h.ids = append(h.ids, runID)
					h.tokens[runID] = response.LeaseToken
					if mode == "cancellation" {
						h.waitTerminalRunning(runID)
						waitComponentLog(t, h.nativeHarness, runID, "client", "kept-output")
						if _, err := h.cli.StopExecution(context.Background(), runID); err != nil {
							t.Fatal(err)
						}
					}
					want := v1.Failed
					if mode == "cancellation" {
						want = v1.Cancelled
					}
					kept := h.state(runID, want).Instance.Execution
					if !kept.Kept || kept.CleanupFailure != "" || len(kept.Artifacts) != 1 {
						t.Fatal("kept result", kept)
					}
					if mode == "job-failure" && (kept.TerminalExitCode == nil || *kept.TerminalExitCode != 17 || kept.Failure != "") {
						t.Fatal("original job exit lost", kept)
					}
					if httpsBody(t, f, kept.Origin, "/ssr") != "ssr:api:/value" {
						t.Fatal("healthy mixed dependencies/routes removed")
					}
					if _, err := h.cli.Destroy(context.Background(), runID); err != nil {
						t.Fatal(err)
					}
					if httpsBody(t, f, kept.Origin, "/api/value") != "unrelated sentinel" {
						t.Fatal("explicit destroy retained owned route or disturbed unrelated route")
					}
					logs, err := h.cli.Logs(context.Background(), v1.LogsRequest{APIVersion: v1.Version, InstanceID: runID, Component: "client"})
					if err != nil || len(logs.Records) == 0 {
						t.Fatal("post-destroy output missing", logs, err)
					}
					if _, err := os.Stat(filepath.Join(kept.Artifacts[0].Path, "report")); err != nil {
						t.Fatal("destroy removed retained artifact", err)
					}
					h.manifest.Components["client"] = original
					h.write()
				})
			}
			fresh := h.state(h.run("dev").Instance.ID, v1.RuntimeReady)
			if fresh.Instance.ID == id || fresh.Instance.Execution.Origin == origin {
				t.Fatal("destroy reused logical identity")
			}
			t.Log("real daemon TLS/SSR; stop/start/restart/reset invariant; crash intent-effect recovery; disposable container origin client; destroy replacement verified")
		})
	}
}
