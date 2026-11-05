package v1

import "strings"

// TaskBinding identifies the accepted parent generation borrowed by one execution.
// It describes lineage; it is not a cleanup capability.
type TaskBinding struct {
	// InstanceID is the recorded persistent parent.
	InstanceID string `json:"instance_id"`
	// Attempt is the accepted parent execution attempt.
	Attempt string `json:"attempt"`
	// Generation identifies retained resources, or the parent attempt without resources.
	Generation string `json:"generation"`
	// Name is the enabled component name.
	Name string `json:"name"`
}

// TaskRunRequest explicitly invokes one enabled task against a recorded ready scene.
// Inspect, Renew and StopRequest address its independent returned instance ID.
type TaskRunRequest struct {
	// APIVersion selects the shared wire contract.
	APIVersion string `json:"api_version"`
	// Target selects the persistent parent without starting it.
	Target SceneTarget `json:"target"`
	// Task selects a captured enabled component.
	Task string `json:"task"`
	// Args appends literal arguments without shell interpretation.
	Args []string `json:"args,omitempty"`
	// Options bounds the independent execution; keep-on-failure is unsupported.
	Options ExecutionOptions `json:"options"`
}

// Validate rejects invalid selectors and arguments before effects.
func (r TaskRunRequest) Validate() error {
	if err := r.Target.Validate(); err != nil {
		return err
	}
	if r.APIVersion != Version || !sceneIdentifier.MatchString(r.Task) || r.Options.KeepOnFailure {
		return &PlanError{Code: "invalid_request", Message: "select an enabled task and compatible API; tasks cannot keep resources on failure"}
	}
	for _, arg := range r.Args {
		if strings.ContainsRune(arg, 0) {
			return &PlanError{Code: "invalid_arguments", Message: "task arguments cannot contain NUL"}
		}
	}
	return nil
}
