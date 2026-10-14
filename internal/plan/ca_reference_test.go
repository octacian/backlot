package plan

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	v1 "github.com/octacian/backlot/api/v1"
)

func TestCAReferenceOfflineAndPrecedence(t *testing.T) {
	noDefaultConfig(t)
	root := t.TempDir()
	const project = `version: backlot/v1
project: trust
tools: {go: go}
resources: {origin: {kind: origin}, port: {kind: port}}
environments:
  trust:
    pass_through: [NODE_EXTRA_CA_CERTS]
    seeds: [trust.env]
    assign:
      NODE_EXTRA_CA_CERTS: {ref: {kind: resource, name: origin, field: ca_cert_path}}
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
	write(t, filepath.Join(root, "backlot.yaml"), []byte(project))
	write(t, filepath.Join(root, "trust.env"), []byte("NODE_EXTRA_CA_CERTS=seed-value\n"))
	t.Setenv("NODE_EXTRA_CA_CERTS", "inherited-value")
	config := filepath.Join(root, "machine.yaml")
	write(t, config, []byte("version: backlot/v1\ncaddy: {mode: managed, executable: /does/not/exist/caddy}\n"))
	before, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	request := v1.PlanRequest{ProjectPath: root, Scene: "dev", ConfigPath: config}
	p, err := Resolve(request)
	if err != nil {
		t.Fatal(err)
	}
	value := p.Components[0].Environment["NODE_EXTRA_CA_CERTS"]
	want := &v1.Reference{Kind: "resource", Name: "origin", Field: "ca_cert_path"}
	if value.Literal != nil || value.Redacted || !reflect.DeepEqual(value.Symbolic, want) {
		t.Fatal(value)
	}
	encoded, _ := json.Marshal(p)
	for _, forbidden := range []string{"root.crt", ".key", "seed-value", "inherited-value"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatal("offline output leaked", forbidden)
		}
	}
	source, err := Locate(root)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := Prepare(source, request)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(snapshot.Plan.Components[0].Environment["NODE_EXTRA_CA_CERTS"], value) {
		t.Fatal("prepare resolved allocation")
	}
	after, err := os.ReadDir(root)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("planner wrote files", err)
	}
	override := strings.Replace(project, "    environment_sets: [trust]", "    environment_sets: [trust]\n    environment: {assign: {NODE_EXTRA_CA_CERTS: {literal: explicit-project-ca}}}", 1)
	write(t, filepath.Join(root, "backlot.yaml"), []byte(override))
	p, err = Resolve(request)
	if err != nil {
		t.Fatal(err)
	}
	value = p.Components[0].Environment["NODE_EXTRA_CA_CERTS"]
	if value.Literal == nil || *value.Literal != "explicit-project-ca" || value.Symbolic != nil {
		t.Fatal("component precedence lost", value)
	}
	for _, machine := range []string{"version: backlot/v1\n", "version: backlot/v1\ncaddy: {endpoint: 'http://127.0.0.1:2019', scope: backlot, domain_suffix: example.test, host_address: localhost}\n"} {
		write(t, config, []byte(machine))
		if _, err := Resolve(request); err == nil {
			t.Fatal("external/unconfigured CA reference accepted", err)
		}
		if _, err := Prepare(source, request); err == nil {
			t.Fatal("metadata preparation accepted external CA reference")
		}
	}
	// An unselected shared assignment does not require a managed gateway.
	write(t, filepath.Join(root, "backlot.yaml"), []byte(strings.Replace(project, "    environment_sets: [trust]\n", "", 1)))
	write(t, config, []byte("version: backlot/v1\ncaddy: {endpoint: 'http://127.0.0.1:2019', scope: backlot, domain_suffix: example.test, host_address: localhost}\n"))
	if _, err := Resolve(request); err != nil {
		t.Fatal(err)
	}
}

func TestContainerCAOffline(t *testing.T) {
	noDefaultConfig(t)
	root := t.TempDir()
	const project = `version: backlot/v1
project: trust
resources: {origin: {kind: origin}, port: {kind: port}}
components:
  web:
    kind: service
    runtime: container
    image: {literal: alpine:3.21}
    resources: [origin, port]
    ports: {http: {resource: port, container_port: 8080}}
    mounts: [{origin_ca_cert: origin, target: /app/root.pem, read_only: true}]
    environment: {assign: {SSL_CERT_FILE: {ref: {kind: resource, name: origin, field: ca_cert_path}}}}
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
	write(t, filepath.Join(root, "backlot.yaml"), []byte(project))
	config := filepath.Join(root, "machine.yaml")
	request := v1.PlanRequest{ProjectPath: root, Scene: "dev", ConfigPath: config}
	machine := "version: backlot/v1\ndocker: {endpoint: 'unix:///nonexistent/backlot21.sock'}\ncaddy: {mode: managed, executable: /nonexistent/caddy}\n"
	write(t, config, []byte(machine))
	p, err := Resolve(request)
	if err != nil {
		t.Fatal(err)
	}
	c := p.Components[0]
	if c.Environment["SSL_CERT_FILE"].Symbolic == nil || c.Mounts[0].OriginCACert != "origin" || c.Mounts[0].Target != "/app/root.pem" {
		t.Fatal(c)
	}
	source, err := Locate(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Prepare(source, request); err != nil {
		t.Fatal("offline prepare contacted providers", err)
	}
	// The typed mount still requires managed mode when there is no trust assignment.
	write(t, filepath.Join(root, "backlot.yaml"), []byte(strings.Replace(project, "    environment: {assign: {SSL_CERT_FILE: {ref: {kind: resource, name: origin, field: ca_cert_path}}}}\n", "", 1)))
	write(t, config, []byte("version: backlot/v1\ndocker: {endpoint: 'unix:///nonexistent/backlot21.sock'}\ncaddy: {endpoint: 'http://127.0.0.1:2019', scope: backlot, domain_suffix: example.test, host_address: localhost}\n"))
	if _, err := Resolve(request); err == nil {
		t.Fatal("external origin CA mount accepted")
	}
}
