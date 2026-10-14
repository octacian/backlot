package gateway

import (
	"encoding/json"
	"net"
	"net/url"
	"strconv"

	v1 "github.com/octacian/backlot/api/v1"
)

// ManagedConfig builds the dedicated loopback gateway bootstrap. Caddy owns
// renewal and autosaving subsequent scoped mutations; trust installation is off.
func ManagedConfig(c v1.CaddyConfig, storage string) ([]byte, error) {
	u, err := url.Parse(c.Endpoint)
	if err != nil {
		return nil, err
	}
	return json.Marshal(map[string]any{
		"admin":   map[string]any{"listen": u.Host},
		"storage": map[string]any{"module": "file_system", "root": storage},
		"apps": map[string]any{
			"pki": map[string]any{"certificate_authorities": map[string]any{"local": map[string]any{"install_trust": false}}},
			"tls": map[string]any{"automation": map[string]any{"policies": []any{map[string]any{"issuers": []any{map[string]any{"module": "internal"}}}}}},
			"http": map[string]any{"https_port": *c.HTTPSPort, "servers": map[string]any{"backlot": map[string]any{
				"listen":                  []string{net.JoinHostPort("127.0.0.1", strconv.Itoa(*c.HTTPSPort)), net.JoinHostPort("::1", strconv.Itoa(*c.HTTPSPort))},
				"protocols":               []string{"h1", "h2"},
				"automatic_https":         map[string]any{"disable_redirects": true},
				"tls_connection_policies": []any{map[string]any{}},
				"routes": []any{map[string]any{
					"match":  []any{map[string]any{"host": []string{"*." + c.DomainSuffix}}},
					"handle": []any{map[string]any{"@id": c.Scope, "handler": "subroute", "routes": []any{}}},
				}},
			}}},
		},
	})
}

// ValidateManagedConfig accepts only the owned bootstrap structure plus dynamic
// children of its single managed scope. It never repairs or reloads live state.
func ValidateManagedConfig(data []byte, c v1.CaddyConfig, storage string) error {
	expected, err := ManagedConfig(c, storage)
	if err != nil {
		return err
	}
	var actual, baseline any
	if json.Unmarshal(data, &actual) != nil || json.Unmarshal(expected, &baseline) != nil {
		return diagnostic("gateway_state", "managed saved configuration is invalid; preserved")
	}
	var scopes []scope
	findScopes(actual, "/config", c.Scope, &scopes)
	if len(scopes) != 1 {
		return diagnostic("gateway_state", "managed saved scope is missing or duplicated; preserved")
	}
	if _, ok := scopes[0].object["routes"].([]any); !ok {
		return diagnostic("gateway_state", "managed saved route array is missing; preserved")
	}
	scopes[0].object["routes"] = []any{}
	if !equalJSON(actual, baseline) {
		return diagnostic("gateway_state", "managed saved configuration changed outside owned routes; preserved")
	}
	return nil
}
