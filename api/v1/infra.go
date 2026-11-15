package v1

import "path/filepath"

// InfraStopContract requires streamed cleanup before foreground ownership proof.
const InfraStopContract = "infra-shutdown/v1"

// InfraRequest selects configured infrastructure in this daemon's scope.
type InfraRequest struct {
	// APIVersion selects the shared API.
	APIVersion string `json:"api_version"`
	// ConfigPath explicitly selects machine configuration for start.
	ConfigPath string `json:"config_path,omitempty"`
	// DefaultConfigPath is considered only without retained gateway configuration.
	DefaultConfigPath string `json:"default_config_path,omitempty"`
	// DaemonGeneration fences shutdown to the observed daemon.
	DaemonGeneration string `json:"daemon_generation,omitempty"`
	// Contract negotiates the streaming stop contract before effects.
	Contract string `json:"contract,omitempty"`
}

// Validate rejects unsupported operations and configuration before effects.
func (q InfraRequest) Validate(action string) error {
	if q.APIVersion != Version || action != "start" && action != "status" && action != "stop" || q.ConfigPath != "" && (!filepath.IsAbs(q.ConfigPath) || q.DefaultConfigPath != "") || q.DefaultConfigPath != "" && !filepath.IsAbs(q.DefaultConfigPath) || action != "start" && (q.ConfigPath != "" || q.DefaultConfigPath != "") || action == "stop" && (q.Contract != InfraStopContract || !buildImageHex(q.DaemonGeneration)) || action != "stop" && (q.Contract != "" || q.DaemonGeneration != "") {
		return &PlanError{Code: "invalid_request", Message: "infra requires a compatible operation, start-only absolute configuration and infra-shutdown/v1 for stop"}
	}
	return nil
}

// InfraResponse separates live infrastructure readiness from bounded recorded activity.
type InfraResponse struct {
	// APIVersion identifies the wire contract.
	APIVersion string `json:"api_version"`
	// DaemonGeneration identifies the responding owner, when available.
	DaemonGeneration string `json:"daemon_generation,omitempty"`
	// Directory identifies exactly one selected private state root.
	Directory string `json:"directory"`
	// Daemon is running, absent, or unknown; running means compatible API access.
	Daemon string `json:"daemon"`
	// Gateway is a managed listener probe, external operator ownership, or unconfigured.
	Gateway *GatewayResponse `json:"gateway,omitempty"`
	// Ready means administration and any selected managed gateway are ready.
	Ready bool `json:"ready"`
	// Activity is one bounded recorded page, never a provider readiness probe.
	Activity *ActivityResponse `json:"recorded_activity,omitempty"`
	// Guidance gives inspection instructions for this selected scope.
	Guidance string `json:"guidance"`
	// Error retains partial startup or unknown observations without hiding the daemon.
	Error *PlanError `json:"error,omitempty"`
}

// InfraTarget is the shutdown coordinator's compact final workload observation.
type InfraTarget struct {
	// ID identifies the recorded workload, without conveying cleanup authority.
	ID string `json:"id"`
	// Outcome is stopped or failed; failed may retain running owners or resources.
	Outcome string `json:"outcome"`
	// Error explains incomplete cleanup.
	Error *PlanError `json:"error,omitempty"`
}

// InfraStopRecord frames transient shutdown observations and foreground completion.
type InfraStopRecord struct {
	// APIVersion identifies this contract.
	APIVersion string `json:"api_version"`
	// Type is accepted, phase, target, cleanup, or completion.
	Type string `json:"type"`
	// DaemonGeneration identifies the original cleanup coordinator.
	DaemonGeneration string `json:"daemon_generation,omitempty"`
	// OperationID identifies this transient shutdown, without durable replay.
	OperationID string `json:"operation_id,omitempty"`
	// Phase identifies the current cleanup responsibility.
	Phase string `json:"phase,omitempty"`
	// State is running/succeeded/failed for phases, or complete/absent/unknown for completion.
	State string `json:"state,omitempty"`
	// Target reports one workload outcome.
	Target *InfraTarget `json:"target,omitempty"`
	// Error reports phase, terminal cleanup or foreground observation failure.
	Error *PlanError `json:"error,omitempty"`
	// Directory appears on foreground completion and identifies the selected scope.
	Directory string `json:"directory,omitempty"`
	// ReplacementGeneration reports only a replacement actually observed, never stopped.
	ReplacementGeneration string `json:"replacement_generation,omitempty"`
}

// Validate checks compact daemon stream records; completion belongs to the foreground.
func (r InfraStopRecord) Validate() error {
	bad := func() error {
		return &PlanError{Code: "invalid_response", Message: "invalid infrastructure shutdown record"}
	}
	if r.APIVersion != Version || !buildImageHex(r.DaemonGeneration) || !buildImageHex(r.OperationID) || r.Directory != "" || r.ReplacementGeneration != "" || r.Error != nil && (r.Error.Code == "" || r.Error.Message == "") {
		return bad()
	}
	switch r.Type {
	case "accepted":
		if r.State != "" || r.Phase != "" || r.Target != nil || r.Error != nil {
			return bad()
		}
	case "phase":
		switch r.Phase {
		case "owners", "expiry", "recovery", "gateway", "shared", "records":
		default:
			return bad()
		}
		if r.State != "running" && r.State != "succeeded" && r.State != "failed" || r.Target != nil || (r.State == "failed") != (r.Error != nil) {
			return bad()
		}
	case "target":
		if r.Target == nil || !buildImageHex(r.Target.ID) || r.Phase != "" || r.State != "" || r.Error != nil || r.Target.Outcome != "stopped" && r.Target.Outcome != "failed" || (r.Target.Outcome == "failed") != (r.Target.Error != nil) || r.Target.Error != nil && (r.Target.Error.Code == "" || r.Target.Error.Message == "") {
			return bad()
		}
	case "cleanup":
		if r.Phase != "" || r.State != "" || r.Target != nil {
			return bad()
		}
	default:
		return bad()
	}
	return nil
}
