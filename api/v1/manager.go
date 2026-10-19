package v1

// ManagerProtocolVersion versions private trusted-command allocation messages.
const ManagerProtocolVersion = "backlot.manager/v1"

// ManagerRequest is private provisioning input, never a public status payload.
// A command reads one bounded JSON document on stdin and writes one result.
// Credentials and ownership are persisted by Backlot before allocate effects.
type ManagerRequest struct {
	Version      string `json:"version"`
	Operation    string `json:"operation"`
	AllocationID string `json:"allocation_id"`
	Ownership    string `json:"ownership"`
	Database     string `json:"database"`
	Username     string `json:"username"`
	Password     string `json:"password"`
}

// ManagerResult reports observation independently from process exit status.
// Ownership is owned, absent or uncertain. Effect is ready, partial or released.
// Uncertainty and partial effects retain a durable cleanup obligation.
type ManagerResult struct {
	Version      string `json:"version"`
	AllocationID string `json:"allocation_id"`
	Ownership    string `json:"ownership"`
	Effect       string `json:"effect"`
}
