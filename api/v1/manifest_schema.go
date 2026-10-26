package v1

import (
	"maps"
	"slices"

	"github.com/invopop/jsonschema"
	orderedmap "github.com/pb33f/ordered-map/v2"
)

// Schema hooks only add local constraints to reflected contracts. They never
// define a parallel field inventory or change JSON/YAML encoding behavior.
const schemaIdentifier = `^[a-z][a-z0-9_-]{0,62}$`
const schemaEnvKey = `^[A-Za-z_][A-Za-z0-9_]*$`

func schemaEnum(values ...string) *jsonschema.Schema {
	s := &jsonschema.Schema{Type: "string"}
	for _, v := range values {
		s.Enum = append(s.Enum, v)
	}
	return s
}

func schemaProperties(values map[string]*jsonschema.Schema) *jsonschema.Schema {
	// Property order must be stable even for the small conditional fragments.
	s := &jsonschema.Schema{Properties: orderedmap.New[string, *jsonschema.Schema]()}
	// Definitions are not inventories: these are only predicates on reflected fields.
	for _, key := range slices.Sorted(maps.Keys(values)) {
		s.Properties.Set(key, values[key])
	}
	return s
}

func schemaRequire(fields ...string) *jsonschema.Schema { return &jsonschema.Schema{Required: fields} }
func schemaForbid(fields ...string) *jsonschema.Schema {
	s := &jsonschema.Schema{}
	for _, f := range fields {
		s.AllOf = append(s.AllOf, &jsonschema.Schema{Not: schemaRequire(f)})
	}
	return s
}

// schemaEmpty matches runtime scalar zero-value checks: omission and empty are equivalent.
func schemaEmpty(fields ...string) *jsonschema.Schema {
	values := make(map[string]*jsonschema.Schema, len(fields))
	for _, field := range fields {
		values[field] = &jsonschema.Schema{Const: ""}
	}
	return schemaProperties(values)
}

func schemaWhen(field, value string, then *jsonschema.Schema) *jsonschema.Schema {
	predicate := schemaProperties(map[string]*jsonschema.Schema{field: {Const: value}})
	predicate.Required = []string{field}
	return &jsonschema.Schema{If: predicate, Then: then}
}
func schemaProperty(s *jsonschema.Schema, name string) *jsonschema.Schema {
	p, ok := s.Properties.Get(name)
	if !ok {
		panic("schema constraint refers to missing contract field: " + name)
	}
	return p
}
func schemaNamedMaps(s *jsonschema.Schema) {
	for pair := s.Properties.Oldest(); pair != nil; pair = pair.Next() {
		if pair.Value.Type == "object" && pair.Value.AdditionalProperties != nil {
			pair.Value.PropertyNames = &jsonschema.Schema{Pattern: schemaIdentifier}
		}
	}
}
func schemaNonemptyMap(s *jsonschema.Schema, name string) {
	n := uint64(1)
	schemaProperty(s, name).MinProperties = &n
}

// JSONSchemaExtend adds project version and named declaration constraints.
func (Manifest) JSONSchemaExtend(s *jsonschema.Schema) {
	schemaProperty(s, "version").Const = ManifestVersion
	schemaProperty(s, "project").Pattern = schemaIdentifier
	schemaNamedMaps(s)
	schemaNonemptyMap(s, "components")
	schemaNonemptyMap(s, "scenes")
	schemaProperty(s, "tools").AdditionalProperties = &jsonschema.Schema{Type: "string", MinLength: schemaOne()}
}

// JSONSchemaExtend adds machine version and override constraints.
func (MachineConfig) JSONSchemaExtend(s *jsonschema.Schema) {
	schemaProperty(s, "version").Const = ManifestVersion
	schemaNamedMaps(s)
	schemaProperty(s, "tools").AdditionalProperties = &jsonschema.Schema{Type: "string", MinLength: schemaOne()}
}
func schemaOne() *uint64 { n := uint64(1); return &n }

