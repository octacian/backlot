package v1

// GatewayRequest controls the daemon-owned gateway; configuration is needed at start.
type GatewayRequest struct {
	APIVersion string `json:"api_version"`
	ConfigPath string `json:"config_path,omitempty"`
}

// GatewayResponse reports process/listener readiness, independently from client trust.
// It never exposes private guardian capabilities or CA keys.
type GatewayResponse struct {
	APIVersion string `json:"api_version"`
	Mode       string `json:"mode"`
	Status     string `json:"status"`
	Endpoint   string `json:"endpoint,omitempty"`
	HTTPSPort  int    `json:"https_port,omitempty"`
	Message    string `json:"message,omitempty"`
}
