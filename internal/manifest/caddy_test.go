package manifest

import (
	"testing"

	v1 "github.com/octacian/backlot/api/v1"
)

func TestManagedCaddyContract(t *testing.T) {
	base := v1.CaddyConfig{Mode: "managed", Executable: "/explicit/caddy"}
	effective := CaddyDefaults(base)
	if *effective.HTTPSPort != 8443 || effective.Endpoint != "http://127.0.0.1:2020" || effective.DomainSuffix != "backlot.localhost" {
		t.Fatal(effective)
	}
	if err := ValidateMachine(v1.MachineConfig{Version: v1.ManifestVersion, Caddy: &base}); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*v1.CaddyConfig){
		func(c *v1.CaddyConfig) { c.Mode = "automatic" },
		func(c *v1.CaddyConfig) { c.Executable = "caddy" },
		func(c *v1.CaddyConfig) { c.Endpoint = "http://0.0.0.0:2020" },
		func(c *v1.CaddyConfig) { c.Endpoint = "http://127.0.0.1:2019/prefix" },
		func(c *v1.CaddyConfig) { c.Endpoint = "http://127.0.0.1:8443" },
		func(c *v1.CaddyConfig) { c.DomainSuffix = "other.localhost" },
		func(c *v1.CaddyConfig) { c.HostAddress = "host.docker.internal" },
		func(c *v1.CaddyConfig) { p := 443; c.HTTPSPort = &p },
		func(c *v1.CaddyConfig) { c.Mode = "external" },
	} {
		c := base
		change(&c)
		if ValidateMachine(v1.MachineConfig{Version: v1.ManifestVersion, Caddy: &c}) == nil {
			t.Fatal("unsafe managed configuration accepted", c)
		}
	}
	legacy := v1.CaddyConfig{Endpoint: "http://127.0.0.1:2019", Scope: "backlot", DomainSuffix: "example.test", HostAddress: "localhost"}
	if CaddyDefaults(legacy).Mode != "external" || ValidateMachine(v1.MachineConfig{Version: v1.ManifestVersion, Caddy: &legacy}) != nil {
		t.Fatal("legacy external config changed")
	}
}
