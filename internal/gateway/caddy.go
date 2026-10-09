// Package gateway mutates only children of an operator-installed Caddy subroute.
package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	v1 "github.com/octacian/backlot/api/v1"
)

// Intent binds a complete route effect to its private ownership ID before mutation.
// Retain it until Remove succeeds, including when Publish returns an ambiguous error.
type Intent struct {
	Endpoint string          `json:"endpoint"`
	Scope    string          `json:"scope"`
	ID       string          `json:"id"`
	Host     string          `json:"host"`
	Route    json.RawMessage `json:"route"`
	Applied  bool            `json:"applied,omitempty"`
}

// NewIntent constructs a literal segment-prefix route tree. Ports are host mapped
// for both runtimes; the configured address must be reachable from this gateway.
func NewIntent(config v1.CaddyConfig, host, token string, publication v1.Publication, components []v1.PlannedComponent, ports map[string]int) (Intent, error) {
	routes := append([]v1.Route(nil), publication.Routes...)
	sort.Slice(routes, func(i, j int) bool { return len(routes[i].Path) > len(routes[j].Path) })
	children := make([]any, 0, len(routes))
	for _, route := range routes {
		port := 0
		for _, component := range components {
			if component.Name == route.Service {
				port = ports[component.Ports[route.Port].Resource]
			}
		}
		if port < 1 || port > 65535 {
			return Intent{}, diagnostic("gateway_config", "published service port is not allocated")
		}
		handlers := []any{}
		if route.Prefix == "strip" && route.Path != "/" {
			handlers = append(handlers, map[string]any{"handler": "rewrite", "strip_path_prefix": route.Path})
		}
		handlers = append(handlers, map[string]any{"handler": "reverse_proxy", "upstreams": []any{map[string]any{"dial": net.JoinHostPort(config.HostAddress, strconv.Itoa(port))}}, "headers": map[string]any{"response": map[string]any{"set": map[string]any{"Backlot-Instance": []string{host}}}}})
		child := map[string]any{"handle": handlers, "terminal": true}
		if route.Path != "/" {
			child["match"] = []any{map[string]any{"path_regexp": map[string]any{"pattern": "^" + regexp.QuoteMeta(route.Path) + "(?:/|$)"}}}
		}
		children = append(children, child)
	}
	id := "backlot-" + token
	route, err := json.Marshal(map[string]any{"@id": id, "match": []any{map[string]any{"host": []string{host}}}, "handle": []any{map[string]any{"handler": "subroute", "routes": children}}, "terminal": true})
	return Intent{Endpoint: config.Endpoint, Scope: config.Scope, ID: id, Host: host, Route: route}, err
}

func diagnostic(code, message string) error {
	return &v1.PlanError{Code: code, Field: "config.caddy", Message: message}
}

// Publish conditionally adds an owned route, rejecting hostname/identity conflicts.
func Publish(ctx context.Context, intent Intent) error { return mutate(ctx, intent, false) }

// Remove idempotently removes only an exact owned effect; changed effects are preserved.
func Remove(ctx context.Context, intent Intent) error { return mutate(ctx, intent, true) }

