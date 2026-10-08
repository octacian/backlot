package gateway

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	v1 "github.com/octacian/backlot/api/v1"
	"github.com/octacian/backlot/internal/gateway/gatewaytest"
)

// TestHTTPSMixed exercises a real configured Caddy, TLS-verified host/container
// consumers, container SSR and native API, prefix boundaries, upgrades and cleanup.
func TestHTTPSMixed(t *testing.T) {
	for _, topology := range []string{"native-gateway", "container-gateway"} {
		t.Run(topology, func(t *testing.T) { testHTTPSMixed(t, topology == "container-gateway") })
	}
}
func testHTTPSMixed(t *testing.T, containerGateway bool) {
	endpoint := os.Getenv("BACKLOT_DOCKER_TEST_ENDPOINT")
	if endpoint == "" {
		t.Skip("set BACKLOT_DOCKER_TEST_ENDPOINT and BACKLOT_CADDY_TEST_BINARY")
	}
	var f gatewaytest.Fixture
	hostAddress := "127.0.0.1"
	if containerGateway {
		f = gatewaytest.StartContainer(t, "fixture.test")
		hostAddress = "host.docker.internal"
	} else {
		f = gatewaytest.Start(t, "fixture.test")
	}
	image := gatewaytest.FixtureImage(t, endpoint, "./testdata/fixture")
	origin := "https://fixture.test:" + strconv.Itoa(f.Port)
	api := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = fmt.Fprint(w, "api:"+r.URL.Path) }))
	_ = api.Listener.Close()
	var listenErr error
	api.Listener, listenErr = net.Listen("tcp4", "0.0.0.0:0")
	if listenErr != nil {
		t.Fatal(listenErr)
	}
	api.Start()
	defer api.Close()
	_, nativePort, _ := net.SplitHostPort(strings.TrimPrefix(api.URL, "http://"))
	native, _ := strconv.Atoi(nativePort)
	webPort := gatewaytest.Port(t)
	web := gatewaytest.OwnedContainer(t, endpoint, image, "-p", fmt.Sprintf("0.0.0.0:%d:8080", webPort), "--add-host", "fixture.test:host-gateway", "-v", f.Cert+":/cert.pem:ro", "-e", "PORT=8080", "-e", "ROLE=web", "-e", "CERT=/cert.pem", "-e", "ORIGIN="+origin)
	gatewaytest.DockerCommand(t, endpoint, "start", web)
	for deadline := time.Now().Add(10 * time.Second); ; {
		conn, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(webPort)), 100*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("fixture web failed to listen", gatewaytest.DockerCommand(t, endpoint, "logs", web))
		}
		time.Sleep(20 * time.Millisecond)
	}
	p := v1.Publication{Routes: []v1.Route{{Path: "/", Service: "web", Port: "http", Prefix: "preserve"}, {Path: "/api", Service: "api", Port: "http", Prefix: "strip"}, {Path: "/api/keep", Service: "api", Port: "http", Prefix: "preserve"}}}
	components := []v1.PlannedComponent{{Name: "web", Ports: map[string]v1.ServicePort{"http": {Resource: "web"}}}, {Name: "api", Ports: map[string]v1.ServicePort{"http": {Resource: "api"}}}}
	intent, err := NewIntent(v1.CaddyConfig{Endpoint: f.Endpoint, Scope: "backlot", HostAddress: hostAddress}, "fixture.test", "fixture-owned", p, components, map[string]int{"api": native, "web": webPort})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := Remove(ctx, intent); err != nil {
			t.Error("fixture route residual", err)
		}
	})
	if err := Publish(context.Background(), intent); err != nil {
		t.Fatal(err)
	}
	transport := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: f.Pool, MinVersion: tls.VersionTLS12}, DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, net.JoinHostPort("127.0.0.1", strconv.Itoa(f.Port)))
	}}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 5 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	for path, expected := range map[string]string{"/api": "api:/", "/api/value": "api:/value", "/apix": "web:/apix", "/API": "web:/API", "/api/keep/value": "api:/api/keep/value", "/ssr": "ssr:api:/value"} {
		response, err := client.Get(origin + path)
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(response.Body)
		_ = response.Body.Close()
		if err != nil || response.StatusCode != 200 || string(body) != expected {
			t.Fatalf("%s: status=%d body=%s expected=%s error=%v", path, response.StatusCode, body, expected, err)
		}
	}
	response, err := client.Get(origin + "/redirect")
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != 302 || response.Header.Get("Location") != origin+"/ssr" || !strings.Contains(response.Header.Get("Set-Cookie"), "Secure") {
		t.Fatal("redirect/cookie origin changed", response.Header)
	}
	conn, err := tls.Dial("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(f.Port)), &tls.Config{ServerName: "fixture.test", RootCAs: f.Pool, MinVersion: tls.VersionTLS12})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	_, _ = fmt.Fprintf(conn, "GET /ws HTTP/1.1\r\nHost: fixture.test\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Version: 13\r\nSec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\n\r\n")
	reader := bufio.NewReader(conn)
	upgrade, err := http.ReadResponse(reader, nil)
	if err != nil || upgrade.StatusCode != 101 {
		t.Fatal("WebSocket upgrade", upgrade, err)
	}
	_, _ = conn.Write([]byte{0x81, 0x85, 0, 0, 0, 0, 'h', 'e', 'l', 'l', 'o'})
	frame := make([]byte, 7)
	if _, err := io.ReadFull(reader, frame); err != nil || string(frame[2:]) != "hello" {
		t.Fatal("WebSocket roundtrip", frame, err)
	}
	// An independent container client verifies the same certificate and SSR path.
	clientID := gatewaytest.OwnedContainer(t, endpoint, image, "--add-host", "fixture.test:host-gateway", "-v", f.Cert+":/cert.pem:ro", "-e", "CERT=/cert.pem", "-e", "ORIGIN="+origin, "-e", "CLIENT=1")
	t.Log(gatewaytest.DockerCommand(t, endpoint, "start", "-a", clientID))
	if code := gatewaytest.DockerCommand(t, endpoint, "inspect", "--format", "{{.State.ExitCode}}", clientID); code != "0" {
		t.Fatal("container public-origin client failed", code)
	}
	if err := Remove(context.Background(), intent); err != nil {
		t.Fatal(err)
	}
	response, err = client.Get(origin + "/api/value")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if string(body) != "unrelated sentinel" {
		t.Fatal("unrelated gateway configuration lost", string(body))
	}
	t.Log("TLS host + container SSR/API, prefix boundaries/longest route, WebSocket bidirectional frame, redirects/cookies, scoped cleanup verified")
}

