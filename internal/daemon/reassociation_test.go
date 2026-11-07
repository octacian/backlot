//go:build darwin || linux

package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	v1 "github.com/octacian/backlot/api/v1"
	"github.com/octacian/backlot/internal/plan"
	"github.com/octacian/backlot/internal/projectconfig"
	bolt "go.etcd.io/bbolt"
)

type repairFixture struct {
	s               *service
	project, config string
	request         v1.ReassociationRequest
	instance        v1.Instance
	manifest        v1.Manifest
}

func newRepairFixture(t *testing.T) *repairFixture {
	t.Helper()
	base := shortTemp(t)
	var canonicalErr error
	base, canonicalErr = filepath.EvalSymlinks(base)
	if canonicalErr != nil {
		t.Fatal(canonicalErr)
	}
	project := filepath.Join(base, "project")
	if err := os.Mkdir(project, 0700); err != nil {
		t.Fatal(err)
	}
	git(t, project, "init", "-q", "-b", "main")
	registry := filepath.Join(base, "registry")
	t.Setenv("BACKLOT_PROJECT_CONFIG_DIR", registry)
	f := &repairFixture{project: project, config: filepath.Join(base, "machine.json")}
	f.manifest = v1.Manifest{Version: v1.ManifestVersion, Project: "repair-fixture", Tools: map[string]string{"shell": "/bin/sh"}, SharedServices: map[string]v1.SharedService{"sql": {Manager: "mariadb", Image: "mariadb:11.4", Storage: "volume", Admin: "generated", Port: 3306, ReadinessTimeout: "60s"}}, Resources: map[string]v1.Resource{"db": {Kind: "allocation", SharedService: "sql"}}, Components: map[string]v1.Component{"job": {Kind: v1.Job, Runtime: v1.Native, Policy: v1.EachStart, Command: &v1.Command{Tool: "shell", Args: v1.StringArguments("-c", "exit 0")}, Resources: []string{"db"}}}, Scenes: map[string]v1.Scene{"dev": {Lifetime: v1.Persistent, Components: []string{"job"}, Resources: []string{"db"}}}}
	f.write(t)
	data, _ := json.Marshal(v1.MachineConfig{Version: v1.ManifestVersion, Docker: &v1.DockerConfig{Endpoint: "unix:///owned-unavailable-fixture.sock"}})
	if err := os.WriteFile(f.config, data, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(base, "state"), 0700); err != nil {
		t.Fatal(err)
	}
	state, err := openStore(filepath.Join(base, "state"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := f.s.store.db.Close(); err != nil {
			t.Error(err)
		}
	})
	f.s = &service{store: state, directory: state.directory, lease: time.Minute, generation: newID()}
	prepared, err := f.s.prepare(context.Background(), request(project, f.config, "dev"))
	if err != nil {
		t.Fatal(err)
	}
	f.instance = prepared.Instance
	source, err := plan.Locate(project)
	if err != nil {
		t.Fatal(err)
	}
	shared, err := identifyShared(source, f.manifest.Project)
	if err != nil {
		t.Fatal(err)
	}
	root, err := projectconfig.Root(project)
	if err != nil {
		t.Fatal(err)
	}
	f.request = v1.ReassociationRequest{APIVersion: v1.Version, ProjectPath: project, ConfigPath: f.config, RegistryRoot: root, ProjectID: shared.ID, CheckoutID: checkoutParent(f.instance)}
	return f
}
func (f *repairFixture) write(t *testing.T) {
	t.Helper()
	data, _ := json.Marshal(f.manifest)
	if err := os.WriteFile(filepath.Join(f.project, "backlot.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
}
func (f *repairFixture) move(t *testing.T, name string) {
	t.Helper()
	next := filepath.Join(filepath.Dir(f.project), name)
	if err := os.Rename(f.project, next); err != nil {
		t.Fatal(err)
	}
	f.project = next
	f.request.ProjectPath = next
}
func (f *repairFixture) preview(t *testing.T) v1.ReassociationResponse {
	t.Helper()
	r, err := f.s.reassociate(context.Background(), f.request)
	if err != nil {
		t.Fatal(err)
	}
	return r
}
func (f *repairFixture) apply(t *testing.T, p v1.ReassociationResponse) error {
	t.Helper()
	r := f.request
	r.Apply = true
	r.Preview = p.Preview
	_, err := f.s.reassociate(context.Background(), r)
	return err
}

func (f *repairFixture) captureSnapshot(t *testing.T) {
	t.Helper()
	source, err := plan.Locate(f.project)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := plan.Prepare(source, v1.PlanRequest{ProjectPath: f.project, ConfigPath: f.config, Scene: "dev"})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.s.store.replaceSnapshot(f.instance.ID, snapshot); err != nil {
		t.Fatal(err)
	}
}

func (f *repairFixture) preparePeer(t *testing.T) v1.Instance {
	t.Helper()
	git(t, f.project, "add", "backlot.json")
	git(t, f.project, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "-qm", "Fixture")
	peer := filepath.Join(filepath.Dir(f.project), "peer")
	git(t, f.project, "worktree", "add", "-q", "-b", "peer", peer)
	prepared, err := f.s.prepare(context.Background(), request(peer, f.config, "dev"))
	if err != nil {
		t.Fatal(err)
	}
	return prepared.Instance
}

func TestReassociationReadOnlyAndAtomic(t *testing.T) {
	f := newRepairFixture(t)
	f.move(t, "moved")
	before := stateContents(t, f.s.store)
	preview := f.preview(t)
	if len(preview.Blockers) > 0 || preview.Applied {
		t.Fatal(preview.Blockers)
	}
	if !reflect.DeepEqual(before, stateContents(t, f.s.store)) {
		t.Fatal("preview mutated state")
	}
	if err := f.apply(t, preview); err != nil {
		t.Fatal(err)
	}
	after := stateContents(t, f.s.store)
	for name, records := range before {
		if name != "checkout_bindings" && name != "project_bindings" && !reflect.DeepEqual(records, after[name]) {
			t.Fatal("repair changed ownership or launch facts", name)
		}
	}
	got, err := f.s.store.inspect(f.instance.ID)
	if err != nil || got.Checkout != f.instance.Checkout || got.Association.ProjectPath != f.project {
		t.Fatal("historical/current identity mixed", err)
	}
	selected, err := f.s.scene(v1.SceneTarget{CWD: f.project, Scene: "dev"}, false)
	if err != nil || selected.Result.Instance.ID != f.instance.ID {
		t.Fatal("named scene lost stable identity", err)
	}
	code(t, f.apply(t, preview), "stale_preview")
	f.move(t, "project")
	_, err = f.s.prepare(context.Background(), request(f.project, f.config, "dev"))
	code(t, err, "project_registration")
	if err := f.apply(t, f.preview(t)); err != nil {
		t.Fatal("explicit move back failed", err)
	}
}
func TestReassociationSamePathIdentityAndReservations(t *testing.T) {
	f := newRepairFixture(t)
	old := filepath.Join(f.project, ".git")
	if err := os.Rename(old, old+"-retained"); err != nil {
		t.Fatal(err)
	}
	git(t, f.project, "init", "-q", "-b", "main")
	p := f.preview(t)
	if p.Current.ID == f.request.ProjectID || p.Checkout.ID == f.instance.Checkout.ID {
		t.Fatal("real filesystem change not observed")
	}
	if err := f.apply(t, p); err != nil {
		t.Fatal(err)
	}
	got, err := f.s.store.inspect(f.instance.ID)
	if err != nil || got.Checkout.ID != f.instance.Checkout.ID || got.Association.CheckoutParent != f.instance.Checkout.ID {
		t.Fatal("reassociation lost immutable parent", err)
	}
	competing := filepath.Join(filepath.Dir(f.project), "clone")
	git(t, filepath.Dir(f.project), "clone", "-q", f.project, competing)
	data, err := os.ReadFile(filepath.Join(f.project, "backlot.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(competing, "backlot.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.prepare(context.Background(), request(competing, f.config, "dev")); err != nil {
		t.Fatal(err)
	}
	f.request.ProjectPath = competing
	p = f.preview(t)
	if len(p.Blockers) == 0 {
		t.Fatal("competing clone merged")
	}
	code(t, f.apply(t, p), "reassociation_blocked")
}
func TestReassociationFreshnessLocksAndCancellation(t *testing.T) {
	for _, scenario := range []string{"source bytes", "instance lock", "cancellation", "rollback", "uncertain commit"} {
		t.Run(scenario, func(t *testing.T) {
			f := newRepairFixture(t)
			f.move(t, "moved")
			p := f.preview(t)
			before := stateContents(t, f.s.store)
			switch scenario {
			case "source bytes":
				path := filepath.Join(f.project, "backlot.json")
				data, _ := os.ReadFile(path)
				if err := os.WriteFile(path, append(data, '\n'), 0600); err != nil {
					t.Fatal(err)
				}
				code(t, f.apply(t, p), "stale_preview")
			case "instance lock":
				lock := f.s.mutationLock(f.instance.ID)
				lock.Lock()
				err := f.apply(t, p)
				lock.Unlock()
				code(t, err, "conflict")
			case "cancellation":
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				r := f.request
				r.Apply = true
				r.Preview = p.Preview
				_, err := f.s.reassociate(ctx, r)
				if !errors.Is(err, context.Canceled) {
					t.Fatal("cancellation lost", err)
				}
			case "rollback":
				f.s.store.sharedCheckpoint = func(point string) error {
					if point == "reassociation-before-commit" {
						return errors.New("injected precommit failure")
					}
					return nil
				}
				if err := f.apply(t, p); err == nil {
					t.Fatal("rollback seam did not fail")
				}
			case "uncertain commit":
				f.s.store.sharedCheckpoint = func(point string) error {
					if point == "reassociation-committed" {
						return errors.New("injected response loss")
					}
					return nil
				}
				code(t, f.apply(t, p), "reassociation_committed_uncertain")
				directory := f.s.store.directory
				if err := f.s.store.db.Close(); err != nil {
					t.Fatal(err)
				}
				restored, err := openStore(directory)
				if err != nil {
					t.Fatal("committed association recovery", err)
				}
				f.s.store = restored
				f.s.generation = newID()
				f.s.repairKey = nil
				var ack string
				if err := f.s.store.db.View(func(tx *bolt.Tx) error {
					b, e := readProjectBinding(tx, f.request.ProjectID)
					ack = b.Acknowledgement
					return e
				}); err != nil || ack != p.Preview {
					t.Fatal("committed acknowledgement unavailable", err)
				}
				return
			}
			if !reflect.DeepEqual(before, stateContents(t, f.s.store)) {
				t.Fatal("refusal changed daemon state")
			}
		})
	}
}
func TestReassociationPrivateRootsAndFreshness(t *testing.T) {
	f := newRepairFixture(t)
	f.move(t, "moved")
	old := f.preview(t)
	other := filepath.Join(shortTemp(t), "other-registry")
	t.Setenv("BACKLOT_PROJECT_CONFIG_DIR", other)
	p := f.preview(t)
	if p.Private.OperationRoot == p.Private.RuntimeRoot || len(p.Blockers) == 0 {
		t.Fatal("operation root silently overridden")
	}
	code(t, f.apply(t, p), "reassociation_blocked")
	f.s.generation = newID()
	f.s.repairKey = nil
	code(t, f.apply(t, old), "stale_preview")
	root, err := projectconfig.Root(f.project)
	if err != nil {
		t.Fatal(err)
	}
	f.request.RegistryRoot = root
	p = f.preview(t)
	if len(p.Blockers) > 0 {
		t.Fatal("matching root could not compose", p.Blockers)
	}
}
func TestReassociationRelativeFreshInitializerRefused(t *testing.T) {
	f := newRepairFixture(t)
	if err := os.Mkdir(filepath.Join(f.project, "bin"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.project, "bin", "setup"), []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
		t.Fatal(err)
	}
	f.manifest.Tools["shell"] = "./bin/setup"
	c := f.manifest.Components["job"]
	c.Policy = v1.FreshOnly
	c.Initializes = []string{"db"}
	f.manifest.Components["job"] = c
	f.write(t)
	f.captureSnapshot(t)
	f.move(t, "moved")
	p := f.preview(t)
	if len(p.Blockers) == 0 {
		t.Fatal("relative fresh initializer relocation implicitly normalized")
	}
	code(t, f.apply(t, p), "reassociation_blocked")
}

func TestReassociationPrivateSetupAndOpaqueFreshness(t *testing.T) {
	f := newRepairFixture(t)
	f.manifest.Inputs = map[string]v1.Input{"token": {Secret: true}}
	f.write(t)
	secret := "private-fixture-value-never-disclose"
	fixtureInputs(t, f.project, f.config, map[string]string{"token": secret})
	// Exercise the retained v1 private-association path, independently of marker duplicate resolution.
	root, err := projectconfig.Root(f.project)
	if err != nil {
		t.Fatal(err)
	}
	registryPath := filepath.Join(root, "projects.json")
	rawRegistry, err := os.ReadFile(registryPath)
	if err != nil {
		t.Fatal(err)
	}
	var legacy struct {
		Version  int                                   `json:"version"`
		Revision string                                `json:"revision"`
		Projects map[string]map[string]json.RawMessage `json:"projects"`
	}
	if err := json.Unmarshal(rawRegistry, &legacy); err != nil {
		t.Fatal(err)
	}
	legacy.Version = 1
	for _, entry := range legacy.Projects {
		delete(entry, "common_id")
		delete(entry, "checkout_path")
		delete(entry, "admin_path")
	}
	rawRegistry, err = json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(registryPath, rawRegistry, 0600); err != nil {
		t.Fatal(err)
	}
	f.move(t, "moved")
	blocked := f.preview(t)
	if len(blocked.Blockers) == 0 || blocked.Private.RecordedAssociation == "" {
		t.Fatal("stale private association hidden")
	}
	setup, err := plan.OpenSetup(v1.ProjectSetupRequest{ProjectPath: f.project, ConfigPath: f.config, Reassociate: true})
	if err != nil {
		t.Fatal(err)
	}
	saved, err := setup.Save(context.Background(), v1.PrivateConfig{Version: v1.ManifestVersion, Inputs: map[string]string{"token": secret}}, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	p := f.preview(t)
	if len(p.Blockers) > 0 || p.Private.Association != saved.Association {
		t.Fatal("explicit setup prerequisite did not clear", p.Blockers)
	}
	raw, _ := json.Marshal(p)
	if strings.Contains(string(raw), secret) || strings.Contains(string(raw), hash(secret)) {
		t.Fatal("preview disclosed private input or unkeyed fingerprint")
	}
	data, err := os.ReadFile(saved.PrivatePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(saved.PrivatePath, append(data, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
	code(t, f.apply(t, p), "stale_preview")
}

func TestReassociationDoctorBeforePrivateComposition(t *testing.T) {
	f := newRepairFixture(t)
	f.move(t, "moved")
	f.manifest.Inputs = map[string]v1.Input{"missing": {Secret: true}}
	c := f.manifest.Components["job"]
	c.Environment = v1.Environment{Assign: map[string]v1.Value{"TOKEN": fixtureRef("input", "missing", "")}}
	f.manifest.Components["job"] = c
	f.write(t)
	response, err := f.s.doctor(context.Background(), v1.DoctorRequest{APIVersion: v1.Version, ProjectPath: f.project, ConfigPath: f.config, Scene: "dev"})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, check := range response.Checks {
		if check.Name == "shared_project_identity" && check.Status == v1.DoctorFailed {
			found = true
		}
	}
	if !found || response.Healthy {
		t.Fatal("private failure masked shared identity conflict")
	}
}

func TestReassociationMigrationRefusesCollisionUnchanged(t *testing.T) {
	for _, scenario := range []string{"namespace collision", "origin index"} {
		t.Run(scenario, func(t *testing.T) {
			root := copyStateFixture(t, "testdata/state/v12/state.db")
			path := filepath.Join(root, "state.db")
			db, err := bolt.Open(path, 0600, nil)
			if err != nil {
				t.Fatal(err)
			}
			if err := db.Update(func(tx *bolt.Tx) error {
				if scenario == "namespace collision" {
					_, e := tx.CreateBucket([]byte("project_bindings"))
					return e
				}
				return tx.Bucket([]byte("paths")).Put([]byte("/invalid-origin"), []byte(newID()))
			}); err != nil {
				t.Fatal(err)
			}
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if opened, err := openStore(root); err == nil {
				_ = opened.db.Close()
				t.Fatal("invalid predecessor accepted")
			}
			after, err := os.ReadFile(path)
			if err != nil || !reflect.DeepEqual(before, after) {
				t.Fatal("failed migration changed state", err)
			}
		})
	}
}

func TestReassociationUnavailablePeerDifferentDeclaration(t *testing.T) {
	f := newRepairFixture(t)
	git(t, f.project, "add", "backlot.json")
	git(t, f.project, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "-qm", "Fixture")
	peer := filepath.Join(filepath.Dir(f.project), "peer")
	git(t, f.project, "worktree", "add", "-q", "-b", "peer", peer)
	document := f.manifest
	document.SharedServices = map[string]v1.SharedService{"peer-only": f.manifest.SharedServices["sql"]}
	document.Resources = map[string]v1.Resource{"db": {Kind: "allocation", SharedService: "peer-only"}}
	document.Scenes = map[string]v1.Scene{"peer": {Lifetime: v1.Persistent, Components: []string{"job"}, Resources: []string{"db"}}}
	raw, _ := json.Marshal(document)
	if err := os.WriteFile(filepath.Join(peer, "backlot.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	prepared, err := f.s.prepare(context.Background(), request(peer, f.config, "peer"))
	if err != nil {
		t.Fatal(err)
	}
	var origin sharedProjectIdentity
	if err := f.s.store.db.View(func(tx *bolt.Tx) error {
		return json.Unmarshal(tx.Bucket([]byte("shared_projects")).Get([]byte(f.request.ProjectID)), &origin)
	}); err != nil {
		t.Fatal(err)
	}
	record := sharedServiceRecord{ID: hash(origin.ID + "/peer-only"), Project: origin, Name: "peer-only", Declaration: document.SharedServices["peer-only"], Endpoint: "unix:///owned-unavailable-fixture.sock", AdminPassword: newID(), Status: "stopped", Server: ownedResource{Kind: "container", Name: "fixture-server", Token: newID()}, Network: ownedResource{Kind: "network", Name: "fixture-network", Token: newID()}, Volume: ownedResource{Kind: "volume", Name: "fixture-volume", Token: newID()}}
	record.Configuration = sharedDigest(record.Declaration, record.Endpoint)
	if err := f.s.store.saveShared(record); err != nil {
		t.Fatal(err)
	}
	allocation := sharedAllocation{ServiceID: record.ID, Request: v1.ManagerRequest{Version: v1.ManagerProtocolVersion, Operation: "allocate", AllocationID: newID(), Ownership: newID(), Database: "bl" + newID()[:32], Username: "bl" + newID()[:32], Password: newID()}}
	if err := f.s.store.saveSharedAllocations(prepared.Instance.ID, map[string]sharedAllocation{"db": allocation}); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(peer, peer+"-unavailable"); err != nil {
		t.Fatal(err)
	}
	f.move(t, "moved")
	preview := f.preview(t)
	if len(preview.Blockers) > 0 || len(preview.Instances) != 2 || len(preview.Allocations) != 1 {
		t.Fatal("requesting branch substituted for unavailable peer", preview.Blockers)
	}
	if err := f.apply(t, preview); err != nil {
		t.Fatal(err)
	}
	observed, err := f.s.store.inspect(prepared.Instance.ID)
	if err != nil || observed.Association.Checkout.Path != prepared.Instance.Checkout.Path {
		t.Fatal("peer binding redirected", err)
	}
}

func TestReassociationProtectedSecretPath(t *testing.T) {
	f := newRepairFixture(t)
	component := f.manifest.Components["job"]
	value := fixtureLiteral("/fixture/old-secret-location")
	value.Secret = true
	component.Environment = v1.Environment{Assign: map[string]v1.Value{"TOKEN": value}}
	f.manifest.Components["job"] = component
	f.write(t)
	f.captureSnapshot(t)
	value = fixtureLiteral("/fixture/new-secret-location")
	value.Secret = true
	component.Environment.Assign["TOKEN"] = value
	f.manifest.Components["job"] = component
	f.write(t)
	f.move(t, "moved")
	p := f.preview(t)
	if len(p.Blockers) == 0 {
		t.Fatal("protected path change accepted")
	}
	code(t, f.apply(t, p), "reassociation_blocked")
	raw, _ := json.Marshal(p)
	for _, secret := range []string{"/fixture/old-secret-location", "/fixture/new-secret-location"} {
		if strings.Contains(string(raw), secret) {
			t.Fatal("protected value disclosed")
		}
	}
}

func TestReassociationRevalidatesSourceBeforeCommit(t *testing.T) {
	f := newRepairFixture(t)
	f.move(t, "moved")
	p := f.preview(t)
	before := stateContents(t, f.s.store)
	f.s.store.sharedCheckpoint = func(point string) error {
		if point != "reassociation-source-validation" {
			return nil
		}
		path := filepath.Join(f.project, ".git")
		head, err := os.ReadFile(filepath.Join(path, "HEAD"))
		if err != nil {
			return err
		}
		if err := os.Rename(path, path+"-before"); err != nil {
			return err
		}
		if err := os.Mkdir(path, 0700); err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(path, "HEAD"), head, 0600)
	}
	code(t, f.apply(t, p), "checkout_changed")
	if !reflect.DeepEqual(before, stateContents(t, f.s.store)) {
		t.Fatal("unstable source changed daemon associations")
	}
}

func TestReassociationRejectsLinkedMetadataWithoutMutation(t *testing.T) {
	f := newRepairFixture(t)
	peer := f.preparePeer(t)
	query := f.request
	query.ProjectPath = peer.Checkout.Path
	query.CheckoutID = checkoutParent(peer)
	valid, err := f.s.reassociate(context.Background(), query)
	if err != nil || len(valid.Blockers) > 0 {
		t.Fatal(err, valid.Blockers)
	}
	before := stateContents(t, f.s.store)
	if err := os.Remove(filepath.Join(peer.Checkout.GitDirectory, "commondir")); err != nil {
		t.Fatal(err)
	}
	_, err = f.s.reassociate(context.Background(), query)
	code(t, err, "project_identity")
	query.Apply = true
	query.Preview = valid.Preview
	_, err = f.s.reassociate(context.Background(), query)
	code(t, err, "project_identity")
	if !reflect.DeepEqual(before, stateContents(t, f.s.store)) {
		t.Fatal("invalid linked metadata changed association")
	}
}

func TestReassociationReservesInactiveForeignOrigins(t *testing.T) {
	for _, collision := range []string{"path", "filesystem identity"} {
		t.Run(collision, func(t *testing.T) {
			f := newRepairFixture(t)
			second := filepath.Join(filepath.Dir(f.project), "second")
			if err := os.Mkdir(second, 0700); err != nil {
				t.Fatal(err)
			}
			git(t, second, "init", "-q", "-b", "main")
			data, err := os.ReadFile(filepath.Join(f.project, "backlot.json"))
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(second, "backlot.json"), data, 0600); err != nil {
				t.Fatal(err)
			}
			prepared, err := f.s.prepare(context.Background(), request(second, f.config, "dev"))
			if err != nil {
				t.Fatal(err)
			}
			source, err := plan.Locate(second)
			if err != nil {
				t.Fatal(err)
			}
			origin, err := identifyShared(source, f.manifest.Project)
			if err != nil {
				t.Fatal(err)
			}
			retainedGit := second + "-original-git"
			if err := os.Rename(filepath.Join(second, ".git"), retainedGit); err != nil {
				t.Fatal(err)
			}
			git(t, second, "init", "-q", "-b", "main")
			moved := second + "-moved"
			if err := os.Rename(second, moved); err != nil {
				t.Fatal(err)
			}
			peer := &repairFixture{s: f.s, request: f.request}
			peer.request.ProjectPath = moved
			peer.request.ProjectID = origin.ID
			peer.request.CheckoutID = checkoutParent(prepared.Instance)
			if err := peer.apply(t, peer.preview(t)); err != nil {
				t.Fatal(err)
			}
			if collision == "path" {
				f.move(t, "second")
			} else {
				if err := os.Rename(filepath.Join(f.project, ".git"), f.project+"-original-git"); err != nil {
					t.Fatal(err)
				}
				if err := os.Rename(retainedGit, filepath.Join(f.project, ".git")); err != nil {
					t.Fatal(err)
				}
			}
			before := stateContents(t, f.s.store)
			p := f.preview(t)
			if !strings.Contains(strings.Join(p.Blockers, "\n"), "retained shared-project origin") {
				t.Fatal("inactive allocation-free shared origin not reserved", p.Blockers)
			}
			if collision == "path" && !strings.Contains(strings.Join(p.Blockers, "\n"), "retained checkout origin") {
				t.Fatal("inactive checkout origin not reserved", p.Blockers)
			}
			code(t, f.apply(t, p), "reassociation_blocked")
			err = f.s.store.db.Update(func(tx *bolt.Tx) error {
				if err := put(tx, "project_bindings", f.request.ProjectID, projectBinding{Current: p.Current, Revision: newID()}); err != nil {
					return err
				}
				return validateBindings(tx)
			})
			code(t, err, "state_corrupt")
			if !reflect.DeepEqual(before, stateContents(t, f.s.store)) {
				t.Fatal("refusal changed immutable reservations or bindings")
			}
		})
	}
}

func TestReassociationMalformedSelectedLocationRefuses(t *testing.T) {
	f := newRepairFixture(t)
	if err := f.s.store.db.Update(func(tx *bolt.Tx) error {
		r, err := load(tx, f.instance.ID)
		if err != nil {
			return err
		}
		r.Instance.Plan.ManifestPath = "/outside-recorded-checkout/backlot.json"
		return put(tx, "instances", f.instance.ID, r)
	}); err != nil {
		t.Fatal(err)
	}
	before := stateContents(t, f.s.store)
	_, err := f.s.reassociate(context.Background(), f.request)
	code(t, err, "state_corrupt")
	if !reflect.DeepEqual(before, stateContents(t, f.s.store)) {
		t.Fatal("malformed selection refusal mutated state")
	}
}

func TestReassociationManifestRenameRefused(t *testing.T) {
	f := newRepairFixture(t)
	f.move(t, "moved")
	if err := os.Rename(filepath.Join(f.project, "backlot.json"), filepath.Join(f.project, "backlot.yaml")); err != nil {
		t.Fatal(err)
	}
	before := stateContents(t, f.s.store)
	p := f.preview(t)
	if !strings.Contains(strings.Join(p.Blockers, "\n"), "project/manifest association differs") {
		t.Fatal("relative manifest filename change was accepted", p.Blockers)
	}
	code(t, f.apply(t, p), "reassociation_blocked")
	if !reflect.DeepEqual(before, stateContents(t, f.s.store)) {
		t.Fatal("manifest relocation refusal mutated state")
	}
}

func TestReassociationR1MismatchedRetainedSelectors(t *testing.T) {
	f := newRepairFixture(t)
	other := filepath.Join(filepath.Dir(f.project), "independent")
	if err := os.Mkdir(other, 0700); err != nil {
		t.Fatal(err)
	}
	git(t, other, "init", "-q", "-b", "main")
	raw, err := os.ReadFile(filepath.Join(f.project, "backlot.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(other, "backlot.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	second, err := f.s.prepare(context.Background(), request(other, f.config, "dev"))
	if err != nil {
		t.Fatal(err)
	}
	source, err := plan.Locate(other)
	if err != nil {
		t.Fatal(err)
	}
	origin, err := identifyShared(source, f.manifest.Project)
	if err != nil {
		t.Fatal(err)
	}
	record := sharedServiceRecord{ID: hash(origin.ID + "/sql"), Project: origin, Name: "sql", Declaration: f.manifest.SharedServices["sql"], Endpoint: "unix:///owned-unavailable-fixture.sock", AdminPassword: newID(), Status: "stopped", Server: ownedResource{Kind: "container", Name: "fixture-server", Token: newID()}, Network: ownedResource{Kind: "network", Name: "fixture-network", Token: newID()}, Volume: ownedResource{Kind: "volume", Name: "fixture-volume", Token: newID()}}
	record.Configuration = sharedDigest(record.Declaration, record.Endpoint)
	if err := f.s.store.saveShared(record); err != nil {
		t.Fatal(err)
	}
	allocation := sharedAllocation{ServiceID: record.ID, Request: v1.ManagerRequest{Version: v1.ManagerProtocolVersion, Operation: "allocate", AllocationID: newID(), Ownership: newID(), Database: "bl" + newID()[:32], Username: "bl" + newID()[:32], Password: newID()}}
	if err := f.s.store.saveSharedAllocations(second.Instance.ID, map[string]sharedAllocation{"db": allocation}); err != nil {
		t.Fatal(err)
	}

	destination := filepath.Join(filepath.Dir(f.project), "destination")
	if err := os.Mkdir(destination, 0700); err != nil {
		t.Fatal(err)
	}
	git(t, destination, "init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(destination, "backlot.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	f.project = destination
	f.request.ProjectPath = destination
	f.request.CheckoutID = checkoutParent(second.Instance)
	p := f.preview(t)
	encoded, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{record.AdminPassword, allocation.Request.Password, allocation.Request.Ownership} {
		if strings.Contains(string(encoded), secret) {
			t.Fatal("preview exposed retained credential/capability")
		}
	}

	if len(p.Blockers) == 0 || len(p.Allocations) != 1 || p.Allocations[0].ServiceID != record.ID || p.Allocations[0].AllocationID != allocation.Request.AllocationID || len(p.Services) != 1 || p.Services[0].ProjectID != origin.ID {
		t.Fatal("cross-owner refusal omitted exact retained data", p.Blockers, p.Allocations, p.Services)
	}
	before := stateContents(t, f.s.store)
	code(t, f.apply(t, p), "reassociation_blocked")
	if !reflect.DeepEqual(before, stateContents(t, f.s.store)) {
		t.Fatal("cross-owner refusal mutated state")
	}
	err = f.s.store.db.Update(func(tx *bolt.Tx) error {
		b, err := readCheckoutBinding(tx, f.request.CheckoutID)
		if err != nil {
			return err
		}
		b.Shared[f.request.ProjectID] = ""
		if err := put(tx, "checkout_bindings", f.request.CheckoutID, b); err != nil {
			return err
		}
		return validateBindings(tx)
	})
	code(t, err, "state_corrupt")
	if !reflect.DeepEqual(before, stateContents(t, f.s.store)) {
		t.Fatal("invalid ownership publication was not rolled back")
	}

}
func TestReassociationR4DoctorUnverifiedBinding(t *testing.T) {
	f := newRepairFixture(t)
	f.move(t, "destination")
	if err := f.apply(t, f.preview(t)); err != nil {
		t.Fatal(err)
	}
	peerInstance := f.preparePeer(t)
	peer := peerInstance.Checkout.Path
	// Retain the legitimate unverified acknowledgement shape produced by pre-marker repair.
	if err := f.s.store.db.Update(func(tx *bolt.Tx) error {
		b, err := readCheckoutBinding(tx, checkoutParent(peerInstance))
		if err != nil {
			return err
		}
		b.Shared[f.request.ProjectID] = ""
		return put(tx, "checkout_bindings", checkoutParent(peerInstance), b)
	}); err != nil {
		t.Fatal(err)
	}
	var err error

	_, err = f.s.prepare(context.Background(), request(peer, f.config, "dev"))
	code(t, err, "project_unverified")
	r, err := f.s.doctor(context.Background(), v1.DoctorRequest{APIVersion: v1.Version, ProjectPath: peer, Scene: "dev", ConfigPath: f.config})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, c := range r.Checks {
		if c.Name == "shared_project_identity" {
			found = true
			if c.Status != v1.DoctorFailed || !strings.Contains(c.Message, "project_unverified") {
				t.Fatal("doctor hid shared verification blocker")
			}
		}
	}
	if !found {
		t.Fatal("shared identity check missing")
	}
	unregistered := filepath.Join(filepath.Dir(f.project), "unregistered")
	if err := os.Mkdir(unregistered, 0700); err != nil {
		t.Fatal(err)
	}
	git(t, unregistered, "init", "-q", "-b", "main")
	raw, err := os.ReadFile(filepath.Join(f.project, "backlot.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(unregistered, "backlot.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	before := stateContents(t, f.s.store)
	fresh, err := f.s.doctor(context.Background(), v1.DoctorRequest{APIVersion: v1.Version, ProjectPath: unregistered, Scene: "dev", ConfigPath: f.config})
	if err != nil {
		t.Fatal(err)
	}
	found = false
	for _, c := range fresh.Checks {
		if c.Name == "shared_project_identity" {
			found = true
			if c.Status != v1.DoctorOK {
				t.Fatal("genuinely unregistered identity reported as conflict", c)
			}
		}
	}
	if !found || !reflect.DeepEqual(before, stateContents(t, f.s.store)) {
		t.Fatal("unregistered identity diagnosis missing or mutated registration")
	}

}
func TestReassociationR2StartingPeerWithoutAllocation(t *testing.T) {
	f := newRepairFixture(t)
	second := f.preparePeer(t)
	attempt := newID()
	if _, err := f.s.store.acceptExecution(second.ID, runtimeRecord{Attempt: attempt}, &v1.ExecutionResult{Attempt: attempt, Components: []v1.ComponentResult{}}, false); err != nil {
		t.Fatal(err)
	}
	f.move(t, "destination")
	p := f.preview(t)
	included := false
	for _, i := range p.Instances {
		included = included || i.ID == second.ID
	}
	if !included || len(p.Blockers) == 0 {
		t.Fatal("allocation-free starting peer omitted", p.Blockers)
	}
	before := stateContents(t, f.s.store)
	code(t, f.apply(t, p), "reassociation_blocked")
	if !reflect.DeepEqual(before, stateContents(t, f.s.store)) {
		t.Fatal("starting peer refusal mutated state")
	}
}

func TestReassociationR2FirstAllocationAdmission(t *testing.T) {
	f := newRepairFixture(t)
	source, err := plan.Locate(f.project)
	if err != nil {
		t.Fatal(err)
	}
	captured, err := captureSource(source)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := f.s.store.snapshot(f.instance.ID)
	if err != nil {
		t.Fatal(err)
	}
	admitted, resume := make(chan struct{}), make(chan struct{})
	done := make(chan error, 1)
	joined := false
	t.Cleanup(func() {
		if !joined {
			close(resume)
			<-done
		}
	})
	stoppedBeforeProvider := errors.New("owned fixture stops before provider effects")
	f.s.store.sharedCheckpoint = func(point string) error {
		switch point {
		case "allocation-admission":
			close(admitted)
			<-resume
		case "server-intent":
			return stoppedBeforeProvider
		}
		return nil
	}
	go func() {
		done <- f.s.ensureSharedAllocations(context.Background(), f.instance.ID, snapshot, &execution{source: captured})
	}()
	select {
	case <-admitted:
	case err := <-done:
		joined = true
		t.Fatal("allocation never reached admission", err)
	case <-time.After(2 * time.Second):
		t.Fatal("allocation admission did not arrive")
	}
	f.move(t, "moved")
	p := f.preview(t)
	if len(p.Blockers) != 0 || len(p.Services) != 0 {
		t.Fatal("unexpected fixture state before first service intent", p.Blockers)
	}
	code(t, f.apply(t, p), "conflict")
	close(resume)
	err = <-done
	joined = true
	if !errors.Is(err, stoppedBeforeProvider) {
		t.Fatal("allocation passed fixture provider boundary", err)
	}
	code(t, f.apply(t, p), "stale_preview")
	if allocations, err := f.s.store.sharedAllocations(f.instance.ID); err != nil || len(allocations) != 0 {
		t.Fatal("provider-free admission created an allocation", err)
	}
}

func TestReassociationWorkingDirectoryIntegration(t *testing.T) {
	for _, scenario := range []struct {
		name, before, after string
		allowed             bool
	}{
		{"equivalent-default", "", ".", true},
		{"fresh-nondefault", "package", "package", false},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			f := newRepairFixture(t)
			if err := os.Mkdir(filepath.Join(f.project, "package"), 0700); err != nil {
				t.Fatal(err)
			}
			c := f.manifest.Components["job"]
			c.Policy = v1.FreshOnly
			c.Command.WorkingDirectory = scenario.before
			c.Initializes = []string{"db"}
			f.manifest.Components["job"] = c
			f.write(t)
			f.captureSnapshot(t)
			f.move(t, "moved")
			c.Command.WorkingDirectory = scenario.after
			f.manifest.Components["job"] = c
			f.write(t)
			before := stateContents(t, f.s.store)
			p := f.preview(t)
			if (len(p.Blockers) == 0) != scenario.allowed || !reflect.DeepEqual(before, stateContents(t, f.s.store)) {
				t.Fatal("effective cwd compatibility or read-only preview changed", p.Blockers)
			}
			if scenario.allowed {
				if err := f.apply(t, p); err != nil {
					t.Fatal(err)
				}
				if stateContents(t, f.s.store)["snapshots"][f.instance.ID] != before["snapshots"][f.instance.ID] {
					t.Fatal("repair rewrote recorded cwd instead of preserving launch history")
				}
			} else {
				if !strings.Contains(strings.Join(p.Blockers, "\n"), "nondefault working-directory relocation") {
					t.Fatal("cwd blocker category missing", p.Blockers)
				}
				code(t, f.apply(t, p), "reassociation_blocked")
				if !reflect.DeepEqual(before, stateContents(t, f.s.store)) {
					t.Fatal("cwd refusal mutated retained state")
				}
			}
		})
	}
}
