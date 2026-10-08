package v1

// Runtime states distinguish observed execution from metadata preparation.
const (
	Starting     InstanceStatus = "starting"
	RuntimeReady InstanceStatus = "ready"
	Stopping     InstanceStatus = "stopping"
	Stopped      InstanceStatus = "stopped"
	Succeeded    InstanceStatus = "succeeded"
	Failed       InstanceStatus = "failed"
)

// ExecutionOptions contains Go durations; omitted fields use documented defaults.
// Zero execution deadlines are unlimited; zero stop grace escalates immediately.
type ExecutionOptions struct {
	StartupTimeout string `json:"startup_timeout,omitempty"`
	JobTimeout     string `json:"job_timeout,omitempty"`
	StopGrace      string `json:"stop_grace,omitempty"`
}

// RunRequest starts or explicitly restarts a validated native scene.
type RunRequest struct {
	APIVersion string           `json:"api_version"`
	Plan       PlanRequest      `json:"plan"`
	Options    ExecutionOptions `json:"options"`
	InstanceID string           `json:"instance_id,omitempty"`
}

// ComponentResult preserves original job status independently of orchestration failures.
type ComponentResult struct {
	Name     string `json:"name"`
	Status   string `json:"status"`
	ExitCode *int   `json:"exit_code,omitempty"`
}

// ExecutionResult is the durable redacted runtime view.
type ExecutionResult struct {
	Attempt           string            `json:"attempt"`
	Components        []ComponentResult `json:"components"`
	Ports             map[string]int    `json:"ports,omitempty"`
	Failure           string            `json:"failure,omitempty"`
	CleanupFailure    string            `json:"cleanup_failure,omitempty"`
	CollectionFailure string            `json:"collection_failure,omitempty"`
	Cancelled         bool              `json:"cancelled,omitempty"`
}

// LogsRequest selects retained records by component and absolute record offset.
type LogsRequest struct {
	APIVersion string `json:"api_version"`
	InstanceID string `json:"instance_id"`
	Component  string `json:"component,omitempty"`
	Offset     int    `json:"offset"`
}

// LogRecord is one typed NDJSON output record. Data is base64 for non-UTF8 chunks;
// otherwise Message contains the original UTF8 text.
type LogRecord struct {
	InstanceID string `json:"instance_id"`
	Component  string `json:"component"`
	Time       string `json:"time"`
	Stream     string `json:"stream"`
	Attempt    string `json:"attempt"`
	Message    string `json:"message"`
	Data       string `json:"data,omitempty"`
}

// LogsResponse returns a bounded page and the next unfiltered record offset.
type LogsResponse struct {
	APIVersion string      `json:"api_version"`
	Records    []LogRecord `json:"records"`
	NextOffset int         `json:"next_offset"`
	Active     bool        `json:"active"`
	Gap        string      `json:"gap,omitempty"`
}
