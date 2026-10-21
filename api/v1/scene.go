package v1

import (
	"encoding/hex"
	"path/filepath"
)

// SceneOutcome describes selection or verified lifecycle action, independently of runtime status.
type SceneOutcome string

// Scene operation outcomes include successful absence and idempotent stop.
const (
	SceneFound          SceneOutcome = "found"
	SceneNotFound       SceneOutcome = "not_found"
	SceneStopped        SceneOutcome = "stopped"
	SceneAlreadyStopped SceneOutcome = "already_stopped"
)

// SceneTarget selects a named persistent scene or an explicit recorded instance.
// CWD and ProjectPath are absolute client locations; the daemon resolves identity.
type SceneTarget struct {
	Scene       string `json:"scene,omitempty"`
	CWD         string `json:"cwd,omitempty"`
	ProjectPath string `json:"project_path,omitempty"`
	InstanceID  string `json:"instance_id,omitempty"`
}

// Validate rejects mixed selectors and paths that depend on daemon working directory.
func (t SceneTarget) Validate() error {
	invalid := func() error {
		return &PlanError{Code: "invalid_request", Message: "select a scene with absolute cwd/project path, or one recorded instance_id"}
	}
	if t.InstanceID != "" {
		if len(t.InstanceID) != 64 || t.Scene != "" || t.CWD != "" || t.ProjectPath != "" {
			return invalid()
		}
		if _, err := hex.DecodeString(t.InstanceID); err != nil {
			return invalid()
		}
		return nil
	}
	if t.Scene == "" || len(t.Scene) > 63 || !filepath.IsAbs(t.CWD) || (t.ProjectPath != "" && !filepath.IsAbs(t.ProjectPath)) {
		return invalid()
	}
	return nil
}

// SceneRequest selects recorded state without reading current manifest inputs.
type SceneRequest struct {
	APIVersion string      `json:"api_version"`
	Target     SceneTarget `json:"target"`
}

// SceneStartRequest selects current inputs for the existing persistent run coordinator.
type SceneStartRequest struct {
	APIVersion string           `json:"api_version"`
	CWD        string           `json:"cwd"`
	Plan       PlanRequest      `json:"plan"`
	Options    ExecutionOptions `json:"options"`
}

// SceneResponse is a coherent recorded snapshot or a successful missing selection.
// Checkout is independently daemon-resolved for named results, even before a first instance.
type SceneResponse struct {
	APIVersion       string            `json:"api_version"`
	DaemonGeneration string            `json:"daemon_generation"`
	Outcome          SceneOutcome      `json:"outcome"`
	Checkout         *CheckoutIdentity `json:"checkout,omitempty"`
	Result           *InstanceResponse `json:"result,omitempty"`
}

// SceneFollowRecord frames sampled observations and their terminal completion or error.
// Time is observation time, not a durable transition timestamp or sequence.
type SceneFollowRecord struct {
	APIVersion string         `json:"api_version"`
	Type       string         `json:"type"`
	Time       string         `json:"time"`
	Snapshot   *SceneResponse `json:"snapshot,omitempty"`
	Reason     string         `json:"reason,omitempty"`
	Error      *PlanError     `json:"error,omitempty"`
}
