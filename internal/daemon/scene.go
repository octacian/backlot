package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	v1 "github.com/octacian/backlot/api/v1"
	"github.com/octacian/backlot/internal/plan"
	bolt "go.etcd.io/bbolt"
)

func (s *service) startScene(ctx context.Context, request v1.SceneStartRequest) (v1.SceneResponse, error) {
	result := v1.SceneResponse{APIVersion: v1.Version, DaemonGeneration: s.generation, Outcome: v1.SceneFound}
	target := v1.SceneTarget{CWD: request.CWD, Scene: request.Plan.Scene, ProjectPath: request.Plan.ProjectPath}
	if err := target.Validate(); err != nil {
		return result, err
	}
	if len(request.Plan.TerminalArgs) > 0 {
		return result, problem("invalid_request", "persistent scene start does not accept terminal arguments")
	}
	_, project, err := plan.Discover(request.CWD, request.Plan.ProjectPath)
	if err != nil {
		return result, err
	}
	canonicalTarget := v1.SceneTarget{Scene: target.Scene, CWD: project}
	if target.ProjectPath != "" {
		canonicalTarget.ProjectPath = project
	} else {
		canonicalTarget.CWD, err = sceneDirectory(request.CWD)
		if err != nil {
			return result, err
		}
	}
	result.CanonicalTarget = &canonicalTarget
	request.Plan.ProjectPath = project
	response, err := s.runSelected(ctx, v1.RunRequest{APIVersion: v1.Version, Plan: request.Plan, Options: request.Options}, false, true)
	if err != nil {
		return result, err
	}
	result.Result = &response
	checkout := response.Instance.Checkout
	result.Checkout = &checkout
	return result, nil
}

// sceneDirectory resolves only directory metadata; a deleted recognized manifest is valid.
func sceneDirectory(path string) (string, error) {
	if info, err := os.Stat(path); err != nil || !info.IsDir() {
		switch filepath.Base(path) {
		case "backlot.yaml", "backlot.yml", "backlot.json":
			path = filepath.Dir(path)
		default:
			return "", problem("checkout_identity", "project location must be an existing directory or recognized manifest path")
		}
	}
	path, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", problem("checkout_identity", "cannot resolve project or working directory; use recorded instance ID")
	}
	info, err := os.Stat(path)
	if err != nil || !info.IsDir() {
		return "", problem("checkout_identity", "project directory is unavailable; use recorded instance ID")
	}
	return path, nil
}