// JSONSchemaExtend constrains environment variable names and unique selections.
func (Environment) JSONSchemaExtend(s *jsonschema.Schema) {
	schemaProperty(s, "assign").PropertyNames = &jsonschema.Schema{Pattern: schemaEnvKey}
	for _, name := range []string{"pass_through", "required", "seeds"} {
		p := schemaProperty(s, name)
		p.UniqueItems = true
		p.Items.MinLength = schemaOne()
		if name != "seeds" {
			p.Items.Pattern = schemaEnvKey
		}
	}
}

// JSONSchemaExtend adds bounded reference discriminator shapes, not graph lookup.
func (Reference) JSONSchemaExtend(s *jsonschema.Schema) {
	schemaProperty(s, "kind").Enum = schemaEnum("input", "instance", "resource", "output", "service").Enum
	named := func(fields ...string) *jsonschema.Schema {
		p := schemaProperties(map[string]*jsonschema.Schema{"name": {Pattern: schemaIdentifier}})
		p.Required = append([]string{"name"}, fields...)
		return p
	}
	s.OneOf = []*jsonschema.Schema{
		{AllOf: []*jsonschema.Schema{schemaProperties(map[string]*jsonschema.Schema{"kind": {Const: "input"}}), named(), schemaEmpty("field", "port")}},
		{AllOf: []*jsonschema.Schema{schemaProperties(map[string]*jsonschema.Schema{"kind": {Const: "instance"}, "field": schemaEnum("id", "checkout", "project", "scene")}), schemaRequire("field"), schemaEmpty("name", "port")}},
		{AllOf: []*jsonschema.Schema{schemaProperties(map[string]*jsonschema.Schema{"kind": {Const: "resource"}, "field": schemaEnum("port", "path", "name", "value", "url", "ca_cert_path", "host", "database", "username", "password")}), named("field"), schemaEmpty("port")}},
		{AllOf: []*jsonschema.Schema{schemaProperties(map[string]*jsonschema.Schema{"kind": {Const: "output"}, "field": {Const: "path"}}), named("field"), schemaEmpty("port")}},
		{AllOf: []*jsonschema.Schema{schemaProperties(map[string]*jsonschema.Schema{"kind": {Const: "service"}, "field": {Const: "host"}}), named("field"), schemaEmpty("port")}},
		{AllOf: []*jsonschema.Schema{schemaProperties(map[string]*jsonschema.Schema{"kind": {Const: "service"}, "field": {Const: "port"}, "port": {Pattern: schemaIdentifier}}), named("field", "port")}},
	}
}

// JSONSchemaExtend constrains supported resource kinds.
func (Resource) JSONSchemaExtend(s *jsonschema.Schema) {
	schemaProperty(s, "kind").Enum = schemaEnum("port", "volume", "directory", "secret", "network", "origin", "allocation").Enum
	s.AllOf = append(s.AllOf, schemaWhen("kind", "allocation", schemaRequire("shared_service")))
}

// JSONSchemaExtend constrains output serialization identifiers.
func (Output) JSONSchemaExtend(s *jsonschema.Schema) {
	schemaProperty(s, "concurrency_group").Pattern = schemaIdentifier
}

// schemaProbeTarget restricts reference discriminators while retaining ordinary
// literal targets. Resource declaration lookup remains runtime validation.
func schemaProbeTarget(field string, kinds ...string) *jsonschema.Schema {
	return schemaProperties(map[string]*jsonschema.Schema{
		"target": schemaProperties(map[string]*jsonschema.Schema{
			"ref": schemaProperties(map[string]*jsonschema.Schema{
				"kind": schemaEnum(kinds...), "field": {Const: field},
			}),
		}),
	})
}

