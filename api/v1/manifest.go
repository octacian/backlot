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
	Version      string                 `json:"version" yaml:"version"`
	Project      string                 `json:"project" yaml:"project"`
	Tools        map[string]string      `json:"tools,omitempty" yaml:"tools,omitempty"`
	Inputs       map[string]Input       `json:"inputs,omitempty" yaml:"inputs,omitempty"`
	Environments map[string]Environment `json:"environments,omitempty" yaml:"environments,omitempty"`
	Resources    map[string]Resource    `json:"resources,omitempty" yaml:"resources,omitempty"`
	Outputs      map[string]Output      `json:"outputs,omitempty" yaml:"outputs,omitempty"`
	Components   map[string]Component   `json:"components" yaml:"components"`
	Scenes       map[string]Scene       `json:"scenes" yaml:"scenes"`
}

// Input declares a required externally supplied value and its sensitivity.
type Input struct {
	Secret bool `json:"secret,omitempty" yaml:"secret,omitempty"`
}

// Environment explicitly layers selected inherited values, literal seeds and assignments.
type Environment struct {
	PassThrough []string         `json:"pass_through,omitempty" yaml:"pass_through,omitempty"`
	Seeds       []string         `json:"seeds,omitempty" yaml:"seeds,omitempty"`
	Assign      map[string]Value `json:"assign,omitempty" yaml:"assign,omitempty"`
	Required    []string         `json:"required,omitempty" yaml:"required,omitempty"`
}

// Value is exactly one literal or bounded discriminated reference.
type Value struct {
	Literal *string    `json:"literal,omitempty" yaml:"literal,omitempty"`
	Ref     *Reference `json:"ref,omitempty" yaml:"ref,omitempty"`
	Secret  bool       `json:"secret,omitempty" yaml:"secret,omitempty"`
}

// Reference identifies a value without expression evaluation or interpolation.
type Reference struct {
	Kind  string `json:"kind" yaml:"kind"`
	Name  string `json:"name,omitempty" yaml:"name,omitempty"`
	Field string `json:"field,omitempty" yaml:"field,omitempty"`
	Port  string `json:"port,omitempty" yaml:"port,omitempty"`
}

// Resource declares an instance-owned allocation; planning never provisions it.
type Resource struct {
	Kind string `json:"kind" yaml:"kind"`
}

// Output declares a checkout-relative mutable path and serialization group.
type Output struct {
	Path             string `json:"path" yaml:"path"`
	ConcurrencyGroup string `json:"concurrency_group" yaml:"concurrency_group"`
}

// Command declares an executable tool and direct argument vector.
type Command struct {
	Tool string   `json:"tool" yaml:"tool"`
	Args []string `json:"args,omitempty" yaml:"args,omitempty"`
}

// Dependency gates execution on another selected component.
type Dependency struct {
	Component string `json:"component" yaml:"component"`
	Condition Gate   `json:"condition" yaml:"condition"`
}

// Probe declares observed availability and a bounded deadline.
type Probe struct {
	Kind    string   `json:"kind" yaml:"kind"`
	Target  *Value   `json:"target,omitempty" yaml:"target,omitempty"`
	Command *Command `json:"command,omitempty" yaml:"command,omitempty"`
	Timeout string   `json:"timeout" yaml:"timeout"`
}

// Mount connects declared storage or checkout output to a container path.
type Mount struct {
	Resource string `json:"resource,omitempty" yaml:"resource,omitempty"`
	Output   string `json:"output,omitempty" yaml:"output,omitempty"`
	Target   string `json:"target" yaml:"target"`
	ReadOnly bool   `json:"read_only,omitempty" yaml:"read_only,omitempty"`
}

// ServicePort maps an allocated host port to an explicit container listener when needed.
type ServicePort struct {
	Resource      string `json:"resource" yaml:"resource"`
	ContainerPort int    `json:"container_port,omitempty" yaml:"container_port,omitempty"`
}

