//go:build darwin || linux

package daemon

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"
	v1 "github.com/octacian/backlot/api/v1"
	"github.com/octacian/backlot/internal/client"
)

// TestNativeWatchers exercises a private copy of the shipped normal Air/npm example.
// Preparation installs the pinned tools/dependencies outside the runtime. Browser
// proof uses the same isolated HOME/NSS pattern as the managed Linux browser gate.
func TestNativeWatchers(t *testing.T) {
	project := os.Getenv("BACKLOT_WATCHER_TEST_PROJECT")
	caddy := os.Getenv("BACKLOT_CADDY_TEST_BINARY")
	if project == "" || caddy == "" {
		t.Skip("set private BACKLOT_WATCHER_TEST_PROJECT and explicit Caddy for real watcher acceptance")
	}
	root := os.Getenv("BACKLOT_WATCHER_TEST_ROOT")
	if root == "" {
		t.Fatal("set an exclusive private BACKLOT_WATCHER_TEST_ROOT")
	}
	binary := filepath.Join(root, "backlot")
	build := exec.Command("go", "build", "-race", "-o", binary, "../../cmd/backlot")
	if data, err := build.CombinedOutput(); err != nil {
		t.Fatal(err, string(data))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	state := filepath.Join(root, "state")
	trust := filepath.Join(state, "gateway", "data", "pki", "authorities", "local", "root.crt")
	empty := filepath.Join(root, "empty-certs")
	if err := os.MkdirAll(empty, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SSL_CERT_FILE", trust)
	t.Setenv("SSL_CERT_DIR", empty)
	sentinel := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "unrelated-sentinel") }))
	defer sentinel.Close()
	h := &nativeHarness{t: t, binary: binary, dir: state, project: project, lease: "30s", pids: filepath.Join(root, "unused-pids")}
	h.cli = client.New(SocketPath(state))
	// Register authenticated cleanup before gateway/application allocation. Failed
	// cleanup leaves the private root intact for reconciliation, never deletes it.
	t.Cleanup(h.close)
	h.start()
	command := func(args ...string) v1.InstanceResponse {
		t.Helper()
		args = append(args, "--state-dir", state, "--json")
		data, err := exec.CommandContext(ctx, binary, args...).CombinedOutput()
		if err != nil {
			t.Fatalf("CLI %v: %v %s", args, err, data)
		}
		var response v1.InstanceResponse
		if len(args) > 0 && args[0] != "gateway" {
			if err := json.Unmarshal(data, &response); err != nil {
				t.Fatal(err)
			}
		}
		return response
	}
	port, admin := managedFixturePort(t), managedFixturePort(t)
	config := filepath.Join(root, "machine.json")
	cfg := v1.MachineConfig{Version: v1.ManifestVersion, Caddy: &v1.CaddyConfig{Mode: "managed", Executable: caddy, Endpoint: fmt.Sprintf("http://127.0.0.1:%d", admin), HTTPSPort: &port}}
	data, _ := json.Marshal(cfg)
	if err := os.WriteFile(config, data, 0600); err != nil {
		t.Fatal(err)
	}
	command("gateway", "start", "--config", config)
	started := command("run", "dev", "--project", project, "--config", config, "--startup-timeout", "45s", "--stop-grace", "1s")
	id := started.Instance.ID
	t.Cleanup(func() {
		if _, err := h.cli.Destroy(context.Background(), id); err != nil {
			h.cleanupUnverified = true
			t.Error("watcher destroy", err)
		}
	})
	deadline := time.Now().Add(50 * time.Second)
	var instance v1.Instance
	for time.Now().Before(deadline) {
		r, err := h.cli.Inspect(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		instance = r.Instance
		if instance.Status == v1.RuntimeReady {
			break
		}
		if instance.Status == v1.Failed {
			t.Fatalf("watcher start: %+v", instance.Execution)
		}
		time.Sleep(100 * time.Millisecond)
	}
	if instance.Status != v1.RuntimeReady {
		t.Fatal("watcher readiness deadline", instance.Status)
	}
	browser := os.Getenv("BACKLOT_BROWSER_TEST_BINARY")
	certutil := os.Getenv("BACKLOT_CERTUTIL_TEST_BINARY")
	if browser == "" || certutil == "" {
		t.Fatal("real browser HMR requires explicit browser and certutil")
	}
	home := filepath.Join(root, "browser-home")
	db := filepath.Join(home, ".pki", "nssdb")
	if err := os.MkdirAll(db, 0700); err != nil {
		t.Fatal(err)
	}
	cert := func(args ...string) {
		t.Helper()
		c := exec.CommandContext(ctx, certutil, args...)
		c.Env = append(os.Environ(), "HOME="+home)
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatal(err, string(out))
		}
	}
	cert("-N", "--empty-password", "-d", "sql:"+db)
	visit := func(trusted bool) {
		t.Helper()
		options := append([]chromedp.ExecAllocatorOption{}, chromedp.DefaultExecAllocatorOptions[:]...)
		profile, err := os.MkdirTemp(root, "profile-")
		if err != nil {
			t.Fatal(err)
		}
		options = append(options, chromedp.ExecPath(browser), chromedp.UserDataDir(profile), chromedp.Env("HOME="+home, "XDG_CONFIG_HOME="+filepath.Join(home, ".config"), "XDG_DATA_HOME="+filepath.Join(home, ".local", "share")))
		allocator, stopAllocator := chromedp.NewExecAllocator(ctx, options...)
		defer stopAllocator()
		tab, stopTab := chromedp.NewContext(allocator)
		defer stopTab()
		var socketMu sync.Mutex
		type socketEvidence struct {
			handshake, connected, updated, editEligible bool
		}
		sockets := make(map[network.RequestID]*socketEvidence)
		editStarted := false
		chromedp.ListenTarget(tab, func(event any) {
			socketMu.Lock()
			defer socketMu.Unlock()
			switch e := event.(type) {
			case *network.EventWebSocketCreated:
				if strings.HasPrefix(e.URL, strings.Replace(instance.Execution.Origin, "https:", "wss:", 1)+"/") {
					sockets[e.RequestID] = &socketEvidence{}
				}
			case *network.EventWebSocketHandshakeResponseReceived:
				if socket := sockets[e.RequestID]; socket != nil && e.Response.Status == http.StatusSwitchingProtocols {
					socket.handshake = true
				}
			case *network.EventWebSocketFrameReceived:
				socket := sockets[e.RequestID]
				if socket == nil || !socket.handshake {
					return
				}
				var message struct {
					Type    string `json:"type"`
					Updates []struct {
						Path string `json:"path"`
					} `json:"updates"`
				}
				if json.Unmarshal([]byte(e.Response.PayloadData), &message) == nil {
					socket.connected = socket.connected || message.Type == "connected"
					if editStarted && socket.editEligible && message.Type == "update" {
						for _, update := range message.Updates {
							if update.Path == "/main.js" {
								socket.updated = true
							}
						}
					}
				}
			}
		})
		err = chromedp.Run(tab, network.Enable(), chromedp.Navigate(instance.Execution.Origin))
		if !trusted {
			if err == nil || !strings.Contains(err.Error(), "ERR_CERT_AUTHORITY_INVALID") {
				t.Fatalf("fresh browser missing-trust rejection: %v", err)
			}
			return
		}
		if err != nil {
			t.Fatal("canonical trusted browser", err)
		}
		if err := chromedp.Run(tab, chromedp.Poll(`document.querySelector('#web')?.textContent==='web-v1' && document.querySelector('#api')?.textContent==='api-v1'`, nil), chromedp.Evaluate(`window.backlotPageIdentity='same-document'`, nil)); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(project, "main.js")
		source, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.WriteFile(path, source, 0600) })
		// Begin edit evidence only after canonical handshake and connected frame.
		socketDeadline := time.Now().Add(10 * time.Second)
		for {
			socketMu.Lock()
			for _, socket := range sockets {
				socket.editEligible = socket.handshake && socket.connected
				editStarted = editStarted || socket.editEligible
			}
			ready := editStarted
			socketMu.Unlock()
			if ready {
				break
			}
			if time.Now().After(socketDeadline) {
				t.Fatal("canonical WSS handshake/connected deadline")
			}
			time.Sleep(10 * time.Millisecond)
		}
		if err := os.WriteFile(path, []byte(strings.ReplaceAll(string(source), "web-v1", "web-v2")), 0600); err != nil {
			t.Fatal(err)
		}
		if err := chromedp.Run(tab, chromedp.Poll(`document.querySelector('#web')?.textContent==='web-v2' && window.backlotPageIdentity==='same-document'`, nil)); err != nil {
			t.Fatal("Vite browser HMR without navigation", err)
		}
		socketMu.Lock()
		socketOK := false
		for _, socket := range sockets {
			socketOK = socketOK || (socket.handshake && socket.connected && socket.updated)
		}
		socketMu.Unlock()
		if !socketOK {
			t.Fatal("canonical browser WebSocket connected/update evidence missing")
		}
		t.Log("canonical verified-TLS browser rendered Go API; canonical WSS connected/update and same-document Vite HMR verified")
		goPath := filepath.Join(project, "main.go")
		goSource, err := os.ReadFile(goPath)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.WriteFile(goPath, goSource, 0600) })
		roots := x509.NewCertPool()
		pem, err := os.ReadFile(trust)
		if err != nil || !roots.AppendCertsFromPEM(pem) {
			t.Fatal("fixture public root unavailable", err)
		}
		transport := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}}
		defer transport.CloseIdleConnections()
		httpClient := &http.Client{Transport: transport, Timeout: time.Second}
		body := func() string {
			r, err := httpClient.Get(instance.Execution.Origin + "/api/")
			if err != nil {
				return ""
			}
			defer func() { _ = r.Body.Close() }()
			data, _ := io.ReadAll(r.Body)
			return string(data)
		}
		awaitAPI := func(want string) {
			t.Helper()
			deadline := time.Now().Add(20 * time.Second)
			for time.Now().Before(deadline) {
				if body() == want {
					return
				}
				time.Sleep(100 * time.Millisecond)
			}
			t.Fatal("Air rebuild response missing", want)
		}
		writeGo := func(source string) {
			t.Helper()
			if err := os.WriteFile(goPath, []byte(source), 0600); err != nil {
				t.Fatal(err)
			}
		}
		writeGo(strings.ReplaceAll(string(goSource), "api-v1", "api-v2"))
		awaitAPI("api-v2")
		beforeFailure, err := h.cli.Logs(ctx, v1.LogsRequest{APIVersion: v1.Version, InstanceID: id, Component: "api"})
		if err != nil {
			t.Fatal(err)
		}
		writeGo(string(goSource) + "\ninvalid Go source\n")
		failureDeadline := time.Now().Add(20 * time.Second)
		offset := beforeFailure.NextOffset
		var failureLog strings.Builder
		for {
			page, err := h.cli.Logs(ctx, v1.LogsRequest{APIVersion: v1.Version, InstanceID: id, Component: "api", Offset: offset})
			if err != nil {
				t.Fatal(err)
			}
			offset = page.NextOffset
			for _, record := range page.Records {
				failureLog.WriteString(record.Message)
			}
			if strings.Contains(failureLog.String(), "syntax error") {
				break
			}
			if time.Now().After(failureDeadline) {
				t.Fatal("Air did not observe invalid Go revision", failureLog.String())
			}
			time.Sleep(50 * time.Millisecond)
		}
		t.Log("Air invalid revision compiler diagnostic:", failureLog.String())
		if body() != "api-v2" {
			t.Fatal("Air failed build did not preserve previous available app")
		}
		writeGo(strings.ReplaceAll(string(goSource), "api-v1", "api-v3"))
		awaitAPI("api-v3")
		t.Log("Air Go rebuild, failed-build previous availability and corrected-source recovery passed through canonical HTTPS")
	}
	visit(false)
	cert("-A", "-d", "sql:"+db, "-n", "Backlot watcher fixture", "-t", "C,,", "-i", trust)
	visit(true)
	stopped, err := h.cli.StopExecution(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if stopped.Instance.Execution.CleanupFailure != "" || len(stopped.Instance.Execution.NativeCleanup) != 2 {
		t.Fatalf("session cleanup: %+v", stopped.Instance.Execution)
	}
	for _, r := range stopped.Instance.Execution.NativeCleanup {
		if r.Confidence != v1.ObservedEmpty {
			t.Fatal(r)
		}
	}
	response, err := http.Get(sentinel.URL)
	if err != nil {
		t.Fatal("unrelated sentinel lost", err)
	}
	sentinelBody, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if string(sentinelBody) != "unrelated-sentinel" {
		t.Fatal("sentinel changed")
	}
	t.Log("Air and ordinary npm/Vite stopped with observed-empty results; browser allocator joined")
}
