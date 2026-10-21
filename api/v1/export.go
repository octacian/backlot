package v1

// ExportRequest selects the latest settled attempt and a new caller-owned directory.
// Wait enables observation without cancellation; WaitTimeout is empty for indefinite waiting.
type ExportRequest struct {
	APIVersion   string `json:"api_version"`
	InstanceID   string `json:"instance_id"`
	Output       string `json:"output"`
	AllowPartial bool   `json:"allow_partial,omitempty"`
	Wait         bool   `json:"wait,omitempty"`
	WaitTimeout  string `json:"wait_timeout,omitempty"`
}

// ExportResponse identifies a published bundle independently of its run outcome.
type ExportResponse struct {
	// Error describes a failed publication; Result still preserves an observed settled run.
	Error      *PlanError   `json:"error,omitempty"`
	Published  bool         `json:"published"`
	APIVersion string       `json:"api_version"`
	Output     string       `json:"output"`
	Report     string       `json:"report"`
	HTML       string       `json:"html"`
	Complete   bool         `json:"complete"`
	Result     ExportResult `json:"result"`
}

// ExportResult preserves original runtime outcomes without private runtime records.
type ExportResult struct {
	NativeOwnershipWarnings []NativeOwnershipWarning `json:"native_ownership_warnings,omitempty"`
	InstanceID              string                   `json:"instance_id"`
	Attempt                 string                   `json:"attempt"`
	Status                  InstanceStatus           `json:"status"`
	TerminalExitCode        *int                     `json:"terminal_exit_code,omitempty"`
	Cancelled               bool                     `json:"cancelled"`
	Failure                 string                   `json:"failure,omitempty"`
	CollectionFailure       string                   `json:"collection_failure,omitempty"`
	CleanupFailure          string                   `json:"cleanup_failure,omitempty"`
	Components              []ComponentResult        `json:"components"`
}

// ExportSource records bounded launch provenance; absent historical facts remain unknown.
type ExportSource struct {
	EffectiveConfigDigest string   `json:"effective_config_digest,omitempty"`
	Project               string   `json:"project"`
	Scene                 string   `json:"scene"`
	Lifetime              Lifetime `json:"lifetime"`
	CheckoutID            string   `json:"checkout_id"`
	// GitHead is the immutable launch commit, or absent when unavailable.
	GitHead        string `json:"git_head,omitempty"`
	ManifestDigest string `json:"manifest_digest"`
	ConfigDigest   string `json:"config_digest,omitempty"`
}

// ExportEvidence is a copied file addressed relative to the bundle root.
type ExportEvidence struct {
	Path      string `json:"path"`
	Kind      string `json:"kind"`
	Component string `json:"component,omitempty"`
	Name      string `json:"name,omitempty"`
	Bytes     int64  `json:"bytes"`
	SHA256    string `json:"sha256"`
}

// ExportReport is the intentional backlot.export/v1 portable agent contract.
// Complete describes evidence, never whether the recorded application succeeded.
type ExportReport struct {
	StartedAt     string           `json:"started_at,omitempty"`
	SchemaVersion string           `json:"schema_version"`
	ExportedAt    string           `json:"exported_at"`
	CreatedAt     string           `json:"created_at"`
	CompletedAt   string           `json:"completed_at,omitempty"`
	Result        ExportResult     `json:"result"`
	Source        ExportSource     `json:"source"`
	Complete      bool             `json:"complete"`
	Omissions     []string         `json:"omissions"`
	Evidence      []ExportEvidence `json:"evidence"`
}

// ExportSchemaVersion identifies the independently versioned portable report.
const ExportSchemaVersion = "backlot.export/v1"
