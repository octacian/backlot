package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	v1 "github.com/octacian/backlot/api/v1"
)

func testIntent(endpoint, host, token string) Intent {
	intent, err := NewIntent(v1.CaddyConfig{Endpoint: endpoint, Scope: "backlot", HostAddress: "127.0.0.1"}, host, token,
		v1.Publication{Routes: []v1.Route{{Path: "/", Service: "web", Port: "http", Prefix: "preserve"}}},
		[]v1.PlannedComponent{{Name: "web", Ports: map[string]v1.ServicePort{"http": {Resource: "port"}}}}, map[string]int{"port": 1234})
	if err != nil {
		panic(err)
	}
	return intent
}

func TestConcurrentPublicationAndOwnership(t *testing.T) {
	var mu sync.Mutex
	version, retries := 0, 0
	root := map[string]any{"routes": []any{map[string]any{"match": []any{map[string]any{"host": []any{"*.example.test"}}}, "handle": []any{map[string]any{"@id": "backlot", "handler": "subroute", "routes": []any{}}}}}, "unrelated": "sentinel"}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		etag := fmt.Sprintf(`"/config %d"`, version)
		switch {
		case r.Method == "GET" && r.URL.Path == "/config/":
			w.Header().Set("Etag", etag)
			_ = json.NewEncoder(w).Encode(root)
		case r.Method == "PATCH" && r.URL.Path == "/config/routes/0/handle/0/routes":
			if r.Header.Get("If-Match") != etag || retries == 0 {
				retries++
				version++
				root["unrelated"] = "concurrent edit"
				w.WriteHeader(412)
				return
			}
			var routes []any
			if err := json.NewDecoder(r.Body).Decode(&routes); err != nil {
				t.Error(err)
			}
			root["routes"].([]any)[0].(map[string]any)["handle"].([]any)[0].(map[string]any)["routes"] = routes
			version++
		default:
			t.Errorf("mutation escaped child scope: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(400)
		}
	}))
	defer server.Close()
	a, b := testIntent(server.URL, "a.example.test", "a"), testIntent(server.URL, "b.example.test", "b")
	var joined sync.WaitGroup
	for _, intent := range []Intent{a, b} {
		joined.Go(func() {
			if err := Publish(context.Background(), intent); err != nil {
				t.Error(err)
			}
		})
	}
	joined.Wait()
	if err := Publish(context.Background(), a); err != nil {
		t.Fatal("idempotent publish", err)
	}
	foreign := testIntent(server.URL, a.Host, "foreign")
	if err := Publish(context.Background(), foreign); err == nil {
		t.Fatal("hostname takeover accepted")
	}
	for range 2 {
		if err := Remove(context.Background(), a); err != nil {
			t.Fatal(err)
		}
	}
	mu.Lock()
	children := root["routes"].([]any)[0].(map[string]any)["handle"].([]any)[0].(map[string]any)["routes"].([]any)
	if len(children) != 1 || !hasID(children[0], b.ID) || root["unrelated"] != "concurrent edit" {
		t.Fatal("lost unrelated or concurrent configuration", root)
	}
	children[0].(map[string]any)["terminal"] = false
	mu.Unlock()
	if err := Remove(context.Background(), b); err == nil {
		t.Fatal("modified owned effect was deleted")
	}
	mu.Lock()
	children[0].(map[string]any)["terminal"] = true
	children[0].(map[string]any)["@id"] = "foreign"
	mu.Unlock()
	if err := Remove(context.Background(), b); err == nil {
		t.Fatal("removed ownership marker falsely acknowledged cleanup")
	}
}

func TestGatewayRejectsUnsafeScopeAndConflicts(t *testing.T) {
	for _, config := range []string{
		`{"handler":"subroute","@id":"other","routes":[]}`,
		`{"handler":"reverse_proxy","@id":"backlot","routes":[]}`,
		`{"handler":"subroute","@id":"backlot"}`,
		`{"handler":"subroute","@id":"backlot","routes":[{"match":[{"host":["*.example.test"]}]}]}`,
		`{"one":{"handler":"subroute","@id":"backlot","routes":[]},"two":{"@id":"backlot"}}`,
	} {
		t.Run(config, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" {
					t.Error("unsafe mutation")
				}
				w.Header().Set("Etag", `"/config test"`)
				_, _ = w.Write([]byte(config))
			}))
			defer server.Close()
			if err := Publish(context.Background(), testIntent(server.URL, "a.example.test", "a")); err == nil {
				t.Fatal("unsafe scope accepted")
			}
		})
	}
}

func TestPublicationRouteOrdering(t *testing.T) {
	p := v1.Publication{Routes: []v1.Route{{Path: "/", Service: "web", Port: "http", Prefix: "strip"}, {Path: "/api", Service: "web", Port: "http", Prefix: "strip"}, {Path: "/api/admin", Service: "web", Port: "http", Prefix: "preserve"}}}
	i, err := NewIntent(v1.CaddyConfig{HostAddress: "::1"}, "fixture.test", "token", p, []v1.PlannedComponent{{Name: "web", Ports: map[string]v1.ServicePort{"http": {Resource: "port"}}}}, map[string]int{"port": 1234})
	if err != nil {
		t.Fatal(err)
	}
	s := string(i.Route)
	if !strings.Contains(s, `"pattern":"^/api(?:/|$)"`) || strings.Contains(s, `"/api*"`) || strings.Index(s, "/api/admin") > strings.Index(s, "^/api(?:") || !strings.Contains(s, "[::1]:1234") {
		t.Fatal("route semantics", s)
	}
}

func TestUnrelatedNumericPrecisionPreserved(t *testing.T) {
	// Caddy durations can be integer nanoseconds above float64's exact range.
	// Unknown sibling handler configuration must survive a child-array patch.
	const config = `{"@id":"backlot","handler":"subroute","routes":[{"match":[{"host":["other.example.test"]}],"handle":[{"handler":"reverse_proxy","transport":{"protocol":"http","read_timeout":9007199254740993},"upstreams":[{"dial":"127.0.0.1:1234"}]}]}]}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" {
			w.Header().Set("Etag", `"/config test"`)
			_, _ = io.WriteString(w, config)
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil || !strings.Contains(string(body), `"read_timeout":9007199254740993`) {
			t.Error("unrelated numeric setting lost precision", string(body), err)
		}
	}))
	defer server.Close()
	if err := Publish(context.Background(), testIntent(server.URL, "a.example.test", "a")); err != nil {
		t.Fatal(err)
	}
}
