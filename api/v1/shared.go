package v1

// SharedService declares a retained project-owned MariaDB server independently
// of scene allocations. Gate 1 supports explicitly owned volume storage and
// generated private administration; it never adopts an external server.
type SharedService struct {
	Manager          string   `json:"manager" yaml:"manager" jsonschema:"enum=mariadb"`
	Image            string   `json:"image" yaml:"image" jsonschema:"minLength=1"`
	Args             []string `json:"args,omitempty" yaml:"args,omitempty"`
	Storage          string   `json:"storage" yaml:"storage" jsonschema:"enum=volume"`
	Admin            string   `json:"admin" yaml:"admin" jsonschema:"enum=generated"`
	Port             int      `json:"port" yaml:"port" jsonschema:"minimum=3306,maximum=3306"`
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
	ID              string `json:"id"`
	ProjectID       string `json:"project_id"`
	Name            string `json:"name"`
	Configuration   string `json:"configuration"`
	Status          string `json:"status"`
	Allocations     int    `json:"allocations"`
	PendingReleases int    `json:"pending_releases"`
	Failure         string `json:"failure,omitempty"`
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
