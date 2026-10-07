package manifest

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"unicode"

	v1 "github.com/octacian/backlot/api/v1"
	"go.yaml.in/yaml/v3"
)

func TestMapKeyDiagnostics(t *testing.T) {
	for _, key := range []string{"\x1b[31msecret-canary\nFORGED", strings.Repeat("secret-canary", 10), "bad.key"} {
		for _, field := range []string{"tools", "inputs", "resources", "outputs", "environments", "components", "scenes"} {
			for _, format := range []string{".json", ".yaml"} {
				t.Run(field+format+key[:3], func(t *testing.T) {
					data := map[string]any{field: map[string]any{key: []any{23}}}
					var encoded []byte
					var err error
					if format == ".json" {
						encoded, err = json.Marshal(data)
					} else {
						encoded, err = yaml.Marshal(data)
					}
					if err != nil {
						t.Fatal(err)
					}
					var m v1.Manifest
					err = Decode(encoded, format, &m)
					assertSafeDiagnostic(t, err, field)
				})
			}
		}
	}
	for _, format := range []string{".json", ".yaml"} {
		data := map[string]any{"environments": map[string]any{"valid": map[string]any{"assign": map[string]any{"\x1bsecret-canary\n": map[string]any{"literal": 23}}}}}
		var encoded []byte
		if format == ".json" {
			encoded, _ = json.Marshal(data)
		} else {
			encoded, _ = yaml.Marshal(data)
		}
		var m v1.Manifest
		assertSafeDiagnostic(t, Decode(encoded, format, &m), "environments.valid.assign")
	}
	for _, key := range []string{"\x1bsecret-canary\n", strings.Repeat("secret-canary", 10)} {
		m := adoption(t)
		m.Tools[key] = "go"
		assertSafeDiagnostic(t, Validate(m), "tools")
		c := v1.MachineConfig{Version: v1.ManifestVersion, Inputs: map[string]string{key: "secret-value"}}
		assertSafeDiagnostic(t, ValidateMachine(c), "config.inputs")
	}
	m := adoption(t)
	m.Environments = map[string]v1.Environment{"valid": {Assign: map[string]v1.Value{strings.Repeat("A", 10000): {}}}}
	assertSafeDiagnostic(t, Validate(m), "environments.valid.assign")
	var config v1.MachineConfig
	err := Decode([]byte(`{"inputs":{"valid":23}}`), ".json", &config)
	if err == nil || !strings.Contains(err.Error(), "$.inputs.valid") {
		t.Fatalf("valid key context lost: %v", err)
	}
}

func assertSafeDiagnostic(t *testing.T, err error, context string) {
	t.Helper()
	var p *v1.PlanError
	if !errors.As(err, &p) {
		t.Fatalf("expected PlanError: %v", err)
	}
	for _, s := range []string{p.Code, p.Field, p.Message, p.Error()} {
		if strings.Contains(s, "secret-canary") || len(s) > 512 || strings.IndexFunc(s, unicode.IsControl) >= 0 {
			t.Fatalf("unsafe diagnostic: %q", s)
		}
	}
	if !strings.Contains(p.Field, context) {
		t.Fatalf("context lost: %+v", p)
	}
}

func TestNetworkDestinationPorts(t *testing.T) {
	for _, port := range []string{"0", "65536", "999999999999999999999", "-1", "+1", "http", "1x", ""} {
		for _, kind := range []string{"tcp", "http", "caddy"} {
			t.Run(kind+"/invalid/"+port, func(t *testing.T) { checkDestination(t, kind, port, false) })
		}
	}
	for _, port := range []string{"1", "65535", "080"} {
		for _, kind := range []string{"tcp", "http", "caddy"} {
			t.Run(kind+"/valid/"+port, func(t *testing.T) { checkDestination(t, kind, port, true) })
		}
	}
	for _, endpoint := range []string{"http://localhost/health", "https://[::1]/health"} {
		m := adoption(t)
		c := m.Components["server"]
		c.Readiness = &v1.Probe{Kind: "http", Timeout: "1s", Target: &v1.Value{Literal: &endpoint}}
		m.Components["server"] = c
		if err := Validate(m); err != nil {
			t.Fatal(err)
		}
		config := v1.MachineConfig{Version: v1.ManifestVersion, Caddy: &v1.CaddyConfig{Endpoint: endpoint, Scope: "backlot", DomainSuffix: "example.test", HostAddress: "localhost"}}
		if err := ValidateMachine(config); err != nil {
			t.Fatal(err)
		}
	}
}

func checkDestination(t *testing.T, kind, port string, valid bool) {
	t.Helper()
	target := "[::1]:" + port
	if kind != "tcp" {
		target = "https://[::1]:" + port + "/health"
	}
	var err error
	if kind == "caddy" {
		err = ValidateMachine(v1.MachineConfig{Version: v1.ManifestVersion, Caddy: &v1.CaddyConfig{Endpoint: target, Scope: "backlot", DomainSuffix: "example.test", HostAddress: "localhost"}})
	} else {
		m := adoption(t)
		c := m.Components["server"]
		c.Readiness = &v1.Probe{Kind: kind, Timeout: "1s", Target: &v1.Value{Literal: &target}}
		m.Components["server"] = c
		err = Validate(m)
	}
	if (err == nil) != valid {
		t.Fatalf("valid=%v, error=%v", valid, err)
	}
}
