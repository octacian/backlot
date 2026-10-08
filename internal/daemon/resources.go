package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/moby/moby/client"
	v1 "github.com/octacian/backlot/api/v1"
	"github.com/octacian/backlot/internal/localipc"
	"github.com/octacian/backlot/internal/plan"
	bolt "go.etcd.io/bbolt"
)

// Resource intents precede external mutation. An absent effect is reconciled by
// exact capability labels, never by names alone. Secret values stay private.
type ownedResource struct {
	Kind      string `json:"kind"`
	Name      string `json:"name"`
	Token     string `json:"token"`
	ID        string `json:"id,omitempty"`
	Value     string `json:"value,omitempty"`
	Component string `json:"component,omitempty"`
	Attempt   string `json:"attempt,omitempty"`
	Removed   bool   `json:"removed,omitempty"`
}
type resourceGeneration struct {
	ID          string                   `json:"id"`
	Endpoint    string                   `json:"endpoint,omitempty"`
	Resources   map[string]ownedResource `json:"resources"`
	Containers  []ownedResource          `json:"containers,omitempty"`
	Initialized map[string]bool          `json:"initialized"`
}

func (s *store) resources(id string) (resourceGeneration, error) {
	var g resourceGeneration
	err := s.db.View(func(tx *bolt.Tx) error {
		if _, err := load(tx, id); err != nil {
			return err
		}
		data := tx.Bucket([]byte("resources")).Get([]byte(id))
		if data == nil {
			return nil
		}
		if err := json.Unmarshal(data, &g); err != nil {
			return problem("state_corrupt", "private resource generation cannot be decoded; preserved")
		}
		if g.ID == "" {
			if g.Endpoint != "" || len(g.Resources) > 0 || len(g.Containers) > 0 || len(g.Initialized) > 0 {
				return problem("state_corrupt", "resource generation identity missing; resources preserved")
			}
			return nil
		}
		if !validID(g.ID) || g.Resources == nil || g.Initialized == nil {
			return problem("state_corrupt", "resource generation incomplete; resources preserved")
		}
		for _, r := range g.Resources {
			if !validID(r.Token) || r.Name == "" {
				return problem("state_corrupt", "resource ownership incomplete; resources preserved")
			}
		}
		for _, r := range g.Containers {
			if r.Kind != "container" || !validID(r.Token) || r.Name == "" || !validID(r.Attempt) {
				return problem("state_corrupt", "container ownership incomplete; resources preserved")
			}
		}
		return nil
	})
	return g, err
}
func (s *store) saveResources(id string, g resourceGeneration) error {
	return s.db.Update(func(tx *bolt.Tx) error { return put(tx, "resources", id, g) })
}
func hasDocker(p v1.PlanResponse) bool {
	for _, c := range p.Components {
		if c.Runtime == v1.Container {
			return true
		}
	}
	for _, r := range p.Resources {
		if r.Kind == "volume" || r.Kind == "network" {
			return true
		}
	}
	return false
}
func retained(p v1.PlanResponse) bool {
	// Every selected Docker workload also retains its implicit instance network.
	if hasDocker(p) {
		return true
	}
	for _, r := range p.Resources {
		if r.Kind != "port" {
			return true
		}
	}
	return false
}
func safeRestart(old, next plan.Snapshot) error {
	unsafe := func() error {
		return problem("unsafe_drift", "retained resource, initialization or sensitive configuration changed; use explicit reset/destroy; runtime preserved")
	}
	if !reflect.DeepEqual(old.Plan.Resources, next.Plan.Resources) {
		return unsafe()
	}
	if !retained(old.Plan) {
		return nil
	}
	if !reflect.DeepEqual(old.Docker, next.Docker) {
		return unsafe()
	}
	type contract struct {
		Name           string
		Runtime        v1.Runtime
		Image          *v1.PlannedValue
		Args           []string
		Resources      []string
		Mounts         []v1.Mount
		Initialization *v1.PlannedComponent
	}
	contracts := func(snap plan.Snapshot) []contract {
		var result []contract
		for _, c := range snap.Plan.Components {
			x := contract{Name: c.Name, Resources: c.Resources, Mounts: c.Mounts, Runtime: c.Runtime}
			if c.Runtime == v1.Container && len(c.Mounts) > 0 {
				x.Image = c.Image
				x.Args = c.Args
			}
			if c.Policy == v1.FreshOnly {
				copy := c
				x.Initialization = &copy
			}
			if len(c.Resources) > 0 || len(c.Mounts) > 0 || x.Initialization != nil {
				result = append(result, x)
			}
		}
		sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
		return result
	}
	if !reflect.DeepEqual(contracts(old), contracts(next)) {
		return unsafe()
	}
	// All selected inherited/seed values are potentially credentials. Baseline
	// process locations are excluded; explicitly secret baseline overrides are not.
	sensitive := func(snap plan.Snapshot) map[string]v1.PlannedValue {
		values := map[string]v1.PlannedValue{}
		initializers := map[string]bool{}
		for _, c := range snap.Plan.Components {
			initializers[c.Name] = c.Policy == v1.FreshOnly
		}
		for key, value := range snap.Secrets {
			component, _, _ := strings.Cut(key, "/")
			if snap.ImplicitBaseline[key] && !initializers[component] {
				continue
			}
			values[key] = value
		}
		return values
	}
	if !reflect.DeepEqual(sensitive(old), sensitive(next)) {
		return unsafe()
	}
	return nil
}
func (s *service) allocateResources(ctx context.Context, id string, snap plan.Snapshot, entry *execution) error {
	g, err := s.store.resources(id)
	if err != nil {
		return err
	}
	if g.ID == "" {
		g = resourceGeneration{ID: newID(), Resources: map[string]ownedResource{}, Initialized: map[string]bool{}}
		// Retain configured cleanup authority even for native-only directories.
		// Recording it does not contact Docker during prepare or native launch.
		if snap.Docker != nil {
			g.Endpoint = snap.Docker.Endpoint
		}
		if err = s.store.saveResources(id, g); err != nil {
			return err
		}
	}
	entry.resources = &g
	if hasDocker(snap.Plan) {
		expected := ""
		if snap.Docker != nil {
			expected = snap.Docker.Endpoint
		}
		if g.Endpoint != expected {
			if len(g.Resources) != 0 || len(g.Containers) != 0 {
				return problem("unsafe_drift", "retained Docker endpoint differs from accepted snapshot; explicit reset/destroy required")
			}
			g.Endpoint = expected
			if err = s.store.saveResources(id, g); err != nil {
				return err
			}
		}
		entry.docker, err = newDocker(g.Endpoint)
		if err != nil {
			return err
		}
	}
	declarations := map[string]v1.Resource{}
	for name, r := range snap.Plan.Resources {
		if r.Kind != "port" {
			declarations[name] = r
		}
	}
	if hasDocker(snap.Plan) {
		declarations["@network"] = v1.Resource{Kind: "network"}
	}
	names := make([]string, 0, len(declarations))
	for name := range declarations {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if err = ctx.Err(); err != nil {
			return err
		}
		r, exists := g.Resources[name]
		if !exists {
			r = ownedResource{Kind: declarations[name].Kind, Token: newID()}
			r.Name = "backlot-" + r.Token
			if r.Kind == "directory" {
				r.Name = filepath.Join(s.directory, "data", r.Token)
			}
			if r.Kind == "secret" {
				r.Value = newID()
			}
			g.Resources[name] = r
			if err = s.store.saveResources(id, g); err != nil {
				return err
			}
		}
		if !exists && s.checkpoint != nil {
			if err = s.checkpoint("resource-intent:" + name); err != nil {
				return err
			}
		}
		// Recover proven effects, but never silently recreate possibly lost storage
		// with its old credentials.
		if exists {
			if r.Removed {
				return problem("incomplete_allocation", "retained generation cleanup is incomplete; explicit reset/destroy required")
			}
			if r.ID == "" {
				effect, effectErr := recoverResourceEffect(ctx, entry.docker, r)
				if effectErr != nil {
					return problem("incomplete_allocation", "retained allocation intent cannot be verified; explicit reset/destroy required")
				}
				r.ID = effect
				g.Resources[name] = r
				if err = s.store.saveResources(id, g); err != nil {
					return err
				}
			}
			if err = verifyResource(ctx, entry.docker, r); err != nil {
				return err
			}
			continue
		}
		switch r.Kind {
		case "secret":
			r.ID = r.Token
		case "directory":
			if err = localipc.Directory(filepath.Join(s.directory, "data"), true); err != nil {
				return err
			}
			if err = os.Mkdir(r.Name, 0700); err != nil {
				return err
			}
			if err = os.WriteFile(filepath.Join(r.Name, ".backlot-owner"), []byte(r.Token), 0600); err != nil {
				return err
			}
			r.ID = r.Token
		default:
			r.ID, err = createDockerResource(ctx, entry.docker, r)
			if err != nil {
				return err
			}
		}
		g.Resources[name] = r
		if err = s.store.saveResources(id, g); err != nil {
			return err
		}
		if s.checkpoint != nil {
			if err = s.checkpoint("resource-effect:" + name); err != nil {
				return err
			}
		}
	}
	return nil
}
func verifyResource(ctx context.Context, d *dockerEngine, r ownedResource) error {
	switch r.Kind {
	case "secret":
		if r.ID != r.Token || r.Value == "" {
			return problem("state_corrupt", "secret generation missing")
		}
		return nil
	case "directory":
		if err := localipc.Directory(r.Name, false); err != nil {
			return err
		}
		path := filepath.Join(r.Name, ".backlot-owner")
		if err := localipc.File(path, false); err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if string(data) != r.Token {
			return problem("cleanup_failed", "directory ownership uncertain; preserved")
		}
		return nil
	default:
		return verifyDockerResource(ctx, d, r)
	}
}
func (s *store) removeResources(ctx context.Context, id string) error {
	g, err := s.resources(id)
	if err != nil || g.ID == "" {
		return err
	}
	var d *dockerEngine
	needsEngine := false
	for _, r := range g.Resources {
		if !r.Removed && (r.Kind == "directory" || r.Kind == "volume" || r.Kind == "network") {
			needsEngine = true
		}
	}
	for _, r := range g.Containers {
		if !r.Removed {
			needsEngine = true
		}
	}
	if g.Endpoint != "" && needsEngine {
		d, err = newDocker(g.Endpoint)
		if err != nil {
			return err
		}
		defer func() { _ = d.Close() }()
	}
	// Container intent/effect must be absent before releasing any retained data.
	if err = s.cleanupContainers(ctx, id, &g, d); err != nil {
		return err
	}
	// Inspect every container, including unrelated consumers. Docker protects its
	// volumes/networks too; bind directories need this explicit consumer check.
	if d != nil {
		if err = d.noConsumers(ctx, g); err != nil {
			return err
		}
	}
	names := make([]string, 0, len(g.Resources))
	for name := range g.Resources {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		r := g.Resources[name]
		if r.Removed {
			continue
		}
		switch r.Kind {
		case "secret":
		case "directory":
			if _, err = os.Lstat(r.Name); !os.IsNotExist(err) {
				if err = verifyResource(ctx, d, r); err != nil {
					return err
				}
				if err = os.RemoveAll(r.Name); err != nil {
					return err
				}
			}
		default:
			if err = removeDockerResource(ctx, d, r); err != nil {
				return err
			}
		}
		r.Removed = true
		g.Resources[name] = r
		if err = s.saveResources(id, g); err != nil {
			return err
		}
	}
	return s.saveResources(id, resourceGeneration{})
}
func (s *service) destroy(ctx context.Context, id string) (v1.Instance, error) {
	unlock := s.lockMutation(id)
	defer unlock()
	instance, err := s.stopExecution(id)
	if err != nil {
		var typed *v1.PlanError
		if !errors.As(err, &typed) || typed.Code != "collection_failed" {
			return instance, err
		}
	}
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	if err = s.store.removeResources(cleanup, id); err != nil {
		return instance, problem("cleanup_failed", fmt.Sprintf("destroy preserved remaining resources: %v", err))
	}
	if instance.Execution == nil {
		instance.Execution = &v1.ExecutionResult{Attempt: newID(), Components: []v1.ComponentResult{}}
	}
	instance, err = s.store.execution(id, v1.Destroyed, instance.Execution)
	if err != nil {
		return instance, err
	}
	err = s.store.db.Update(func(tx *bolt.Tx) error {
		key := instance.Plan.Project + "/" + instance.Checkout.ID + "/" + instance.Plan.Scene
		bucket := tx.Bucket([]byte("persistent"))
		if string(bucket.Get([]byte(key))) == id {
			return bucket.Delete([]byte(key))
		}
		return nil
	})
	return instance, err
}

