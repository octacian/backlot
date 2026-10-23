package v1

// AttachedOwner fences client authority to an exact daemon execution attempt.
// Token is private transport data and must never be emitted as command output.
type AttachedOwner struct {
	DaemonGeneration string `json:"daemon_generation"`
	Attempt          string `json:"attempt"`
	Token            string `json:"token"`
}

// Attachment is returned only by the accepting attached start request.
type Attachment struct {
	Owner         AttachedOwner `json:"owner"`
	ExpiresAt     string        `json:"expires_at"`
	LeaseDuration string        `json:"lease_duration"`
}

// AttachedRequest renews or cancels only the originally accepted attempt.
type AttachedRequest struct {
	APIVersion string        `json:"api_version"`
	InstanceID string        `json:"instance_id"`
	Owner      AttachedOwner `json:"owner"`
}

// AttachedRecord frames sampled lifecycle observations and aggregate log output.
type AttachedRecord struct {
	APIVersion string            `json:"api_version"`
	Type       string            `json:"type"`
	Result     *InstanceResponse `json:"result,omitempty"`
	Log        *LogRecord        `json:"log,omitempty"`
	Gap        string            `json:"gap,omitempty"`
	Error      *PlanError        `json:"error,omitempty"`
}