// resolveScene selects and reads one snapshot in the same transaction. It does not
// inspect manifest/configuration contents, contact providers or persist discovery.
func (s *service) resolveScene(target v1.SceneTarget) (v1.SceneResponse, error) {
	response := v1.SceneResponse{APIVersion: v1.Version, DaemonGeneration: s.generation, Outcome: v1.SceneNotFound}
	if err := target.Validate(); err != nil {
		return response, err
	}
	if target.InstanceID != "" {
		instance, err := s.store.inspect(target.InstanceID)
		var detail *v1.PlanError
		if errors.As(err, &detail) && detail.Code == "not_found" {
			return response, nil
		}
		if err != nil {
			return response, err
		}
		if err := validateSceneRecord(instance); err != nil {
			return response, err
		}
		if instance.Plan.Lifetime != v1.Persistent {
			return response, problem("invalid_lifetime", "scene operations require persistent instances; use recorded-ID commands for disposable work")
		}
		response.Outcome = v1.SceneFound
		response.Result = &v1.InstanceResponse{APIVersion: v1.Version, Instance: instance}
		return response, nil
	}
	anchor := target.CWD
	if target.ProjectPath != "" {
		anchor = target.ProjectPath
	}
	anchor, err := sceneDirectory(anchor)
	if err != nil {
		return response, err
	}
	canonicalTarget := v1.SceneTarget{Scene: target.Scene, CWD: anchor}
	if target.ProjectPath != "" {
		canonicalTarget.ProjectPath = anchor
	}
	response.CanonicalTarget = &canonicalTarget
	root := plan.CheckoutRoot(anchor)
	// CheckoutRoot returns anchor both at a Git root and when no Git boundary
	// exists. Only the latter permits recorded non-Git ancestor discovery.
	_, boundaryErr := os.Lstat(filepath.Join(root, ".git"))
	gitBoundary := boundaryErr == nil
	if boundaryErr != nil && !os.IsNotExist(boundaryErr) {
		return response, problem("checkout_identity", "cannot inspect Git checkout boundary")
	}
	err = s.store.db.View(func(tx *bolt.Tx) error {
		// Collect recorded locations before filtering by scene/status. This also makes
		// destroyed history available for nearest-project discovery and non-Git roots.
		instances, err := storedSceneInstances(tx)
		if err != nil {
			return err
		}
		nearest := ""
		for _, i := range instances {
			project := i.project
			if within(i.Checkout.Path, anchor) && within(i.Checkout.Path, project) && within(project, anchor) && (!gitBoundary || i.Checkout.Path == root) && (target.ProjectPath == "" || project == anchor) && len(project) > len(nearest) {
				nearest = project
			}
		}
		// With no Git boundary, a recorded ancestor project is the checkout root.
		// An unrecorded explicit non-Git directory identifies itself; no manifest read.
		if !gitBoundary {
			if nearest != "" {
				root = plan.CheckoutRoot(nearest)
			} else if target.ProjectPath == "" {
				return problem("checkout_identity", "unrecorded non-Git project root is unknown; select --project directory or a recorded instance ID")
			}
		}
		checkout, err := identify(root)
		if err != nil {
			return err
		}
		response.Checkout = &checkout
		if data := tx.Bucket([]byte("paths")).Get([]byte(checkout.Path)); data != nil && string(data) != checkout.ID {
			return problem("checkout_replaced", "recorded checkout path identifies different files; use explicit recorded ID")
		}
		if data := tx.Bucket([]byte("checkouts")).Get([]byte(checkout.ID)); data != nil {
			var previous v1.CheckoutIdentity
			if err := json.Unmarshal(data, &previous); err != nil {
				return problem("state_corrupt", "cannot decode checkout identity")
			}
			if previous.Path != checkout.Path || previous.GitDirectory != checkout.GitDirectory {
				return problem("checkout_moved", "checkout moved; use explicit recorded ID")
			}
		}
		selected := ""
		for _, instance := range instances {
			project := instance.project
			if project != nearest {
				continue
			}
			if instance.Checkout.Path != checkout.Path || instance.Checkout.ID != checkout.ID || instance.Checkout.GitDirectory != checkout.GitDirectory {
				return problem("checkout_changed", "recorded project checkout identity changed; use explicit recorded ID")
			}
			if instance.lifetime != v1.Persistent || instance.status == v1.Destroyed || instance.scene != target.Scene {
				continue
			}
			if selected != "" {
				return problem("ambiguous_target", "multiple recorded persistent instances match; select an explicit instance ID")
			}
			selected = instance.id
		}
		if selected != "" {
			r, err := load(tx, selected)
			if err != nil {
				return err
			}
			response.Outcome = v1.SceneFound
			response.Result = &v1.InstanceResponse{APIVersion: v1.Version, Instance: r.Instance}
		}
		return nil
	})
	return response, err
}

func sameCheckout(a, b *v1.CheckoutIdentity) bool {
	return a != nil && b != nil && a.ID == b.ID && a.Path == b.Path && a.GitDirectory == b.GitDirectory
}

func (s *service) scene(target v1.SceneTarget, stop bool) (v1.SceneResponse, error) {
	response, err := s.resolveScene(target)
	if err != nil || !stop || response.Result == nil {
		return response, err
	}
	id := response.Result.Instance.ID
	if s.beforeSceneMutation != nil {
		s.beforeSceneMutation(id)
	}
	unlock := s.lockMutation(id)
	defer unlock()
	current, err := s.resolveScene(target)
	if err != nil {
		return current, err
	}
	if current.Result == nil || current.Result.Instance.ID != id || target.InstanceID == "" && !sameCheckout(response.Checkout, current.Checkout) {
		return current, problem("conflict", "scene selection changed while awaiting mutation lock; retry or use explicit recorded ID")
	}
	s.mu.Lock()
	entry := s.executions[id]
	s.mu.Unlock()
	running := false
	if entry != nil {
		select {
		case <-entry.done:
		default:
			running = true
		}
	}
	instance, err := s.stopExecution(id)
	if err != nil {
		return current, err
	}
	// An old no-owner/failed record must not mask collection failures merely
	// because joinExecution had no live owner to join.
	if instance.Execution != nil && instance.Execution.CollectionFailure != "" {
		return current, problem("collection_failed", instance.Execution.CollectionFailure)
	}
	current.Result = &v1.InstanceResponse{APIVersion: v1.Version, Instance: instance}
	current.Outcome = v1.SceneAlreadyStopped
	if running {
		current.Outcome = v1.SceneStopped
	}
	return current, nil
}

