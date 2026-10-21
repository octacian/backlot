package v1

// StateVersion identifies the supported durable database format.
const StateVersion = 7

// InstanceStatus distinguishes metadata preparation from observed execution.
type InstanceStatus string

// Supported preparation states.
const (
	Preparing   InstanceStatus = "preparing"
	Prepared    InstanceStatus = "prepared"
	Cancelled   InstanceStatus = "cancelled"
	Interrupted InstanceStatus = "interrupted"
)

// PrepareRequest selects a metadata-only preparation; paths must be absolute.
type PrepareRequest struct {
	APIVersion string      `json:"api_version"`
	Plan       PlanRequest `json:"plan"`
}

// InstanceRequest targets a durable instance identity.
type InstanceRequest struct {
	APIVersion string `json:"api_version"`
	InstanceID string `json:"instance_id"`
}

// LeaseRequest renews a disposable instance with its private capability.
type LeaseRequest struct {
	APIVersion string `json:"api_version"`
	InstanceID string `json:"instance_id"`
	LeaseToken string `json:"lease_token"`
}

// ControlRequest negotiates API compatibility for daemon control.
type ControlRequest struct {
	APIVersion string `json:"api_version"`
}

// CheckoutIdentity records canonical checkout and filesystem/Git provenance.
type CheckoutIdentity struct {
	ID           string `json:"id"`
	Path         string `json:"path"`
	GitDirectory string `json:"git_directory,omitempty"`
	GitHead      string `json:"git_head,omitempty"`
}

// AllocationRecord preserves intent/effect ownership across interruption.
// Metadata preparation allocates only a private snapshot, never runtime resources.
type AllocationRecord struct {
	ID   string `json:"id"`
	Kind string `json:"kind"`
	// OwnershipToken is private persisted state; the server removes it from public responses.
	OwnershipToken string `json:"ownership_token,omitempty"`
	Phase          string `json:"phase"`
}

// Operation records a stable preparation attempt and terminal reason.
type Operation struct {
	ID          string             `json:"id"`
	Status      InstanceStatus     `json:"status"`
	Reason      string             `json:"reason,omitempty"`
	Allocations []AllocationRecord `json:"allocations"`
}

// Instance is the redacted durable public view of preparation and optional execution.
type Instance struct {
	NativeOwnershipWarnings []NativeOwnershipWarning `json:"native_ownership_warnings,omitempty"`
	ID                      string                   `json:"id"`
	Execution               *ExecutionResult         `json:"execution,omitempty"`
	Checkout                CheckoutIdentity         `json:"checkout"`
	Status                  InstanceStatus           `json:"status"`
	Operation               Operation                `json:"operation"`
	Plan                    PlanResponse             `json:"plan"`
	CreatedAt               string                   `json:"created_at"`
	LeaseExpiresAt          string                   `json:"lease_expires_at,omitempty"`
}

// InstanceResponse returns redacted metadata and explicit snapshot drift.
// LeaseToken appears only on initial disposable preparation, never inspection.
type InstanceResponse struct {
	APIVersion    string   `json:"api_version"`
	Instance      Instance `json:"instance"`
	ManifestDrift bool     `json:"manifest_drift,omitempty"`
	ConfigDrift   bool     `json:"config_drift,omitempty"`
	LeaseToken    string   `json:"lease_token,omitempty"`
}

// ListRequest selects a bounded identity-ordered page, including historical instances.
type ListRequest struct {
	APIVersion string `json:"api_version"`
	AfterID    string `json:"after_id,omitempty"`
	Limit      int    `json:"limit,omitempty"`
}

// InstanceSummary discovers a recorded target without exposing its private state.
type InstanceSummary struct {
	ID        string         `json:"id"`
	Project   string         `json:"project"`
	Scene     string         `json:"scene"`
	Checkout  string         `json:"checkout"`
	Status    InstanceStatus `json:"status"`
	Lifetime  Lifetime       `json:"lifetime"`
	CreatedAt string         `json:"created_at"`
	Origin    string         `json:"origin,omitempty"`
}

// ListResponse is one bounded page; NextAfterID is empty on the last page.
type ListResponse struct {
	APIVersion  string            `json:"api_version"`
	Instances   []InstanceSummary `json:"instances"`
	NextAfterID string            `json:"next_after_id,omitempty"`
}

// DaemonStatusResponse reports transport and state compatibility.
type DaemonStatusResponse struct {
	APIVersion    string `json:"api_version"`
	StateVersion  int    `json:"state_version"`
	Status        string `json:"status"`
	PID           int    `json:"pid"`
	LeaseDuration string `json:"lease_duration"`
}
