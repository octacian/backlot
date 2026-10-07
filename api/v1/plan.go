package v1

// PlanRequest selects an offline scene and arguments for its terminal job.
type PlanRequest struct {
	Scene        string   `json:"scene"`
	ProjectPath  string   `json:"project_path,omitempty"`
	ConfigPath   string   `json:"config_path,omitempty"`
	TerminalArgs []string `json:"terminal_args,omitempty"`
}

// PlanResponse is a redacted, allocation-free execution plan.
type PlanResponse struct {
	APIVersion     string              `json:"api_version"`
	Project        string              `json:"project"`
	Checkout       string              `json:"checkout"`
	ManifestPath   string              `json:"manifest_path"`
	ManifestDigest string              `json:"manifest_digest"`
	ConfigDigest   string              `json:"config_digest,omitempty"`
	Scene          string              `json:"scene"`
	Lifetime       Lifetime            `json:"lifetime"`
	TerminalJob    string              `json:"terminal_job,omitempty"`
	Components     []PlannedComponent  `json:"components"`
	Resources      map[string]Resource `json:"resources,omitempty"`
	Outputs        map[string]Output   `json:"outputs,omitempty"`
	Publish        *Publication        `json:"publish,omitempty"`
}

// PlannedValue contains a known value, a symbolic allocation reference, or redaction.
// Redacted values never carry their literal or reference payload.
type PlannedValue struct {
	Literal  *string    `json:"literal,omitempty"`
	Symbolic *Reference `json:"symbolic,omitempty"`
	Redacted bool       `json:"redacted,omitempty"`
}

// PlannedComponent contains selected work in dependency order.
type PlannedComponent struct {
	Name        string                  `json:"name"`
	Kind        ComponentKind           `json:"kind"`
	Runtime     Runtime                 `json:"runtime"`
	Command     *Command                `json:"command,omitempty"`
	Args        []string                `json:"args,omitempty"`
	Policy      JobPolicy               `json:"policy,omitempty"`
	Initializes []string                `json:"initializes,omitempty"`
	DependsOn   []Dependency            `json:"depends_on,omitempty"`
	Resources   []string                `json:"resources,omitempty"`
	Outputs     []string                `json:"outputs,omitempty"`
	Ports       map[string]ServicePort  `json:"ports,omitempty"`
	Mounts      []Mount                 `json:"mounts,omitempty"`
	Readiness   *PlannedProbe           `json:"readiness,omitempty"`
	Executable  string                  `json:"executable,omitempty"`
	Environment map[string]PlannedValue `json:"environment"`
	Image       *PlannedValue           `json:"image,omitempty"`
}

// ErrorResponse is the shared actionable error envelope for finite operations.
type ErrorResponse struct {
	APIVersion string    `json:"api_version"`
	Error      PlanError `json:"error"`
}

// PlanError identifies a failure category and field/component context.
type PlanError struct {
	Code    string `json:"code"`
	Field   string `json:"field,omitempty"`
	Message string `json:"message"`
}

// Error implements the error interface without including untrusted input values.
func (e *PlanError) Error() string {
	if e.Field == "" {
		return e.Message
	}
	return e.Field + ": " + e.Message
}

// PlannedProbe preserves readiness intent with a redacted or symbolic target.
type PlannedProbe struct {
	Executable string        `json:"executable,omitempty"`
	Kind       string        `json:"kind"`
	Target     *PlannedValue `json:"target,omitempty"`
	Command    *Command      `json:"command,omitempty"`
	Timeout    string        `json:"timeout"`
}
