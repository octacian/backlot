package plan

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"maps"
	"slices"

	v1 "github.com/octacian/backlot/api/v1"
	"github.com/octacian/backlot/internal/manifest"
)

// Setup holds private captured sources while an adapter gathers explicit changes.
// Do not serialize it or log its values.
type Setup struct {
	source  Source
	layers  layers
	request v1.ProjectSetupRequest
}

// OpenSetup validates all sources, deferring stale private membership only for explicit edits.
func OpenSetup(request v1.ProjectSetupRequest) (*Setup, error) {
	mutation := request.Edit || request.Update != nil || len(request.Generate) > 0 || len(request.RemoveInputs) > 0 || len(request.RemoveTools) > 0 || request.Reassociate
	if request.RevealSecrets && mutation {
		return nil, problem("invalid_arguments", "project setup", "secret reveal is inspection-only and cannot be combined with edits")
	}
	source, err := Locate(request.ProjectPath)
	if err != nil {
		return nil, err
	}
	l, err := loadLayers(source, request.ConfigPath, mutation, request.Reassociate)
	if err != nil {
		return nil, err
	}
	return &Setup{source: source, layers: l, request: request}, nil
}

// Inspect reports sorted status, revealing secret values only when explicitly requested locally.
func (s *Setup) Inspect() v1.ProjectSetupResponse {
	l := s.layers
	r := v1.ProjectSetupResponse{APIVersion: v1.Version, Registered: l.private.Provenance.Present, Association: l.private.Provenance.Association, PrivatePath: l.private.Provenance.Path, Inputs: []v1.SetupInput{}, Tools: []v1.SetupTool{}}
	for _, name := range slices.Sorted(maps.Keys(l.manifest.Inputs)) {
		declaration := l.manifest.Inputs[name]
		value := l.effective.Inputs[name]
		item := v1.SetupInput{Name: name, Description: declaration.Description, Secret: declaration.Secret, Generate: declaration.Generate, Configured: value != ""}
		if s.request.RevealSecrets && declaration.Secret && value != "" {
			item.RevealedValue = &value
		}
		r.Inputs = append(r.Inputs, item)
	}
	for _, name := range slices.Sorted(maps.Keys(l.manifest.Tools)) {
		executable := l.manifest.Tools[name]
		override, ok := l.effective.Tools[name]
		if ok {
			executable = override
		}
		r.Tools = append(r.Tools, v1.SetupTool{Name: name, Executable: executable, Overridden: ok})
	}
	return r
}

// Save validates and publishes a full candidate; unspecified private entries are preserved.
func (s *Setup) Save(ctx context.Context, update v1.PrivateConfig, generate, removeInputs, removeTools []string) (v1.ProjectSetupResponse, error) {
	fail := func(field, message string) (v1.ProjectSetupResponse, error) {
		return v1.ProjectSetupResponse{}, problem("invalid_setup", field, message)
	}
	if s.request.RevealSecrets {
		return fail("project setup", "secret reveal is inspection-only")
	}
	m := s.layers.manifest
	if err := manifest.ValidatePrivate(update, m, true); err != nil {
		return v1.ProjectSetupResponse{}, err
	}
	candidate := s.layers.private.Config
	candidate.Inputs = maps.Clone(candidate.Inputs)
	candidate.Tools = maps.Clone(candidate.Tools)
	if candidate.Inputs == nil {
		candidate.Inputs = map[string]string{}
	}
	if candidate.Tools == nil {
		candidate.Tools = map[string]string{}
	}
	generation := map[string]bool{}
	for _, name := range generate {
		d, ok := m.Inputs[name]
		_, literal := update.Inputs[name]
		if !ok || !manifest.ValidIdentifier(name) || generation[name] || literal || !d.Secret || d.Generate != "random-hex-32" {
			return fail("generate", "name must be a unique eligible declared secret, without a literal replacement")
		}
		generation[name] = true
	}
	remove := func(names []string, current, replacements map[string]string, generated map[string]bool) error {
		seen := map[string]bool{}
		for _, name := range names {
			_, exists := current[name]
			_, replacement := replacements[name]
			if !manifest.ValidIdentifier(name) || seen[name] || !exists || replacement || generated[name] {
				return problem("invalid_setup", "removal", "remove names must be unique existing private entries without a simultaneous replacement")
			}
			seen[name] = true
			delete(current, name)
		}
		return nil
	}
	if err := remove(removeInputs, candidate.Inputs, update.Inputs, generation); err != nil {
		return v1.ProjectSetupResponse{}, err
	}
	if err := remove(removeTools, candidate.Tools, update.Tools, nil); err != nil {
		return v1.ProjectSetupResponse{}, err
	}
	maps.Copy(candidate.Inputs, update.Inputs)
	maps.Copy(candidate.Tools, update.Tools)
	for name := range generation {
		b := make([]byte, 32)
		if _, err := rand.Read(b); err != nil {
			return fail("generate", "secure random generation failed")
		}
		candidate.Inputs[name] = hex.EncodeToString(b)
	}
	if err := manifest.ValidatePrivate(candidate, m, true); err != nil {
		return v1.ProjectSetupResponse{}, manifest.SourceError(err, "private", s.layers.private.Provenance.Path)
	}
	effective := compose(s.layers.machine, candidate)
	for _, name := range slices.Sorted(maps.Keys(m.Inputs)) {
		if effective.Inputs[name] == "" {
			return fail("inputs."+name, "setup requires every declared input; provide a value or explicitly accept eligible generation")
		}
	}
	provenance, err := s.layers.private.Commit(ctx, candidate, func() error {
		next, err := loadLayers(s.source, s.request.ConfigPath, true, s.request.Reassociate)
		if err != nil {
			return err
		}
		if next.manifestDigest != s.layers.manifestDigest || next.machineDigest != s.layers.machineDigest || next.machinePresent != s.layers.machinePresent || !s.layers.private.Same(next.private) {
			return problem("setup_conflict", "project setup", "sources changed during setup; retry without overwriting another writer")
		}
		return nil
	})
	if err != nil {
		return v1.ProjectSetupResponse{}, err
	}
	s.layers.private.Provenance = provenance
	s.layers.private.Config = candidate
	s.layers.effective = effective
	result := s.Inspect()
	result.Changed = true
	return result, nil
}

// GenerateSetupSecret generates an explicitly accepted random-hex-32 secret.
func GenerateSetupSecret() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", problem("generation_failed", "generate", "secure random generation failed")
	}
	return hex.EncodeToString(b), nil
}
