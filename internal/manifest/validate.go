package manifest

import (
	"net"
	"net/netip"
	"net/url"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	v1 "github.com/octacian/backlot/api/v1"
)

var identifier = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,62}$`)
var envKey = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
var routePath = regexp.MustCompile(v1.LiteralRoutePathPattern)

func invalid(field, message string) error {
	return &v1.PlanError{Code: "invalid_contract", Field: field, Message: message}
}

func names[T any](values map[string]T, field string) error {
	for name := range values {
		if !identifier.MatchString(name) {
			return invalid(field, "names must start with a lowercase letter and contain at most 63 lowercase letters, digits, underscores or hyphens")
		}
	}
	return nil
}

func unique(values []string, field string) error {
	seen := map[string]bool{}
	for _, value := range values {
		if value == "" || seen[value] {
			return invalid(field, "empty or duplicate selection")
		}
		seen[value] = true
	}
	return nil
}

// Validate checks every reusable declaration and each scene's closed graph.
func Validate(m v1.Manifest) error {
	if m.Version != v1.ManifestVersion {
		return invalid("version", "unsupported version; use backlot/v1")
	}
	if !identifier.MatchString(m.Project) {
		return invalid("project", "provide a stable lowercase project identifier")
	}
	if len(m.Components) == 0 || len(m.Scenes) == 0 {
		return invalid("manifest", "declare components and scenes")
	}
	checks := []error{names(m.Tools, "tools"), names(m.Inputs, "inputs"), names(m.Environments, "environments"), names(m.Resources, "resources"), names(m.Outputs, "outputs"), names(m.Components, "components"), names(m.Scenes, "scenes")}
	for _, err := range checks {
		if err != nil {
			return err
		}
	}
	for name, tool := range m.Tools {
		if tool == "" || strings.ContainsRune(tool, 0) {
			return invalid("tools."+name, "provide an executable name or path")
		}
	}
	for name, r := range m.Resources {
		if !slices.Contains([]string{"port", "volume", "directory", "secret", "network", "origin"}, r.Kind) {
			return invalid("resources."+name, "unsupported resource kind")
		}
	}
	outputPaths := map[string]string{}
	for name, o := range m.Outputs {
		if !safePath(o.Path) || !identifier.MatchString(o.ConcurrencyGroup) {
			return invalid("outputs."+name, "declare a safe relative path and concurrency_group")
		}
		for existing, group := range outputPaths {
			if (o.Path == existing || strings.HasPrefix(o.Path, existing+"/") || strings.HasPrefix(existing, o.Path+"/")) && group != o.ConcurrencyGroup {
				return invalid("outputs."+name, "overlapping output paths must share a concurrency group")
			}
		}
		outputPaths[o.Path] = o.ConcurrencyGroup
	}
	for name, e := range m.Environments {
		if err := environment(m, e, "environments."+name); err != nil {
			return err
		}
	}
	for name, c := range m.Components {
		if err := component(m, name, c); err != nil {
			return err
		}
	}
	all := v1.Scene{}
	for name := range m.Components {
		all.Components = append(all.Components, name)
	}
	slices.Sort(all.Components)
	if _, err := Order(m, all); err != nil {
		return err
	}
	for name, s := range m.Scenes {
		if err := scene(m, name, s); err != nil {
			return err
		}
	}
	return nil
}

func safePath(p string) bool {
	return p != "" && p != "." && path.Clean(p) == p && !strings.HasPrefix(p, "/") && p != ".." && !strings.HasPrefix(p, "../") && !strings.ContainsAny(p, "\\:\x00")
}

func command(m v1.Manifest, c *v1.Command, field string) error {
	if c == nil || c.Tool == "" {
		return invalid(field, "declare a tool and argument array")
	}
	if _, ok := m.Tools[c.Tool]; !ok {
		return invalid(field+".tool", "unknown tool; declare it in tools")
	}
	for _, arg := range c.Args {
		if strings.ContainsRune(arg, 0) {
			return invalid(field+".args", "NUL is not permitted")
		}
	}
	return nil
}

func environment(m v1.Manifest, e v1.Environment, field string) error {
	for _, list := range [][]string{e.PassThrough, e.Seeds, e.Required} {
		if err := unique(list, field); err != nil {
			return err
		}
	}
	for _, key := range append(slices.Clone(e.PassThrough), e.Required...) {
		if !envKey.MatchString(key) {
			return invalid(field, "invalid environment variable name")
		}
	}
	for _, seed := range e.Seeds {
		if !safePath(seed) {
			return invalid(field+".seeds", "seed paths must be safe checkout-relative paths")
		}
	}
	for key, v := range e.Assign {
		if !envKey.MatchString(key) {
			return invalid(field+".assign", "invalid environment variable name")
		}
		if err := value(m, v, field+".assign."+diagnosticKey(key)); err != nil {
			return err
		}
	}
	return nil
}

func value(m v1.Manifest, v v1.Value, field string) error {
	if (v.Literal == nil) == (v.Ref == nil) {
		return invalid(field, "provide exactly one literal or ref")
	}
	if v.Literal != nil {
		if strings.ContainsRune(*v.Literal, 0) {
			return invalid(field, "NUL is not permitted")
		}
		return nil
	}
	r := v.Ref
	switch r.Kind {
	case "input":
		if _, ok := m.Inputs[r.Name]; !ok || r.Field != "" || r.Port != "" {
			return invalid(field, "input reference requires a declared name only")
		}
	case "instance":
		if r.Name != "" || r.Port != "" || !slices.Contains([]string{"id", "checkout", "project", "scene"}, r.Field) {
			return invalid(field, "invalid instance reference")
		}
	case "resource":
		resource, ok := m.Resources[r.Name]
		fields := map[string]string{"port": "port", "directory": "path", "volume": "name", "network": "name", "secret": "value", "origin": "url"}
		if !ok || r.Field != fields[resource.Kind] || r.Port != "" {
			return invalid(field, "reference must use the declared resource kind's field")
		}
	case "output":
		if _, ok := m.Outputs[r.Name]; !ok || r.Field != "path" || r.Port != "" {
			return invalid(field, "output reference requires a declared name and path field")
		}
	case "service":
		c, ok := m.Components[r.Name]
		if !ok || c.Kind != v1.Service || (r.Field != "host" && r.Field != "port") || (r.Field == "host" && r.Port != "") {
			return invalid(field, "invalid service reference")
		}
		if r.Field == "port" {
			if _, ok := c.Ports[r.Port]; !ok {
				return invalid(field, "unknown service port")
			}
		}
	default:
		return invalid(field, "unknown reference kind")
	}
	return nil
}

func probe(m v1.Manifest, p *v1.Probe, field string) error {
	if p == nil {
		return invalid(field, "services require an explicit readiness probe")
	}
	d, err := time.ParseDuration(p.Timeout)
	if err != nil || d < 0 || d > 24*time.Hour {
		return invalid(field+".timeout", "provide a nonnegative deadline at most 24h (0 is unlimited)")
	}
	switch p.Kind {
	case "command":
		if p.Target != nil {
			return invalid(field, "command probe cannot have target")
		}
		return command(m, p.Command, field+".command")
	case "tcp", "http":
		if p.Command != nil || p.Target == nil {
			return invalid(field, "network probe requires target only")
		}
		if err := value(m, *p.Target, field+".target"); err != nil {
			return err
		}
		if r := p.Target.Ref; r != nil {
			if p.Kind == "tcp" && ((r.Kind != "service" && r.Kind != "resource") || r.Field != "port") {
				return invalid(field, "TCP probe requires a symbolic port or literal host:port")
			}
			if p.Kind == "http" && (r.Kind != "resource" || r.Field != "url") {
				return invalid(field, "HTTP probe requires an origin reference or literal HTTP(S) URL")
			}
		} else if p.Kind == "tcp" {
			_, port, err := net.SplitHostPort(*p.Target.Literal)
			if err != nil || !validNetworkPort(port) {
				return invalid(field, "TCP probe target must be host:port with a numeric port in 1..65535")
			}
		} else {
			u, err := url.Parse(*p.Target.Literal)
			if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || !validURLPort(u) {
				return invalid(field, "HTTP probe target must be an HTTP(S) URL without credentials and with any explicit port in 1..65535")
			}
		}
		return nil
	default:
		return invalid(field, "unsupported probe kind")
	}
}

func component(m v1.Manifest, name string, c v1.Component) error {
	f := "components." + name
	if c.Kind != v1.Service && c.Kind != v1.Job {
		return invalid(f+".kind", "use service or job")
	}
	switch c.Runtime {
	case v1.Native:
		if c.Image != nil || len(c.Args) > 0 || len(c.Mounts) > 0 {
			return invalid(f, "native work uses command; image, args and mounts are container-only")
		}
		if err := command(m, c.Command, f+".command"); err != nil {
			return err
		}
	case v1.Container:
		if c.Command != nil || c.Image == nil {
			return invalid(f, "container work requires image and optional args; command is native-only")
		}
		if c.Image.Literal != nil && *c.Image.Literal == "" {
			return invalid(f+".image", "provide a nonempty image reference")
		}
		if c.Image.Ref != nil && c.Image.Ref.Kind != "input" {
			return invalid(f+".image", "image references must name a declared input")
		}
		if err := value(m, *c.Image, f+".image"); err != nil {
			return err
		}
	default:
		return invalid(f+".runtime", "use native or container")
	}
	for _, arg := range c.Args {
		if strings.ContainsRune(arg, 0) {
			return invalid(f+".args", "NUL is not permitted")
		}
	}
	if c.Kind == v1.Job {
		if c.Policy != v1.FreshOnly && c.Policy != v1.EachStart {
			return invalid(f+".policy", "jobs require fresh-only or each-start")
		}
		if c.Readiness != nil || len(c.Ports) > 0 {
			return invalid(f, "jobs cannot declare readiness or ports")
		}
		if c.Policy == v1.FreshOnly && len(c.Initializes) == 0 {
			return invalid(f+".initializes", "fresh-only jobs must name resource generations they initialize")
		}
		if c.Policy == v1.EachStart && len(c.Initializes) > 0 {
			return invalid(f+".initializes", "each-start jobs cannot record fresh initialization")
		}
	} else {
		if c.Policy != "" || len(c.Initializes) > 0 {
			return invalid(f, "job policy and initializes are job-only")
		}
		if err := probe(m, c.Readiness, f+".readiness"); err != nil {
			return err
		}
		if c.Readiness.Target != nil && c.Readiness.Target.Ref != nil {
			r := c.Readiness.Target.Ref
			if r.Kind == "resource" && m.Resources[r.Name].Kind == "origin" {
				return invalid(f+".readiness", "published origin readiness belongs in the aggregate publish probe to avoid a startup cycle")
			}
		}
	}
	for _, list := range [][]string{c.Resources, c.Outputs, c.EnvironmentSets, c.Initializes} {
		if err := unique(list, f); err != nil {
			return err
		}
	}
	for _, r := range c.Resources {
		if _, ok := m.Resources[r]; !ok {
			return invalid(f+".resources", "unknown resource")
		}
	}
	for _, r := range c.Initializes {
		res, ok := m.Resources[r]
		if !ok || !slices.Contains(c.Resources, r) || (res.Kind != "volume" && res.Kind != "directory") {
			return invalid(f+".initializes", "initialize a declared, attached volume or directory")
		}
	}
	for _, o := range c.Outputs {
		if _, ok := m.Outputs[o]; !ok {
			return invalid(f+".outputs", "unknown output")
		}
	}
	for _, e := range c.EnvironmentSets {
		if _, ok := m.Environments[e]; !ok {
			return invalid(f+".environment_sets", "unknown environment set")
		}
	}
	if err := environment(m, c.Environment, f+".environment"); err != nil {
		return err
	}
	portResources := map[string]bool{}
	for name, port := range c.Ports {
		r := port.Resource
		if (c.Runtime == v1.Native && port.ContainerPort != 0) || (c.Runtime == v1.Container && (port.ContainerPort < 1 || port.ContainerPort > 65535)) {
			return invalid(f+".ports", "container ports must be 1..65535; native ports omit container_port")
		}
		if !identifier.MatchString(name) || m.Resources[r].Kind != "port" || !slices.Contains(c.Resources, r) || portResources[r] {
			return invalid(f+".ports", "ports need unique declared, attached port resources")
		}
		portResources[r] = true
	}
	mounts := map[string]bool{}
	for _, mount := range c.Mounts {
		if (mount.Resource == "") == (mount.Output == "") || !strings.HasPrefix(mount.Target, "/") || path.Clean(mount.Target) != mount.Target || mounts[mount.Target] || strings.ContainsRune(mount.Target, 0) {
			return invalid(f+".mounts", "declare one storage source and unique absolute target")
		}
		mounts[mount.Target] = true
		if mount.Resource != "" {
			kind := m.Resources[mount.Resource].Kind
			if !slices.Contains(c.Resources, mount.Resource) || (kind != "volume" && kind != "directory") {
				return invalid(f+".mounts", "mount requires an attached volume or directory")
			}
		}
		if mount.Output != "" && !slices.Contains(c.Outputs, mount.Output) {
			return invalid(f+".mounts", "mount requires an attached output")
		}
	}
	deps := map[string]bool{}
	for _, d := range c.DependsOn {
		target, ok := m.Components[d.Component]
		if !ok || d.Component == name || deps[d.Component] || (target.Kind == v1.Service && d.Condition != v1.Ready) || (target.Kind == v1.Job && d.Condition != v1.Completed) {
			return invalid(f+".depends_on", "dependency must name another component with ready service or completed job gate")
		}
		deps[d.Component] = true
	}
	return nil
}

func scene(m v1.Manifest, name string, s v1.Scene) error {
	f := "scenes." + name
	if s.Lifetime != v1.Persistent && s.Lifetime != v1.Disposable {
		return invalid(f+".lifetime", "use persistent or disposable")
	}
	if len(s.Components) == 0 {
		return invalid(f+".components", "select at least one component")
	}
	if err := unique(s.Components, f+".components"); err != nil {
		return err
	}
	if err := unique(s.Resources, f+".resources"); err != nil {
		return err
	}
	for _, r := range s.Resources {
		if _, ok := m.Resources[r]; !ok {
			return invalid(f+".resources", "unknown resource")
		}
	}
	ports := map[string]bool{}
	initializers := map[string]bool{}
	for _, name := range s.Components {
		c, ok := m.Components[name]
		if !ok {
			return invalid(f+".components", "unknown component")
		}
		for _, r := range c.Resources {
			if !slices.Contains(s.Resources, r) {
				return invalid(f+".resources", "selected component resource is not selected")
			}
		}
		for _, port := range c.Ports {
			r := port.Resource
			if ports[r] {
				return invalid(f, "two services cannot own the same port")
			}
			ports[r] = true
		}
		for _, r := range c.Initializes {
			if initializers[r] {
				return invalid(f, "resource has conflicting fresh-only initializers")
			}
			initializers[r] = true
		}
		for _, d := range c.DependsOn {
			if !slices.Contains(s.Components, d.Component) {
				return invalid(f, "dependency is not selected; explicitly include it")
			}
		}
		values := componentValues(m, c)
		for _, v := range values {
			if err := selectedReference(m, s, c, v, f+".components."+name); err != nil {
				return err
			}
		}
	}
	if s.Lifetime == v1.Disposable {
		c, ok := m.Components[s.TerminalJob]
		if !ok || c.Kind != v1.Job || c.Policy != v1.EachStart || !slices.Contains(s.Components, s.TerminalJob) {
			return invalid(f+".terminal_job", "disposable scenes require a selected each-start job")
		}
		reachable := map[string]bool{}
		var visit func(string)
		visit = func(n string) {
			if reachable[n] {
				return
			}
			reachable[n] = true
			for _, d := range m.Components[n].DependsOn {
				visit(d.Component)
			}
		}
		visit(s.TerminalJob)
		if len(reachable) != len(s.Components) {
			return invalid(f+".terminal_job", "all selected work must gate the terminal job")
		}
	} else if s.TerminalJob != "" {
		return invalid(f+".terminal_job", "terminal job is disposable-only")
	}
	if s.Publish != nil {
		p := s.Publish
		if m.Resources[p.Resource].Kind != "origin" || !slices.Contains(s.Resources, p.Resource) || len(p.Routes) == 0 {
			return invalid(f+".publish", "select an origin resource and at least one route")
		}
		paths := map[string]bool{}
		for _, r := range p.Routes {
			c := m.Components[r.Service]
			if !routePath.MatchString(r.Path) || path.Clean(r.Path) != r.Path || paths[r.Path] || !slices.Contains(s.Components, r.Service) || c.Kind != v1.Service || c.Ports[r.Port].Resource == "" || (r.Prefix != "preserve" && r.Prefix != "strip") {
				return invalid(f+".publish.routes", "declare unique absolute paths, selected service ports and preserve/strip prefix policy")
			}
			paths[r.Path] = true
		}
		if p.Probe.Kind != "http" {
			return invalid(f+".publish.probe", "aggregate publication probe must use http")
		}
		if err := probe(m, &p.Probe, f+".publish.probe"); err != nil {
			return err
		}
		if p.Probe.Target.Ref == nil || p.Probe.Target.Ref.Kind != "resource" || p.Probe.Target.Ref.Name != p.Resource {
			return invalid(f+".publish.probe", "aggregate probe must reference the published origin")
		}
	}
	_, err := Order(m, s)
	return err
}

func componentValues(m v1.Manifest, c v1.Component) []v1.Value {
	var values []v1.Value
	for _, name := range c.EnvironmentSets {
		for _, v := range m.Environments[name].Assign {
			values = append(values, v)
		}
	}
	for _, v := range c.Environment.Assign {
		values = append(values, v)
	}
	if c.Image != nil {
		values = append(values, *c.Image)
	}
	if c.Readiness != nil && c.Readiness.Target != nil {
		values = append(values, *c.Readiness.Target)
	}
	return values
}

func selectedReference(m v1.Manifest, s v1.Scene, c v1.Component, v v1.Value, field string) error {
	if v.Ref == nil {
		return nil
	}
	r := v.Ref
	switch r.Kind {
	case "resource":
		if !slices.Contains(s.Resources, r.Name) || !slices.Contains(c.Resources, r.Name) {
			return invalid(field, "resource reference requires scene selection and component attachment")
		}
		if m.Resources[r.Name].Kind == "origin" && (s.Publish == nil || s.Publish.Resource != r.Name) {
			return invalid(field, "origin reference requires publication")
		}
	case "service":
		if !slices.Contains(s.Components, r.Name) {
			return invalid(field, "referenced service is not selected")
		}
	case "output":
		if !slices.Contains(c.Outputs, r.Name) {
			return invalid(field, "output reference requires component attachment")
		}
	}
	return nil
}

// Order returns a deterministic dependency-first order, rejecting cycles.
func Order(m v1.Manifest, s v1.Scene) ([]string, error) {
	state := map[string]int{}
	var order []string
	var visit func(string) error
	visit = func(n string) error {
		if state[n] == 1 {
			return invalid("components."+diagnosticKey(n)+".depends_on", "dependency cycle detected")
		}
		if state[n] == 2 {
			return nil
		}
		state[n] = 1
		for _, d := range m.Components[n].DependsOn {
			if err := visit(d.Component); err != nil {
				return err
			}
		}
		state[n] = 2
		order = append(order, n)
		return nil
	}
	for _, name := range s.Components {
		if err := visit(name); err != nil {
			return nil, err
		}
	}
	return order, nil
}

// ValidateMachine validates local settings without contacting providers.
func ValidateMachine(c v1.MachineConfig) error {
	if c.Version != v1.ManifestVersion {
		return invalid("config.version", "unsupported version; use backlot/v1")
	}
	if err := names(c.Tools, "config.tools"); err != nil {
		return err
	}
	if err := names(c.Inputs, "config.inputs"); err != nil {
		return err
	}
	for _, tool := range c.Tools {
		if tool == "" || strings.ContainsRune(tool, 0) {
			return invalid("config.tools", "provide executable names or paths")
		}
	}
	for _, input := range c.Inputs {
		if strings.ContainsRune(input, 0) {
			return invalid("config.inputs", "NUL is not permitted")
		}
	}
	if c.Docker != nil {
		if c.Docker.HostAddress != "" && !validHostAddress(c.Docker.HostAddress) {
			return invalid("config.docker.host_address", "use a host name or numeric IP without port, scheme or credentials")
		}
		if c.Docker.PublishAddress != "" {
			address, err := netip.ParseAddr(c.Docker.PublishAddress)
			if err != nil || address.Zone() != "" || address.IsMulticast() {
				return invalid("config.docker.publish_address", "use a numeric host bind address without zone or port")
			}
		}
		u, err := url.Parse(c.Docker.Endpoint)
		if err != nil || u.Scheme != "unix" || u.Path == "" || u.Host != "" || u.RawQuery != "" || u.Fragment != "" || u.User != nil {
			return invalid("config.docker.endpoint", "use a local unix socket URL")
		}
	}
	if c.Caddy != nil {
		if c.Caddy.HTTPSPort != nil && (*c.Caddy.HTTPSPort < 1 || *c.Caddy.HTTPSPort > 65535) {
			return invalid("config.caddy.https_port", "use a port in 1..65535")
		}
		u, err := url.Parse(c.Caddy.Endpoint)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || !validURLPort(u) || u.RawQuery != "" || u.Fragment != "" {
			return invalid("config.caddy.endpoint", "provide an HTTP(S) admin URL without credentials, query or fragment and with any explicit port in 1..65535")
		}
		if !identifier.MatchString(c.Caddy.Scope) || !validDomain(c.Caddy.DomainSuffix) || len(c.Caddy.DomainSuffix) > 209 || !validHostAddress(c.Caddy.HostAddress) {
			return invalid("config.caddy", "declare owned scope, domain_suffix and gateway-reachable host_address")
		}
	}
	if c.Storage != nil {
		d, err := time.ParseDuration(c.Storage.RetentionAge)
		if err != nil || d <= 0 || c.Storage.RetentionBytes <= 0 || c.Storage.Directory == "" {
			return invalid("config.storage", "declare directory, positive retention_age and retention_bytes")
		}
	}
	return nil
}

func validHostAddress(value string) bool {
	if ip, err := netip.ParseAddr(value); err == nil {
		return ip.Zone() == "" && !ip.IsUnspecified() && !ip.IsMulticast()
	}
	if len(value) == 0 || len(value) > 253 {
		return false
	}
	for _, label := range strings.Split(value, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, char := range label {
			if char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' || char == '-' {
				continue
			}
			return false
		}
	}
	return true
}

func validDomain(value string) bool {
	if len(value) > 253 || !strings.Contains(value, ".") {
		return false
	}
	for _, label := range strings.Split(value, ".") {
		if len(label) == 0 || len(label) > 63 || strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
			return false
		}
		for _, r := range label {
			if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '-' {
				return false
			}
		}
	}
	return true
}

// validNetworkPort accepts numeric destinations only, without service lookup.
func validNetworkPort(port string) bool {
	number, err := strconv.ParseUint(port, 10, 16)
	return err == nil && number > 0
}

// validURLPort allows HTTP(S) defaults but rejects an explicitly empty port.
// url.Parse has already checked the authority and nonnumeric port syntax.
func validURLPort(u *url.URL) bool {
	if u.Port() == "" {
		return !strings.HasSuffix(u.Host, ":")
	}
	return validNetworkPort(u.Port())
}
