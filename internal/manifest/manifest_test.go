package manifest

import (
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"

	v1 "github.com/octacian/backlot/api/v1"
	"go.yaml.in/yaml/v3"
)

func adoption(t *testing.T) v1.Manifest {
	t.Helper()
	data, err := os.ReadFile("../../examples/adoption/backlot.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var m v1.Manifest
	if err := Decode(data, ".yaml", &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestEquivalentFormatsAndSharedContract(t *testing.T) {
	m := adoption(t)
	if err := Validate(m); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	var decoded v1.Manifest
	if err := Decode(data, ".json", &decoded); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(m, decoded) {
		t.Fatal("JSON and YAML differ")
	}
	if err := Validate(decoded); err != nil {
		t.Fatal(err)
	}
	yamlData, err := yaml.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	var yamlDecoded v1.Manifest
	if err := Decode(yamlData, ".yaml", &yamlDecoded); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(m, yamlDecoded) {
		t.Fatal("shared contract YAML roundtrip differs")
	}
}

func TestStrictDocuments(t *testing.T) {
	cases := []struct{ name, format, data string }{
		{"unknown", ".yaml", "version: backlot/v1\npassword: secret-canary"},
		{"case", ".json", `{"Version":"backlot/v1"}`},
		{"duplicate", ".yaml", "version: backlot/v1\nversion: secret-canary"},
		{"duplicate-json", ".json", `{"version":"backlot/v1","vers\u0069on":"secret-canary"}`},
		{"trailing-json", ".json", `{} {"password":"secret-canary"}`},
		{"trailing-yaml", ".yaml", "version: backlot/v1\n---\n{}"},
		{"trailing-empty", ".yaml", "{}\n---\n"},
		{"malformed", ".yaml", "version: [secret-canary"},
		{"null", ".json", `{"tools":null}`},
		{"typed", ".yaml", "project: 123"},
		{"alias", ".yaml", "tools: &tools {go: go}"},
		{"merge", ".yaml", "tools: {<<: {go: go}}"},
		{"tag", ".yaml", "project: !secret secret-canary"},
		{"mapping-tag", ".yaml", "tools: !custom {go: go}"},
		{"sequence-tag", ".yaml", "scenes: {test: {components: !custom [test]}}"},
		{"key-anchor", ".yaml", "&key project: adoption"},
		{"non-string-key", ".yaml", "tools: {true: go}"},
		{"nested-unknown", ".json", `{"components":{"web":{"bogus":"secret-canary"}}}`},
		{"oversize", ".yaml", strings.Repeat("x", MaxBytes+1)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var m v1.Manifest
			err := Decode([]byte(tc.data), tc.format, &m)
			if err == nil {
				t.Fatal("invalid document accepted")
			}
			if strings.Contains(err.Error(), "secret-canary") {
				t.Fatalf("secret leaked: %s", err)
			}
		})
	}
}

func TestManifestFailures(t *testing.T) {
	cases := map[string]func(*v1.Manifest){
		"version":           func(m *v1.Manifest) { m.Version = "backlot/v2" },
		"project":           func(m *v1.Manifest) { m.Project = "Invalid" },
		"kind":              func(m *v1.Manifest) { c := m.Components["test"]; c.Kind = "task"; m.Components["test"] = c },
		"runtime":           func(m *v1.Manifest) { c := m.Components["test"]; c.Runtime = "shell"; m.Components["test"] = c },
		"policy":            func(m *v1.Manifest) { c := m.Components["test"]; c.Policy = "retry"; m.Components["test"] = c },
		"lifetime":          func(m *v1.Manifest) { s := m.Scenes["test"]; s.Lifetime = "forever"; m.Scenes["test"] = s },
		"required-terminal": func(m *v1.Manifest) { s := m.Scenes["test"]; s.TerminalJob = ""; m.Scenes["test"] = s },
		"tool":              func(m *v1.Manifest) { delete(m.Tools, "go") },
		"resource":          func(m *v1.Manifest) { delete(m.Resources, "http") },
		"reference": func(m *v1.Manifest) {
			c := m.Components["test"]
			c.Environment.Assign["WORK_DIR"] = v1.Value{Ref: &v1.Reference{Kind: "resource", Name: "missing", Field: "path"}}
			m.Components["test"] = c
		},
		"reference-field": func(m *v1.Manifest) {
			c := m.Components["test"]
			c.Environment.Assign["WORK_DIR"] = v1.Value{Ref: &v1.Reference{Kind: "resource", Name: "work", Field: "value"}}
			m.Components["test"] = c
		},
		"reference-discriminator": func(m *v1.Manifest) {
			c := m.Components["test"]
			c.Environment.Assign["WORK_DIR"] = v1.Value{Ref: &v1.Reference{Kind: "template", Name: "work"}}
			m.Components["test"] = c
		},
		"reference-extra": func(m *v1.Manifest) { c := m.Components["test"]; c.Environment.Assign["WORK_DIR"].Ref.Port = "extra" },
		"value-conflict": func(m *v1.Manifest) {
			c := m.Components["test"]
			v := c.Environment.Assign["WORK_DIR"]
			s := "x"
			v.Literal = &s
			c.Environment.Assign["WORK_DIR"] = v
		},
		"unselected-resource": func(m *v1.Manifest) { s := m.Scenes["test"]; s.Resources = nil; m.Scenes["test"] = s },
		"unattached-resource": func(m *v1.Manifest) { c := m.Components["test"]; c.Resources = nil; m.Components["test"] = c },
		"missing-dependency":  func(m *v1.Manifest) { s := m.Scenes["dev"]; s.Components = []string{"server"}; m.Scenes["dev"] = s },
		"gate": func(m *v1.Manifest) {
			c := m.Components["server"]
			c.DependsOn[0].Condition = v1.Ready
			m.Components["server"] = c
		},
		"cycle": func(m *v1.Manifest) {
			c := m.Components["build"]
			c.DependsOn = []v1.Dependency{{Component: "server", Condition: v1.Ready}}
			m.Components["build"] = c
		},
		"unused-cycle": func(m *v1.Manifest) {
			delete(m.Scenes, "dev")
			c := m.Components["build"]
			c.DependsOn = []v1.Dependency{{Component: "server", Condition: v1.Ready}}
			m.Components["build"] = c
		},
		"duplicate-selection": func(m *v1.Manifest) {
			s := m.Scenes["test"]
			s.Components = append(s.Components, "test")
			m.Scenes["test"] = s
		},
		"output-conflict":          func(m *v1.Manifest) { m.Outputs["nested"] = v1.Output{Path: "dist/app", ConcurrencyGroup: "other"} },
		"output-traversal":         func(m *v1.Manifest) { m.Outputs["bad"] = v1.Output{Path: "../outside", ConcurrencyGroup: "build"} },
		"seed-traversal":           func(m *v1.Manifest) { m.Environments["app"] = v1.Environment{Seeds: []string{"../secret"}} },
		"fresh-without-generation": func(m *v1.Manifest) { c := m.Components["test"]; c.Policy = v1.FreshOnly; m.Components["test"] = c },
		"readiness":                func(m *v1.Manifest) { c := m.Components["server"]; c.Readiness = nil; m.Components["server"] = c },
		"deadline":                 func(m *v1.Manifest) { c := m.Components["server"]; c.Readiness.Timeout = "0s" },
		"probe-type": func(m *v1.Manifest) {
			c := m.Components["server"]
			c.Readiness.Target.Ref.Field = "host"
			c.Readiness.Target.Ref.Port = ""
		},
		"ungated-terminal-work": func(m *v1.Manifest) {
			s := m.Scenes["test"]
			s.Components = append(s.Components, "build")
			m.Scenes["test"] = s
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			m := adoption(t)
			mutate(&m)
			if err := Validate(m); err == nil {
				t.Fatal("invalid manifest accepted")
			}
		})
	}
}

func TestFullstackAndMachine(t *testing.T) {
	data, err := os.ReadFile("../../examples/fullstack/backlot.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var m v1.Manifest
	if err := Decode(data, ".yaml", &m); err != nil {
		t.Fatal(err)
	}
	if err := Validate(m); err != nil {
		t.Fatal(err)
	}
	data, err = os.ReadFile("../../examples/fullstack/machine.example.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var c v1.MachineConfig
	if err := Decode(data, ".yaml", &c); err != nil {
		t.Fatal(err)
	}
	if err := ValidateMachine(c); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*v1.MachineConfig){func(c *v1.MachineConfig) { c.Version = "v2" }, func(c *v1.MachineConfig) { c.Docker.Endpoint = "tcp://remote:2375" }, func(c *v1.MachineConfig) { c.Caddy.Scope = "" }, func(c *v1.MachineConfig) { c.Caddy.Endpoint = "http://user:secret-canary@localhost:2019" }, func(c *v1.MachineConfig) { c.Caddy.DomainSuffix = "*.example.test" }, func(c *v1.MachineConfig) {
		c.Storage = &v1.StorageConfig{Directory: "/tmp", RetentionAge: "0s", RetentionBytes: 1}
	}} {
		var copy v1.MachineConfig
		if err := Decode(data, ".yaml", &copy); err != nil {
			t.Fatal(err)
		}
		mutate(&copy)
		err := ValidateMachine(copy)
		if err == nil {
			t.Fatal("invalid config accepted")
		}
		if strings.Contains(err.Error(), "secret-canary") {
			t.Fatal("config secret leaked")
		}
	}
}

func TestResourceConflictsAndPorts(t *testing.T) {
	data, err := os.ReadFile("../../examples/fullstack/backlot.yaml")
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]func(*v1.Manifest){
		"initializer-conflict": func(m *v1.Manifest) {
			m.Components["other_init"] = m.Components["initialize"]
			s := m.Scenes["dev"]
			s.Components = append(s.Components, "other_init")
			m.Scenes["dev"] = s
		},
		"port-conflict": func(m *v1.Manifest) {
			c := m.Components["watch"]
			c.Ports["http"] = v1.ServicePort{Resource: "api_port"}
			c.Resources = append(c.Resources, "api_port")
			m.Components["watch"] = c
		},
		"container-port": func(m *v1.Manifest) {
			c := m.Components["database"]
			c.Ports["sql"] = v1.ServicePort{Resource: "db_port"}
			m.Components["database"] = c
		},
		"native-container-port": func(m *v1.Manifest) {
			c := m.Components["backend"]
			c.Ports["http"] = v1.ServicePort{Resource: "api_port", ContainerPort: 3000}
			m.Components["backend"] = c
		},
		"mount": func(m *v1.Manifest) {
			c := m.Components["database"]
			c.Mounts[0].Resource = "db_admin"
			m.Components["database"] = c
		},
		"route": func(m *v1.Manifest) { s := m.Scenes["dev"]; s.Publish.Routes[0].Port = "missing" },
		"origin-readiness-cycle": func(m *v1.Manifest) {
			c := m.Components["backend"]
			c.Readiness = &v1.Probe{Kind: "http", Target: &v1.Value{Ref: &v1.Reference{Kind: "resource", Name: "public", Field: "url"}}, Timeout: "30s"}
			m.Components["backend"] = c
		},
		"publication-probe": func(m *v1.Manifest) { s := m.Scenes["dev"]; s.Publish.Probe.Target.Ref.Field = "value" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			var m v1.Manifest
			if err := Decode(data, ".yaml", &m); err != nil {
				t.Fatal(err)
			}
			mutate(&m)
			if err := Validate(m); err == nil {
				t.Fatal("invalid resource contract accepted")
			}
		})
	}
}
