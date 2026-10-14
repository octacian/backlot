package manifest

import v1 "github.com/octacian/backlot/api/v1"

// CaddyDefaults resolves ownership-specific defaults without contacting providers.
func CaddyDefaults(c v1.CaddyConfig) v1.CaddyConfig {
	if c.Mode == "" {
		c.Mode = "external"
	}
	if c.Mode != "managed" {
		return c
	}
	if c.Endpoint == "" {
		c.Endpoint = "http://127.0.0.1:2020"
	}
	if c.Scope == "" {
		c.Scope = "backlot-managed"
	}
	if c.DomainSuffix == "" {
		c.DomainSuffix = "backlot.localhost"
	}
	if c.HostAddress == "" {
		c.HostAddress = "127.0.0.1"
	}
	if c.HTTPSPort == nil {
		port := 8443
		c.HTTPSPort = &port
	}
	return c
}