// JSONSchemaExtend enforces network versus command probe shapes.
func (Probe) JSONSchemaExtend(s *jsonschema.Schema) {
	schemaProperty(s, "kind").Enum = schemaEnum("tcp", "http", "command").Enum
	s.AllOf = []*jsonschema.Schema{
		schemaWhen("kind", "command", &jsonschema.Schema{AllOf: []*jsonschema.Schema{schemaRequire("command"), schemaForbid("target")}}),
		schemaWhen("kind", "tcp", &jsonschema.Schema{AllOf: []*jsonschema.Schema{schemaRequire("target"), schemaForbid("command"), schemaProbeTarget("port", "service", "resource")}}),
		schemaWhen("kind", "http", &jsonschema.Schema{AllOf: []*jsonschema.Schema{schemaRequire("target"), schemaForbid("command"), schemaProbeTarget("url", "resource")}}),
	}
}

// JSONSchemaExtend enforces exactly one mount source.
func (Mount) JSONSchemaExtend(s *jsonschema.Schema) {
	s.OneOf = []*jsonschema.Schema{
		{AllOf: []*jsonschema.Schema{schemaRequire("resource"), schemaProperties(map[string]*jsonschema.Schema{"resource": {MinLength: schemaOne()}}), schemaEmpty("output"), schemaEmpty("origin_ca_cert")}},
		{AllOf: []*jsonschema.Schema{schemaRequire("output"), schemaProperties(map[string]*jsonschema.Schema{"output": {MinLength: schemaOne()}}), schemaEmpty("resource"), schemaEmpty("origin_ca_cert")}},
		{AllOf: []*jsonschema.Schema{schemaRequire("origin_ca_cert", "read_only"), schemaProperties(map[string]*jsonschema.Schema{"origin_ca_cert": {MinLength: schemaOne()}, "read_only": {Const: true}}), schemaEmpty("resource"), schemaEmpty("output")}},
	}
	schemaProperty(s, "target").Pattern = "^/"
}

// JSONSchemaExtend bounds explicit origin identifiers.
func (HostMapping) JSONSchemaExtend(s *jsonschema.Schema) {
	schemaProperty(s, "origin").Pattern = schemaIdentifier
}

// JSONSchemaExtend adds native/container and service/job conditional constraints.
func (Component) JSONSchemaExtend(s *jsonschema.Schema) {
	schemaProperty(s, "kind").Enum = schemaEnum(string(Service), string(Job)).Enum
	schemaProperty(s, "runtime").Enum = schemaEnum(string(Native), string(Container)).Enum
	schemaProperty(s, "policy").Enum = schemaEnum("", string(FreshOnly), string(EachStart)).Enum
	schemaNamedMaps(s)
	native := &jsonschema.Schema{AllOf: []*jsonschema.Schema{
		schemaRequire("command"), schemaForbid("image"),
		schemaProperties(map[string]*jsonschema.Schema{
			"args":          {MaxItems: schemaZero()},
			"mounts":        {MaxItems: schemaZero()},
			"host_mappings": {MaxItems: schemaZero()},
			"ports":         {AdditionalProperties: schemaProperties(map[string]*jsonschema.Schema{"container_port": {Const: 0}})},
		}),
	}}
	container := &jsonschema.Schema{AllOf: []*jsonschema.Schema{
		schemaRequire("image"), schemaForbid("command"),
		schemaProperties(map[string]*jsonschema.Schema{"image": schemaProperties(map[string]*jsonschema.Schema{
			"literal": {MinLength: schemaOne()},
			"ref":     schemaProperties(map[string]*jsonschema.Schema{"kind": {Const: "input"}}),
		})}),
		schemaProperties(map[string]*jsonschema.Schema{
			"ports": {AdditionalProperties: &jsonschema.Schema{AllOf: []*jsonschema.Schema{
				schemaRequire("container_port"),
				schemaProperties(map[string]*jsonschema.Schema{"container_port": {Minimum: "1"}}),
			}}},
		}),
	}}
	service := &jsonschema.Schema{AllOf: []*jsonschema.Schema{
		schemaRequire("readiness"),
		schemaProperties(map[string]*jsonschema.Schema{
			"policy": {Const: ""}, "initializes": {MaxItems: schemaZero()},
		}),
	}}
	job := &jsonschema.Schema{AllOf: []*jsonschema.Schema{
		schemaRequire("policy"), schemaForbid("readiness"),
		schemaProperties(map[string]*jsonschema.Schema{
			"policy": schemaEnum(string(FreshOnly), string(EachStart)),
			"ports":  {MaxProperties: schemaZero()},
		}),
	}}
	fresh := schemaProperties(map[string]*jsonschema.Schema{"initializes": {MinItems: schemaOne()}})
	fresh.Required = []string{"initializes"}
	s.AllOf = []*jsonschema.Schema{
		schemaWhen("runtime", string(Native), native),
		schemaWhen("runtime", string(Container), container),
		schemaWhen("kind", string(Service), service),
		schemaWhen("kind", string(Job), job),
		schemaWhen("policy", string(FreshOnly), fresh),
		schemaWhen("policy", string(EachStart), schemaProperties(map[string]*jsonschema.Schema{"initializes": {MaxItems: schemaZero()}})),
	}
	for _, name := range []string{"initializes", "environment_sets", "resources", "outputs"} {
		p := schemaProperty(s, name)
		p.UniqueItems = true
		p.Items.MinLength = schemaOne()
	}
}
func schemaZero() *uint64 { n := uint64(0); return &n }

