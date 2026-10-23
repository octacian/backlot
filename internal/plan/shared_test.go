package plan

import (
	"encoding/json"
	v1 "github.com/octacian/backlot/api/v1"
	"github.com/octacian/backlot/internal/manifest"
	"os"
	"path/filepath"
	"testing"
)

func TestSharedSelectionAndSecrets(t *testing.T) {
	noDefaultConfig(t)
	root := t.TempDir()
	m := v1.Manifest{Version: v1.ManifestVersion, Project: "shared", Tools: map[string]string{"true": "true"}, SharedServices: map[string]v1.SharedService{"sql": {Manager: "mariadb", Image: "mariadb:11.4", Storage: "volume", Admin: "generated", Port: 3306, ReadinessTimeout: "1m"}}, Resources: map[string]v1.Resource{"data": {Kind: "allocation", SharedService: "sql"}}, Components: map[string]v1.Component{}, Scenes: map[string]v1.Scene{}}
	m.Components["selected"] = v1.Component{Kind: v1.Job, Runtime: v1.Native, Policy: v1.EachStart, Command: &v1.Command{Tool: "true"}, Resources: []string{"data"}, Environment: v1.Environment{Assign: map[string]v1.Value{}}}
	c := m.Components["selected"]
	for _, field := range []string{"host", "port", "database", "username", "password"} {
		c.Environment.Assign[field] = v1.Value{Ref: &v1.Reference{Kind: "resource", Name: "data", Field: field}}
	}
	m.Components["selected"] = c
	m.Components["offline"] = v1.Component{Kind: v1.Job, Runtime: v1.Native, Policy: v1.EachStart, Command: &v1.Command{Tool: "true"}}
	m.Scenes["selected"] = v1.Scene{Lifetime: v1.Persistent, Components: []string{"selected"}, Resources: []string{"data"}}
	m.Scenes["offline"] = v1.Scene{Lifetime: v1.Persistent, Components: []string{"offline"}}
	save := func() {
		t.Helper()
		data, err := json.Marshal(m)
		if err != nil {
			t.Fatal(err)
		}
		write(t, filepath.Join(root, "backlot.json"), data)
	}
	save()
	// The endpoint need not exist: offline planning never contacts providers.
	config := filepath.Join(t.TempDir(), "machine.json")
	write(t, config, []byte(`{"version":"backlot/v1","docker":{"endpoint":"unix:///nonexistent/backlot-shared-test.sock"}}`))
	result, err := Resolve(v1.PlanRequest{ProjectPath: root, ConfigPath: config, Scene: "selected"})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.SharedServices) != 1 {
		t.Fatal("selected shared definition missing")
	}
	env := result.Components[0].Environment
	if !env["password"].Redacted || env["password"].Symbolic != nil {
		t.Fatal("allocation password is not automatically redacted")
	}
	for _, field := range []string{"host", "port", "database", "username"} {
		if env[field].Symbolic == nil || env[field].Literal != nil {
			t.Fatal("allocation resolved offline")
		}
	}
	before, _ := os.ReadDir(root)
	result, err = Resolve(v1.PlanRequest{ProjectPath: root, Scene: "offline"})
	if err != nil || len(result.SharedServices) != 0 {
		t.Fatal("unselected shared service requires provider", err)
	}
	after, _ := os.ReadDir(root)
	if len(before) != len(after) {
		t.Fatal("offline allocation wrote project")
	}
	for _, bad := range []v1.Resource{{Kind: "secret", SharedService: "sql"}, {Kind: "allocation", SharedService: "missing"}} {
		m.Resources["data"] = bad
		if manifest.Validate(m) == nil {
			t.Fatal("invalid shared resource accepted")
		}
	}
	m.Resources["data"] = v1.Resource{Kind: "allocation", SharedService: "sql"}
	c.Environment.Assign["password"] = v1.Value{Ref: &v1.Reference{Kind: "resource", Name: "data", Field: "admin_password"}}
	m.Components["selected"] = c
	if manifest.Validate(m) == nil {
		t.Fatal("administrative credential reference accepted")
	}
	c.Environment.Assign["password"] = v1.Value{Ref: &v1.Reference{Kind: "resource", Name: "data"}}
	m.Components["selected"] = c
	if manifest.Validate(m) == nil {
		t.Fatal("allocation reference without a field accepted")
	}

}
