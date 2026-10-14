//go:build darwin && arm64

package acceptance

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"io"
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

// TestMacOSQuickstart runs the shipped Node application in two real Git
// worktrees, with normal DNS and explicit private TLS roots. It does not install
// trust and cannot replace the separate operator/browser adoption checkpoint.
func TestMacOSQuickstart(t *testing.T) {
	caddy, node, npm := os.Getenv("BACKLOT_CADDY_TEST_BINARY"), os.Getenv("BACKLOT_NODE_TEST_BINARY"), os.Getenv("BACKLOT_NPM_TEST_BINARY")
	if caddy == "" || node == "" || npm == "" {
		t.Skip("set BACKLOT_CADDY_TEST_BINARY, BACKLOT_NODE_TEST_BINARY and BACKLOT_NPM_TEST_BINARY")
	}
	for _, tool := range []string{caddy, node, npm} {
		if !filepath.IsAbs(tool) {
			t.Fatal("fixture tools must be explicit absolute paths")
		}
	}
	root, err := newAcceptanceRoot("bl-quick-")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("private quickstart state/evidence: %s", root)
	h := &harness{t: t, root: root, binary: filepath.Join(root, "backlot"), state: filepath.Join(root, "d"), config: filepath.Join(root, "machine.json"), fixtureRepo: filepath.Join(root, "repo")}
	// Register cleanup before daemon/gateway creation or fault injection. All
	// instance actuation goes through the authenticated daemon/guardian protocol.
	t.Cleanup(func() {
		if h.daemon != nil {
			h.cleanup()
		} else if h.daemonLog != nil {
			h.releaseLog()
		}
	})
	run := func(dir, tool string, args ...string) []byte {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		cmd := exec.CommandContext(ctx, tool, args...)
		cmd.Dir = dir
		output, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("fixture command %s: %v %s", filepath.Base(tool), err, output)
		}
		return output
	}
	run("../..", "go", "build", "-race", "-o", h.binary, "./cmd/backlot")
	if err := os.Mkdir(h.fixtureRepo, 0700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"backlot.yaml", "app.mjs", "test.mjs", "package.json", "package-lock.json", ".gitignore"} {
		data, err := os.ReadFile(filepath.Join("../../examples/macos-quickstart", name))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(h.fixtureRepo, name), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	run(h.fixtureRepo, "git", "init", "--quiet")
	run(h.fixtureRepo, "git", "add", ".")
	run(h.fixtureRepo, "git", "-c", "user.name=Backlot Fixture", "-c", "user.email=fixture@example.invalid", "commit", "--quiet", "-m", "fixture")
	for _, name := range []string{"one", "two"} {
		path := filepath.Join(root, name)
		run(h.fixtureRepo, "git", "worktree", "add", "--quiet", "--detach", path, "HEAD")
		h.worktrees = append(h.worktrees, path)
		run(path, npm, "ci", "--ignore-scripts", "--no-audit", "--no-fund")
	}
	h.project = h.worktrees[0]
	// Hold the HTTPS port to prove conflict handling preserves its real owner.
	blocker, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = blocker.Close() })
	httpsPort := blocker.Addr().(*net.TCPAddr).Port
	admin, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	adminPort := admin.Addr().(*net.TCPAddr).Port
	if err := admin.Close(); err != nil {
		t.Fatal(err)
	}
	config := v1.MachineConfig{Version: v1.ManifestVersion, Tools: map[string]string{"node": node}, Caddy: &v1.CaddyConfig{Mode: "managed", Executable: caddy, Endpoint: "http://127.0.0.1:" + strconv.Itoa(adminPort), HTTPSPort: &httpsPort}}
	data, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(h.config, data, 0600); err != nil {
		t.Fatal(err)
	}
	caPath := filepath.Join(h.state, "gateway/data/pki/authorities/local/root.crt")
	// This is fixture-only process trust, never operator system/browser trust.
	// Go 1.27 on Darwin supports these explicit root inputs.
	t.Setenv("SSL_CERT_FILE", caPath)
	t.Setenv("SSL_CERT_DIR", root)
	h.daemonLog, err = os.OpenFile(filepath.Join(root, "daemon.log"), os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	h.start()
	gateway := func(ok bool, action string, extra ...string) []byte {
		t.Helper()
		args := append([]string{"gateway", action, "--state-dir", h.state, "--json"}, extra...)
		output, err := h.command(args...)
		if (err == nil) != ok {
			t.Fatalf("gateway %s expected success=%t: %v", action, ok, err)
		}
		return output
	}
	if !strings.Contains(string(gateway(false, "start", "--config", h.config)), "gateway_port_conflict") {
		t.Fatal("missing occupied-port diagnostic")
	}
	if connection, err := net.DialTimeout("tcp", blocker.Addr().String(), time.Second); err != nil {
		t.Fatal("unrelated occupied listener lost")
	} else {
		_ = connection.Close()
	}
	if err := blocker.Close(); err != nil {
		t.Fatal(err)
	}
	// A new unrelated listener remains alive throughout all scene/gateway cleanup.
	unrelated, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = unrelated.Close() })
	gateway(true, "start", "--config", h.config)
	ca, err := os.ReadFile(caPath)
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(ca) {
		t.Fatal("invalid CA")
	}
	transport := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 10 * time.Second}
	page := func(origin string) {
		t.Helper()
		response, err := client.Get(origin)
		if err != nil {
			t.Fatal("normal DNS/verified HTTPS", err)
		}
		body, err := io.ReadAll(response.Body)
		_ = response.Body.Close()
		if err != nil || response.StatusCode != 200 || !strings.Contains(string(body), "Native SSR through the canonical origin: hello from API") {
			t.Fatal("frontend/SSR failed", response.StatusCode, err)
		}
	}
	first := h.require("run", "dev")
	h.project = h.worktrees[1]
	second := h.require("run", "dev")
	if first.ID == second.ID || first.Execution.Origin == second.Execution.Origin || first.Checkout.ID == second.Checkout.ID {
		t.Fatal("worktree identity/origin collision")
	}
	for _, a := range first.Execution.Ports {
		for _, b := range second.Execution.Ports {
			if a == b {
				t.Fatal("worktrees share service port")
			}
		}
	}
	page(first.Execution.Origin)
	page(second.Execution.Origin)
	// Normal Node HTTPS without the manifest trust assignment must reject this CA.
	missing := exec.Command(node, "--input-type=module", "-e", `import https from 'node:https'; https.get(process.argv[1], () => process.exitCode=2).on('error',e=>{ console.error(e.code); process.exitCode=1; });`, first.Execution.Origin)
	missing.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + root}
	output, err := missing.CombinedOutput()
	if err == nil || !strings.Contains(string(output), "UNABLE_TO_GET_ISSUER_CERT_LOCALLY") {
		t.Fatal("missing Node trust did not fail with authority diagnostic", err, string(output))
	}
	gateway(false, "restart")
	h.project = h.worktrees[0]
	for _, fail := range []bool{false, true} {
		args := []string{}
		if fail {
			args = []string{"--", "--fail"}
		}
		result, err := h.instance("run", "test", args...)
		if (err == nil) == fail || result.Execution == nil || result.Execution.TerminalExitCode == nil {
			t.Fatal("disposable result", err)
		}
		want := 0
		if fail {
			want = 1
		}
		if *result.Execution.TerminalExitCode != want || result.Execution.CollectionFailure != "" || result.Execution.CleanupFailure != "" || len(result.Execution.Artifacts) != 1 {
			t.Fatal("test/evidence outcome mismatch")
		}
		report, err := os.ReadFile(filepath.Join(result.Execution.Artifacts[0].Path, "result.json"))
		if err != nil {
			t.Fatal(err)
		}
		var value struct {
			Passed bool     `json:"passed"`
			Checks []string `json:"checks"`
		}
		if json.Unmarshal(report, &value) != nil || value.Passed == fail || len(value.Checks) != 4 {
			t.Fatal("retained report incomplete")
		}
		h.require("destroy", result.ID)
		if retained, err := os.ReadFile(filepath.Join(result.Execution.Artifacts[0].Path, "result.json")); err != nil || !bytes.Equal(report, retained) {
			t.Fatal("destroy removed evidence")
		}
	}
	h.require("stop", first.ID)
	page(second.Execution.Origin)
	resumed := h.require("run", "dev")
	if resumed.ID != first.ID || resumed.Execution.Origin != first.Execution.Origin {
		t.Fatal("scene stop/start lost origin")
	}
	h.require("stop", first.ID)
	h.require("stop", second.ID)
	gateway(true, "restart")
	after, err := os.ReadFile(caPath)
	if err != nil || !bytes.Equal(ca, after) {
		t.Fatal("gateway restart changed CA")
	}
	restarted := h.require("restart", first.ID)
	if restarted.Execution.Origin != first.Execution.Origin {
		t.Fatal("restart changed origin")
	}
	page(restarted.Execution.Origin)
	h.require("stop", first.ID)
	gateway(true, "stop")
	unavailable, err := h.command("run", "dev", "--project", h.project, "--config", h.config, "--state-dir", h.state, "--json")
	var failure v1.ErrorResponse
	if err == nil || json.Unmarshal(unavailable, &failure) != nil || failure.Error.Code != "gateway_unavailable" {
		t.Fatal("unavailable gateway did not return the pre-allocation error envelope")
	}
	gateway(true, "start")
	recovered := h.require("restart", first.ID)
	page(recovered.Execution.Origin)
	h.require("destroy", first.ID)
	h.require("destroy", second.ID)
	gateway(true, "stop")
	if connection, err := net.DialTimeout("tcp", unrelated.Addr().String(), time.Second); err != nil {
		t.Fatal("unrelated listener lost during cleanup")
	} else {
		_ = connection.Close()
	}
	t.Log("two Git worktrees, native SSR/API/variable WebSockets/redirects/cookies, exact disposable outcomes and retained evidence, missing trust, conflict/unavailable recovery, stable CA/origins and scoped cleanup passed")
}
