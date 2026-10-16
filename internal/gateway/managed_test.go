package gateway

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	v1 "github.com/octacian/backlot/api/v1"
	"github.com/octacian/backlot/internal/manifest"
)

func TestManagedSavedBoundary(t *testing.T) {
	c := manifest.CaddyDefaults(v1.CaddyConfig{Mode: "managed", Executable: "/explicit/caddy"})
	data, err := ManagedConfig(c, "/private/owned/data")
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateManagedConfig(data, c, "/private/owned/data"); err != nil {
		t.Fatal(err)
	}
	var root any
	if err := json.Unmarshal(data, &root); err != nil {
		t.Fatal(err)
	}
	var scopes []scope
	findScopes(root, "/config", c.Scope, &scopes)
	scopes[0].object["routes"] = []any{map[string]any{"@id": "retained-effect", "handle": []any{map[string]any{"handler": "static_response", "body": "preserve"}}}}
	saved, _ := json.Marshal(root)
	if err := ValidateManagedConfig(saved, c, "/private/owned/data"); err != nil {
		t.Fatal("dynamic effects refused", err)
	}
	object := root.(map[string]any)
	object["admin"].(map[string]any)["listen"] = "0.0.0.0:2019"
	foreign, _ := json.Marshal(root)
	if ValidateManagedConfig(foreign, c, "/private/owned/data") == nil {
		t.Fatal("changed ownership boundary accepted")
	}
	if ValidateManagedConfig(data, c, "/different/storage") == nil {
		t.Fatal("changed CA storage accepted")
	}
}

func TestManagedExposureBoundary(t *testing.T) {
	for _, tc := range []struct {
		address   string
		listeners []string
	}{
		{"", []string{"127.0.0.1:8443", "[::1]:8443"}},
		{"0.0.0.0", []string{"tcp4/0.0.0.0:8443", "[::1]:8443"}},
		{"172.17.0.1", []string{"127.0.0.1:8443", "[::1]:8443", "172.17.0.1:8443"}},
	} {
		t.Run(tc.address, func(t *testing.T) {
			c := manifest.CaddyDefaults(v1.CaddyConfig{Mode: "managed", Executable: "/explicit/caddy", HTTPSBindAddress: tc.address})
			data, err := ManagedConfig(c, "/private/owned/data")
			if err != nil {
				t.Fatal(err)
			}
			var root map[string]any
			if err := json.Unmarshal(data, &root); err != nil {
				t.Fatal(err)
			}
			if root["admin"].(map[string]any)["listen"] != "127.0.0.1:2020" {
				t.Fatal("admin exposure widened")
			}
			if !reflect.DeepEqual(ManagedHTTPSListeners(c), tc.listeners) {
				t.Fatal("HTTPS exposure mismatch")
			}
			if err := ValidateManagedConfig(data, c, "/private/owned/data"); err != nil {
				t.Fatal(err)
			}
			if tc.address != "" {
				c.HTTPSBindAddress = ""
				if ValidateManagedConfig(data, c, "/private/owned/data") == nil {
					t.Fatal("changed exposure accepted on resume")
				}
			}
		})
	}
}

func TestManagedSavedIPv4WildcardRequiresFamily(t *testing.T) {
	c := manifest.CaddyDefaults(v1.CaddyConfig{Mode: "managed", Executable: "/explicit/caddy", HTTPSBindAddress: "0.0.0.0"})
	data, err := ManagedConfig(c, "/private/data")
	if err != nil {
		t.Fatal(err)
	}
	unsafe := strings.Replace(string(data), "tcp4/0.0.0.0:", "0.0.0.0:", 1)
	if ValidateManagedConfig([]byte(unsafe), c, "/private/data") == nil {
		t.Fatal("unsafe generic wildcard saved config accepted")
	}
	bindings := ManagedHTTPSBindings(c)
	if bindings[0].Network != "tcp4" || bindings[0].Address != "0.0.0.0:8443" {
		t.Fatal("preflight network differs from Caddy", bindings)
	}
}
