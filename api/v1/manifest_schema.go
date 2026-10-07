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
		{AllOf: []*jsonschema.Schema{schemaProperties(map[string]*jsonschema.Schema{"kind": {Const: "resource"}, "field": schemaEnum("port", "path", "name", "value", "url")}), named("field"), schemaEmpty("port")}},
		{AllOf: []*jsonschema.Schema{schemaProperties(map[string]*jsonschema.Schema{"kind": {Const: "output"}, "field": {Const: "path"}}), named("field"), schemaEmpty("port")}},
		{AllOf: []*jsonschema.Schema{schemaProperties(map[string]*jsonschema.Schema{"kind": {Const: "service"}, "field": {Const: "host"}}), named("field"), schemaEmpty("port")}},
		{AllOf: []*jsonschema.Schema{schemaProperties(map[string]*jsonschema.Schema{"kind": {Const: "service"}, "field": {Const: "port"}, "port": {Pattern: schemaIdentifier}}), named("field", "port")}},
	}
}

// JSONSchemaExtend constrains supported resource kinds.
func (Resource) JSONSchemaExtend(s *jsonschema.Schema) {
	schemaProperty(s, "kind").Enum = schemaEnum("port", "volume", "directory", "secret", "network", "origin").Enum
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
		{AllOf: []*jsonschema.Schema{schemaRequire("resource"), schemaProperties(map[string]*jsonschema.Schema{"resource": {MinLength: schemaOne()}}), schemaEmpty("output")}},
		{AllOf: []*jsonschema.Schema{schemaRequire("output"), schemaProperties(map[string]*jsonschema.Schema{"output": {MinLength: schemaOne()}}), schemaEmpty("resource")}},
	}
	schemaProperty(s, "target").Pattern = "^/"
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
			"args":   {MaxItems: schemaZero()},
			"mounts": {MaxItems: schemaZero()},
			"ports":  {AdditionalProperties: schemaProperties(map[string]*jsonschema.Schema{"container_port": {Const: 0}})},
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
	s.AllOf = []*jsonschema.Schema{schemaWhen("lifetime", string(Disposable), &jsonschema.Schema{AllOf: []*jsonschema.Schema{schemaRequire("terminal_job"), schemaProperties(map[string]*jsonschema.Schema{"terminal_job": {MinLength: schemaOne()}})}}), schemaWhen("lifetime", string(Persistent), schemaProperties(map[string]*jsonschema.Schema{"terminal_job": {Const: ""}}))}
}

// JSONSchemaExtend constrains dependency gates.
func (Dependency) JSONSchemaExtend(s *jsonschema.Schema) {
	schemaProperty(s, "condition").Enum = schemaEnum(string(Ready), string(Completed)).Enum
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
	schemaProperty(s, "scope").Pattern = schemaIdentifier
	schemaProperty(s, "domain_suffix").Pattern = `^[a-z0-9-]+(\.[a-z0-9-]+)+$`
	schemaProperty(s, "host_address").Pattern = "^[^/\x00 \n\r]+$"
}
