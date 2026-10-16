package daemon

import (
	"fmt"
	"net/url"
	"slices"

	v1 "github.com/octacian/backlot/api/v1"
	"github.com/octacian/backlot/internal/plan"
)

// containerOriginHosts derives only the selected publication's canonical host.
// Docker expands the explicit token at create; there is no host DNS lookup,
// helper, cached IP, alias inference or environment injection here.
func containerOriginHosts(c v1.PlannedComponent, snap plan.Snapshot, origin string) ([]string, error) {
	if len(c.HostMappings) == 0 {
		return nil, nil
	}
	if c.Runtime != v1.Container || snap.Caddy == nil || snap.Caddy.Mode != "managed" || snap.Plan.Publish == nil {
		return nil, problem("invalid_contract", "origin host mappings require a container and managed publication")
	}
	parsed, err := url.Parse(origin)
	if err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" || parsed.User != nil || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, problem("invalid_contract", "origin host mapping has no canonical HTTPS origin")
	}
	var hosts []string
	for _, mapping := range c.HostMappings {
		if mapping.Host != "host-gateway" || mapping.Origin != snap.Plan.Publish.Resource || snap.Plan.Resources[mapping.Origin].Kind != "origin" || !slices.Contains(c.Resources, mapping.Origin) || len(hosts) != 0 {
			return nil, problem("invalid_contract", "origin host mapping requires the unique attached published origin and explicit host-gateway token")
		}
		hosts = append(hosts, parsed.Hostname()+":host-gateway")
	}
	return hosts, nil
}

// validateGatewayBindSource rejects generic bind mounts of private gateway state,
// including parent directories and aliases. Only the separately validated,
// read-only origin_ca_cert source may expose its existing public-root file.
func (s *store) validateGatewayBindSource(source string) error {
	root, _, _ := managedPaths(s.directory)
	return validatePrivateBindBoundary(source, root)
}

// validateOutputBindSource protects private daemon state even before a managed
// gateway is initialized. Owned retained data uses explicit resource mounts,
// rather than checkout outputs. Check before creating any output directories.
func (s *store) validateOutputBindSource(source string) error {
	return validatePrivateBindBoundary(source, s.directory)
}

func validatePrivateBindBoundary(source, boundary string) error {
	overlap, err := bindSourcesOverlap(boundary, source)
	if err != nil {
		return fmt.Errorf("validate private mount boundary: %w", err)
	}
	if overlap {
		return problem("gateway_ca", "bind source includes private daemon/gateway state; use declared resources or origin_ca_cert")
	}
	return nil
}
