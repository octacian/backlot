package v1

import (
	"encoding/hex"
	"path/filepath"
	"strings"
)

// GatewayRequest controls the daemon-owned gateway; configuration is needed at start.
type GatewayRequest struct {
	ExpectedClaimRevision string `json:"expected_claim_revision,omitempty"`
	APIVersion            string `json:"api_version"`
	ConfigPath            string `json:"config_path,omitempty"`
	Force                 bool   `json:"force,omitempty"`
	// DefaultConfigPath is a CLI-resolved candidate, used only while unconfigured.
	DefaultConfigPath string `json:"default_config_path,omitempty"`
}

// GatewayResponse reports process/listener readiness, independently from client trust.
// It never exposes private guardian capabilities or CA keys.
type GatewayResponse struct {
	ClaimRevision           string                   `json:"claim_revision"`
	NativeOwnershipWarnings []NativeOwnershipWarning `json:"native_ownership_warnings,omitempty"`
	APIVersion              string                   `json:"api_version"`
	Mode                    string                   `json:"mode"`
	Status                  string                   `json:"status"`
	Endpoint                string                   `json:"endpoint,omitempty"`
	HTTPSPort               int                      `json:"https_port,omitempty"`
	Message                 string                   `json:"message,omitempty"`
	// RootFingerprint identifies the actual retained public root DER SHA-256.
	RootFingerprint string `json:"root_fingerprint,omitempty"`
	// HostTrust is informational OS trust, independent of gateway readiness.
	HostTrust *TrustObservation `json:"host_trust,omitempty"`
	// TrustCommand selects this state and exact root for explicit authorization.
	TrustCommand string `json:"trust_command,omitempty"`
	// ConfigSource describes this selection: explicit, default, or retained.
	ConfigSource string `json:"config_source,omitempty"`
}

// Validate checks gateway readiness and optional, scoped host-trust guidance.
func (r GatewayResponse) Validate() error {
	bad := func() error { return &PlanError{Code: "invalid_response", Message: "invalid gateway response"} }
	if r.APIVersion != Version || r.Mode != "managed" {
		return bad()
	}
	switch r.Status {
	case "unconfigured", "running", "stopped", "interrupted", "unavailable", "uncertain":
	default:
		return bad()
	}
	if r.HostTrust == nil && r.RootFingerprint == "" && r.TrustCommand == "" {
		return nil
	}
	if r.HostTrust == nil || r.HostTrust.Validate() != nil || r.HostTrust.Context != "os" {
		return bad()
	}
	pin, err := hex.DecodeString(r.RootFingerprint)
	if err != nil || len(pin) != 32 || hex.EncodeToString(pin) != r.RootFingerprint {
		return bad()
	}
	quoted, ok := strings.CutPrefix(r.TrustCommand, "backlot gateway trust install --state-dir '")
	if !ok {
		return bad()
	}
	quoted, ok = strings.CutSuffix(quoted, "' --fingerprint "+r.RootFingerprint)
	// Only the producer's single-quoted literal and apostrophe escape are allowed.
	directory := strings.ReplaceAll(quoted, "'\\''", "'")
	if !ok || !filepath.IsAbs(directory) || strings.ContainsRune(directory, 0) || strings.ReplaceAll(directory, "'", "'\\''") != quoted {
		return bad()
	}
	return nil
}
