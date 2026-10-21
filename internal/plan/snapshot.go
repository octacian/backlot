package plan

import (
	v1 "github.com/octacian/backlot/api/v1"
	"github.com/octacian/backlot/internal/projectconfig"
)

// Snapshot retains validated declarations and resolved sensitive values privately.
// Allocation references stay symbolic; this is preparation, never execution.
type Snapshot struct {
	PrivateSourceState string                    `json:"-"` // Safe partial current-source status; never persisted as launch fact.
	PrivateProvenance  *projectconfig.Provenance `json:"private_provenance,omitempty"`
	// ConfigProvenance records the effective launch file, including an absent default.
	ConfigProvenance *ConfigProvenance          `json:"config_provenance,omitempty"`
	Caddy            *v1.CaddyConfig            `json:"caddy,omitempty"`
	Docker           *v1.DockerConfig           `json:"docker,omitempty"`
	Manifest         v1.Manifest                `json:"manifest"`
	Plan             v1.PlanResponse            `json:"plan"`
	Secrets          map[string]v1.PlannedValue `json:"-"`
	// ImplicitBaseline distinguishes launch locations from explicit inherited,
	// seed and secret configuration while keeping all those values private.
	ImplicitBaseline map[string]bool `json:"implicit_baseline,omitempty"`
}

// Prepare resolves a source returned by Locate without rediscovering its paths.
// It reads snapshot inputs but does not require provider configuration.
// The caller must protect the returned manifest and secrets, never serialize them publicly.
func Prepare(source Source, request v1.PlanRequest) (Snapshot, error) {
	snapshot := Snapshot{Secrets: map[string]v1.PlannedValue{}, ImplicitBaseline: map[string]bool{}}
	_, err := resolve(request, &snapshot, source)
	return snapshot, err
}

func (v resolvedValue) private() v1.PlannedValue {
	return v1.PlannedValue{Literal: v.literal, Symbolic: v.symbolic}
}

// ConfigProvenance distinguishes historical absence from unrecorded older snapshots.
type ConfigProvenance struct {
	Path    string `json:"path,omitempty"`
	Present bool   `json:"present"`
}
