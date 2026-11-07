package v1

import "time"

// SharedService declares a retained project-owned SQL server independently
// of scene allocations. Bundled managers support explicitly owned volume storage and
// generated private administration; it never adopts an external server.
type SharedService struct {
	Manager          string   `json:"manager" yaml:"manager" jsonschema:"enum=mariadb,enum=postgresql"`
	Image            string   `json:"image" yaml:"image" jsonschema:"minLength=1"`
	Args             []string `json:"args,omitempty" yaml:"args,omitempty"`
	Storage          string   `json:"storage" yaml:"storage" jsonschema:"enum=volume"`
	Admin            string   `json:"admin" yaml:"admin" jsonschema:"enum=generated"`
	Port             int      `json:"port" yaml:"port" jsonschema:"enum=3306,enum=5432"`
	ReadinessTimeout string   `json:"readiness_timeout" yaml:"readiness_timeout" jsonschema:"minLength=1"`
}

// SharedRequest selects a recorded service by ID, never by an engine name.
type SharedRequest struct {
	APIVersion string `json:"api_version"`
	ServiceID  string `json:"service_id"`
}

// SharedStatus is a safe view of durable server intent and allocation counts.
// Private engine capabilities, administrative and application secrets are absent.
type SharedStatus struct {
	// LastTransition is the latest actual status change; absent legacy history stays unknown.
	LastTransition  *SharedTransition `json:"last_transition,omitempty"`
	ID              string            `json:"id"`
	ProjectID       string            `json:"project_id"`
	Name            string            `json:"name"`
	Configuration   string            `json:"configuration"`
	Status          string            `json:"status"`
	Allocations     int               `json:"allocations"`
	PendingReleases int               `json:"pending_releases"`
	Failure         string            `json:"failure,omitempty"`
}

// SharedResponse returns project-scoped retained services without private authority.
type SharedResponse struct {
	APIVersion string         `json:"api_version"`
	Services   []SharedStatus `json:"services"`
}

// SharedDestroyRequest authorizes deletion of one allocation-free recorded server.
// Force waives capability-label proof, never retained allocation/consumer fences.
type SharedDestroyRequest struct {
	APIVersion string `json:"api_version"`
	ServiceID  string `json:"service_id"`
	Force      bool   `json:"force,omitempty"`
}

// SharedTransition records one actual shared-service status change without private authority.
type SharedTransition struct {
	// From is the prior status, empty only for the first intent.
	From string `json:"from"`
	// To is the resulting status.
	To string `json:"to"`
	// Cause identifies the operation responsible for the change.
	Cause string `json:"cause"`
	// At is the actual UTC observation time.
	At string `json:"at"`
}

// Validate rejects unknown statuses, causes, timestamps and no-op transitions.
func (t SharedTransition) Validate() error {
	validStatus := func(status string) bool {
		switch status {
		case "intent", "running", "stopped", "uncertain", "destroying", "destroyed":
			return true
		}
		return false
	}
	bad := func() error {
		return &PlanError{Code: "invalid_response", Message: "invalid shared-service transition"}
	}
	if !validStatus(t.To) || t.From == t.To || t.From == "" && t.To != "intent" || t.From != "" && !validStatus(t.From) {
		return bad()
	}
	switch t.Cause {
	case "consumer_start", "allocation_release", "explicit_shared_start", "explicit_shared_restart", "explicit_shared_stop", "explicit_shared_destroy", "daemon_recovery", "daemon_shutdown":
	default:
		return bad()
	}
	if _, err := time.Parse(time.RFC3339Nano, t.At); err != nil {
		return bad()
	}
	return nil
}
