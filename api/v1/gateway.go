package v1

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
	// ConfigSource describes this selection: explicit, default, or retained.
	ConfigSource string `json:"config_source,omitempty"`
}
