package v1

// ManifestVersion is the supported project and machine schema version.
const ManifestVersion = "backlot/v1"

// Lifetime specifies resource retention across executions.
type Lifetime string

// Supported scene lifetimes.
const (
	Persistent Lifetime = "persistent"
	Disposable Lifetime = "disposable"
)

// ComponentKind distinguishes long-running services from finite jobs.
type ComponentKind string

// Supported component kinds.
const (
	Service ComponentKind = "service"
	Job     ComponentKind = "job"
)

// Runtime selects native or generic container execution.
type Runtime string

// Supported execution runtimes.
const (
	Native    Runtime = "native"
	Container Runtime = "container"
)

// JobPolicy determines when a job is eligible to run.
type JobPolicy string

// Supported job policies.
const (
	FreshOnly JobPolicy = "fresh-only"
	EachStart JobPolicy = "each-start"
)

// Gate is an explicit dependency completion condition.
type Gate string

// Supported dependency gates.
const (
	Ready     Gate = "ready"
	Completed Gate = "completed"
)

// Manifest declares reusable components and independently selected scenes.
type Manifest struct {
	Version      string                 `json:"version" yaml:"version" jsonschema_description:"Contract version; use backlot/v1."`
	Project      string                 `json:"project" yaml:"project" jsonschema_description:"Stable project identifier, independent of branch names."`
	Tools        map[string]string      `json:"tools,omitempty" yaml:"tools,omitempty" jsonschema_description:"Named executables; planning checks availability without running them."`
	Inputs       map[string]Input       `json:"inputs,omitempty" yaml:"inputs,omitempty" jsonschema_description:"Explicit external inputs; keep machine credentials out of projects."`
	Environments map[string]Environment `json:"environments,omitempty" yaml:"environments,omitempty" jsonschema_description:"Reusable environment layers; include them explicitly."`
	Resources    map[string]Resource    `json:"resources,omitempty" yaml:"resources,omitempty" jsonschema_description:"Instance-owned allocations selected explicitly; planning does not provision them."`
	Outputs      map[string]Output      `json:"outputs,omitempty" yaml:"outputs,omitempty" jsonschema_description:"Checkout output paths and serialization groups."`
	Components   map[string]Component   `json:"components" yaml:"components" jsonschema_description:"Reusable native or container services and jobs."`
	Scenes       map[string]Scene       `json:"scenes" yaml:"scenes" jsonschema_description:"Named workflows with explicit selection and lifetime."`
}

// Input declares a required externally supplied value and its sensitivity.
type Input struct {
	Secret bool `json:"secret,omitempty" yaml:"secret,omitempty" jsonschema_description:"Treat the value as sensitive in ordinary plan output."`
}

// Environment explicitly layers selected inherited values, literal seeds and assignments.
type Environment struct {
	PassThrough []string         `json:"pass_through,omitempty" yaml:"pass_through,omitempty" jsonschema_description:"Selected inherited environment variable names; never inherit the full environment."`
	Seeds       []string         `json:"seeds,omitempty" yaml:"seeds,omitempty" jsonschema_description:"Read-only checkout-relative KEY=value files, layered in declaration order."`
	Assign      map[string]Value `json:"assign,omitempty" yaml:"assign,omitempty" jsonschema_description:"Explicit environment assignments applied after seeds."`
	Required    []string         `json:"required,omitempty" yaml:"required,omitempty" jsonschema_description:"Environment keys that must be present after layering."`
}

// Value is exactly one literal or bounded discriminated reference.
type Value struct {
	Literal *string    `json:"literal,omitempty" yaml:"literal,omitempty" jsonschema:"oneof_required=literal" jsonschema_description:"Literal string, including an empty string; mutually exclusive with ref."`
	Ref     *Reference `json:"ref,omitempty" yaml:"ref,omitempty" jsonschema:"oneof_required=reference" jsonschema_description:"Bounded symbolic reference; mutually exclusive with literal."`
	Secret  bool       `json:"secret,omitempty" yaml:"secret,omitempty" jsonschema_description:"Treat the value as sensitive in ordinary plan output."`
}

// Reference identifies a value without expression evaluation or interpolation.
type Reference struct {
	Kind  string `json:"kind" yaml:"kind" jsonschema_description:"Explicit discriminator; see the supported values and conditional fields."`
	Name  string `json:"name,omitempty" yaml:"name,omitempty" jsonschema_description:"Declared identifier used by this reference; graph lookup happens in backlot plan."`
	Field string `json:"field,omitempty" yaml:"field,omitempty" jsonschema_description:"Bounded field selected from the referenced object."`
	Port  string `json:"port,omitempty" yaml:"port,omitempty" jsonschema_description:"Named service port, required by service port references."`
}

// Resource declares an instance-owned allocation; planning never provisions it.
type Resource struct {
	Kind string `json:"kind" yaml:"kind" jsonschema_description:"Explicit discriminator; see the supported values and conditional fields."`
}

