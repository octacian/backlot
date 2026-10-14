package gateway

import (
	"encoding/json"
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
