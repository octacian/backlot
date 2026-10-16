//go:build darwin || linux

package daemon

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/containerd/errdefs"
	"github.com/moby/moby/api/types/mount"
	"github.com/moby/moby/client"
	v1 "github.com/octacian/backlot/api/v1"
	"github.com/octacian/backlot/internal/gateway/gatewaytest"
)

// TestContainerManagedHTTPSRuntime exercises explicit public-root inputs and
// Engine host-gateway mappings through the real CLI/daemon/Docker path.
func TestContainerManagedHTTPSRuntime(t *testing.T) {
	testContainerManagedHTTPSRuntime(t, false)
}

// TestLinuxHTTPS is the early real CLI/native/Caddy/bridge trust and cleanup gate.
func TestLinuxHTTPS(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux runtime acceptance")
	}
	testContainerManagedHTTPSRuntime(t, true)
}

func testContainerManagedHTTPSRuntime(t *testing.T, smoke bool) {
	runBrowser := false
	if smoke {
		var err error
		runBrowser, err = managedBrowserOptIn(os.Getenv("BACKLOT_BROWSER_TEST_BINARY"), os.Getenv("BACKLOT_CERTUTIL_TEST_BINARY"))
		if err != nil {
			t.Fatal(err)
		}
		if !runBrowser {
			t.Log("browser opt-ins absent; browser trust evidence is incomplete")
		}
	}
	caddy, endpoint := os.Getenv("BACKLOT_CADDY_TEST_BINARY"), os.Getenv("BACKLOT_DOCKER_TEST_ENDPOINT")
	if caddy == "" || endpoint == "" {
		t.Skip("set BACKLOT_CADDY_TEST_BINARY and BACKLOT_DOCKER_TEST_ENDPOINT")
	}
	// Keep a private rejecting socket alive through all harness cleanup. Any
	// daemon Docker contact for the native-only peer is observable, even if it
	// fails before issuing an HTTP request.
	trapDir, err := os.MkdirTemp("/tmp", "backlot-docker-trap-")
	if err != nil {
		t.Fatal(err)
	}
	trapPath := filepath.Join(trapDir, "engine.sock")
	trap, err := net.Listen("unix", trapPath)
	if err != nil {
		_ = os.RemoveAll(trapDir)
		t.Fatal(err)
	}
	var contacts atomic.Int64
	trapDone := make(chan struct{})
	go func() {
		defer close(trapDone)
		for {
			conn, acceptErr := trap.Accept()
			if acceptErr != nil {
				return
			}
			contacts.Add(1)
			_ = conn.Close()
		}
	}()
	t.Cleanup(func() {
		if closeErr := trap.Close(); closeErr != nil {
			t.Error(closeErr)
		}
		<-trapDone
		if removeErr := os.RemoveAll(trapDir); removeErr != nil {
			t.Error("remove owned Docker trap", trapDir, removeErr)
		}
		if count := contacts.Load(); count != 0 {
			t.Errorf("native-only managed peer contacted Docker %d times", count)
		}
	})
	buildDir := t.TempDir()
	binary, app := filepath.Join(buildDir, "backlot"), filepath.Join(buildDir, "fixture")
	builds := []struct{ path, source string }{{app, "../../examples/container-https"}}
	if installed := os.Getenv("BACKLOT_INSTALLED_TEST_BINARY"); smoke && installed != "" {
		if !filepath.IsAbs(installed) {
			t.Fatal("installed Linux executable must be absolute")
		}
		binary = installed
		t.Log("canonical HTTPS gate uses explicitly installed CLI/daemon", binary)
	} else {
		builds = append(builds, struct{ path, source string }{binary, "../../cmd/backlot"})
	}
	for _, build := range builds {
		if output, err := exec.Command("go", "build", "-race", "-o", build.path, build.source).CombinedOutput(); err != nil {
			t.Fatal(err, string(output))
		}
	}
	image := gatewaytest.FixtureImage(t, endpoint, "../../examples/container-https")
	t.Log("pre-pulled Alpine:", gatewaytest.DockerCommand(t, endpoint, "image", "inspect", "--format", "{{.Id}} {{.Architecture}}", "alpine:3.21"))
	trust, emptyRoots := filepath.Join(buildDir, "trust.pem"), filepath.Join(buildDir, "empty-roots")
	if err := os.Mkdir(emptyRoots, 0700); err != nil {
		t.Fatal(err)
	}
	// Trust is scoped only to this fixture daemon and explicit fixture clients.
	t.Setenv("SSL_CERT_FILE", trust)
	t.Setenv("SSL_CERT_DIR", emptyRoots)
	h := newDockerHarness(t, binary, endpoint)
	// Register managed recovery before any gateway allocation or later faults.
	// The ordinary Docker harness then reconciles generations and engine handles.
	t.Cleanup(func() {
		h.close()
		state, err := openStore(h.dir)
		if err == nil {
			err = errors.Join(state.recoverRuntime(), state.recoverManaged(), state.cleanupFailures())
			record, readErr := state.managed()
			err = errors.Join(err, readErr)
			if record.Pending || record.Identity != nil || record.Status == "uncertain" {
				err = errors.Join(err, errors.New("managed guardian cleanup remains uncertain"))
			}
			err = errors.Join(err, state.db.Close())
		}
		if err != nil {
			h.cleanupUnverified = true
			t.Error("container trust cleanup uncertain; preserving", h.dir, err)
		}
	})
	port, admin := managedFixturePort(t), managedFixturePort(t)
	for admin == port {
		admin = managedFixturePort(t)
	}
	config := v1.MachineConfig{Version: v1.ManifestVersion, Docker: &v1.DockerConfig{Endpoint: "unix://" + trapPath}, Caddy: &v1.CaddyConfig{Mode: "managed", Executable: caddy, Endpoint: "http://127.0.0.1:" + strconv.Itoa(admin), HTTPSPort: &port}}
	// This fixture explicitly selects wider HTTPS reachability on rootful Linux.
	// Production never detects Docker or changes the loopback default.
	if runtime.GOOS == "linux" {
		config.Caddy.HTTPSBindAddress = "0.0.0.0"
	}
	data, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	nativeConfig := append([]byte(nil), data...)
	if err := os.WriteFile(h.config, data, 0600); err != nil {
		t.Fatal(err)
	}
	command := func(args ...string) []byte {
		t.Helper()
		args = append(args, "--state-dir", h.dir, "--json")
		output, commandErr := exec.Command(binary, args...).CombinedOutput()
		if commandErr != nil {
			// Report only named public failure fields, never a lease/capability.
			var envelope v1.ErrorResponse
			var response v1.InstanceResponse
			_ = json.Unmarshal(output, &envelope)
			_ = json.Unmarshal(output, &response)
			if result := response.Instance.Execution; result != nil {
				t.Fatalf("CLI %v: %v; failure=%s cleanup=%s collection=%s", args, commandErr, result.Failure, result.CleanupFailure, result.CollectionFailure)
			}
			t.Fatalf("CLI %v: %v; code=%s message=%s", args, commandErr, envelope.Error.Code, envelope.Error.Message)
		}
		return output
	}
	command("gateway", "start", "--config", h.config)
	_, _, storage := managedPaths(h.dir)
	rootPath := filepath.Join(storage, "pki", "authorities", "local", "root.crt")
	ca, err := os.ReadFile(rootPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(trust, ca, 0600); err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(ca) {
		t.Fatal("invalid managed public root")
	}
	f := gatewaytest.Fixture{Endpoint: config.Caddy.Endpoint, Port: port, Cert: trust, Pool: pool}
	portRef := func(name string) v1.Value { return fixtureRef("resource", name, "port") }
	origin := fixtureRef("resource", "origin", "url")
	cert := fixtureRef("resource", "origin", "ca_cert_path")
	api := v1.Component{Kind: v1.Service, Runtime: v1.Native, Command: &v1.Command{Tool: "fixture"}, Resources: []string{"api"}, Ports: map[string]v1.ServicePort{"http": {Resource: "api"}}, Environment: v1.Environment{Assign: map[string]v1.Value{"PORT": portRef("api"), "BIND": fixtureLiteral("127.0.0.1"), "ROLE": fixtureLiteral("api")}}, Readiness: &v1.Probe{Kind: "tcp", Target: ptrValue(portRef("api")), Timeout: "10s"}}
	front := v1.Component{Kind: v1.Service, Runtime: v1.Native, Command: &v1.Command{Tool: "fixture"}, Resources: []string{"front", "origin"}, Ports: map[string]v1.ServicePort{"http": {Resource: "front"}}, DependsOn: []v1.Dependency{{Component: "api", Condition: v1.Ready}}, Environment: v1.Environment{Assign: map[string]v1.Value{"PORT": portRef("front"), "BIND": fixtureLiteral("127.0.0.1"), "ROLE": fixtureLiteral("front"), "APP_CA_CERT": cert, "ORIGIN": origin}}, Readiness: &v1.Probe{Kind: "tcp", Target: ptrValue(portRef("front")), Timeout: "10s"}}
	input := v1.Component{Kind: v1.Job, Runtime: v1.Container, Policy: v1.EachStart, Image: ptrValue(fixtureLiteral("alpine:3.21")), Resources: []string{"origin"}, Mounts: []v1.Mount{{OriginCACert: "origin", Target: "/app/trust/root.pem", ReadOnly: true}}, DependsOn: []v1.Dependency{{Component: "front", Condition: v1.Ready}}, Environment: v1.Environment{Assign: map[string]v1.Value{"SSL_CERT_FILE": cert, "SSL_CERT_DIR": fixtureLiteral("/empty-roots"), "ORIGIN": origin}}, Args: []string{"sh", "-ec", `test "$SSL_CERT_FILE" = /app/trust/root.pem
test -f "$SSL_CERT_FILE"
grep -q '^-----BEGIN CERTIFICATE-----$' "$SSL_CERT_FILE"
test ! -e /app/trust/root.key
test ! -e /app/trust/intermediate.key
test ! -d /app/trust/pki
awk '$2 == "/app/trust/root.pem" && $4 ~ /(^|,)ro(,|$)/ {found=1} END {exit !found}' /proc/mounts
printf '%s\n' 'public root input and readonly file mount verified'`}}
	publication := &v1.Publication{Resource: "origin", Routes: []v1.Route{{Path: "/", Service: "front", Port: "http", Prefix: "preserve"}, {Path: "/api", Service: "api", Port: "http", Prefix: "strip"}}, Probe: v1.Probe{Kind: "http", Target: &origin, Timeout: "10s"}}
	resources := []string{"api", "front", "origin"}
	h.manifest = v1.Manifest{Version: v1.ManifestVersion, Project: "container-trust-fixture", Tools: map[string]string{"fixture": app}, Resources: map[string]v1.Resource{"api": {Kind: "port"}, "front": {Kind: "port"}, "origin": {Kind: "origin"}}, Components: map[string]v1.Component{"api": api, "front": front, "ca-input": input}, Scenes: map[string]v1.Scene{"dev": {Lifetime: v1.Persistent, Components: []string{"api", "front"}, Resources: resources, Publish: publication}, "mount": {Lifetime: v1.Disposable, Components: []string{"api", "front", "ca-input"}, Resources: resources, TerminalJob: "ca-input", Publish: publication}}}
	unused := input
	unused.HostMappings = []v1.HostMapping{{Origin: "origin", Host: "host-gateway"}}
	h.manifest.Components["unselected-consumer"] = unused
	h.write()
	ready := h.state(h.run("dev").Instance.ID, v1.RuntimeReady)
	peer, peerOrigin := ready.Instance.ID, ready.Instance.Execution.Origin
	if httpsBody(t, f, peerOrigin, "/ssr") != "ssr:api:/value" {
		t.Fatal("native canonical frontend/API/SSR trust failed")
	}
	if contacts.Load() != 0 {
		t.Fatal("native-only startup contacted Docker for an unselected consumer")
	}
	// Only the selected disposable container scene receives the real endpoint.
	// The persistent peer retains its original rejecting endpoint snapshot.
	config.Docker.Endpoint = endpoint
	data, err = json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(h.config, data, 0600); err != nil {
		t.Fatal(err)
	}
	var tested v1.InstanceResponse
	if err := json.Unmarshal(command("run", "mount", "--project", h.project, "--config", h.config), &tested); err != nil {
		t.Fatal(err)
	}
	result := tested.Instance.Execution
	if tested.Instance.Status != v1.Succeeded || result == nil || result.TerminalExitCode == nil || *result.TerminalExitCode != 0 || result.Failure != "" || result.CleanupFailure != "" || result.CollectionFailure != "" {
		t.Fatal("container public-root input did not complete cleanly")
	}
	logs, err := h.cli.Logs(context.Background(), v1.LogsRequest{APIVersion: v1.Version, InstanceID: tested.Instance.ID, Component: "ca-input"})
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(logs)
	if err != nil || !strings.Contains(string(encoded), "public root input and readonly file mount verified") {
		t.Fatal("container input verification log not retained", err)
	}
	after, err := os.ReadFile(rootPath)
	if err != nil || !bytes.Equal(ca, after) {
		t.Fatal("public root changed during container input consumption", err)
	}
	if result.Origin == peerOrigin || httpsBody(t, f, peerOrigin, "/ssr") != "ssr:api:/value" {
		t.Fatal("disposable input cleanup disturbed the persistent native peer")
	}
	if _, err := h.cli.Destroy(context.Background(), tested.Instance.ID); err != nil {
		t.Fatal(err)
	}
	// Prove ordinary container DNS and verified TLS before expanding lifecycle
	// coverage. The image's Go client uses SystemCertPool and the explicit file
	// mapping; CERT/custom dialing are deliberately absent.
	consumer := input
	consumer.Image = ptrValue(fixtureLiteral(image))
	consumer.Args = []string{"client"}
	consumer.HostMappings = []v1.HostMapping{{Origin: "origin", Host: "host-gateway"}}
	for _, trustCase := range []string{"correct", "missing", "wrong"} {
		consumer.Environment.Assign = map[string]v1.Value{"SSL_CERT_FILE": cert, "SSL_CERT_DIR": fixtureLiteral("/empty-roots"), "ORIGIN": origin}
		switch trustCase {
		case "missing":
			consumer.Environment.Assign["SSL_CERT_FILE"] = fixtureLiteral("/missing.pem")
		case "wrong":
			consumer.Environment.Assign["SSL_CERT_FILE"] = fixtureLiteral("/etc/ssl/cert.pem")
		}
		h.manifest.Components["client"] = consumer
		h.manifest.Scenes["trust"] = v1.Scene{Lifetime: v1.Disposable, Components: []string{"api", "front", "client"}, Resources: resources, TerminalJob: "client", Publish: publication}
		h.write()
		want := v1.Succeeded
		if trustCase != "correct" {
			want = v1.Failed
		}
		run := h.state(h.run("trust").Instance.ID, want)
		execution := run.Instance.Execution
		if execution == nil || execution.TerminalExitCode == nil || execution.CleanupFailure != "" || execution.CollectionFailure != "" || execution.Failure != "" || (*execution.TerminalExitCode == 0) != (trustCase == "correct") {
			t.Fatal("container TLS trust result/cleanup mismatch", trustCase)
		}
		logs, err := h.cli.Logs(context.Background(), v1.LogsRequest{APIVersion: v1.Version, InstanceID: run.Instance.ID, Component: "client"})
		if err != nil {
			t.Fatal(err)
		}
		encoded, err := json.Marshal(logs)
		marker := "container HTTPS TLS+SSR+API+WebSocket+redirect+cookie verified"
		if trustCase != "correct" {
			marker = "x509: certificate signed by unknown authority"
		}
		if err != nil || !strings.Contains(string(encoded), marker) {
			t.Fatal("missing retained real-client TLS evidence", trustCase, err)
		}
		if httpsBody(t, f, peerOrigin, "/ssr") != "ssr:api:/value" || execution.Origin == peerOrigin {
			t.Fatal("container trust scene disturbed native peer", trustCase)
		}
		if _, err := h.cli.Destroy(context.Background(), run.Instance.ID); err != nil {
			t.Fatal(err)
		}
		t.Log("daemon-integrated canonical container TLS", trustCase, "verified with native frontend/API, variable WebSocket payloads and redirect/cookie checks in success case")
	}
	if smoke {
		if runBrowser {
			testManagedBrowser(t, peerOrigin, rootPath)
		}
		if _, err := h.cli.Destroy(context.Background(), peer); err != nil {
			t.Fatal(err)
		}
		command("gateway", "stop")
		// Resume the validated saved wildcard configuration through the real
		// installed daemon; no bootstrap replay or CA replacement is allowed.
		command("gateway", "start")
		resumedCA, err := os.ReadFile(rootPath)
		if err != nil || !bytes.Equal(ca, resumedCA) {
			t.Fatal("saved listener resume changed CA identity", err)
		}
		command("gateway", "stop")
		t.Log("early real Linux CLI/native/Caddy/bridge trust, saved gateway resume and lifecycle cleanup verified")
		return
	}
	consumer.Environment.Assign = map[string]v1.Value{"SSL_CERT_FILE": cert, "SSL_CERT_DIR": fixtureLiteral("/empty-roots"), "ORIGIN": origin}
	delete(h.manifest.Scenes, "trust")
	consumer.DependsOn = []v1.Dependency{{Component: "front", Condition: v1.Ready}, {Component: "ssr", Condition: v1.Ready}}
	h.manifest.Components["client"] = consumer
	ssr := v1.Component{Kind: v1.Service, Runtime: v1.Container, Image: consumer.Image, Resources: []string{"origin", "ssr", "data"}, HostMappings: consumer.HostMappings, Mounts: append([]v1.Mount{{Resource: "data", Target: "/data"}}, consumer.Mounts...), Ports: map[string]v1.ServicePort{"http": {Resource: "ssr", ContainerPort: 8080}}, DependsOn: []v1.Dependency{{Component: "api", Condition: v1.Ready}}, Environment: v1.Environment{Assign: map[string]v1.Value{"SSL_CERT_FILE": cert, "SSL_CERT_DIR": fixtureLiteral("/empty-roots"), "ORIGIN": origin, "PORT": fixtureLiteral("8080"), "BIND": fixtureLiteral("0.0.0.0"), "BACKLOT_FIXTURE": fixtureLiteral(h.token), "INSTANCE": fixtureRef("instance", "", "id")}}, Readiness: &v1.Probe{Kind: "tcp", Target: ptrValue(portRef("ssr")), Timeout: "10s"}}
	h.manifest.Components["ssr"] = ssr
	h.manifest.Resources["ssr"] = v1.Resource{Kind: "port"}
	h.manifest.Resources["data"] = v1.Resource{Kind: "volume"}
	mixedPublication := *publication
	mixedPublication.Routes = append([]v1.Route{{Path: "/ssr", Service: "ssr", Port: "http", Prefix: "preserve"}}, publication.Routes...)
	mixedResources := append([]string{"ssr", "data"}, resources...)
	h.manifest.Scenes["mixed"] = v1.Scene{Lifetime: v1.Persistent, Components: []string{"api", "front", "ssr"}, Resources: mixedResources, Publish: &mixedPublication}
	h.manifest.Scenes["mixed-test"] = v1.Scene{Lifetime: v1.Disposable, Components: []string{"api", "front", "ssr", "client"}, Resources: mixedResources, TerminalJob: "client", Publish: &mixedPublication}
	h.write()
	mixed := h.state(h.run("mixed").Instance.ID, v1.RuntimeReady)
	id, canonical := mixed.Instance.ID, mixed.Instance.Execution.Origin
	check := func(id, canonical string) ownedResource {
		t.Helper()
		if httpsBody(t, f, canonical, "/ssr") != "ssr:api:/value" || httpsBody(t, f, peerOrigin, "/ssr") != "ssr:api:/value" || canonical == peerOrigin {
			t.Fatal("mixed container SSR or independent native peer lost its canonical origin")
		}
		r := h.container(id, "ssr")
		inspect, err := h.engine.ContainerInspect(context.Background(), r.ID, client.ContainerInspectOptions{})
		if err != nil {
			t.Fatal(err)
		}
		c := inspect.Container
		parsed, err := url.Parse(canonical)
		if err != nil || len(c.HostConfig.ExtraHosts) != 1 {
			t.Fatal("canonical container mapping missing", err)
		}
		mapped, valid := strings.CutPrefix(c.HostConfig.ExtraHosts[0], parsed.Hostname()+":")
		if !valid || mapped == "" {
			t.Fatal("canonical Engine mapping missing", c.HostConfig.ExtraHosts)
		}
		// Inspect can retain the token or show an expanded address depending on
		// the negotiated API. Observe actual container resolution as well.
		if mapped != "host-gateway" {
			if address, err := netip.ParseAddr(mapped); err != nil || address.IsLoopback() || address.IsUnspecified() {
				t.Fatal("invalid inspected Engine host address")
			}
		}
		resolved, code := h.exec(r, []string{"getent", "hosts", parsed.Hostname()})
		fields := strings.Fields(resolved)
		if code != 0 || len(fields) < 2 || fields[1] != parsed.Hostname() {
			t.Fatal("container canonical resolution failed")
		}
		if address, err := netip.ParseAddr(fields[0]); err != nil || address.IsLoopback() || address.IsUnspecified() {
			t.Fatal("Engine did not expand the explicit mapping inside the container")
		}
		if !contains(c.Config.Env, "SSL_CERT_FILE=/app/trust/root.pem") || len(c.Mounts) != 2 {
			t.Fatal("container trust input or exact mount set changed")
		}
		rootFound := false
		canonicalRoot, err := filepath.EvalSymlinks(rootPath)
		if err != nil {
			t.Fatal(err)
		}
		configuredRoot := false
		for _, m := range c.HostConfig.Mounts {
			if m.Target == "/app/trust/root.pem" {
				// Desktop can expose the exact host file with its VM prefix in
				// both configured and effective metadata. Accept only these two
				// exact spellings, never a parent, suffix match or arbitrary file.
				configuredRoot = (m.Source == canonicalRoot || m.Source == "/host_mnt"+canonicalRoot) && m.ReadOnly && m.Type == mount.TypeBind
			}
		}
		for _, m := range c.Mounts {
			if m.Destination == "/app/trust/root.pem" {
				rootFound = !m.RW && m.Type == mount.TypeBind && (m.Source == canonicalRoot || m.Source == "/host_mnt"+canonicalRoot)
			} else if m.Destination != "/data" {
				t.Fatal("unexpected container mount")
			}
		}
		mountedRoot, code := h.exec(r, []string{"cat", "/app/trust/root.pem"})
		if !rootFound || !configuredRoot || code != 0 || mountedRoot != string(ca) {
			for _, m := range c.HostConfig.Mounts {
				if m.Target == "/app/trust/root.pem" {
					t.Log("configured public root", m.Source, m.ReadOnly, "expected host file", rootPath)
				}
			}
			t.Fatal("typed trust mount evidence", "effectiveReadonly", rootFound, "configuredFileIdentity", configuredRoot, "readExit", code, "bytesEqual", mountedRoot == string(ca))
		}
		return r
	}
	r := check(id, canonical)
	if _, code := h.exec(r, []string{"sh", "-ec", "printf retained >/data/marker"}); code != 0 {
		t.Fatal("write owned volume marker")
	}
	// Unsafe mapping drift must fail before stopping the current trusted consumer.
	changed := ssr
	changed.HostMappings = nil
	h.manifest.Components["ssr"] = changed
	h.write()
	request := h.request("mixed")
	request.InstanceID = id
	var drift *v1.PlanError
	if _, err := h.cli.Restart(context.Background(), request); !errors.As(err, &drift) || drift.Code != "unsafe_drift" {
		t.Fatal("unsafe mapping drift accepted", err)
	}
	check(id, canonical)
	h.manifest.Components["ssr"] = ssr
	h.write()
	assertAbsent := func(canonical string) {
		t.Helper()
		transport := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}}
		defer transport.CloseIdleConnections()
		response, err := (&http.Client{Transport: transport, Timeout: 5 * time.Second}).Get(canonical + "/ssr")
		if err != nil {
			t.Fatal("route absence TLS request", err)
		}
		body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
		_ = response.Body.Close()
		// An empty managed subroute falls through to Caddy's empty HTTP 200.
		// Verify both the observed no-upstream response and the exact host's
		// absence from the owned live configuration, rather than assuming 404.
		if err != nil || response.StatusCode != http.StatusOK || len(body) != 0 || response.Header.Get("Backlot-Instance") != "" {
			t.Fatal("owned route still responds after cleanup", response.StatusCode, err)
		}
		admin, err := (&http.Client{Timeout: 5 * time.Second}).Get(f.Endpoint + "/config/")
		if err != nil {
			t.Fatal(err)
		}
		config, err := io.ReadAll(io.LimitReader(admin.Body, 1<<20))
		_ = admin.Body.Close()
		parsed, parseErr := url.Parse(canonical)
		if err != nil || parseErr != nil || admin.StatusCode != http.StatusOK || !json.Valid(config) {
			t.Fatal("owned route absence inspection failed", err, parseErr)
		}
		quotedHost, err := json.Marshal(parsed.Hostname())
		if err != nil || bytes.Contains(config, quotedHost) {
			t.Fatal("exact owned host remains in live gateway configuration", err)
		}
	}
	if stopped, err := h.cli.StopExecution(context.Background(), id); err != nil || stopped.Instance.Status != v1.Stopped || stopped.Instance.Execution.CleanupFailure != "" {
		t.Fatal(err)
	}
	assertAbsent(canonical)
	for _, action := range []string{"start", "restart", "reset"} {
		var response v1.InstanceResponse
		var err error
		switch action {
		case "start":
			response = h.run("mixed")
		case "restart":
			response, err = h.cli.Restart(context.Background(), request)
		case "reset":
			response, err = h.cli.Reset(context.Background(), request)
		}
		if err != nil {
			t.Fatal(action, err)
		}
		current := h.state(response.Instance.ID, v1.RuntimeReady)
		if current.Instance.ID != id || current.Instance.Execution.Origin != canonical {
			t.Fatal("logical origin changed during", action)
		}
		next := check(id, canonical)
		if next.ID == r.ID {
			t.Fatal("lifecycle reused the old consumer instead of creating a fresh mapped container", action)
		}
		script := "test \"$(cat /data/marker)\" = retained"
		if action == "reset" {
			script = "test ! -e /data/marker"
		}
		if _, code := h.exec(next, []string{"sh", "-ec", script}); code != 0 {
			t.Fatal("owned retained volume contract failed", action)
		}
		r = next
	}
	job := h.state(h.run("mixed-test").Instance.ID, v1.Succeeded)
	if job.Instance.Execution.CleanupFailure != "" || job.Instance.Execution.CollectionFailure != "" {
		t.Fatal("mixed disposable consumer cleanup failed")
	}
	waitComponentLog(t, h.nativeHarness, job.Instance.ID, "client", "container HTTPS TLS+SSR+API+WebSocket+redirect+cookie verified")
	assertAbsent(job.Instance.Execution.Origin)
	check(id, canonical)
	if _, err := h.cli.Destroy(context.Background(), job.Instance.ID); err != nil {
		t.Fatal(err)
	}
	// Existing cleanup owners precede this direct-child crash. Recreate the
	// journal windows after Docker create and route apply but before effect flags.
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
		_ = state.db.Close()
		t.Fatal("missing durable gateway recovery intent", err)
	}
	journal.Gateway.Applied = false
	resourcesState, err := state.resources(id)
	if err == nil {
		for i := range resourcesState.Containers {
			if resourcesState.Containers[i].ID == r.ID {
				resourcesState.Containers[i].ID = ""
			}
		}
		err = errors.Join(state.saveRuntime(id, journal), state.saveResources(id, resourcesState))
	}
	err = errors.Join(err, state.db.Close())
	if err != nil {
		t.Fatal(err)
	}
	h.start()
	for _, recoveredID := range []string{id, peer} {
		interrupted := h.state(recoveredID, v1.Interrupted)
		if interrupted.Instance.Execution.CleanupFailure != "" {
			t.Fatal("crash recovery cleanup failed")
		}
	}
	if _, err := h.engine.ContainerInspect(context.Background(), r.ID, client.ContainerInspectOptions{}); !errdefs.IsNotFound(err) {
		t.Fatal("crash recovery retained the owned container intent/effect", err)
	}
	if !strings.Contains(string(command("gateway", "status")), "interrupted") {
		t.Fatal("managed gateway auto-resumed after crash")
	}
	command("gateway", "start", "--config", h.config)
	assertAbsent(canonical)
	assertAbsent(peerOrigin)
	// Recovery retains logical identity and root, then a fresh mapped consumer
	// can start after the gateway is explicitly restarted.
	if err := os.WriteFile(h.config, nativeConfig, 0600); err != nil {
		t.Fatal(err)
	}
	peerRequest := h.request("dev")
	peerRequest.InstanceID = peer
	peerResponse, err := h.cli.Restart(context.Background(), peerRequest)
	if err != nil {
		t.Fatal(err)
	}
	peerReady := h.state(peerResponse.Instance.ID, v1.RuntimeReady)
	if peerReady.Instance.ID != peer || peerReady.Instance.Execution.Origin != peerOrigin {
		t.Fatal("recovered native peer identity changed")
	}
	if err := os.WriteFile(h.config, data, 0600); err != nil {
		t.Fatal(err)
	}
	recoveredResponse, err := h.cli.Restart(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	restarted := h.state(recoveredResponse.Instance.ID, v1.RuntimeReady)
	if restarted.Instance.ID != id || restarted.Instance.Execution.Origin != canonical {
		t.Fatal("recovered mixed identity changed")
	}
	check(id, canonical)
	if _, err := h.cli.Destroy(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	assertAbsent(canonical)
	if httpsBody(t, f, peerOrigin, "/ssr") != "ssr:api:/value" {
		t.Fatal("mixed destroy disturbed native peer")
	}
	fresh := h.state(h.run("mixed").Instance.ID, v1.RuntimeReady)
	if fresh.Instance.ID == id || fresh.Instance.Execution.Origin == canonical {
		t.Fatal("destroy reused logical identity")
	}
	check(fresh.Instance.ID, fresh.Instance.Execution.Origin)
	if _, err := h.cli.Destroy(context.Background(), fresh.Instance.ID); err != nil {
		t.Fatal(err)
	}
	after, err = os.ReadFile(rootPath)
	if err != nil || !bytes.Equal(ca, after) {
		t.Fatal("lifecycle or recovery changed the managed public root", err)
	}
	if _, err := h.cli.Destroy(context.Background(), peer); err != nil {
		t.Fatal(err)
	}
	t.Log("mixed native frontend/API, container SSR/test, actual root/mapping inspection, drift fencing, peer isolation, fresh creates on stop/start/restart/reset, owned data retention/reset, destroy replacement and crash intent/effect recovery verified")
}