// Output declares a checkout-relative mutable path and serialization group.
type Output struct {
	Path             string `json:"path" yaml:"path" jsonschema:"minLength=1" jsonschema_description:"Safe checkout-relative mutable output path."`
	ConcurrencyGroup string `json:"concurrency_group" yaml:"concurrency_group" jsonschema:"minLength=1" jsonschema_description:"Named serialization group for overlapping checkout outputs."`
}

// Command declares an executable tool and direct argument vector.
type Command struct {
	Tool string   `json:"tool" yaml:"tool" jsonschema:"minLength=1" jsonschema_description:"Name from the project tools map; no implicit shell interpretation."`
	Args []string `json:"args,omitempty" yaml:"args,omitempty" jsonschema_description:"Direct argument vector; shell expansion requires an explicit shell command."`
}

// Dependency gates execution on another selected component.
type Dependency struct {
	Component string `json:"component" yaml:"component" jsonschema:"minLength=1" jsonschema_description:"Selected dependency component name."`
	Condition Gate   `json:"condition" yaml:"condition" jsonschema_description:"Wait for a service to be ready or a job to complete successfully."`
}

// Probe declares observed availability and a bounded deadline.
type Probe struct {
	Kind    string   `json:"kind" yaml:"kind" jsonschema_description:"Explicit discriminator; see the supported values and conditional fields."`
	Target  *Value   `json:"target,omitempty" yaml:"target,omitempty" jsonschema_description:"Probe destination or absolute mount target, according to this contract."`
	Command *Command `json:"command,omitempty" yaml:"command,omitempty" jsonschema_description:"Explicit native executable tool and argument vector."`
	Timeout string   `json:"timeout" yaml:"timeout" jsonschema:"minLength=1" jsonschema_description:"Positive Go duration, at most 24h; for example 30s or 1m30s. Planning checks the bound."`
}

// Mount connects declared storage or checkout output to a container path.
type Mount struct {
	Resource string `json:"resource,omitempty" yaml:"resource,omitempty" jsonschema_description:"Declared resource name; selection and kind are checked by backlot plan."`
	Output   string `json:"output,omitempty" yaml:"output,omitempty" jsonschema_description:"Declared checkout output name attached to the component."`
	Target   string `json:"target" yaml:"target" jsonschema:"minLength=1" jsonschema_description:"Probe destination or absolute mount target, according to this contract."`
	ReadOnly bool   `json:"read_only,omitempty" yaml:"read_only,omitempty" jsonschema_description:"Mount the declared source read-only."`
}

// ServicePort maps an allocated host port to an explicit container listener when needed.
type ServicePort struct {
	Resource      string `json:"resource" yaml:"resource" jsonschema:"minLength=1" jsonschema_description:"Declared resource name; selection and kind are checked by backlot plan."`
	ContainerPort int    `json:"container_port,omitempty" yaml:"container_port,omitempty" jsonschema:"minimum=0,maximum=65535" jsonschema_description:"Container listener port 1..65535; native services omit this or use zero."`
}

// Component declares native or container work with explicit dependencies.
type Component struct {
	Kind            ComponentKind          `json:"kind" yaml:"kind" jsonschema_description:"Explicit discriminator; see the supported values and conditional fields."`
	Runtime         Runtime                `json:"runtime" yaml:"runtime" jsonschema_description:"Native command or generic container; selects command versus image."`
	Command         *Command               `json:"command,omitempty" yaml:"command,omitempty" jsonschema_description:"Explicit native executable tool and argument vector."`
	Image           *Value                 `json:"image,omitempty" yaml:"image,omitempty" jsonschema_description:"Container image literal or declared input reference."`
	Args            []string               `json:"args,omitempty" yaml:"args,omitempty" jsonschema_description:"Direct argument vector; shell expansion requires an explicit shell command."`
	Policy          JobPolicy              `json:"policy,omitempty" yaml:"policy,omitempty" jsonschema_description:"Job scheduling policy; services omit this field."`
	Initializes     []string               `json:"initializes,omitempty" yaml:"initializes,omitempty" jsonschema_description:"Attached volume/directory generations initialized by a fresh-only job."`
	DependsOn       []Dependency           `json:"depends_on,omitempty" yaml:"depends_on,omitempty" jsonschema_description:"Explicit ready-service or completed-job dependencies."`
	EnvironmentSets []string               `json:"environment_sets,omitempty" yaml:"environment_sets,omitempty" jsonschema_description:"Reusable environment sets included in this order."`
	Environment     Environment            `json:"environment,omitempty" yaml:"environment,omitempty" jsonschema_description:"Component environment layers applied after included sets."`
	Resources       []string               `json:"resources,omitempty" yaml:"resources,omitempty" jsonschema_description:"Instance-owned allocations selected explicitly; planning does not provision them."`
	Outputs         []string               `json:"outputs,omitempty" yaml:"outputs,omitempty" jsonschema_description:"Checkout output paths and serialization groups."`
	Ports           map[string]ServicePort `json:"ports,omitempty" yaml:"ports,omitempty" jsonschema_description:"Named allocated host ports with explicit container listeners when needed."`
	Mounts          []Mount                `json:"mounts,omitempty" yaml:"mounts,omitempty" jsonschema_description:"Container storage/output mounts with exactly one source each."`
	Readiness       *Probe                 `json:"readiness,omitempty" yaml:"readiness,omitempty" jsonschema_description:"Required service probe; process liveness alone is insufficient."`
}

