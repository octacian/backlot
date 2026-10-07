package schema

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	v1 "github.com/octacian/backlot/api/v1"
	"github.com/octacian/backlot/internal/manifest"
	validator "github.com/santhosh-tekuri/jsonschema/v6"
	"go.yaml.in/yaml/v3"
)

func compile(t *testing.T, name string) *validator.Schema {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("../../schemas", name))
	if err != nil {
		t.Fatal(err)
	}
	doc, err := validator.UnmarshalJSON(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	c := validator.NewCompiler()
	// No network loader: every reference must resolve inside the artifact.
	if err := c.AddResource(name, doc); err != nil {
		t.Fatal(err)
	}
	s, err := c.Compile(name)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// yamlValue uses the project's existing YAML parser, preserving document field
// presence before JSON conversion. Marshaling a Go contract first would hide
// missing required fields by adding zero values and omitting empty fields.
func yamlValue(t *testing.T, data []byte) any {
	t.Helper()
	var value any
	if err := yaml.Unmarshal(data, &value); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	value, err = validator.UnmarshalJSON(bytes.NewReader(encoded))
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func TestEditorExamples(t *testing.T) {
	for _, tc := range []struct {
		path, schema string
		target       any
	}{
		{"../../examples/adoption/backlot.yaml", "manifest-v1.schema.json", &v1.Manifest{}},
		{"../../examples/fullstack/backlot.yaml", "manifest-v1.schema.json", &v1.Manifest{}},
		{"../../examples/fullstack/machine.example.yaml", "machine-config-v1.schema.json", &v1.MachineConfig{}},
	} {
		t.Run(tc.path, func(t *testing.T) {
			data, err := os.ReadFile(tc.path)
			if err != nil {
				t.Fatal(err)
			}
			if err := manifest.Decode(data, ".yaml", tc.target); err != nil {
				t.Fatal(err)
			}
			if err := compile(t, tc.schema).Validate(yamlValue(t, data)); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestEarlyInvalidMachine(t *testing.T) {
	s := compile(t, "machine-config-v1.schema.json")
	for _, data := range []string{
		"version: backlot/v2\n",
		"version: backlot/v1\nstorage: {directory: state, retention_age: 24h, retention_bytes: -1}\n",
		"version: backlot/v1\ndocker: {endpoint: tcp://remote:2375}\n",
	} {
		if err := s.Validate(yamlValue(t, []byte(data))); err == nil {
			t.Fatalf("accepted invalid config: %s", data)
		}
	}
}

func TestGeneratedArtifacts(t *testing.T) {
	generated, err := Generate("../..")
	if err != nil {
		t.Fatal(err)
	}
	for name, data := range generated {
		actual, err := os.ReadFile(filepath.Join("../../schemas", name))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(actual, data) {
			t.Errorf("%s stale; run make schema-generate", name)
		}
	}
}

func TestExampleAssociations(t *testing.T) {
	for _, file := range []string{"../../examples/adoption/backlot.yaml", "../../examples/fullstack/backlot.yaml", "../../examples/fullstack/machine.example.yaml"} {
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		line, _, _ := strings.Cut(string(data), "\n")
		path, ok := strings.CutPrefix(line, "# yaml-language-server: $schema=")
		if !ok {
			t.Fatalf("missing modeline: %s", file)
		}
		if _, err := os.Stat(filepath.Join(filepath.Dir(file), path)); err != nil {
			t.Fatal(err)
		}
	}
}

func TestLocalContractValidation(t *testing.T) {
	project := compile(t, "manifest-v1.schema.json")
	machine := compile(t, "machine-config-v1.schema.json")
	minimal := `version: backlot/v1
project: demo
tools: {go: go}
components:
  test: {kind: job, runtime: native, policy: each-start, command: {tool: go}}
scenes:
  test: {lifetime: disposable, components: [test], terminal_job: test}
`
	tests := []struct {
		name, doc string
		valid     bool
	}{
		{"minimal native job", minimal, true},
		{"empty literal", strings.Replace(minimal, "command: {tool: go}", "command: {tool: go}, environment: {assign: {EMPTY: {literal: ''}}}", 1), true},
		{"unknown root", minimal + "typo: true\n", false},
		{"unknown nested", strings.Replace(minimal, "tool: go}", "tool: go, typo: true}", 1), false},
		{"version", strings.Replace(minimal, "backlot/v1", "backlot/v2", 1), false},
		{"missing project", strings.Replace(minimal, "project: demo\n", "", 1), false},
		{"bad identifier", strings.Replace(minimal, "project: demo", "project: Demo", 1), false},
		{"bad map name", strings.Replace(minimal, "tools: {go: go}", "tools: {Go: go}", 1), false},
		{"empty components", strings.Replace(minimal, "components:\n  test: {kind: job, runtime: native, policy: each-start, command: {tool: go}}", "components: {}", 1), false},
		{"runtime enum", strings.Replace(minimal, "runtime: native", "runtime: shell", 1), false},
		{"kind enum", strings.Replace(minimal, "kind: job", "kind: task", 1), false},
		{"empty job policy", strings.Replace(minimal, "policy: each-start", "policy: ''", 1), false},
		{"policy enum", strings.Replace(minimal, "policy: each-start", "policy: always", 1), false},
		{"missing native command", strings.Replace(minimal, ", command: {tool: go}", "", 1), false},
		{"native image", strings.Replace(minimal, "command: {tool: go}", "command: {tool: go}, image: {literal: alpine}", 1), false},
		{"missing terminal", strings.Replace(minimal, ", terminal_job: test", "", 1), false},
		{"empty terminal", strings.Replace(minimal, "terminal_job: test", "terminal_job: ''", 1), false},
		{"empty scene", strings.Replace(minimal, "components: [test]", "components: []", 1), false},
		{"duplicate selection", strings.Replace(minimal, "components: [test]", "components: [test, test]", 1), false},
		{"empty value", strings.Replace(minimal, "command: {tool: go}", "command: {tool: go}, environment: {assign: {A: {}}}", 1), false},
		{"two value sources", strings.Replace(minimal, "command: {tool: go}", "command: {tool: go}, environment: {assign: {A: {literal: x, ref: {kind: instance, field: id}}}}", 1), false},
		{"invalid env key", strings.Replace(minimal, "command: {tool: go}", "command: {tool: go}, environment: {assign: {bad-key: {literal: x}}}", 1), false},
		{"fresh job no initializer", strings.Replace(minimal, "each-start", "fresh-only", 1), false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := project.Validate(yamlValue(t, []byte(tc.doc)))
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v: %v", tc.valid, err)
			}
			if tc.valid {
				var m v1.Manifest
				if err := manifest.Decode([]byte(tc.doc), ".yaml", &m); err != nil {
					t.Fatal(err)
				}
				if err := manifest.Validate(m); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
	for _, tc := range []struct {
		name, doc string
		valid     bool
	}{
		{"minimal", "version: backlot/v1", true},
		{"complete", "version: backlot/v1\ntools: {go: /usr/bin/go}\ninputs: {token: ''}\ndocker: {endpoint: 'unix:///var/run/docker.sock'}\ncaddy: {endpoint: 'https://localhost:2019', scope: backlot, domain_suffix: example.test, host_address: '::1'}\nstorage: {directory: state, retention_age: 168h, retention_bytes: 1024}", true},
		{"unknown", "version: backlot/v1\nunknown: yes", false},
		{"missing version", "tools: {}", false},
		{"empty executable", "version: backlot/v1\ntools: {go: ''}", false},
		{"bad override name", "version: backlot/v1\ninputs: {BAD: x}", false},
		{"missing endpoint", "version: backlot/v1\ndocker: {}", false},
		{"missing retention", "version: backlot/v1\nstorage: {directory: state}", false},
		{"zero retention", "version: backlot/v1\nstorage: {directory: state, retention_age: 24h, retention_bytes: 0}", false},
		{"bad gateway scheme", "version: backlot/v1\ncaddy: {endpoint: 'ftp://localhost', scope: backlot, domain_suffix: example.test, host_address: localhost}", false},
	} {
		t.Run("machine/"+tc.name, func(t *testing.T) {
			err := machine.Validate(yamlValue(t, []byte(tc.doc)))
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v: %v", tc.valid, err)
			}
			if tc.valid {
				var c v1.MachineConfig
				if err := manifest.Decode([]byte(tc.doc), ".yaml", &c); err != nil {
					t.Fatal(err)
				}
				if err := manifest.ValidateMachine(c); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

func TestContractVariants(t *testing.T) {
	// Validate individual $defs within the actual generated artifact. This covers
	// independent valid branches without inventing graph/executable fixtures.
	data, err := os.ReadFile("../../schemas/manifest-v1.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	doc, err := validator.UnmarshalJSON(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	c := validator.NewCompiler()
	if err := c.AddResource("manifest.json", doc); err != nil {
		t.Fatal(err)
	}
	for typ, cases := range map[string][]struct {
		doc   string
		valid bool
	}{
		"Value": {{`{literal: ''}`, true}, {`{ref: {kind: input, name: image}}`, true}, {`{literal: null}`, false}, {`{ref: {kind: unknown}}`, false}},
		"Reference": {
			{`{kind: instance, field: checkout}`, true}, {`{kind: resource, name: db, field: path}`, true}, {`{kind: output, name: build, field: path}`, true}, {`{kind: service, name: web, field: host}`, true}, {`{kind: service, name: web, field: port, port: http}`, true},
			{`{kind: input}`, false}, {`{kind: instance, name: web, field: id}`, false}, {`{kind: service, name: web, field: port}`, false}, {`{kind: output, name: build, field: url}`, false},
		},
		"Probe": {{`{kind: command, command: {tool: go}, timeout: 30s}`, true}, {`{kind: tcp, target: {literal: 'localhost:8080'}, timeout: 1m}`, true}, {`{kind: http, target: {literal: 'https://example.test'}, timeout: 1s}`, true}, {`{kind: tcp, command: {tool: go}, timeout: 1m}`, false}, {`{kind: command, command: {tool: go}, target: {literal: x}, timeout: 1s}`, false}, {`{kind: sleep, timeout: 1s}`, false}},
		"Mount": {{`{resource: data, target: /data}`, true}, {`{output: build, target: /app, read_only: true}`, true}, {`{target: /data}`, false}, {`{resource: data, output: build, target: /data}`, false}, {`{resource: data, target: relative}`, false}},
		"Component": {
			{`{kind: job, runtime: container, image: {literal: ''}, policy: each-start}`, false},
			{`{kind: job, runtime: container, image: {ref: {kind: instance, field: id}}, policy: each-start}`, false},
			{`{kind: service, runtime: native, command: {tool: go}, readiness: {kind: command, command: {tool: go}, timeout: 1s}}`, true},
			{`{kind: service, runtime: container, image: {literal: alpine}, ports: {http: {resource: web_port, container_port: 65535}}, readiness: {kind: tcp, target: {ref: {kind: resource, name: web_port, field: port}}, timeout: 1s}}`, true},
			{`{kind: job, runtime: container, image: {ref: {kind: input, name: image}}, policy: fresh-only, initializes: [data], mounts: [{resource: data, target: /data}]}`, true},
			{`{kind: job, runtime: native, command: {tool: go}, policy: each-start, readiness: {kind: command, command: {tool: go}, timeout: 1s}}`, false},
			{`{kind: service, runtime: container, image: {literal: alpine}}`, false},
			{`{kind: service, runtime: container, image: {literal: alpine}, ports: {http: {resource: web_port, container_port: 65536}}, readiness: {kind: tcp, target: {literal: 'localhost:8080'}, timeout: 1s}}`, false},
			{`{kind: service, runtime: container, image: {literal: alpine}, ports: {http: {resource: web_port}}, readiness: {kind: tcp, target: {literal: 'localhost:8080'}, timeout: 1s}}`, false},
		},
		"Scene":      {{`{lifetime: persistent, components: [web]}`, true}, {`{lifetime: disposable, components: [test], terminal_job: test}`, true}, {`{lifetime: persistent, components: [web], terminal_job: test}`, false}, {`{lifetime: forever, components: [web]}`, false}},
		"Dependency": {{`{component: web, condition: ready}`, true}, {`{component: build, condition: completed}`, true}, {`{component: web, condition: started}`, false}},
	} {
		s, err := c.Compile("manifest.json#/$defs/" + typ)
		if err != nil {
			t.Fatal(err)
		}
		for _, tc := range cases {
			t.Run(typ+"/"+tc.doc, func(t *testing.T) {
				err := s.Validate(yamlValue(t, []byte(tc.doc)))
				if (err == nil) != tc.valid {
					t.Fatalf("valid=%v: %v", tc.valid, err)
				}
			})
		}
	}
}