func mutate(ctx context.Context, intent Intent, remove bool) (result error) {
	if intent.Endpoint == "" || intent.Scope == "" || intent.ID == "" || intent.Host == "" || !json.Valid(intent.Route) {
		return diagnostic("gateway_ownership", "publication ownership journal is incomplete; preserve gateway and inspect private state")
	}
	possiblyApplied := false
	defer func() {
		if !remove && possiblyApplied && result != nil {
			result = errors.Join(diagnostic("gateway_uncertain", "Caddy mutation could not be reconciled after response loss; retain intent for cleanup/recovery"), result)
		}
	}()
	client := &http.Client{Timeout: 5 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	lastError := diagnostic("gateway_concurrency", "Caddy configuration kept changing; bounded retry exhausted without takeover")
	for range 8 {
		data, etag, status, err := request(ctx, client, intent.Endpoint, http.MethodGet, "/config/", "", nil)
		if err != nil || status != http.StatusOK {
			lastError = diagnostic("gateway_unavailable", "cannot read configured Caddy API; verify endpoint and operator-installed scope")
			if ctx.Err() != nil {
				return lastError
			}
			continue
		}
		if etag == "" {
			return diagnostic("gateway_concurrency", "Caddy API did not supply an ETag; refusing unguarded mutation")
		}
		var root any
		decoder := json.NewDecoder(bytes.NewReader(data))
		decoder.UseNumber()
		if err := decoder.Decode(&root); err != nil {
			return diagnostic("gateway_config", "Caddy returned invalid configuration JSON")
		}
		var scopes []scope
		findScopes(root, "/config", intent.Scope, &scopes)
		if len(scopes) != 1 || scopes[0].object["handler"] != "subroute" {
			return diagnostic("gateway_scope", "configure exactly one JSON subroute with the configured @id and an explicit routes array")
		}
		s := scopes[0]
		children, ok := s.object["routes"].([]any)
		if !ok {
			return diagnostic("gateway_scope", "operator-installed subroute requires an explicit routes array")
		}
		var expected any
		if err := json.Unmarshal(intent.Route, &expected); err != nil {
			return err
		}
		expectedObject, ok := expected.(map[string]any)
		if !ok || expectedObject["@id"] != intent.ID {
			return diagnostic("gateway_ownership", "publication route and ownership ID disagree; preserved")
		}
		if countID(root, intent.ID) > 1 {
			return diagnostic("gateway_ownership", "publication ownership ID is duplicated; preserved")
		}
		found := -1
		for index, child := range children {
			object, _ := child.(map[string]any)
			if object["@id"] == intent.ID {
				if found >= 0 || !equalJSON(child, expected) {
					return diagnostic("gateway_ownership", "owned route differs from recorded effect; preserved for operator diagnosis")
				}
				found = index
			}
		}
		if remove && found < 0 {
			// A moved route is uncertain ownership, not successful absence.
			if hasID(root, intent.ID) {
				return diagnostic("gateway_ownership", "recorded route moved outside configured scope; preserved")
			}
			if conflict(root, s.path, "/config", intent.Host, intent.ID) {
				return diagnostic("gateway_ownership", "hostname remains claimed without recorded ownership; preserved for diagnosis")
			}
			return nil
		}
		if !remove {
			if conflict(root, s.path, "/config", intent.Host, intent.ID) {
				return diagnostic("gateway_conflict", "hostname or ownership ID already exists in Caddy; no takeover performed")
			}
			if found >= 0 {
				return nil
			}
			children = append(children, expected)
		} else {
			children = append(children[:found:found], children[found+1:]...)
		}
		body, err := json.Marshal(children)
		if err != nil {
			return err
		}
		_, _, status, err = request(ctx, client, intent.Endpoint, http.MethodPatch, s.path+"/routes", etag, body)
		if err != nil {
			possiblyApplied = true
			lastError = diagnostic("gateway_uncertain", "Caddy mutation response was lost; ownership intent retained for cleanup/recovery")
			if ctx.Err() != nil {
				return lastError
			}
			continue
		}
		if status == http.StatusPreconditionFailed {
			lastError = diagnostic("gateway_concurrency", "Caddy configuration kept changing; bounded retry exhausted without takeover")
			continue
		}
		if status != http.StatusOK {
			return diagnostic("gateway_rejected", fmt.Sprintf("Caddy rejected scoped route mutation (HTTP %d); verify configured upstream address and scope", status))
		}
		return nil
	}
	return lastError
}

type scope struct {
	path   string
	object map[string]any
}

func findScopes(value any, path, id string, result *[]scope) {
	switch value := value.(type) {
	case map[string]any:
		if value["@id"] == id {
			*result = append(*result, scope{path, value})
		}
		for key, child := range value {
			if strings.ContainsAny(key, "/\\?#%") || key == "." || key == ".." {
				continue
			}
			findScopes(child, path+"/"+key, id, result)
		}
	case []any:
		for index, child := range value {
			findScopes(child, path+"/"+strconv.Itoa(index), id, result)
		}
	}
}

func hasID(value any, id string) bool {
	return countID(value, id) > 0
}

func countID(value any, id string) int {
	count := 0
	switch value := value.(type) {
	case map[string]any:
		if value["@id"] == id {
			count++
		}
		for _, child := range value {
			count += countID(child, id)
		}
	case []any:
		for _, child := range value {
			count += countID(child, id)
		}
	}
	return count
}

// Ancestor host matches delegate to the configured scope, so they are allowed.
// Sibling/descendant exact or wildcard hostname claims must never be taken over.
func conflict(value any, scopePath, path, host, ownID string) bool {
	switch value := value.(type) {
	case map[string]any:
		if value["@id"] == ownID {
			return !strings.HasPrefix(path, scopePath+"/routes/")
		}
		if !strings.HasPrefix(scopePath, path+"/") && path != scopePath {
			if hosts, ok := value["host"].([]any); ok {
				for _, item := range hosts {
					pattern, ok := item.(string)
					if !ok || hostMatch(pattern, host) {
						return true
					}
				}
			}
		}
		for key, child := range value {
			if key == "match" && strings.HasPrefix(scopePath, path+"/") {
				continue
			}
			if conflict(child, scopePath, path+"/"+key, host, ownID) {
				return true
			}
		}
	case []any:
		for index, child := range value {
			if conflict(child, scopePath, path+"/"+strconv.Itoa(index), host, ownID) {
				return true
			}
		}
	}
	return false
}

func hostMatch(pattern, host string) bool {
	pattern = strings.ToLower(pattern)
	if pattern == host || pattern == "*" {
		return true
	}
	// Fail closed on patterns outside the literal/single-label wildcard forms.
	if strings.Contains(pattern, "*") {
		return !strings.HasPrefix(pattern, "*.") || strings.HasSuffix(host, pattern[1:])
	}
	return false
}

func equalJSON(a, b any) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return bytes.Equal(x, y)
}

func request(ctx context.Context, client *http.Client, endpoint, method, path, etag string, body []byte) ([]byte, string, int, error) {
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(endpoint, "/")+path, bytes.NewReader(body))
	if err != nil {
		return nil, "", 0, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("If-Match", etag)
	}
	response, err := client.Do(req)
	if err != nil {
		return nil, "", 0, err
	}
	defer func() { _ = response.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(response.Body, (8<<20)+1))
	if len(data) > 8<<20 {
		err = errors.New("caddy configuration exceeds limit")
	}
	return data, response.Header.Get("Etag"), response.StatusCode, err
}
