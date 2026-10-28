package plan

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	v1 "github.com/octacian/backlot/api/v1"
	"github.com/octacian/backlot/internal/manifest"
)

func TestDeclarationDiscoveryBeforeSetup(t *testing.T) {
	project, machine := setupFixture(t)
	// Deliberately invalid caller sources cannot affect declaration-only discovery.
	t.Setenv("BACKLOT_PROJECT_CONFIG_DIR", "invalid-registry")
	writeSetup(t, machine, "invalid machine")
	m := v1.Manifest{}
	source, err := os.ReadFile(filepath.Join(project, "backlot.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if err := manifest.Decode(source, ".yaml", &m); err != nil {
		t.Fatal(err)
	}
	c := m.Components["check"]
	c.Command.Args = []string{"argument-canary"}
	c.EnvironmentSets = []string{"base"}
	c.Environment.Seeds = []string{"missing.env"}
	literal := "literal-canary"
	c.Environment.Assign["VALUE"] = v1.Value{Literal: &literal}
	c.Fixtures = map[string]v1.Fixture{"token": {Value: v1.Value{Ref: &v1.Reference{Kind: "input", Name: "token"}}}, "literal": {Value: v1.Value{Literal: &literal}}}
	c.Environment.Required = []string{"VALUE"}
	m.Components["check"] = c
	m.Environments = map[string]v1.Environment{"base": {PassThrough: []string{"OPTIONAL"}, Seeds: []string{"missing.env"}, Assign: map[string]v1.Value{"TOKEN": {Ref: &v1.Reference{Kind: "input", Name: "token"}}}}}
	m.Tools["shell"] = "missing-executable"
	data, _ := json.Marshal(m)
	if err := os.Remove(filepath.Join(project, "backlot.yaml")); err != nil {
		t.Fatal(err)
	}
	writeSetup(t, filepath.Join(project, "backlot.json"), string(data))
	list, err := ListScenes(project)
	if err != nil || len(list.Scenes) != 1 || list.Scenes[0].TerminalJob != "check" {
		t.Fatal(list, err)
	}
	r, err := DescribeScene(project, "test")
	if err != nil || len(r.Components) != 1 || !reflect.DeepEqual(r.Prerequisites.Tools, []string{"shell"}) || !reflect.DeepEqual(r.Prerequisites.Seeds, []string{"missing.env"}) || len(r.Prerequisites.Inputs) != 1 || !r.Prerequisites.Inputs[0].Secret || r.Prerequisites.Inputs[0].Generate != "random-hex-32" || len(r.Prerequisites.Required) != 1 || len(r.Prerequisites.PassThrough) != 1 {
		t.Fatal(r, err)
	}
	encoded, _ := json.Marshal(r)
	for _, forbidden := range []string{"literal-canary", "argument-canary", "missing-executable", "configured", "revealed_value"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatal("unsafe or invented prerequisite fact", string(encoded))
		}
	}
	if _, err := DescribeScene(project, "absent"); err == nil {
		t.Fatal("unknown scene accepted")
	}
	// Invalid unselected declarations must fail, even when the selected graph is valid.
	m.Scenes["invalid"] = v1.Scene{Lifetime: v1.Disposable, Components: []string{"absent"}, TerminalJob: "absent"}
	data, _ = json.Marshal(m)
	writeSetup(t, filepath.Join(project, "backlot.json"), string(data))
	if _, err := ListScenes(project); err == nil {
		t.Fatal("invalid unselected scene ignored")
	}
	if _, err := DescribeScene(project, "test"); err == nil {
		t.Fatal("invalid unselected scene ignored")
	}
}

func TestDiscoverySelectedGraphAndProviders(t *testing.T) {
	r, err := DescribeScene("../../examples/build-images", "test")
	if err != nil || len(r.Components) != 2 || r.Components[0].Name != "build" || r.Components[1].DependsOn[0].Condition != v1.Completed || r.BuildImages["app"].Producer != "build" || !r.Prerequisites.Docker {
		t.Fatal(r, err)
	}
	r, err = DescribeScene("../../examples/macos-quickstart", "dev")
	if err != nil || !r.Prerequisites.Caddy || r.Publication == nil || len(r.Publication.Routes) == 0 || len(r.Resources) == 0 {
		t.Fatal(r, err)
	}
	for _, kind := range []string{"volume", "network"} {
		t.Run(kind, func(t *testing.T) {
			project, _ := setupFixture(t)
			fixture := strings.Replace(setupManifest, "test: {", "test: {resources: [selected], ", 1)
			fixture += "\nresources:\n selected: {kind: " + kind + "}\n"
			writeSetup(t, filepath.Join(project, "backlot.yaml"), fixture)
			r, err := DescribeScene(project, "test")
			if err != nil || !r.Prerequisites.Docker {
				t.Fatal(r, err)
			}
		})
	}
}

func TestConfigurationUsesCapturedLayersWithoutValues(t *testing.T) {
	project, machine := setupFixture(t)
	saveSetup(t, openSetup(t, v1.ProjectSetupRequest{ProjectPath: project, ConfigPath: machine, Edit: true}), map[string]string{"token": "private-canary", "unused": "unused-canary"}, map[string]string{"shell": "not-installed"})
	writeSetup(t, machine, `{"version":"backlot/v1","tools":{"shell":"machine-not-installed"},"docker":{"endpoint":"unix:///absent/docker.sock"},"storage":{"directory":"/declared-only","retention_age":"1h","retention_bytes":123}}`)
	r, err := Configuration(project, machine)
	if err != nil || !r.MachinePresent || !r.PrivatePresent || !r.Inputs[0].Configured || r.Tools[0].SourceLayer != "private" || r.Tools[0].Candidate != "not-installed" || r.Docker.PublishAddress != "127.0.0.1" || r.DeclaredStorage.Directory != "/declared-only" {
		t.Fatal(r, err)
	}
	data, _ := json.Marshal(r)
	if strings.Contains(string(data), "private-canary") || strings.Contains(string(data), "unused-canary") || strings.Contains(string(data), "digest") {
		t.Fatal("private values exposed", string(data))
	}
	writeSetup(t, machine, `{"version":"backlot/v1","tools":{"typo":"sh"}}`)
	if _, err := Configuration(project, machine); err == nil {
		t.Fatal("invalid membership accepted")
	}
}
