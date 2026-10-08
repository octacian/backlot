package v1

// StateVersion identifies the supported durable database format.
const StateVersion = 3

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
	ID             string `json:"id"`
	Kind           string `json:"kind"`
	OwnershipToken string `json:"ownership_token"`
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
	ID             string           `json:"id"`
	Execution      *ExecutionResult `json:"execution,omitempty"`
	Checkout       CheckoutIdentity `json:"checkout"`
	Status         InstanceStatus   `json:"status"`
	Operation      Operation        `json:"operation"`
	Plan           PlanResponse     `json:"plan"`
	CreatedAt      string           `json:"created_at"`
	LeaseExpiresAt string           `json:"lease_expires_at,omitempty"`
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

// DaemonStatusResponse reports transport and state compatibility.
type DaemonStatusResponse struct {
	APIVersion    string `json:"api_version"`
	StateVersion  int    `json:"state_version"`
	Status        string `json:"status"`
	PID           int    `json:"pid"`
	LeaseDuration string `json:"lease_duration"`
}

// DoctorCheck is a local diagnostic, with provider checks explicitly deferred.
type DoctorCheck struct {
	Name    string `json:"name"`
	Status  string `json:"status"`
	Message string `json:"message"`
}

// DoctorResponse reports only local permissions, connectivity and compatibility.
type DoctorResponse struct {
	APIVersion string        `json:"api_version"`
	Healthy    bool          `json:"healthy"`
	Checks     []DoctorCheck `json:"checks"`
}
