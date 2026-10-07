package plan

import v1 "github.com/octacian/backlot/api/v1"

// Snapshot retains validated declarations and resolved sensitive values privately.
// Allocation references stay symbolic; this is preparation, never execution.
type Snapshot struct {
	Manifest v1.Manifest                `json:"manifest"`
	Plan     v1.PlanResponse            `json:"plan"`
	Secrets  map[string]v1.PlannedValue `json:"-"`
}

// Prepare resolves immutable metadata without requiring provider configuration.
// The caller must protect the returned manifest and secrets, never serialize them publicly.
func Prepare(request v1.PlanRequest) (Snapshot, error) {
	snapshot := Snapshot{Secrets: map[string]v1.PlannedValue{}}
	_, err := resolve(request, &snapshot)
	return snapshot, err
}

func (v resolvedValue) private() v1.PlannedValue {
	return v1.PlannedValue{Literal: v.literal, Symbolic: v.symbolic}
}