// JSONSchemaExtend adds scene selection and disposable terminal-job constraints.
func (Scene) JSONSchemaExtend(s *jsonschema.Schema) {
	schemaProperty(s, "lifetime").Enum = schemaEnum(string(Persistent), string(Disposable)).Enum
	p := schemaProperty(s, "components")
	p.MinItems = schemaOne()
	p.UniqueItems = true
	p.Items.MinLength = schemaOne()
	schemaProperty(s, "resources").UniqueItems = true
	s.AllOf = []*jsonschema.Schema{schemaWhen("lifetime", string(Disposable), &jsonschema.Schema{AllOf: []*jsonschema.Schema{schemaRequire("terminal_job"), schemaProperties(map[string]*jsonschema.Schema{"startup_details": {MaxItems: schemaZero()}}), schemaProperties(map[string]*jsonschema.Schema{"terminal_job": {MinLength: schemaOne()}})}}), schemaWhen("lifetime", string(Persistent), schemaProperties(map[string]*jsonschema.Schema{"terminal_job": {Const: ""}}))}
}

// JSONSchemaExtend constrains dependency gates.
func (Dependency) JSONSchemaExtend(s *jsonschema.Schema) {
	schemaProperty(s, "condition").Enum = schemaEnum(string(Ready), string(Completed)).Enum
}

// JSONSchemaExtend constrains publication routes and aggregate probe kind.
func (Route) JSONSchemaExtend(s *jsonschema.Schema) {
	schemaProperty(s, "path").Pattern = LiteralRoutePathPattern
}

// JSONSchemaExtend constrains publication routes and aggregate probe kind.
func (Publication) JSONSchemaExtend(s *jsonschema.Schema) {
	schemaProperty(s, "routes").MinItems = schemaOne()
	s.AllOf = []*jsonschema.Schema{schemaProperties(map[string]*jsonschema.Schema{
		"probe": schemaProperties(map[string]*jsonschema.Schema{
			"kind": {Const: "http"}, "target": schemaRequire("ref"),
		}),
	})}
}

// JSONSchemaExtend adds useful local socket guidance without contacting Docker.
func (DockerConfig) JSONSchemaExtend(s *jsonschema.Schema) {
	// Accept absolute paths with or without an empty authority, but no host.
	// Go also accepts a case-insensitive scheme and empty query/fragment delimiters.
	schemaProperty(s, "endpoint").Pattern = `^[Uu][Nn][Ii][Xx]:(/([^/?#][^?#]*)?|///[^?#]*)\??#?$`
}

