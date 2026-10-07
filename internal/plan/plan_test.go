package plan

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	v1 "github.com/octacian/backlot/api/v1"
	"github.com/octacian/backlot/internal/manifest"
)

func write(t *testing.T, file string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func fixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, file := range []string{"backlot.yaml", "defaults.env"} {
		data, err := os.ReadFile("../../examples/adoption/" + file)
		if err != nil {
			t.Fatal(err)
		}
		write(t, filepath.Join(root, file), data)
	}
	resolved, err := canonical(root)
	if err != nil {
		t.Fatal(err)
	}
	return resolved
}

func noDefaultConfig(t *testing.T) {
	t.Helper()
	root := t.TempDir()
	t.Setenv("HOME", root)
	t.Setenv("XDG_CONFIG_HOME", root)
	t.Setenv("APPDATA", root)
}

func TestPlanSuccessSymbolicRedactedAndReadOnly(t *testing.T) {
	noDefaultConfig(t)
	root := fixture(t)
	before, err := os.ReadFile(filepath.Join(root, "defaults.env"))
	if err != nil {
		t.Fatal(err)
	}
	result, err := Resolve(v1.PlanRequest{ProjectPath: root, Scene: "test", TerminalArgs: []string{"-run", "TestOrders", "--json", "a b"}})
	if err != nil {
		t.Fatal(err)
	}
	if result.APIVersion != v1.Version || result.Lifetime != v1.Disposable || len(result.ManifestDigest) != 64 || result.ConfigDigest != "" || len(result.Components) != 1 {
		t.Fatalf("invalid plan: %+v", result)
	}
	c := result.Components[0]
	if !reflect.DeepEqual(c.Command.Args, []string{"test", "./...", "-run", "TestOrders", "--json", "a b"}) {
		t.Fatalf("args=%v", c.Command.Args)
	}
	if c.Environment["WORK_DIR"].Symbolic == nil || c.Environment["MODE"].Literal == nil || *c.Environment["MODE"].Literal != "development" {
		t.Fatalf("env=%+v", c.Environment)
	}
	if !c.Environment["EXAMPLE_LITERAL"].Redacted {
		t.Fatal("seed value is not redacted")
	}
	after, err := os.ReadFile(filepath.Join(root, "defaults.env"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("seed was rewritten")
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatal("planner wrote checkout files")
	}
	dev, err := Resolve(v1.PlanRequest{ProjectPath: root, Scene: "dev"})
	if err != nil {
		t.Fatal(err)
	}
	if dev.Components[0].Name != "build" || dev.Components[1].Name != "server" || dev.Components[1].Environment["PORT"].Symbolic == nil || dev.Outputs["build"].ConcurrencyGroup != "build" {
		t.Fatalf("invalid dev plan: %+v", dev)
	}
}

func TestEnvironmentLayeringAndLiteralSeeds(t *testing.T) {
	root, err := canonical(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(root, "executed")
	first := "MODE=first\nFROM_FIRST=value\nQUOTED=\"quotes stay\"\nLITERAL=$(touch " + marker + ")\nHASH=value # literal\n"
	write(t, filepath.Join(root, "first.env"), []byte(first))
	write(t, filepath.Join(root, "last.env"), []byte("MODE=last\n"))
	t.Setenv("MODE", "inherited")
	t.Setenv("UNSELECTED_CANARY", "ambient-secret")
	explicit := "explicit"
	m := v1.Manifest{Environments: map[string]v1.Environment{"shared": {PassThrough: []string{"MODE"}, Seeds: []string{"first.env"}, Assign: map[string]v1.Value{"MODE": {Literal: &explicit}}}}}
	c := v1.Component{EnvironmentSets: []string{"shared"}, Environment: v1.Environment{Seeds: []string{"last.env"}, Required: []string{"MODE", "FROM_FIRST"}}}
	values, err := resolveEnvironmentValues(m, v1.MachineConfig{}, c, root, v1.PlanResponse{})
	if err != nil {
		t.Fatal(err)
	}
	if *values["MODE"].literal != "explicit" || *values["QUOTED"].literal != "\"quotes stay\"" || *values["HASH"].literal != "value # literal" || *values["LITERAL"].literal != "$(touch "+marker+")" {
		t.Fatalf("incorrect layers: %+v", values)
	}
	if _, ok := values["UNSELECTED_CANARY"]; ok {
		t.Fatal("copied full process environment")
	}
	delete(m.Environments["shared"].Assign, "MODE")
	values, err = resolveEnvironmentValues(m, v1.MachineConfig{}, c, root, v1.PlanResponse{})
	if err != nil {
		t.Fatal(err)
	}
	if *values["MODE"].literal != "last" {
		t.Fatal("seed order lost")
	}
	c.Environment.Seeds = nil
	shared := m.Environments["shared"]
	shared.Seeds = nil
	m.Environments["shared"] = shared
	c.Environment.Required = []string{"MODE"}
	values, err = resolveEnvironmentValues(m, v1.MachineConfig{}, c, root, v1.PlanResponse{})
	if err != nil {
		t.Fatal(err)
	}
	if *values["MODE"].literal != "inherited" {
		t.Fatal("pass-through not applied")
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("seed executed a command")
	}
}

func TestPlanningFailuresAndRecovery(t *testing.T) {
	noDefaultConfig(t)
	root := fixture(t)
	for _, request := range []v1.PlanRequest{{ProjectPath: root, Scene: "missing"}, {ProjectPath: root, Scene: "dev", TerminalArgs: []string{"extra"}}, {ProjectPath: root, Scene: "test", ConfigPath: filepath.Join(root, "missing")}} {
		if _, err := Resolve(request); err == nil {
			t.Fatal("invalid request accepted")
		}
	}
	configFile := filepath.Join(root, "machine.json")
	write(t, configFile, []byte(`{"version":"backlot/v1","tools":{"go":"missing-backlot-executable-canary"}}`))
	_, err := Resolve(v1.PlanRequest{ProjectPath: root, Scene: "test", ConfigPath: configFile})
	var detail *v1.PlanError
	if !errors.As(err, &detail) || detail.Code != "missing_tool" || !strings.Contains(err.Error(), "override") {
		t.Fatalf("missing tool error=%v", err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	config := v1.MachineConfig{Version: v1.ManifestVersion, Tools: map[string]string{"go": executable}}
	encoded, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	write(t, configFile, encoded)
	if _, err := Resolve(v1.PlanRequest{ProjectPath: root, Scene: "test", ConfigPath: configFile}); err != nil {
		t.Fatal(err)
	} // The test binary would recurse if executed.
	write(t, filepath.Join(root, "defaults.env"), []byte("MODE=\n"))
	data, err := os.ReadFile(filepath.Join(root, "backlot.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var m v1.Manifest
	if err := manifest.Decode(data, ".yaml", &m); err != nil {
		t.Fatal(err)
	}
	environment := m.Environments["app"]
	environment.Assign = nil
	m.Environments["app"] = environment
	encoded, err = json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(root, "backlot.yaml"), encoded)
	if _, err := Resolve(v1.PlanRequest{ProjectPath: root, Scene: "test"}); err == nil || !strings.Contains(err.Error(), "missing or empty") {
		t.Fatalf("missing required seed value: %v", err)
	}
	write(t, filepath.Join(root, "defaults.env"), []byte("MODE=recovered\n"))
	if _, err := Resolve(v1.PlanRequest{ProjectPath: root, Scene: "test"}); err != nil {
		t.Fatal(err)
	}
}

func TestSecretSafetyAndProviderSelection(t *testing.T) {
	noDefaultConfig(t)
	data, err := os.ReadFile("../../examples/fullstack/backlot.yaml")
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	write(t, filepath.Join(root, "backlot.yaml"), data)
	write(t, filepath.Join(root, "web.env"), []byte("NODE_ENV=production\n"))
	request := v1.PlanRequest{ProjectPath: root, Scene: "backend-test"}
	if _, err := Resolve(request); err == nil || !strings.Contains(err.Error(), "config.docker") {
		t.Fatalf("missing provider: %v", err)
	}
	c := v1.MachineConfig{Version: v1.ManifestVersion, Docker: &v1.DockerConfig{Endpoint: "unix:///unused.sock"}}
	configFile := filepath.Join(root, "machine.json")
	encoded, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	write(t, configFile, encoded)
	request.ConfigPath = configFile
	p, err := Resolve(request)
	if err != nil {
		t.Fatal(err)
	}
	for _, component := range p.Components {
		if v, ok := component.Environment["DB_PASSWORD"]; ok && (!v.Redacted || v.Symbolic != nil || v.Literal != nil) {
			t.Fatal("generated secret leaked")
		}
	}
	request.Scene = "production"
	if _, err := Resolve(request); err == nil || !strings.Contains(err.Error(), "config.caddy") {
		t.Fatalf("missing Caddy: %v", err)
	}
	c.Caddy = &v1.CaddyConfig{Endpoint: "http://localhost:2019", Scope: "backlot", DomainSuffix: "preview.example.test", HostAddress: "host.docker.internal"}
	c.Tools = map[string]string{}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	c.Tools["npm"] = executable
	encoded, err = json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	write(t, configFile, encoded)
	if _, err := Resolve(request); err == nil || !strings.Contains(err.Error(), "inputs.app_image") {
		t.Fatalf("missing input: %v", err)
	}
	c.Inputs = map[string]string{"app_image": "secret-canary"}
	var m v1.Manifest
	if err := manifest.Decode(data, ".yaml", &m); err != nil {
		t.Fatal(err)
	}
	m.Inputs["app_image"] = v1.Input{Secret: true}
	literal := "literal-secret-canary"
	backend := m.Components["backend"]
	backend.Environment.Assign["SECRET"] = v1.Value{Literal: &literal, Secret: true}
	m.Components["backend"] = backend
	encoded, err = json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(root, "backlot.yaml"), encoded)
	encoded, err = json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	write(t, configFile, encoded)
	p, err = Resolve(request)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err = json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(encoded, []byte("secret-canary")) {
		t.Fatal("ordinary plan contains secret literal")
	}
	var roundtrip v1.PlanResponse
	if err := json.Unmarshal(encoded, &roundtrip); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(p, roundtrip) {
		t.Fatal("plan shared contract roundtrip differs")
	}
}

func TestSeedSecurityAndDiscovery(t *testing.T) {
	noDefaultConfig(t)
	for _, data := range []string{"export TOKEN=secret-canary", "TOKEN=secret-canary\nTOKEN=second", "broken secret-canary", "TOKEN=bad\x00value"} {
		if _, err := parseSeed([]byte(data), "seed"); err == nil || strings.Contains(err.Error(), "secret-canary") {
			t.Fatalf("unsafe seed error: %v", err)
		}
	}
	root := fixture(t)
	child := filepath.Join(root, "nested")
	if err := os.MkdirAll(child, 0o755); err != nil {
		t.Fatal(err)
	}
	file, project, err := Discover(child, "")
	if err != nil || project != root || file != filepath.Join(root, "backlot.yaml") {
		t.Fatalf("discovery %s %s %v", file, project, err)
	}
	write(t, filepath.Join(child, ".git"), []byte("gitdir: unused"))
	if _, _, err := Discover(child, ""); err == nil {
		t.Fatal("crossed checkout boundary")
	}
	write(t, filepath.Join(root, "backlot.json"), []byte("{}"))
	if _, _, err := Discover(child, root); err == nil || !strings.Contains(err.Error(), "multiple manifests") {
		t.Fatalf("ambiguous manifests accepted: %v", err)
	}
	if err := os.Remove(filepath.Join(root, "backlot.json")); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(root, alias); err != nil {
		t.Fatal(err)
	}
	_, project, err = Discover(child, alias)
	if err != nil || project != root {
		t.Fatalf("alias identity: %s %v", project, err)
	}
	external := filepath.Join(t.TempDir(), "outside.env")
	write(t, external, []byte("SECRET=secret-canary"))
	if err := os.Remove(filepath.Join(root, "defaults.env")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(external, filepath.Join(root, "defaults.env")); err != nil {
		t.Fatal(err)
	}
	if _, err := Resolve(v1.PlanRequest{ProjectPath: root, Scene: "test"}); err == nil || !strings.Contains(err.Error(), "escapes") {
		t.Fatalf("seed symlink escape accepted: %v", err)
	}
}

func TestContainerTerminalAndOutputEscape(t *testing.T) {
	noDefaultConfig(t)
	root := fixture(t)
	data, err := os.ReadFile(filepath.Join(root, "backlot.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var m v1.Manifest
	if err := manifest.Decode(data, ".yaml", &m); err != nil {
		t.Fatal(err)
	}
	image := "example/test:local"
	c := m.Components["test"]
	c.Runtime = v1.Container
	c.Command = nil
	c.Image = &v1.Value{Literal: &image}
	c.Args = []string{"test"}
	m.Components["test"] = c
	encoded, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(root, "backlot.yaml"), encoded)
	config := filepath.Join(root, "machine.yaml")
	write(t, config, []byte("version: backlot/v1\ndocker: {endpoint: 'unix:///unused.sock'}\n"))
	p, err := Resolve(v1.PlanRequest{ProjectPath: root, ConfigPath: config, Scene: "test", TerminalArgs: []string{"--grep", "orders"}})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(p.Components[0].Args, []string{"test", "--grep", "orders"}) {
		t.Fatalf("container args=%v", p.Components[0].Args)
	}
	if err := os.Symlink(t.TempDir(), filepath.Join(root, "dist")); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"dist", "dist/new/deep"} {
		m.Outputs["build"] = v1.Output{Path: path, ConcurrencyGroup: "build"}
		encoded, err := json.Marshal(m)
		if err != nil {
			t.Fatal(err)
		}
		write(t, filepath.Join(root, "backlot.yaml"), encoded)
		if _, err := Resolve(v1.PlanRequest{ProjectPath: root, Scene: "dev"}); err == nil || !strings.Contains(err.Error(), "output symlink escapes") {
			t.Fatalf("output escape accepted for %s: %v", path, err)
		}
		if _, err := Resolve(v1.PlanRequest{ProjectPath: root, ConfigPath: config, Scene: "test"}); err != nil {
			t.Fatalf("unselected output blocked planning: %v", err)
		}
	}
}

func TestOutputCanonicalOverlap(t *testing.T) {
	noDefaultConfig(t)
	for _, tc := range []struct {
		name, first, second                 string
		sameGroup, separateScenes, existing bool
		conflict                            bool
	}{
		{name: "equal missing suffix", first: "real/new/deep", second: "alias/new/deep", conflict: true},
		{name: "nested missing suffix", first: "real/new", second: "alias/new/deep", conflict: true},
		{name: "reverse nested", first: "alias/new/deep", second: "real/new", conflict: true},
		{name: "chained alias", first: "real/new", second: "chain/new", conflict: true},
		{name: "ordinary missing ancestors", first: "missing/one/deep", second: "alias/new"},
		{name: "existing equal", first: "real", second: "alias", existing: true, conflict: true},
		{name: "separate scenes", first: "real/new", second: "alias/new", separateScenes: true, conflict: true},
		{name: "same group equal", first: "real/new", second: "alias/new", sameGroup: true},
		{name: "same group nested", first: "real/new", second: "alias/new/deep", sameGroup: true},
		{name: "distinct siblings", first: "real/new", second: "alias/newer"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := fixture(t)
			if err := os.Mkdir(filepath.Join(root, "real"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink("real", filepath.Join(root, "alias")); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink("alias", filepath.Join(root, "chain")); err != nil {
				t.Fatal(err)
			}
			group := "two"
			if tc.sameGroup {
				group = "one"
			}
			m := v1.Manifest{
				Version: v1.ManifestVersion, Project: "aliases", Tools: map[string]string{"go": "go"},
				Outputs: map[string]v1.Output{"first": {Path: tc.first, ConcurrencyGroup: "one"}, "second": {Path: tc.second, ConcurrencyGroup: group}},
				Components: map[string]v1.Component{
					"first":  {Kind: v1.Job, Runtime: v1.Native, Policy: v1.EachStart, Command: &v1.Command{Tool: "go"}, Outputs: []string{"first"}},
					"second": {Kind: v1.Job, Runtime: v1.Native, Policy: v1.EachStart, Command: &v1.Command{Tool: "go"}, Outputs: []string{"second"}},
				},
				Scenes: map[string]v1.Scene{"both": {Lifetime: v1.Persistent, Components: []string{"first", "second"}}},
			}
			scenes := []string{"both"}
			if tc.separateScenes {
				m.Scenes = map[string]v1.Scene{"first": {Lifetime: v1.Persistent, Components: []string{"first"}}, "second": {Lifetime: v1.Persistent, Components: []string{"second"}}}
				scenes = []string{"first", "second"}
			}
			encoded, err := json.Marshal(m)
			if err != nil {
				t.Fatal(err)
			}
			write(t, filepath.Join(root, "backlot.yaml"), encoded)
			for _, scene := range scenes {
				result, err := Resolve(v1.PlanRequest{ProjectPath: root, Scene: scene})
				if tc.conflict {
					var planErr *v1.PlanError
					if !errors.As(err, &planErr) || planErr.Code != "invalid_output" || !strings.HasPrefix(planErr.Field, "outputs.") || !strings.Contains(planErr.Message, "share a concurrency group") {
						t.Fatalf("expected actionable canonical overlap error, got %v", err)
					}
				} else {
					if err != nil {
						t.Fatal(err)
					}
					if result.Outputs["first"] != m.Outputs["first"] || result.Outputs["second"] != m.Outputs["second"] {
						t.Fatalf("output declarations changed: %+v", result.Outputs)
					}
				}
			}
			if !tc.existing {
				if _, err := os.Lstat(filepath.Join(root, "real", "new")); !os.IsNotExist(err) {
					t.Fatalf("planner created output: %v", err)
				}
			}
		})
	}
}

func TestSuppliedConfigRequiresVersion(t *testing.T) {
	for _, location := range []string{"explicit-json", "explicit-yaml", "default-yaml"} {
		for _, version := range []string{"absent", "", "backlot/v2", "backlot/v1"} {
			t.Run(location+"/"+version, func(t *testing.T) {
				noDefaultConfig(t)
				root := fixture(t)
				request := v1.PlanRequest{ProjectPath: root, Scene: "test"}
				file := filepath.Join(t.TempDir(), "machine.json")
				if location != "explicit-json" {
					file = filepath.Join(t.TempDir(), "machine.yaml")
				}
				if location == "default-yaml" {
					dir, err := os.UserConfigDir()
					if err != nil {
						t.Fatal(err)
					}
					file = filepath.Join(dir, "backlot", "config.yaml")
				} else {
					request.ConfigPath = file
				}
				data := "{}"
				if version != "absent" {
					encoded, err := json.Marshal(v1.MachineConfig{Version: version})
					if err != nil {
						t.Fatal(err)
					}
					data = string(encoded)
				}
				write(t, file, []byte(data))
				_, err := Resolve(request)
				if version == v1.ManifestVersion {
					if err != nil {
						t.Fatal(err)
					}
				} else {
					var p *v1.PlanError
					if !errors.As(err, &p) || p.Field != "config.version" {
						t.Fatalf("expected version failure, got %v", err)
					}
				}
			})
		}
	}
	noDefaultConfig(t)
	if _, err := Resolve(v1.PlanRequest{ProjectPath: fixture(t), Scene: "test"}); err != nil {
		t.Fatalf("absent optional config: %v", err)
	}
}
