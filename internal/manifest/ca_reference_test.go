package manifest

import (
	"strings"
	"testing"

	v1 "github.com/octacian/backlot/api/v1"
)

const caProject = `version: backlot/v1
project: trust
tools: {go: go}
resources: {origin: {kind: origin}, port: {kind: port}}
environments:
  trust:
    assign: {CA: {ref: {kind: resource, name: origin, field: ca_cert_path}}}
components:
  web:
    kind: service
    runtime: native
    command: {tool: go}
    resources: [origin, port]
    ports: {http: {resource: port}}
    environment_sets: [trust]
    readiness: {kind: tcp, target: {ref: {kind: resource, name: port, field: port}}, timeout: 1s}
scenes:
  dev:
    lifetime: persistent
    components: [web]
    resources: [origin, port]
    publish:
      resource: origin
      routes: [{path: /, service: web, port: http, prefix: preserve}]
      probe: {kind: http, target: {ref: {kind: resource, name: origin, field: url}}, timeout: 1s}
`

func TestCAReferenceBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*v1.Manifest)
		valid  bool
	}{
		{"reusable assignment", func(_ *v1.Manifest) {}, true},
		{"direct assignment", func(m *v1.Manifest) {
			c := m.Components["web"]
			c.Environment = m.Environments["trust"]
			c.EnvironmentSets = nil
			m.Components["web"] = c
		}, true},
		{"container", func(m *v1.Manifest) {
			c := m.Components["web"]
			c.Runtime = v1.Container
			c.Command = nil
			image := "alpine:3.21"
			c.Image = &v1.Value{Literal: &image}
			c.Ports["http"] = v1.ServicePort{Resource: "port", ContainerPort: 8080}
			m.Components["web"] = c
		}, false},
		{"unattached", func(m *v1.Manifest) {
			c := m.Components["web"]
			c.Resources = []string{"port"}
			m.Components["web"] = c
		}, false},
		{"unselected", func(m *v1.Manifest) { s := m.Scenes["dev"]; s.Resources = []string{"port"}; m.Scenes["dev"] = s }, false},
		{"unpublished", func(m *v1.Manifest) { s := m.Scenes["dev"]; s.Publish = nil; m.Scenes["dev"] = s }, false},
		{"wrong resource kind", func(m *v1.Manifest) { m.Resources["origin"] = v1.Resource{Kind: "directory"} }, false},
		{"private key field", func(m *v1.Manifest) { e := m.Environments["trust"]; e.Assign["CA"].Ref.Field = "ca_key_path" }, false},
		{"fixture", func(m *v1.Manifest) {
			c := m.Components["web"]
			c.Fixtures = map[string]v1.Fixture{"ca": {Value: m.Environments["trust"].Assign["CA"]}}
			m.Components["web"] = c
		}, false},
		{"probe", func(m *v1.Manifest) {
			c := m.Components["web"]
			v := m.Environments["trust"].Assign["CA"]
			c.Readiness.Target = &v
			m.Components["web"] = c
		}, false},
		{"aggregate probe", func(m *v1.Manifest) {
			v := m.Environments["trust"].Assign["CA"]
			m.Scenes["dev"].Publish.Probe.Target = &v
		}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var m v1.Manifest
			if err := Decode([]byte(caProject), ".yaml", &m); err != nil {
				t.Fatal(err)
			}
			tc.mutate(&m)
			if err := Validate(m); (err == nil) != tc.valid {
				t.Fatal(tc.valid, err)
			}
		})
	}
	// A reusable set is not a consumer until a component explicitly includes it.
	var m v1.Manifest
	if err := Decode([]byte(strings.Replace(caProject, "    environment_sets: [trust]\n", "", 1)), ".yaml", &m); err != nil {
		t.Fatal(err)
	}
	c := m.Components["web"]
	c.Resources = []string{"port"}
	m.Components["web"] = c
	if err := Validate(m); err != nil {
		t.Fatal(err)
	}
}