// recoverResourceEffect records an already-existing effect only after exact
// ownership proof. It never creates or repairs missing retained storage.
func recoverResourceEffect(ctx context.Context, d *dockerEngine, r ownedResource) (string, error) {
	if !validID(r.Token) {
		return "", problem("state_corrupt", "resource capability missing")
	}
	switch r.Kind {
	case "secret":
		if !validID(r.Value) {
			return "", problem("state_corrupt", "secret generation incomplete")
		}
		return r.Token, nil
	case "directory":
		if err := verifyResource(ctx, d, r); err != nil {
			return "", err
		}
		return r.Token, nil
	case "volume":
		if d == nil {
			return "", problem("missing_provider", "recorded Docker engine required")
		}
		out, err := d.VolumeInspect(ctx, r.Name, client.VolumeInspectOptions{})
		if err != nil {
			return "", err
		}
		if err = owned(out.Volume.Labels, r); err != nil {
			return "", err
		}
		return out.Volume.Name, nil
	case "network":
		if d == nil {
			return "", problem("missing_provider", "recorded Docker engine required")
		}
		out, err := d.NetworkInspect(ctx, r.Name, client.NetworkInspectOptions{})
		if err != nil {
			return "", err
		}
		if err = owned(out.Network.Labels, r); err != nil {
			return "", err
		}
		return out.Network.ID, nil
	}
	return "", problem("state_corrupt", "unknown retained allocation kind")
}
