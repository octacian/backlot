package daemon

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	v1 "github.com/octacian/backlot/api/v1"
	"github.com/octacian/backlot/internal/manifest"
	"github.com/octacian/backlot/internal/plan"
	bolt "go.etcd.io/bbolt"
)

func buildImageTag(id, attempt, name string) string {
	return "backlot-build/" + id + ":" + attempt + "-" + name
}

func validImageID(id string) bool {
	raw, ok := strings.CutPrefix(id, "sha256:")
	if !ok || len(raw) != 64 || strings.ToLower(raw) != raw {
		return false
	}
	_, err := hex.DecodeString(raw)
	return err == nil
}

func validateBuildImagePins(id string, g resourceGeneration) error {
	for name, pin := range g.BuildImages {
		if g.ID == "" || pin.Name != name || !manifest.ValidIdentifier(name) || !manifest.ValidIdentifier(pin.Producer) || !validID(pin.Attempt) || !validImageID(pin.ID) || pin.Tag != buildImageTag(id, pin.Attempt, name) {
			return problem("state_corrupt", "invalid retained build-image provenance; preserved")
		}
	}
	return nil
}

func (s *service) prepareBuildImages(ctx context.Context, id string, snap plan.Snapshot, entry *execution) error {
	if len(snap.Plan.BuildImages) == 0 {
		return nil
	}
	if len(entry.resources.BuildImages) == 0 {
		entry.acceptImages = true
	}
	if entry.acceptImages {
		return nil
	}
	for name := range snap.Plan.BuildImages {
		pin, ok := entry.resources.BuildImages[name]
		if !ok {
			return problem("image_unavailable", "build image "+name+" has no accepted ID; use explicit restart")
		}
		if _, err := entry.docker.ImageInspect(ctx, pin.ID); err != nil {
			return providerFailure("inspect retained build image "+name+" (restore the pinned local image or explicitly restart)", err)
		}
	}
	return nil
}

// completeBuildImages commits only a successful joined producer's current handoff.
// Engine observations stay outside the transaction; admission and generation are rechecked inside.
func (s *service) completeBuildImages(ctx context.Context, id, producer string, snap plan.Snapshot, entry *execution, result *v1.ExecutionResult) error {
	names := []string{}
	for name, image := range snap.Plan.BuildImages {
		if image.Producer == producer {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	if len(names) == 0 {
		return nil
	}
	if result.CollectionFailure != "" {
		return problem("image_handoff", "producer "+producer+" output/status collection failed; no image accepted")
	}
	candidates := map[string]v1.BuildImageResult{}
	warnings := []v1.ImageChangeWarning{}
	for _, name := range names {
		tag := buildImageTag(id, result.Attempt, name)
		image, err := entry.docker.ImageInspect(ctx, tag)
		if err != nil {
			return providerFailure("inspect build image "+name+" from producer "+producer+" (successful producer must tag its supplied attempt name)", err)
		}
		if !validImageID(image.ID) {
			return problem("image_handoff", "producer "+producer+" returned an invalid local image ID for "+name)
		}
		candidate := v1.BuildImageResult{Name: name, Producer: producer, Attempt: result.Attempt, Tag: tag, ID: image.ID}
		if !entry.acceptImages {
			candidate = entry.resources.BuildImages[name]
		} else if old, ok := entry.resources.BuildImages[name]; ok && old.ID != candidate.ID {
			for _, component := range snap.Plan.Components {
				if len(component.Mounts) == 0 || component.Image == nil {
					continue
				}
				value := *component.Image
				if value.Redacted {
					value = snap.Secrets[component.Name+"/image"]
				}
				r := value.Symbolic
				if r == nil || r.Kind != "build_image" || r.Name != name {
					continue
				}
				if !entry.allowImageChange {
					return problem("unsafe_drift", "build image "+name+" changed for mounted consumer "+component.Name+"; runtime stopped, data and old pin retained; use restart --allow-image-change to accept application/data compatibility risk, or reset")
				}
				warnings = append(warnings, v1.ImageChangeWarning{Component: component.Name, Image: name, PreviousID: old.ID, ID: candidate.ID, Message: "Application image changed with retained mounts; application/data compatibility and possible data loss are the operator's responsibility."})
			}
		}
		candidates[name] = candidate
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	next := *entry.resources
	next.BuildImages = make(map[string]v1.BuildImageResult, len(next.BuildImages)+len(candidates))
	for name, pin := range entry.resources.BuildImages {
		next.BuildImages[name] = pin
	}
	for name, pin := range candidates {
		next.BuildImages[name] = pin
	}
	nextResult := *result
	nextResult.BuildImages = slices.Clone(result.BuildImages)
	for _, name := range names {
		nextResult.BuildImages = append(nextResult.BuildImages, candidates[name])
	}
	nextResult.ImageChangeWarnings = append(slices.Clone(result.ImageChangeWarnings), warnings...)
	if err := s.store.db.Update(func(tx *bolt.Tx) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		current, err := load(tx, id)
		if err != nil {
			return err
		}
		if current.Instance.Execution == nil || current.Instance.Execution.Attempt != result.Attempt || current.Instance.Status != v1.Starting {
			return problem("conflict", "build-image handoff attempt was replaced")
		}
		var retained resourceGeneration
		if err = json.Unmarshal(tx.Bucket([]byte("resources")).Get([]byte(id)), &retained); err != nil {
			return err
		}
		if retained.ID != next.ID {
			return problem("conflict", "build-image generation was replaced")
		}
		if err = put(tx, "resources", id, next); err != nil {
			return err
		}
		current.Instance.Execution = &nextResult
		if err = put(tx, "instances", id, current); err != nil {
			return err
		}
		return ctx.Err()
	}); err != nil {
		return err
	}
	entry.resources = &next
	*result = nextResult
	return nil
}

func resolvedBuildImage(r *v1.Reference, key, id string, snap plan.Snapshot, result *v1.ExecutionResult, entry *execution) (string, error) {
	component, _, _ := strings.Cut(key, "/")
	declaration, ok := snap.Plan.BuildImages[r.Name]
	if !ok || entry == nil || entry.resources == nil {
		return "", problem("image_handoff", "build-image declaration is unavailable")
	}
	if r.Field == "tag" && component == declaration.Producer && strings.HasPrefix(key, component+"/environment/") {
		return buildImageTag(id, result.Attempt, r.Name), nil
	}
	if r.Field == "id" && key == component+"/image" {
		// A retained pin alone never substitutes for a failed producer in this attempt.
		for _, pin := range result.BuildImages {
			if pin.Name == r.Name {
				return pin.ID, nil
			}
		}
	}
	return "", problem("image_handoff", fmt.Sprintf("build image %s has no successful current producer handoff", r.Name))
}
