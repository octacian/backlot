package manifest

import (
	"os"
	"testing"

	v1 "github.com/octacian/backlot/api/v1"
)

func TestLiteralPublicationPaths(t *testing.T) {
	data, err := os.ReadFile("../../examples/fullstack/backlot.yaml")
	if err != nil {
		t.Fatal(err)
	}
	for _, route := range []string{"/api*", "/api/*", "/api/(.*)", "/api/[a-z]", "/api/{id}", "/api/../admin", "/api//value", "/api/", "/api%2fvalue", "/api?x=y", "/api#hash", "/api\nvalue", "/api\\value"} {
		t.Run(route, func(t *testing.T) {
			var manifest v1.Manifest
			if err := Decode(data, ".yaml", &manifest); err != nil {
				t.Fatal(err)
			}
			scene := manifest.Scenes["dev"]
			scene.Publish.Routes[0].Path = route
			if err := Validate(manifest); err == nil {
				t.Fatal("nonliteral publication route accepted")
			}
		})
	}
}

func TestReachabilityConfig(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*v1.MachineConfig)
		valid  bool
	}{
		{"defaults", func(_ *v1.MachineConfig) {}, true},
		{"explicit", func(c *v1.MachineConfig) {
			c.Docker.HostAddress = "host.docker.internal"
			c.Docker.PublishAddress = "0.0.0.0"
			p := 8443
			c.Caddy.HTTPSPort = &p
		}, true},
		{"IPv6", func(c *v1.MachineConfig) {
			c.Docker.HostAddress = "::1"
			c.Docker.PublishAddress = "::"
			c.Caddy.HostAddress = "::1"
		}, true},
		{"host URL", func(c *v1.MachineConfig) { c.Docker.HostAddress = "http://localhost" }, false},
		{"host port", func(c *v1.MachineConfig) { c.Docker.HostAddress = "localhost:8080" }, false},
		{"gateway credentials", func(c *v1.MachineConfig) { c.Caddy.HostAddress = "user@host" }, false},
		{"non-numeric bind", func(c *v1.MachineConfig) { c.Docker.PublishAddress = "localhost" }, false},
		{"zone bind", func(c *v1.MachineConfig) { c.Docker.PublishAddress = "fe80::1%en0" }, false},
		{"multicast bind", func(c *v1.MachineConfig) { c.Docker.PublishAddress = "224.1.2.3" }, false},
		{"zero port", func(c *v1.MachineConfig) { p := 0; c.Caddy.HTTPSPort = &p }, false},
		{"large port", func(c *v1.MachineConfig) { p := 65536; c.Caddy.HTTPSPort = &p }, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			config := v1.MachineConfig{Version: v1.ManifestVersion, Docker: &v1.DockerConfig{Endpoint: "unix:///var/run/docker.sock"}, Caddy: &v1.CaddyConfig{Endpoint: "http://127.0.0.1:2019", Scope: "backlot", DomainSuffix: "example.com", HostAddress: "localhost"}}
			test.mutate(&config)
			if err := ValidateMachine(config); (err == nil) != test.valid {
				t.Fatal(test.valid, err)
			}
		})
	}
}