// Component declares native or container work with explicit dependencies.
type Component struct {
	Kind            ComponentKind          `json:"kind" yaml:"kind"`
	Runtime         Runtime                `json:"runtime" yaml:"runtime"`
	Command         *Command               `json:"command,omitempty" yaml:"command,omitempty"`
	Image           *Value                 `json:"image,omitempty" yaml:"image,omitempty"`
	Args            []string               `json:"args,omitempty" yaml:"args,omitempty"`
	Policy          JobPolicy              `json:"policy,omitempty" yaml:"policy,omitempty"`
	Initializes     []string               `json:"initializes,omitempty" yaml:"initializes,omitempty"`
	DependsOn       []Dependency           `json:"depends_on,omitempty" yaml:"depends_on,omitempty"`
	EnvironmentSets []string               `json:"environment_sets,omitempty" yaml:"environment_sets,omitempty"`
	Environment     Environment            `json:"environment,omitempty" yaml:"environment,omitempty"`
	Resources       []string               `json:"resources,omitempty" yaml:"resources,omitempty"`
	Outputs         []string               `json:"outputs,omitempty" yaml:"outputs,omitempty"`
	Ports           map[string]ServicePort `json:"ports,omitempty" yaml:"ports,omitempty"`
	Mounts          []Mount                `json:"mounts,omitempty" yaml:"mounts,omitempty"`
	Readiness       *Probe                 `json:"readiness,omitempty" yaml:"readiness,omitempty"`
}

// Route is a bounded path route to a selected service port.
type Route struct {
	Path    string `json:"path" yaml:"path"`
	Service string `json:"service" yaml:"service"`
	Port    string `json:"port" yaml:"port"`
	Prefix  string `json:"prefix" yaml:"prefix"`
}

// Publication requests a symbolic HTTPS origin and owned routes.
type Publication struct {
	Resource string  `json:"resource" yaml:"resource"`
	Routes   []Route `json:"routes" yaml:"routes"`
	Probe    Probe   `json:"probe" yaml:"probe"`
}

// Scene explicitly selects all work and resources for one workflow.
type Scene struct {
	Lifetime    Lifetime     `json:"lifetime" yaml:"lifetime"`
	Components  []string     `json:"components" yaml:"components"`
	Resources   []string     `json:"resources,omitempty" yaml:"resources,omitempty"`
	TerminalJob string       `json:"terminal_job,omitempty" yaml:"terminal_job,omitempty"`
	Publish     *Publication `json:"publish,omitempty" yaml:"publish,omitempty"`
}

// MachineConfig contains local provider settings and executable/input overrides.
// It is never included in ordinary plan output.
type MachineConfig struct {
	Version string            `json:"version" yaml:"version"`
	Tools   map[string]string `json:"tools,omitempty" yaml:"tools,omitempty"`
	Inputs  map[string]string `json:"inputs,omitempty" yaml:"inputs,omitempty"`
	Docker  *DockerConfig     `json:"docker,omitempty" yaml:"docker,omitempty"`
	Caddy   *CaddyConfig      `json:"caddy,omitempty" yaml:"caddy,omitempty"`
	Storage *StorageConfig    `json:"storage,omitempty" yaml:"storage,omitempty"`
}

// DockerConfig selects local engine access without contacting it during planning.
type DockerConfig struct {
	Endpoint string `json:"endpoint" yaml:"endpoint"`
}

// CaddyConfig identifies the operator-owned gateway scope and reachability.
type CaddyConfig struct {
	Endpoint     string `json:"endpoint" yaml:"endpoint"`
	Scope        string `json:"scope" yaml:"scope"`
	DomainSuffix string `json:"domain_suffix" yaml:"domain_suffix"`
	HostAddress  string `json:"host_address" yaml:"host_address"`
}

// StorageConfig describes future daemon storage and evidence retention bounds.
type StorageConfig struct {
	Directory      string `json:"directory" yaml:"directory"`
	RetentionAge   string `json:"retention_age" yaml:"retention_age"`
	RetentionBytes int64  `json:"retention_bytes" yaml:"retention_bytes"`
}