// JSONSchemaExtend adds local gateway shape constraints; planning validates full URLs/domains.
func (CaddyConfig) JSONSchemaExtend(s *jsonschema.Schema) {
	// Match Go's case-insensitive schemes and zero-valued query/fragment delimiters.
	schemaProperty(s, "endpoint").Pattern = `^[Hh][Tt][Tt][Pp][Ss]?://[^/?#@]+(/[^?#]*)?\??#?$`
	octet := `(25[0-5]|2[0-4][0-9]|1[0-9]{2}|[1-9]?[0-9])`
	schemaProperty(s, "https_bind_address").Pattern = `^$|^(` + octet + `\.){3}` + octet + `$`
	schemaProperty(s, "https_bind_address").Not = &jsonschema.Schema{Pattern: `^(22[4-9]|23[0-9])\.`}
	schemaProperty(s, "scope").Pattern = schemaIdentifier
	schemaProperty(s, "domain_suffix").Pattern = `^[a-z0-9-]+(\.[a-z0-9-]+)+$`
	schemaProperty(s, "host_address").Pattern = "^[^/\x00 \n\r]+$"
	// Omitted mode retains external requirements; only managed mode supplies
	// defaults for the endpoint, scope, suffix and upstream address.
	managed := schemaWhen("mode", "managed", &jsonschema.Schema{AllOf: []*jsonschema.Schema{
		schemaRequire("executable"),
		schemaProperties(map[string]*jsonschema.Schema{
			"executable": {MinLength: schemaOne(), Pattern: "^/"},
			"https_port": {Minimum: "1024"},
		}),
	}})
	managed.Else = &jsonschema.Schema{AllOf: []*jsonschema.Schema{
		schemaRequire("endpoint", "scope", "domain_suffix", "host_address"),
		schemaEmpty("executable"),
		schemaEmpty("https_bind_address"),
	}}
	s.AllOf = []*jsonschema.Schema{managed}
}

// JSONSchemaExtend constrains private document version and named scalar overrides.
func (PrivateConfig) JSONSchemaExtend(s *jsonschema.Schema) {
	schemaProperty(s, "version").Const = ManifestVersion
	schemaNamedMaps(s)
	schemaProperty(s, "tools").AdditionalProperties = &jsonschema.Schema{Type: "string", MinLength: schemaOne()}
}

// JSONSchemaExtend restricts generation hints to eligible secret declarations.
func (Input) JSONSchemaExtend(s *jsonschema.Schema) {
	schemaProperty(s, "generate").Enum = schemaEnum("", "random-hex-32").Enum
	s.AllOf = append(s.AllOf, schemaWhen("generate", "random-hex-32", schemaProperties(map[string]*jsonschema.Schema{"secret": {Const: true}})))
	s.AllOf[len(s.AllOf)-1].Then.Required = []string{"secret"}
}

// JSONSchemaExtend constrains startup fixture selectors without duplicating values.
func (StartupDetailSelection) JSONSchemaExtend(s *jsonschema.Schema) {
	schemaProperty(s, "component").Pattern = schemaIdentifier
	schemaProperty(s, "fixture").Pattern = schemaIdentifier
	schemaProperty(s, "label").Pattern = `^[^\x00\r\n]*\S[^\x00\r\n]*$`
}

// JSONSchemaExtend pairs each bundled manager with its internal SQL port.
func (SharedService) JSONSchemaExtend(s *jsonschema.Schema) {
	s.AllOf = append(s.AllOf,
		schemaWhen("manager", "mariadb", schemaProperties(map[string]*jsonschema.Schema{"port": {Const: 3306}})),
		schemaWhen("manager", "postgresql", schemaProperties(map[string]*jsonschema.Schema{"port": {Const: 5432}})))
}
