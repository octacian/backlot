// Package v1 defines the shared JSON contract for Backlot API version 1.
package v1

// Version is the supported API namespace, independent of the binary version.
const Version = "v1"

// VersionResponse describes the executable and its supported API contract.
// The CLI and future daemon use this same type for version discovery.
type VersionResponse struct {
	// Version is the binary release identifier, or dev for a local build.
	Version string `json:"version"`
	// APIVersion is the wire contract namespace supported by the executable.
	APIVersion string `json:"api_version"`
	// Commit is the source revision when supplied by the build.
	Commit string `json:"commit,omitempty"`
	// BuildTime is the UTC RFC3339 build timestamp when supplied by the build.
	BuildTime string `json:"build_time,omitempty"`
}
