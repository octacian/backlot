package plan

import (
	"encoding/json"
	v1 "github.com/octacian/backlot/api/v1"
	"github.com/octacian/backlot/internal/manifest"
	"os"
	"path/filepath"
	"testing"
)

func TestBuildImagesRemainSymbolicOffline(t *testing.T) {
	noDefaultConfig(t)
	root := t.TempDir()
	data, err := os.ReadFile("../../examples/build-images/backlot.yaml")
	if err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(root, "backlot.yaml"), data)
	config := filepath.Join(root, "machine.json")
	write(t, config, []byte(`{"version":"backlot/v1","docker":{"endpoint":"unix:///nonexistent-backlot-build-image.sock"}}`))
	result, err := Resolve(v1.PlanRequest{ProjectPath: root, Scene: "test", ConfigPath: config})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.BuildImages) != 1 || result.BuildImages["app"].Producer != "build" || result.Components[0].Environment["APP_IMAGE"].Symbolic == nil || result.Components[1].Image.Symbolic == nil {
		t.Fatalf("lost symbolic handoff: %+v", result)
	}
	if result.Components[0].Environment["APP_IMAGE"].Symbolic.Field != "tag" || result.Components[1].Image.Symbolic.Field != "id" {
		t.Fatal("wrong reference fields")
	}
	var m v1.Manifest
	if err = manifest.Decode(data, ".yaml", &m); err != nil {
		t.Fatal(err)
	}
	m.Components["noop"] = v1.Component{Kind: v1.Job, Runtime: v1.Native, Policy: v1.EachStart, Command: &v1.Command{Tool: "sh", Args: []string{"-c", "exit 0"}}}
	m.Scenes["host"] = v1.Scene{Lifetime: v1.Persistent, Components: []string{"noop"}}
	raw, _ := json.Marshal(m)
	if err = os.Remove(filepath.Join(root, "backlot.yaml")); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(root, "backlot.json"), raw)
	host, err := Resolve(v1.PlanRequest{ProjectPath: root, Scene: "host"})
	if err != nil {
		t.Fatal(err)
	}
	if len(host.BuildImages) != 0 {
		t.Fatal("unselected build-image declarations leaked into runtime plan")
	}
}
