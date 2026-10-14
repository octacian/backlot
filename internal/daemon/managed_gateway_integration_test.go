//go:build darwin || linux

package daemon

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"github.com/octacian/backlot/internal/native"
	"io"
	"math/big"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	v1 "github.com/octacian/backlot/api/v1"
)

func managedFixturePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	return port
}

// TestManagedHTTPSRuntime uses real CLI/daemon/guardian/Caddy/native services,
// normal host DNS and isolated native-client trust. It never installs trust or
// changes resolver/dialing behavior and does not claim browser adoption evidence.
func TestManagedHTTPSRuntime(t *testing.T) {
	caddy := os.Getenv("BACKLOT_CADDY_TEST_BINARY")
	if caddy == "" {
		t.Skip("set BACKLOT_CADDY_TEST_BINARY to an explicit stock native executable")
	}
	buildDir := t.TempDir()
	binary, app := filepath.Join(buildDir, "backlot"), filepath.Join(buildDir, "fixture")
	for _, build := range []struct{ path, source string }{{binary, "../../cmd/backlot"}, {app, "../gateway/testdata/fixture"}} {
		if output, err := exec.Command("go", "build", "-race", "-o", build.path, build.source).CombinedOutput(); err != nil {
			t.Fatal(err, string(output))
		}
	}
	trust := filepath.Join(buildDir, "trust.pem")
	t.Setenv("SSL_CERT_FILE", trust)
	t.Setenv("SSL_CERT_DIR", buildDir)
	h := newNativeHarness(t, binary, "30s")
	configPath := filepath.Join(filepath.Dir(h.dir), "machine.json")
	port, admin := managedFixturePort(t), managedFixturePort(t)
	config := v1.MachineConfig{Version: v1.ManifestVersion, Caddy: &v1.CaddyConfig{Mode: "managed", Executable: caddy, Endpoint: "http://127.0.0.1:" + strconv.Itoa(admin), HTTPSPort: &port}}
	data, _ := json.Marshal(config)
	if err := os.WriteFile(configPath, data, 0600); err != nil {
		t.Fatal(err)
	}
	command := func(wantSuccess bool, args ...string) []byte {
		t.Helper()
		args = append(args, "--state-dir", h.dir, "--json")
		output, err := exec.Command(binary, args...).CombinedOutput()
		if (err == nil) != wantSuccess {
			logs, _ := os.ReadFile(filepath.Join(h.dir, "gateway", "gateway.log"))
			t.Fatalf("CLI %v: %v %s\nGateway logs: %s", args, err, output, logs)
		}
		return output
	}
	// Register authenticated cleanup/reconciliation before creating or faulting
	// the gateway. Preserve private residuals if any proof fails.
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
			t.Error("managed fixture cleanup uncertain", h.dir, err)
		}
	})
	if !strings.Contains(string(command(false, "gateway", "start")), "gateway_config") {
		t.Fatal("missing managed config diagnostic")
	}
	blocker, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(port))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = blocker.Close() })
	if !strings.Contains(string(command(false, "gateway", "start", "--config", configPath)), "gateway_port_conflict") {
		t.Fatal("missing conflict diagnostic")
	}
	if err := blocker.Close(); err != nil {
		t.Fatal(err)
	}
	command(true, "gateway", "start", "--config", configPath)
	root, _, storage := managedPaths(h.dir)
	ca, err := os.ReadFile(filepath.Join(storage, "pki", "authorities", "local", "root.crt"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(trust, ca, 0600); err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(ca) {
		t.Fatal("invalid generated CA")
	}
	portRef := func(name string) v1.Value { return fixtureRef("resource", name, "port") }
	originRef := fixtureRef("resource", "origin", "url")
	component := func(name string) v1.Component {
		return v1.Component{Kind: v1.Service, Runtime: v1.Native, Command: &v1.Command{Tool: "fixture"}, Resources: []string{name, "origin"}, Ports: map[string]v1.ServicePort{"http": {Resource: name}}, Environment: v1.Environment{Assign: map[string]v1.Value{"PORT": portRef(name), "BIND": fixtureLiteral("127.0.0.1"), "ROLE": fixtureLiteral(name), "CERT": fixtureLiteral(trust), "ORIGIN": originRef}}, Readiness: &v1.Probe{Kind: "tcp", Target: ptrValue(portRef(name)), Timeout: "10s"}}
	}
	h.manifest = v1.Manifest{Version: v1.ManifestVersion, Project: "managed-fixture", Tools: map[string]string{"fixture": app}, Resources: map[string]v1.Resource{"api": {Kind: "port"}, "web": {Kind: "port"}, "origin": {Kind: "origin"}, "data": {Kind: "directory"}}, Components: map[string]v1.Component{"api": component("api"), "web": component("web")}, Scenes: map[string]v1.Scene{"dev": {Lifetime: v1.Persistent, Components: []string{"api", "web"}, Resources: []string{"api", "web", "origin", "data"}, Publish: &v1.Publication{Resource: "origin", Routes: []v1.Route{{Path: "/api", Service: "api", Port: "http", Prefix: "strip"}, {Path: "/", Service: "web", Port: "http", Prefix: "preserve"}}, Probe: v1.Probe{Kind: "http", Target: &originRef, Timeout: "10s"}}}}}
	web := h.manifest.Components["web"]
	web.Resources = append(web.Resources, "data")
	web.Fixtures = map[string]v1.Fixture{"data": {Value: fixtureRef("resource", "data", "path")}}
	h.manifest.Components["web"] = web
	h.write()
	git := func(args ...string) {
		t.Helper()
		output, err := exec.Command("git", append([]string{"-C", h.project}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatal(err, string(output))
		}
	}
	git("init", "--quiet")
	git("add", "backlot.json")
	git("-c", "user.name=Backlot Fixture", "-c", "user.email=fixture@example.invalid", "commit", "--quiet", "-m", "fixture")
	secondProject := filepath.Join(filepath.Dir(h.dir), "second-worktree")
	git("worktree", "add", "--quiet", "--detach", secondProject, "HEAD")
	run := func() v1.InstanceResponse {
		t.Helper()
		var response v1.InstanceResponse
		output := command(true, "run", "--project", h.project, "--config", configPath, "--startup-timeout", "20s", "dev")
		if err := json.Unmarshal(output, &response); err != nil {
			t.Fatal(err, string(output))
		}
		return response
	}
	first := run()
	id, origin := first.Instance.ID, first.Instance.Execution.Origin
	if first.Instance.Status != v1.RuntimeReady || !strings.HasSuffix(origin, ".backlot.localhost:"+strconv.Itoa(port)) {
		t.Fatal(first)
	}
	if len(first.Instance.Execution.Fixtures) != 1 {
		t.Fatal("retained data fixture missing")
	}
	dataPath := filepath.Join(first.Instance.Execution.Fixtures[0].Value, "retained-marker")
	if err := os.WriteFile(dataPath, []byte("preserve"), 0600); err != nil {
		t.Fatal(err)
	}
	var other v1.InstanceResponse
	otherOutput := command(true, "run", "--project", secondProject, "--config", configPath, "dev")
	if err := json.Unmarshal(otherOutput, &other); err != nil {
		t.Fatal(err)
	}
	if other.Instance.ID == id || other.Instance.Execution.Origin == origin || other.Instance.Checkout.ID == first.Instance.Checkout.ID {
		t.Fatal("two actual worktrees share identity/origin")
	}
	command(true, "stop", other.Instance.ID)
	transport := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport}
	response, err := client.Get(origin + "/ssr")
	if err != nil {
		t.Fatal("normal hostname resolution/TLS failed", err)
	}
	body, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil || string(body) != "ssr:api:/value" {
		t.Fatal("frontend/API/SSR", string(body), err)
	}
	wrongRoots := x509.NewCertPool()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	wrongCA := &x509.Certificate{SerialNumber: big.NewInt(1), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour)}
	der, err := x509.CreateCertificate(rand.Reader, wrongCA, wrongCA, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	wrongRoots.AddCert(cert)
	for _, roots := range []*x509.CertPool{x509.NewCertPool(), wrongRoots} {
		tr := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}}
		response, err := (&http.Client{Transport: tr}).Get(origin)
		if err == nil {
			_ = response.Body.Close()
			t.Fatal("missing/wrong trust accepted managed CA")
		}
		var authority x509.UnknownAuthorityError
		if !errors.As(err, &authority) {
			t.Fatal("untrusted request failed for a non-trust cause", err)
		}
		tr.CloseIdleConnections()
	}

	check := exec.Command(app, "client")
	check.Env = append(os.Environ(), "CERT="+trust, "ORIGIN="+origin)
	if output, err := check.CombinedOutput(); err != nil {
		t.Fatal("TLS paths/variable WebSocket/redirect/cookie", err, string(output))
	}
	for _, action := range []string{"stop", "restart"} {
		if !strings.Contains(string(command(false, "gateway", action)), "gateway_busy") {
			t.Fatal("active scene gateway fence", action)
		}
	}
	command(true, "stop", id)
	command(true, "gateway", "restart")
	if hash, err := validateCA(storage, ""); err != nil || hash == "" {
		t.Fatal(err)
	}
	second := run()
	if retained, err := os.ReadFile(dataPath); err != nil || string(retained) != "preserve" {
		t.Fatal("retained scene data lost", err)
	}
	if second.Instance.ID != id || second.Instance.Execution.Origin != origin {
		t.Fatal("origin changed across stop/start")
	}
	// A retained direct child handle is fixture crash authority; recovery must
	// stop surviving authenticated groups and gateway without resuming workloads.
	if err := h.process.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = h.process.Wait()
	h.waited = true
	h.start()
	interrupted := h.state(id, v1.Interrupted)
	if interrupted.Instance.Execution.CleanupFailure != "" {
		t.Fatal(interrupted.Instance.Execution)
	}
	if !strings.Contains(string(command(true, "gateway", "status")), "interrupted") {
		t.Fatal("gateway auto-resumed")
	}
	if !strings.Contains(string(command(false, "run", "--project", h.project, "--config", configPath, "dev")), "gateway_unavailable") {
		t.Fatal("unavailable gateway launch accepted")
	}
	command(true, "gateway", "start")
	command(true, "restart", "--project", h.project, "--config", configPath, id)
	command(true, "stop", id)
	command(true, "gateway", "stop")
	after, err := os.ReadFile(filepath.Join(storage, "pki", "authorities", "local", "root.crt"))
	if err != nil || string(after) != string(ca) {
		t.Fatal("CA identity changed", err)
	}
	// Unavailable-Caddy crash recovery retains exact route intent and leaves
	// daemon administration usable. Only authenticated guardian authority stops it.
	command(true, "gateway", "start")
	command(true, "restart", "--project", h.project, "--config", configPath, id)
	if err := h.process.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = h.process.Wait()
	h.waited = true
	state, err := openStore(h.dir)
	if err != nil {
		t.Fatal(err)
	}
	managed, err := state.managed()
	if err != nil {
		t.Fatal(err)
	}
	if managed.Identity == nil {
		t.Fatal("gateway authenticated authority missing")
	}
	if err := native.Stop(*managed.Identity, 0); err != nil {
		t.Fatal(err)
	}
	if err := state.db.Close(); err != nil {
		t.Fatal(err)
	}
	h.start()
	unavailable := h.state(id, v1.Interrupted)
	if unavailable.Instance.Execution.CleanupFailure == "" {
		t.Fatal("unavailable gateway cleanup uncertainty discarded")
	}
	command(true, "gateway", "start")
	command(true, "restart", "--project", h.project, "--config", configPath, id)
	repaired, err := h.cli.Inspect(context.Background(), id)
	if err != nil || repaired.Instance.Execution.CleanupFailure != "" || repaired.Instance.Execution.Origin != origin {
		t.Fatal("exact route cleanup retry failed", err, repaired)
	}
	command(true, "stop", id)
	command(true, "gateway", "stop")

	// Production aggregate readiness also rejects wrong trust. Changing the
	// fixture daemon's explicit trust file needs a new process, because Go caches
	// root pools; this is test setup, never a product trust-injection mechanism.
	h.close()
	if err := os.WriteFile(trust, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0600); err != nil {
		t.Fatal(err)
	}
	h.start()
	command(true, "gateway", "start")
	failedOutput := command(false, "restart", "--project", h.project, "--config", configPath, id)
	var failed v1.InstanceResponse
	if err := json.Unmarshal(failedOutput, &failed); err != nil {
		t.Fatal(err)
	}
	if failed.Instance.Execution.FailureDetail == nil || failed.Instance.Execution.FailureDetail.Code != "publication_unreachable" || failed.Instance.Execution.CleanupFailure != "" {
		t.Fatal("wrong trust produced false readiness or hid cleanup", failed)
	}
	h.close()
	if err := os.WriteFile(trust, ca, 0600); err != nil {
		t.Fatal(err)
	}
	h.start()
	command(true, "gateway", "start")
	command(true, "restart", "--project", h.project, "--config", configPath, id)
	command(true, "stop", id)
	command(true, "gateway", "stop")

	// Missing saved state must not resurrect stale bootstrap over retained routes.
	_, saved, _ := managedPaths(h.dir)
	if err := os.Rename(saved, saved+".fixture-held"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(command(false, "gateway", "start")), "gateway_state") {
		t.Fatal("missing saved state bootstrap fallback")
	}
	if err := os.Rename(saved+".fixture-held", saved); err != nil {
		t.Fatal(err)
	}
	keyPath := filepath.Join(storage, "pki", "authorities", "local", "root.key")
	if err := os.Rename(keyPath, keyPath+".fixture-held"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(command(false, "gateway", "start")), "gateway_ca") {
		t.Fatal("missing CA silently recreated")
	}
	if err := os.Rename(keyPath+".fixture-held", keyPath); err != nil {
		t.Fatal(err)
	}
	command(true, "gateway", "start")
	command(true, "gateway", "stop")
	t.Log("real managed CLI/native HTTPS, normal host DNS, private trust, API/SSR/WebSocket/redirect/cookie, conflict/busy/unavailable gates, stop/start, daemon crash, CA/saved-state persistence verified; browser trust remains a separate gate", root)
}
