//go:build darwin || linux

package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	v1 "github.com/octacian/backlot/api/v1"
	"github.com/octacian/backlot/internal/client"
	"github.com/octacian/backlot/internal/plan"
	"github.com/octacian/backlot/internal/projectconfig"
	bolt "go.etcd.io/bbolt"
)

func TestCheckoutOnlyLegacyRepair(t *testing.T) {
	base := shortTemp(t)
	project := filepath.Join(base, "project")
	if err := os.Mkdir(project, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BACKLOT_PROJECT_CONFIG_DIR", filepath.Join(base, "registry"))
	m := v1.Manifest{Version: v1.ManifestVersion, Project: "checkout-only", Tools: map[string]string{"shell": "/bin/sh"}, Components: map[string]v1.Component{"job": {Kind: v1.Job, Runtime: v1.Native, Policy: v1.EachStart, Command: &v1.Command{Tool: "shell", Args: v1.StringArguments("-c", "exit 0")}}}, Scenes: map[string]v1.Scene{"dev": {Lifetime: v1.Persistent, Components: []string{"job"}}}}
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, "backlot.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(base, "state"), 0700); err != nil {
		t.Fatal(err)
	}
	state, err := openStore(filepath.Join(base, "state"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = state.db.Close() }()
	s := &service{store: state, lease: time.Minute, generation: newID()}
	prepared, err := s.prepare(context.Background(), request(project, "", "dev"))
	if err != nil {
		t.Fatal(err)
	}
	parent := checkoutParent(prepared.Instance)
	// Model retained pre-enrollment records, not a historical migration fixture.
	if err := state.db.Update(func(tx *bolt.Tx) error {
		b, err := readCheckoutBinding(tx, parent)
		if err != nil {
			return err
		}
		b.Current.Registration = v1.RegistrationIdentity{}
		if err := put(tx, "checkout_bindings", parent, b); err != nil {
			return err
		}
		var origin v1.CheckoutIdentity
		if err := json.Unmarshal(tx.Bucket([]byte("checkouts")).Get([]byte(parent)), &origin); err != nil {
			return err
		}
		origin.Registration = v1.RegistrationIdentity{}
		return put(tx, "checkouts", parent, origin)
	}); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(project, ".backlot-local")); err != nil {
		t.Fatal(err)
	}
	old := project + "-old"
	if err := os.Rename(project, old); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("cp", "-R", old, project).CombinedOutput(); err != nil {
		t.Fatalf("owned replacement: %v %s", err, out)
	}
	_, err = s.prepare(context.Background(), request(project, "", "dev"))
	code(t, err, "checkout_replaced")
	q := v1.ReassociationRequest{APIVersion: v1.Version, ProjectPath: project, CheckoutID: parent, RegistryRoot: filepath.Join(base, "registry")}
	p, err := s.reassociate(context.Background(), q)
	if err != nil || len(p.Blockers) > 0 {
		t.Fatal("checkout-only preview blocked", err, p.Blockers)
	}
	q.Apply = true
	q.Preview = p.Preview
	applied, err := s.reassociate(context.Background(), q)
	if err != nil || !applied.Applied {
		t.Fatal("checkout-only apply failed", err)
	}
	repeated, err := s.prepare(context.Background(), request(project, "", "dev"))
	if err != nil || repeated.Instance.ID != prepared.Instance.ID {
		t.Fatal("repaired checkout lost retained instance", err)
	}
	if err := state.db.View(func(tx *bolt.Tx) error {
		if tx.Bucket([]byte("shared_projects")).Stats().KeyN != 0 {
			t.Fatal("checkout repair fabricated shared owner")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestRegistrationStagingRequiresExplicitResume(t *testing.T) {
	for _, faultPoint := range []string{"registration-staged", "registration-markers-published"} {
		t.Run(faultPoint, func(t *testing.T) {
			f := newRepairFixture(t)
			setup, err := plan.OpenSetup(v1.ProjectSetupRequest{ProjectPath: f.project, ConfigPath: f.config, Edit: true})
			if err != nil {
				t.Fatal(err)
			}
			originalPrivate, err := setup.Save(context.Background(), v1.PrivateConfig{Version: v1.ManifestVersion, Tools: map[string]string{"shell": "/bin/sh"}}, nil, nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			privateBytes, err := os.ReadFile(originalPrivate.PrivatePath)
			if err != nil {
				t.Fatal(err)
			}
			originalBinding := storedBytes(t, f.s.store, "checkout_bindings", f.request.CheckoutID)
			originalOwner := storedBytes(t, f.s.store, "shared_projects", f.request.ProjectID)
			originalRegistration := f.instance.Checkout.Registration
			copyPath := filepath.Join(filepath.Dir(f.project), "copy")
			if out, err := exec.Command("cp", "-R", f.project, copyPath).CombinedOutput(); err != nil {
				t.Fatalf("copy: %v %s", err, out)
			}
			q := f.request
			q.ProjectPath = copyPath
			q.FreshIdentityAt = copyPath
			q.CheckoutID = ""
			q.ProjectID = ""
			p, err := f.s.reassociate(context.Background(), q)
			if err != nil || len(p.Blockers) > 0 {
				t.Fatal(err, p.Blockers)
			}
			f.s.store.sharedCheckpoint = func(point string) error {
				if point == faultPoint {
					return errors.New("owned staging fault")
				}
				return nil
			}
			q.Apply = true
			q.Preview = p.Preview
			if _, err := f.s.reassociate(context.Background(), q); err == nil {
				t.Fatal("staging fault ignored")
			}
			f.s.store.sharedCheckpoint = nil
			_, err = f.s.prepare(context.Background(), request(copyPath, f.config, "dev"))
			code(t, err, "project_registration")
			if _, err := f.s.prepare(context.Background(), request(f.project, f.config, "dev")); err != nil {
				t.Fatal("copy staging fenced untouched original", err)
			}
			if err := f.s.store.db.View(func(tx *bolt.Tx) error {
				return tx.Bucket([]byte("checkout_bindings")).ForEach(func(k, _ []byte) error {
					b, err := readCheckoutBinding(tx, string(k))
					if err == nil && b.PendingPath != "" {
						q.CheckoutID = string(k)
					}
					return err
				})
			}); err != nil {
				t.Fatal(err)
			}
			q.Apply = false
			q.Preview = ""
			p, err = f.s.reassociate(context.Background(), q)
			if err != nil || len(p.Blockers) > 0 {
				t.Fatal(err, p.Blockers)
			}
			q.Apply = true
			q.Preview = p.Preview
			if _, err := f.s.reassociate(context.Background(), q); err != nil {
				t.Fatal("explicit exact-owner resume failed", err)
			}
			resumed, err := f.s.prepare(context.Background(), request(copyPath, f.config, "dev"))
			if err != nil || checkoutParent(resumed.Instance) != q.CheckoutID {
				t.Fatal("resumed copy lost exact staged owner", err)
			}
			original, err := identify(f.project)
			if err != nil || original.Registration != originalRegistration {
				t.Fatal("copy repair changed original markers", err)
			}
			if resumed.Instance.Association.Checkout.Registration.CommonID == originalRegistration.CommonID {
				t.Fatal("copy retained conflicting common role")
			}
			if !reflect.DeepEqual(originalBinding, storedBytes(t, f.s.store, "checkout_bindings", f.request.CheckoutID)) || !reflect.DeepEqual(originalOwner, storedBytes(t, f.s.store, "shared_projects", f.request.ProjectID)) {
				t.Fatal("copy repair changed original authority")
			}
			inspected, err := plan.OpenSetup(v1.ProjectSetupRequest{ProjectPath: f.project, ConfigPath: f.config})
			if err != nil {
				t.Fatal(err)
			}
			afterPrivate := inspected.Inspect()
			afterBytes, err := os.ReadFile(originalPrivate.PrivatePath)
			if err != nil || afterPrivate.Association != originalPrivate.Association || afterPrivate.PrivatePath != originalPrivate.PrivatePath || !reflect.DeepEqual(privateBytes, afterBytes) {
				t.Fatal("copy repair changed original private generation/values", err)
			}

		})
	}
}

func TestLinkedCommonReplacementIsOperationLocal(t *testing.T) {
	f := newRepairFixture(t)
	peer := f.preparePeer(t)
	source, err := plan.Locate(filepath.Dir(peer.Plan.ManifestPath))
	if err != nil {
		t.Fatal(err)
	}
	captured, err := captureSource(source)
	if err != nil {
		t.Fatal(err)
	}
	common := captured.registrationLocation.Common
	old := common + "-old"
	if err := os.Rename(common, old); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(common, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(old, "worktrees"), filepath.Join(common, "worktrees")); err != nil {
		t.Fatal(err)
	}
	marker, err := os.ReadFile(filepath.Join(old, "backlot-common.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(common, "backlot-common.json"), marker, 0600); err != nil {
		t.Fatal(err)
	}
	current, err := captureSource(source)
	if err != nil {
		t.Fatal(err)
	}
	if current.checkout.ID != captured.checkout.ID {
		t.Fatal("fixture changed checkout/admin rather than only common object")
	}
	if captured.verify() == nil {
		t.Fatal("same-token common replacement escaped physical source guard")
	}
}

func TestRegistrationSamePathContinuityWithoutPeerOrRevisionChurn(t *testing.T) {
	for name, legacy := range map[string]bool{"registered": false, "legacy-enrolled": true} {
		t.Run(name, func(t *testing.T) { testRegistrationSamePathContinuity(t, legacy) })
	}
}

func testRegistrationSamePathContinuity(t *testing.T, legacy bool) {
	t.Helper()
	f := newRepairFixture(t)
	if legacy {
		parent := checkoutParent(f.instance)
		if err := f.s.store.db.Update(func(tx *bolt.Tx) error {
			b, err := readCheckoutBinding(tx, parent)
			if err != nil {
				return err
			}
			b.Current.Registration = v1.RegistrationIdentity{}
			if err := put(tx, "checkout_bindings", parent, b); err != nil {
				return err
			}
			if err := put(tx, "checkouts", parent, b.Current); err != nil {
				return err
			}
			r, err := load(tx, f.instance.ID)
			if err != nil {
				return err
			}
			r.Instance.Checkout.Registration = v1.RegistrationIdentity{}
			return put(tx, "instances", f.instance.ID, r)
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := f.s.prepare(context.Background(), request(f.project, f.config, "dev")); err != nil {
			t.Fatal("exact legacy enrollment failed", err)
		}
		if err := f.s.store.db.View(func(tx *bolt.Tx) error {
			b, err := readCheckoutBinding(tx, parent)
			var origin v1.CheckoutIdentity
			if err != nil || json.Unmarshal(tx.Bucket([]byte("checkouts")).Get([]byte(parent)), &origin) != nil || b.Current.Registration.CheckoutID == "" || origin.Registration.CheckoutID != "" {
				t.Fatal("legacy enrollment must preserve the original unregistered checkout", err)
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	peer := f.preparePeer(t)
	f.manifest.Inputs = map[string]v1.Input{"token": {Secret: true}}
	f.write(t)
	fixtureInputs(t, f.project, f.config, map[string]string{"token": "owned-continuity-value"})
	source, err := plan.Locate(f.project)
	if err != nil {
		t.Fatal(err)
	}
	beforePrivate, err := projectconfig.Load(source.ProjectPath, source.CheckoutPath, source.ManifestPath, f.manifest, false, false)
	if err != nil {
		t.Fatal(err)
	}
	before := stateContents(t, f.s.store)
	if err := os.Rename(peer.Checkout.Path, peer.Checkout.Path+"-unavailable"); err != nil {
		t.Fatal(err)
	}
	old := f.project + "-old"
	if err := os.Rename(f.project, old); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("cp", "-R", old, f.project).CombinedOutput(); err != nil {
		t.Fatalf("owned same-path restoration: %v %s", err, out)
	}
	current, err := identify(f.project)
	if err != nil {
		t.Fatal(err)
	}
	if current.ID == f.instance.Checkout.ID {
		t.Fatal("fixture did not change physical observation")
	}
	result, err := f.s.prepare(context.Background(), request(f.project, f.config, "dev"))
	if err != nil || result.Instance.ID != f.instance.ID {
		t.Fatal("same-path marker continuity failed", err)
	}
	after := stateContents(t, f.s.store)
	for _, bucket := range []string{"checkout_bindings", "project_bindings", "checkouts", "shared_projects", "shared_allocations", "snapshots", "secrets"} {
		if !reflect.DeepEqual(before[bucket], after[bucket]) {
			t.Fatal("recognition rewrote ownership, revision or provenance", bucket)
		}
	}
	afterPrivate, err := projectconfig.Load(source.ProjectPath, source.CheckoutPath, source.ManifestPath, f.manifest, false, false)
	if err != nil || afterPrivate.Provenance != beforePrivate.Provenance || afterPrivate.Config.Inputs["token"] != "owned-continuity-value" {
		t.Fatal("same-path private continuity changed generation", err)
	}
	f.manifest.Scenes["inspect"] = v1.Scene{Lifetime: v1.Disposable, Components: []string{"job"}, Resources: []string{"db"}, TerminalJob: "job"}
	f.write(t)
	fresh, err := f.s.prepare(context.Background(), request(f.project, f.config, "inspect"))
	if err != nil {
		t.Fatal("new attempt after physical change failed", err)
	}
	if fresh.Instance.Checkout.ID != current.ID || checkoutParent(fresh.Instance) != checkoutParent(f.instance) {
		t.Fatal("new attempt lost physical provenance or stable parent")
	}
	if err := f.s.store.db.Close(); err != nil {
		t.Fatal(err)
	}
	cli, stop := startFixture(t, f.s.directory, time.Minute)
	target := v1.SceneRequest{APIVersion: v1.Version, Target: v1.SceneTarget{CWD: f.project, Scene: "dev"}}
	status, err := cli.SceneStatus(context.Background(), target)
	if err != nil || status.Result == nil || status.Result.Instance.ID != f.instance.ID {
		t.Fatal("registered scene status lost the original instance", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	snapshots := 0
	err = cli.FollowScene(ctx, target, client.SceneFollowOptions{Interval: 100 * time.Millisecond, Duration: time.Second}, func(record v1.SceneFollowRecord) error {
		if record.Type == "snapshot" {
			snapshots++
			if record.Snapshot.Result == nil || record.Snapshot.Result.Instance.ID != f.instance.ID {
				t.Fatal("registered scene follow retargeted the original instance")
			}
			cancel()
		}
		return nil
	})
	if !errors.Is(err, context.Canceled) || snapshots != 1 {
		t.Fatal("registered scene follow failed", err, snapshots)
	}
	stop()
	f.s.store, err = openStore(f.s.directory)
	if err != nil {
		t.Fatal("accepted marker continuity prevented daemon restart", err)
	}
	if _, err := f.s.store.inspect(fresh.Instance.ID); err != nil {
		t.Fatal("accepted attempt unreadable after restart", err)
	}
}

func TestRegistrationOriginalCompatibility(t *testing.T) {
	for _, change := range []string{"valid", "declaration", "provider", "initializer", "private", "project"} {
		t.Run(change, func(t *testing.T) {
			f := newRepairFixture(t)
			if change == "private" {
				f.manifest.Inputs = map[string]v1.Input{"token": {Secret: true}}
				job := f.manifest.Components["job"]
				job.Environment = v1.Environment{Assign: map[string]v1.Value{"TOKEN": fixtureRef("input", "token", "")}}
				f.manifest.Components["job"] = job
				f.write(t)
				setup, err := plan.OpenSetup(v1.ProjectSetupRequest{ProjectPath: f.project, ConfigPath: f.config, Edit: true})
				if err != nil {
					t.Fatal(err)
				}
				if _, err := setup.Save(context.Background(), v1.PrivateConfig{Version: v1.ManifestVersion, Inputs: map[string]string{"token": "owned-original"}}, nil, nil, nil); err != nil {
					t.Fatal(err)
				}
			}
			f.captureSnapshot(t)
			copyPath := filepath.Join(filepath.Dir(f.project), "copy")
			if out, err := exec.Command("cp", "-R", f.project, copyPath).CombinedOutput(); err != nil {
				t.Fatalf("copy: %v %s", err, out)
			}
			switch change {
			case "project":
				f.manifest.Project = "changed-project"
				f.write(t)
			case "declaration":
				decl := f.manifest.SharedServices["sql"]
				decl.Image = "mariadb:11.5"
				f.manifest.SharedServices["sql"] = decl
				f.write(t)
			case "provider":
				raw, _ := json.Marshal(v1.MachineConfig{Version: v1.ManifestVersion, Docker: &v1.DockerConfig{Endpoint: "unix:///owned-changed-provider.sock"}})
				if err := os.WriteFile(f.config, raw, 0600); err != nil {
					t.Fatal(err)
				}
			case "initializer":
				job := f.manifest.Components["job"]
				job.Policy = v1.FreshOnly
				job.Initializes = []string{"db"}
				f.manifest.Components["job"] = job
				f.write(t)
			case "private":
				setup, err := plan.OpenSetup(v1.ProjectSetupRequest{ProjectPath: f.project, ConfigPath: f.config, Edit: true})
				if err != nil {
					t.Fatal(err)
				}
				if _, err := setup.Save(context.Background(), v1.PrivateConfig{Version: v1.ManifestVersion, Inputs: map[string]string{"token": "owned-changed"}}, nil, nil, nil); err != nil {
					t.Fatal(err)
				}
			}
			before := stateContents(t, f.s.store)
			markers, err := identify(f.project)
			if err != nil {
				t.Fatal(err)
			}
			ordinary := f.preview(t)
			q := f.request
			q.ProjectPath = copyPath
			q.FreshIdentityAt = f.project
			duplicate, err := f.s.reassociate(context.Background(), q)
			if err != nil {
				t.Fatal(err)
			}
			if (len(ordinary.Blockers) == 0) != (change == "valid") || (len(duplicate.Blockers) == 0) != (change == "valid") {
				t.Fatal("repair policies differ", ordinary.Blockers, duplicate.Blockers)
			}
			if change != "valid" {
				q.Apply = true
				q.Preview = duplicate.Preview
				_, err = f.s.reassociate(context.Background(), q)
				code(t, err, "reassociation_blocked")
				after, err := identify(f.project)
				if err != nil || after != markers || !reflect.DeepEqual(before, stateContents(t, f.s.store)) {
					t.Fatal("refused original repair changed markers/bindings", err)
				}
			}
			q = f.request
			q.ProjectPath = copyPath
			q.FreshIdentityAt = copyPath
			q.CheckoutID = ""
			q.ProjectID = ""
			isolated, err := f.s.reassociate(context.Background(), q)
			if err != nil || len(isolated.Blockers) != 0 {
				t.Fatal("copy imported original compatibility/private state", err, isolated.Blockers)
			}
		})
	}
}

func TestRegistrationPreviewReaderLockOrder(t *testing.T) {
	f := newRepairFixture(t)
	reached, resume := make(chan struct{}), make(chan struct{})
	f.s.store.sharedCheckpoint = func(point string) error {
		if point == "registration-consumers-preview" {
			close(reached)
			<-resume
		}
		return nil
	}
	q := f.request
	q.FreshIdentityAt = f.project
	previewDone := make(chan error, 1)
	go func() { _, err := f.s.rotationPreview(q); previewDone <- err }()
	<-reached
	f.s.mu.Lock()
	writeDone := make(chan error, 1)
	go func() {
		writeDone <- f.s.store.db.Update(func(tx *bolt.Tx) error {
			return tx.Bucket([]byte("meta")).Put([]byte("owned-lock-order-probe"), make([]byte, 4<<20))
		})
	}()
	close(resume)
	var writerErr error
	timedOut := false
	select {
	case writerErr = <-writeDone:
	case <-time.After(5 * time.Second):
		timedOut = true
	}
	f.s.mu.Unlock()
	if timedOut {
		writerErr = <-writeDone
	}
	if err := <-previewDone; err != nil {
		t.Fatal(err)
	}
	if timedOut || writerErr != nil {
		t.Fatal("preview reader blocked writer behind service mutex", writerErr)
	}
	f.s.store.sharedCheckpoint = nil
	done := make(chan struct{})
	f.s.executions = map[string]*execution{f.instance.ID: {done: done}}
	preview, err := f.s.rotationPreview(q)
	if err != nil || !strings.Contains(strings.Join(preview.result.Blockers, " "), "must be joined") {
		t.Fatal("unjoined owner lost its fence", err)
	}
	close(done)
	preview, err = f.s.rotationPreview(q)
	if err != nil || len(preview.result.Blockers) != 0 {
		t.Fatal("joined owner remained fenced", err, preview.result.Blockers)
	}
	f.s.expiryWorkers = map[string]chan struct{}{f.instance.ID: nil}
	preview, err = f.s.rotationPreview(q)
	if err != nil || len(preview.result.Blockers) != 0 {
		t.Fatal("absent expiry owner was fenced", err)
	}
	f.s.expiryWorkers[f.instance.ID] = make(chan struct{})
	preview, err = f.s.rotationPreview(q)
	if err != nil || !strings.Contains(strings.Join(preview.result.Blockers, " "), "must be joined") {
		t.Fatal("expiry owner lost its fence", err)
	}
}

func TestRegistrationPrivateMissingRoleWithEmptyStore(t *testing.T) {
	f := newRepairFixture(t)
	setup, err := plan.OpenSetup(v1.ProjectSetupRequest{ProjectPath: f.project, ConfigPath: f.config, Edit: true})
	if err != nil {
		t.Fatal(err)
	}
	saved, err := setup.Save(context.Background(), v1.PrivateConfig{Version: v1.ManifestVersion, Tools: map[string]string{"shell": "/bin/sh"}}, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	privateBefore, err := os.ReadFile(saved.PrivatePath)
	if err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(f.project, ".git", "backlot-checkout.json")
	if err := os.Remove(marker); err != nil {
		t.Fatal(err)
	}
	_, err = plan.Resolve(v1.PlanRequest{ProjectPath: f.project, ConfigPath: f.config, Scene: "dev"})
	code(t, err, "project_registration")
	state, err := openStore(shortTemp(t))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := state.db.Close(); err != nil {
			t.Error(err)
		}
	}()
	empty := &service{store: state, lease: time.Minute, generation: newID()}
	_, err = empty.prepare(context.Background(), request(f.project, f.config, "dev"))
	code(t, err, "project_registration")
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("known missing checkout marker regenerated", err)
	}
	after, err := os.ReadFile(saved.PrivatePath)
	if err != nil || !reflect.DeepEqual(privateBefore, after) {
		t.Fatal("refusal changed private generation", err)
	}
	if err := state.db.View(func(tx *bolt.Tx) error {
		if tx.Bucket([]byte("instances")).Stats().KeyN != 0 || tx.Bucket([]byte("checkout_bindings")).Stats().KeyN != 0 {
			t.Fatal("refusal published daemon bindings")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestRegistrationOriginalPrivateFreshness(t *testing.T) {
	f := newRepairFixture(t)
	copyPath := filepath.Join(filepath.Dir(f.project), "copy")
	if out, err := exec.Command("cp", "-R", f.project, copyPath).CombinedOutput(); err != nil {
		t.Fatalf("copy: %v %s", err, out)
	}
	q := f.request
	q.ProjectPath = copyPath
	q.FreshIdentityAt = f.project
	preview, err := f.s.reassociate(context.Background(), q)
	if err != nil || len(preview.Blockers) != 0 {
		t.Fatal(err, preview.Blockers)
	}
	setup, err := plan.OpenSetup(v1.ProjectSetupRequest{ProjectPath: f.project, ConfigPath: f.config, Edit: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := setup.Save(context.Background(), v1.PrivateConfig{Version: v1.ManifestVersion, Tools: map[string]string{"shell": "/bin/sh"}}, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	before := stateContents(t, f.s.store)
	markers, err := identify(f.project)
	if err != nil {
		t.Fatal(err)
	}
	q.Apply = true
	q.Preview = preview.Preview
	_, err = f.s.reassociate(context.Background(), q)
	code(t, err, "stale_preview")
	after, err := identify(f.project)
	if err != nil || after != markers || !reflect.DeepEqual(before, stateContents(t, f.s.store)) {
		t.Fatal("stale private generation changed markers/bindings", err)
	}
}