// Route is a bounded path route to a selected service port.
type Route struct {
	Path    string `json:"path" yaml:"path" jsonschema:"minLength=1" jsonschema_description:"Checkout-relative output path or absolute route path, according to this contract."`
	Service string `json:"service" yaml:"service" jsonschema:"minLength=1" jsonschema_description:"Selected service name receiving this route."`
	Port    string `json:"port" yaml:"port" jsonschema:"minLength=1" jsonschema_description:"Named service port, required by service port references."`
	Prefix  string `json:"prefix" yaml:"prefix" jsonschema:"enum=preserve,enum=strip" jsonschema_description:"Preserve or strip the matched route prefix."`
}

// Publication requests a symbolic HTTPS origin and owned routes.
type Publication struct {
	Resource string  `json:"resource" yaml:"resource" jsonschema:"minLength=1" jsonschema_description:"Declared resource name; selection and kind are checked by backlot plan."`
	Routes   []Route `json:"routes" yaml:"routes" jsonschema_description:"Nonempty bounded routes inside the owned publication."`
	Probe    Probe   `json:"probe" yaml:"probe" jsonschema_description:"Aggregate HTTP readiness through the published origin."`
}

// Scene explicitly selects all work and resources for one workflow.
type Scene struct {
	Lifetime    Lifetime     `json:"lifetime" yaml:"lifetime" jsonschema_description:"Persistent retains data; disposable runs finish at a terminal job."`
	Components  []string     `json:"components" yaml:"components" jsonschema_description:"Explicit nonempty selection of component names; dependencies are never included implicitly."`
	Resources   []string     `json:"resources,omitempty" yaml:"resources,omitempty" jsonschema_description:"Instance-owned allocations selected explicitly; planning does not provision them."`
	TerminalJob string       `json:"terminal_job,omitempty" yaml:"terminal_job,omitempty" jsonschema_description:"Selected each-start job; required for disposable scenes."`
	Publish     *Publication `json:"publish,omitempty" yaml:"publish,omitempty" jsonschema_description:"Optional owned HTTPS origin, routes and aggregate readiness."`
}

// MachineConfig contains local provider settings and executable/input overrides.
// It is never included in ordinary plan output.
type MachineConfig struct {
	Version string            `json:"version" yaml:"version" jsonschema_description:"Contract version; use backlot/v1."`
	Tools   map[string]string `json:"tools,omitempty" yaml:"tools,omitempty" jsonschema_description:"Named executables; planning checks availability without running them."`
	Inputs  map[string]string `json:"inputs,omitempty" yaml:"inputs,omitempty" jsonschema_description:"Explicit external inputs; keep machine credentials out of projects."`
	Docker  *DockerConfig     `json:"docker,omitempty" yaml:"docker,omitempty" jsonschema_description:"Local engine configuration; planning does not contact Docker."`
	Caddy   *CaddyConfig      `json:"caddy,omitempty" yaml:"caddy,omitempty" jsonschema_description:"Operator-owned gateway scope and reachability; no health check during planning."`
	Storage *StorageConfig    `json:"storage,omitempty" yaml:"storage,omitempty" jsonschema_description:"Future daemon storage and evidence retention settings."`
}

// DockerConfig selects local engine access without contacting it during planning.
type DockerConfig struct {
	Endpoint string `json:"endpoint" yaml:"endpoint" jsonschema:"minLength=1" jsonschema_description:"Local Docker unix socket URL or Caddy HTTP(S) admin URL."`
}

// CaddyConfig identifies the operator-owned gateway scope and reachability.
type CaddyConfig struct {
	Endpoint     string `json:"endpoint" yaml:"endpoint" jsonschema:"minLength=1" jsonschema_description:"Local Docker unix socket URL or Caddy HTTP(S) admin URL."`
	Scope        string `json:"scope" yaml:"scope" jsonschema:"minLength=1" jsonschema_description:"Identifier of the Backlot-owned gateway scope."`
	DomainSuffix string `json:"domain_suffix" yaml:"domain_suffix" jsonschema:"minLength=1" jsonschema_description:"Operator-managed lowercase domain suffix, including at least one dot."`
	HostAddress  string `json:"host_address" yaml:"host_address" jsonschema:"minLength=1" jsonschema_description:"Gateway-reachable host address for native upstreams."`
}

// StorageConfig describes future daemon storage and evidence retention bounds.
type StorageConfig struct {
	Directory      string `json:"directory" yaml:"directory" jsonschema:"minLength=1" jsonschema_description:"Future daemon state and evidence directory."`
	RetentionAge   string `json:"retention_age" yaml:"retention_age" jsonschema:"minLength=1" jsonschema_description:"Positive Go duration; for example 168h. Planning checks duration syntax."`
	RetentionBytes int64  `json:"retention_bytes" yaml:"retention_bytes" jsonschema:"minimum=1" jsonschema_description:"Positive evidence retention size in bytes."`
}