// validateSceneRecord fences selection metadata before records can contribute to
// or disappear from nearest-project discovery. Legacy record/recovery decoding
// remains independent of the new location-based selection contract.
func validateSceneRecord(instance v1.Instance) error {
	if err := instance.ValidateSceneLocation(); err != nil {
		return problem("state_corrupt", "invalid recorded scene location or selector; preserve state for diagnosis")
	}
	return nil
}

func requireSceneLocation(instance v1.Instance, project string) error {
	if err := validateSceneRecord(instance); err != nil {
		return err
	}
	if filepath.Dir(instance.Plan.ManifestPath) != project {
		return problem("conflict", "persistent scene belongs to a different recorded project directory; use its explicit recorded instance ID")
	}
	return nil
}

// recordedScene retains only selection metadata, never historical plans or secrets.
type recordedScene struct {
	id, project, scene, slot string
	Checkout                 v1.CheckoutIdentity
	lifetime                 v1.Lifetime
	status                   v1.InstanceStatus
}

// storedSceneInstances validates existing relationships before named discovery or
// acceptance. Each bucket is traversed once; temporary maps keep both slot checks
// linear without changing persistent indexes or the legacy decoder. Only a unique
// selected result is reloaded in full by resolveScene within the same transaction.
func storedSceneInstances(tx *bolt.Tx) (map[string]recordedScene, error) {
	instances := make(map[string]recordedScene)
	if err := tx.Bucket([]byte("instances")).ForEach(func(key, data []byte) error {
		var r record
		if err := decodeRecord(data, string(key), &r); err != nil {
			return err
		}
		if err := validateSceneRecord(r.Instance); err != nil {
			return err
		}
		i := r.Instance
		instances[string(key)] = recordedScene{
			id: i.ID, project: filepath.Dir(i.Plan.ManifestPath), scene: i.Plan.Scene,
			slot: sceneSlot(i), lifetime: i.Plan.Lifetime, status: i.Status,
			Checkout: v1.CheckoutIdentity{ID: i.Checkout.ID, Path: i.Checkout.Path, GitDirectory: i.Checkout.GitDirectory},
		}
		return nil
	}); err != nil {
		return nil, err
	}
	checkouts := make(map[string]v1.CheckoutIdentity)
	if err := tx.Bucket([]byte("checkouts")).ForEach(func(key, data []byte) error {
		var checkout v1.CheckoutIdentity
		if json.Unmarshal(data, &checkout) != nil || checkout.ID != string(key) {
			return problem("state_corrupt", "cannot decode recorded checkout identity")
		}
		checkout.GitHead = "" // HEAD is provenance, not selection identity.
		checkouts[string(key)] = checkout
		return nil
	}); err != nil {
		return nil, err
	}
	paths := make(map[string]string)
	if err := tx.Bucket([]byte("paths")).ForEach(func(key, data []byte) error {
		paths[string(key)] = string(data)
		return nil
	}); err != nil {
		return nil, err
	}
	slots := make(map[string]string)
	if err := tx.Bucket([]byte("persistent")).ForEach(func(key, data []byte) error {
		i, exists := instances[string(data)]
		if !exists || i.lifetime != v1.Persistent || string(key) != i.slot {
			return problem("state_corrupt", "persistent slot and recorded scene identity disagree")
		}
		// Destroy commits Destroyed before removing this slot in a separate
		// transaction. The matching slot is valid throughout that existing gap.
		slots[string(key)] = string(data)
		return nil
	}); err != nil {
		return nil, err
	}
	for _, i := range instances {
		checkout, exists := checkouts[i.Checkout.ID]
		if !exists || !sameCheckout(&checkout, &i.Checkout) || paths[i.Checkout.Path] != i.Checkout.ID {
			return nil, problem("state_corrupt", "recorded scene and checkout location indexes disagree")
		}
		if i.lifetime == v1.Persistent && i.status != v1.Destroyed && slots[i.slot] != i.id {
			return nil, problem("state_corrupt", "recorded persistent scene has no matching slot")
		}
	}
	return instances, nil
}

func sceneSlot(i v1.Instance) string {
	return i.Plan.Project + "/" + i.Checkout.ID + "/" + i.Plan.Scene
}
