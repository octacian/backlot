// Package v1 defines the shared JSON contract for Backlot API version 1.
package v1

// Version is the supported API namespace, independent of the binary version.
const Version = "v1"

// VersionResponse describes the executable and its supported API contract.
// The CLI and future daemon use this same type for version discovery.
type VersionResponse struct {
	// StateVersion identifies the supported durable state and configuration semantics.
	StateVersion int `json:"state_version"`
	// Version is the release/module identifier, or a dev-prefixed local build.
	Version string `json:"version"`
	// APIVersion is the wire contract namespace supported by the executable.
	APIVersion string `json:"api_version"`
	// Commit is the source revision when available from the build.
	Commit string `json:"commit,omitempty"`
	// BuildTime is the source commit's UTC RFC3339 time for linked builds.
	BuildTime string `json:"build_time,omitempty"`
}
