package plan

import v1 "github.com/octacian/backlot/api/v1"

// Snapshot retains validated declarations and resolved sensitive values privately.
// Allocation references stay symbolic; this is preparation, never execution.
type Snapshot struct {
	Manifest v1.Manifest                `json:"manifest"`
	Plan     v1.PlanResponse            `json:"plan"`
	Secrets  map[string]v1.PlannedValue `json:"-"`
}

// Prepare resolves a source returned by Locate without rediscovering its paths.
// It reads snapshot inputs but does not require provider configuration.
// The caller must protect the returned manifest and secrets, never serialize them publicly.
func Prepare(source Source, request v1.PlanRequest) (Snapshot, error) {
	snapshot := Snapshot{Secrets: map[string]v1.PlannedValue{}}
	_, err := resolve(request, &snapshot, source)
	return snapshot, err
}

func (v resolvedValue) private() v1.PlannedValue {
	return v1.PlannedValue{Literal: v.literal, Symbolic: v.symbolic}
}
