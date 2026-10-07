package schema

import (
	"fmt"
	"strings"
	"testing"

	v1 "github.com/octacian/backlot/api/v1"
	"github.com/octacian/backlot/internal/manifest"
)

func TestReferenceYAMLParity(t *testing.T) {
	s := compile(t, "manifest-v1.schema.json")
	// A closed scene exercises actual reference validation and selection, including
	// each resource kind, rather than validating isolated schema definitions.
	const project = `version: backlot/v1
project: demo
tools: {go: go}
inputs: {token: {}}
resources:
  listener: {kind: port}
  data: {kind: directory}
  volume: {kind: volume}
  network: {kind: network}
  secret: {kind: secret}
  origin: {kind: origin}
outputs: {build: {path: dist, concurrency_group: build}}
components:
  web:
    kind: service
    runtime: native
    command: {tool: go}
    resources: [listener, data, volume, network, secret, origin]
    outputs: [build]
    ports: {http: {resource: listener}}
    readiness: {kind: command, command: {tool: go}, timeout: 1s}
    environment: {assign: {VALUE: {ref: %s}}}
scenes:
  dev:
    lifetime: persistent
    components: [web]
    resources: [listener, data, volume, network, secret, origin]
    publish:
      resource: origin
      routes: [{path: /, service: web, port: http, prefix: preserve}]
      probe: {kind: http, target: {ref: {kind: resource, name: origin, field: url}}, timeout: 1s}
`
	valid := []struct{ name, fields, unused string }{
		{"input", "kind: input, name: token", "field: '', port: ''"},
		{"instance id", "kind: instance, field: id", "name: '', port: ''"},
		{"instance checkout", "kind: instance, field: checkout", "name: '', port: ''"},
		{"instance project", "kind: instance, field: project", "name: '', port: ''"},
		{"instance scene", "kind: instance, field: scene", "name: '', port: ''"},
		{"resource port", "kind: resource, name: listener, field: port", "port: ''"},
		{"resource directory", "kind: resource, name: data, field: path", "port: ''"},
		{"resource volume", "kind: resource, name: volume, field: name", "port: ''"},
		{"resource network", "kind: resource, name: network, field: name", "port: ''"},
		{"resource secret", "kind: resource, name: secret, field: value", "port: ''"},
		{"resource origin", "kind: resource, name: origin, field: url", "port: ''"},
		{"output", "kind: output, name: build, field: path", "port: ''"},
		{"service host", "kind: service, name: web, field: host", "port: ''"},
		{"service port", "kind: service, name: web, field: port, port: http", ""},
	}
	for _, tc := range valid {
		for _, empty := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/explicit-empty=%v", tc.name, empty), func(t *testing.T) {
				fields := tc.fields
				if empty && tc.unused != "" {
					fields += ", " + tc.unused
				}
				doc := []byte(fmt.Sprintf(project, "{"+fields+"}"))
				checkManifestParity(t, doc, true, s.Validate(yamlValue(t, doc)))
			})
		}
	}
	for _, fields := range []string{
		"kind: input, name: token, field: path",
		"kind: input, name: token, port: http",
		"kind: input, name: ''",
		"kind: instance, field: id, name: web",
		"kind: instance, field: id, port: http",
		"kind: resource, name: data, field: path, port: http",
		"kind: output, name: build, field: path, port: http",
		"kind: service, name: web, field: host, port: http",
		"kind: service, name: web, field: port",
		"kind: service, name: web, field: port, port: ''",
	} {
		t.Run("invalid/"+fields, func(t *testing.T) {
			doc := []byte(fmt.Sprintf(project, "{"+fields+"}"))
			checkManifestParity(t, doc, false, s.Validate(yamlValue(t, doc)))
		})
	}
}

func TestMountYAMLParity(t *testing.T) {
	s := compile(t, "manifest-v1.schema.json")
	const project = `version: backlot/v1
project: demo
resources: {data: {kind: volume}}
outputs: {build: {path: dist, concurrency_group: build}}
components:
  test:
    kind: job
    runtime: container
    policy: each-start
    image: {literal: alpine}
    resources: [data]
    outputs: [build]
    mounts: [%s]
scenes:
  test: {lifetime: disposable, components: [test], resources: [data], terminal_job: test}
`
	for _, tc := range []struct {
		mount string
		valid bool
	}{
		{"resource: data", true},
		{"resource: data, output: ''", true},
		{"output: build", true},
		{"output: build, resource: ''", true},
		{"resource: data, output: build", false},
		{"resource: '', output: ''", false},
		{"resource: ''", false},
		{"output: ''", false},
		{"", false},
	} {
		t.Run(tc.mount, func(t *testing.T) {
			mount := "{target: /data"
			if tc.mount != "" {
				mount += ", " + tc.mount
			}
			doc := []byte(fmt.Sprintf(project, mount+"}"))
			checkManifestParity(t, doc, tc.valid, s.Validate(yamlValue(t, doc)))
		})
	}
}

func checkManifestParity(t *testing.T, doc []byte, valid bool, schemaErr error) {
	t.Helper()
	var m v1.Manifest
	if err := manifest.Decode(doc, ".yaml", &m); err != nil {
		t.Fatal(err)
	}
	runtimeErr := manifest.Validate(m)
	if (runtimeErr == nil) != valid || (schemaErr == nil) != valid {
		t.Fatalf("want valid=%v; runtime=%v; schema=%v", valid, runtimeErr, schemaErr)
	}
}

func TestDockerEndpointYAMLParity(t *testing.T) {
	s := compile(t, "machine-config-v1.schema.json")
	for _, tc := range []struct {
		endpoint string
		valid    bool
	}{
		{"unix:/var/run/docker.sock", true},
		{"unix:///var/run/docker.sock", true},
		{"unix:////var/run/docker.sock", true},
		{"UNIX:/var/run/docker.sock", true},
		{"unix:/", true},
		{"unix:/var/run/docker.sock?#", true},
		{"unix:/var/run/docker.sock?", true},
		{"unix:/var/run/docker.sock#", true},
		{"tcp://remote:2375", false},
		{"unix://remote/var/run/docker.sock", false},
		{"unix://user@remote/var/run/docker.sock", false},
		{"unix:relative.sock", false},
		{"unix:", false},
		{"unix://", false},
		{"unix:/var/run/docker.sock?query=x", false},
		{"unix:/var/run/docker.sock#fragment", false},
	} {
		t.Run(tc.endpoint, func(t *testing.T) {
			doc := []byte("version: backlot/v1\ndocker: {endpoint: '" + strings.ReplaceAll(tc.endpoint, "'", "''") + "'}\n")
			var c v1.MachineConfig
			if err := manifest.Decode(doc, ".yaml", &c); err != nil {
				t.Fatal(err)
			}
			runtimeErr := manifest.ValidateMachine(c)
			schemaErr := s.Validate(yamlValue(t, doc))
			if (runtimeErr == nil) != tc.valid || (schemaErr == nil) != tc.valid {
				t.Fatalf("want valid=%v; runtime=%v; schema=%v", tc.valid, runtimeErr, schemaErr)
			}
		})
	}
}