func TestCaddyAmbiguousMutationRecovery(t *testing.T) {
	f := gatewaytest.Start(t, "fixture.test")
	var unavailable atomic.Bool
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if unavailable.Load() {
			connection, _, err := w.(http.Hijacker).Hijack()
			if err == nil {
				_ = connection.Close()
			}
			return
		}
		request, err := http.NewRequestWithContext(r.Context(), r.Method, f.Endpoint+r.URL.Path, r.Body)
		if err != nil {
			t.Error(err)
			return
		}
		request.Header = r.Header.Clone()
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Error(err)
			return
		}
		defer func() { _ = response.Body.Close() }()
		if r.Method == http.MethodPatch && response.StatusCode == 200 {
			unavailable.Store(true)
			// Effect has committed but its response is lost. Register gateway fixture
			// cleanup before this fault, so any assertion still retains the owner.
			connection, _, err := w.(http.Hijacker).Hijack()
			if err != nil {
				t.Error(err)
				return
			}
			_ = connection.Close()
			return
		}
		for name, values := range response.Header {
			w.Header()[name] = values
		}
		w.WriteHeader(response.StatusCode)
		_, _ = io.Copy(w, response.Body)
	}))
	defer proxy.Close()
	intent := testIntent(proxy.URL, "fixture.test", "ambiguous-owned")
	clean := intent
	clean.Endpoint = f.Endpoint
	t.Cleanup(func() {
		if err := Remove(context.Background(), clean); err != nil {
			t.Error("recovery fixture residual", err)
		}
	})
	if err := Remove(context.Background(), clean); err != nil {
		t.Fatal("intent before effect was not idempotent", err)
	}
	if err := Publish(context.Background(), intent); err == nil {
		t.Fatal("lost publication response falsely succeeded")
	}
	// A fresh owner can reconcile the exact durable intent without completion.
	if err := Publish(context.Background(), clean); err != nil {
		t.Fatal("effect from interrupted intent not recognized", err)
	}
	unavailable.Store(false)
	if err := Remove(context.Background(), intent); err == nil {
		t.Fatal("lost cleanup response falsely succeeded")
	}
	for range 2 {
		if err := Remove(context.Background(), clean); err != nil {
			t.Fatal("cleanup after lost response was not idempotent", err)
		}
	}
	t.Log("real Caddy intent-only absence, lost publish response/effect recovery and lost cleanup response/idempotent recovery verified")
}

func TestCaddyConcurrentScope(t *testing.T) {
	f := gatewaytest.Start(t, "fixture.test")
	var joined sync.WaitGroup
	for _, host := range []string{"a.fixture.test", "b.fixture.test", "c.fixture.test"} {
		intent := testIntent(f.Endpoint, host, host)
		t.Cleanup(func() {
			if err := Remove(context.Background(), intent); err != nil {
				t.Error(err)
			}
		})
		joined.Go(func() {
			if err := Publish(context.Background(), intent); err != nil {
				t.Error(err)
			}
		})
	}
	joined.Wait()
	data, _, status, err := request(context.Background(), http.DefaultClient, f.Endpoint, "GET", "/id/backlot/routes", "", nil)
	if err != nil || status != 200 {
		t.Fatal(status, err)
	}
	var routes []any
	if err := json.Unmarshal(data, &routes); err != nil || len(routes) != 3 {
		t.Fatal("concurrent real Caddy changes lost", len(routes), err)
	}
	a := testIntent(f.Endpoint, "race.fixture.test", "race-a")
	b := testIntent(f.Endpoint, "race.fixture.test", "race-b")
	results := make(chan error, 2)
	for _, intent := range []Intent{a, b} {
		joined.Go(func() { results <- Publish(context.Background(), intent) })
	}
	joined.Wait()
	first, second := <-results, <-results
	if (first == nil) == (second == nil) {
		t.Fatal("same hostname race must have exactly one winner", first, second)
	}
	// Only the exact winner is cleanup authority. A losing intent cannot remove it.
	data, _, _, err = request(context.Background(), http.DefaultClient, f.Endpoint, "GET", "/id/backlot/routes", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	var tree any
	if err := json.Unmarshal(data, &tree); err != nil {
		t.Fatal(err)
	}
	winner, loser := a, b
	if hasID(tree, b.ID) {
		winner, loser = b, a
	}
	t.Cleanup(func() {
		if err := Remove(context.Background(), winner); err != nil {
			t.Error(err)
		}
	})
	if err := Remove(context.Background(), loser); err == nil {
		t.Fatal("losing intent falsely acknowledged cleanup of a foreign hostname")
	}
	t.Log("real root-ETag concurrency preserves three instances; same-host race rejects takeover")
}
