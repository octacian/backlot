//go:build darwin || linux

package daemon

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	v1 "github.com/octacian/backlot/api/v1"
)

func TestDoctorSceneDefersRetainedReadiness(t *testing.T) {
	base := shortTemp(t)
	base, err := filepath.EvalSymlinks(base)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("BACKLOT_PROJECT_CONFIG_DIR", filepath.Join(base, "registry"))
	project := filepath.Join(base, "main")
	if err := os.Mkdir(project, 0700); err != nil {
		t.Fatal(err)
	}
	git(t, project, "init", "-q", "-b", "main")
	m := v1.Manifest{Version: v1.ManifestVersion, Project: "doctor-scope", Tools: map[string]string{"shell": "/bin/sh"}, Components: map[string]v1.Component{"browser": {Kind: v1.Job, Runtime: v1.Native, Policy: v1.EachStart, Command: &v1.Command{Tool: "shell", Args: v1.StringArguments("-c", "exit 0")}, Environment: v1.Environment{Assign: map[string]v1.Value{"TOKEN": {Literal: ptrDoctorString("fixture-old"), Secret: true}}}}}, Scenes: map[string]v1.Scene{"dev": {Lifetime: v1.Persistent, Components: []string{"browser"}}}}
	m.Resources = map[string]v1.Resource{"data": {Kind: "directory"}}
	scene := m.Scenes["dev"]
	scene.Resources = []string{"data"}
	m.Scenes["dev"] = scene
	writeDoctorJSON(t, filepath.Join(project, "backlot.json"), m)
	git(t, project, "add", "backlot.json")
	git(t, project, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "-qm", "Fixture")
	peer := filepath.Join(base, "peer")
	git(t, project, "worktree", "add", "-q", "-b", "peer", peer)
	config := filepath.Join(base, "machine.json")
	writeDoctorJSON(t, config, v1.MachineConfig{Version: v1.ManifestVersion})
	stateDir := filepath.Join(base, "state")
	if err := os.Mkdir(stateDir, 0700); err != nil {
		t.Fatal(err)
	}
	state, err := openStore(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := state.db.Close(); err != nil {
			t.Error(err)
		}
	})
	s := &service{store: state, directory: stateDir, generation: newID()}
	prepared, err := s.prepare(context.Background(), request(peer, config, "dev"))
	if err != nil {
		t.Fatal(err)
	}
	f := &repairFixture{s: s, project: peer, config: config, instance: prepared.Instance, manifest: m, request: v1.ReassociationRequest{APIVersion: v1.Version, ProjectPath: peer, ConfigPath: config, RegistryRoot: filepath.Join(base, "registry"), CheckoutID: checkoutParent(prepared.Instance)}}
	observe := func(label string, blocked bool) {
		t.Helper()
		before := stateContents(t, state)
		d, err := s.doctor(context.Background(), v1.DoctorRequest{APIVersion: v1.Version, Scene: "dev", ProjectPath: f.project, ConfigPath: config})
		if err != nil || d.Validate() != nil {
			t.Fatal(err, d)
		}
		assertDoctorSceneLimits(t, d)
		p := f.preview(t)
		if !d.Healthy || (len(p.Blockers) > 0) != blocked {
			t.Fatalf("%s: healthy=%v blockers=%v checks=%v", label, d.Healthy, p.Blockers, d.Checks)
		}
		if !reflect.DeepEqual(before, stateContents(t, state)) {
			t.Fatal("diagnostics mutated store")
		}
		payload, err := json.Marshal(struct {
			Doctor   v1.DoctorResponse
			Blockers []string
		}{d, p.Blockers})
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(payload), "fixture-old") || strings.Contains(string(payload), "fixture-new") {
			t.Fatal("secret leaked")
		}
	}
	observe("control: prepared linked checkout", false)
	c := m.Components["browser"]
	c.Environment.Assign["TOKEN"] = v1.Value{Literal: ptrDoctorString("fixture-new"), Secret: true}
	m.Components["browser"] = c
	writeDoctorJSON(t, filepath.Join(peer, "backlot.json"), m)
	observe("protected input drift", true)
	c.Environment.Assign["TOKEN"] = v1.Value{Literal: ptrDoctorString("fixture-old"), Secret: true}
	m.Components["browser"] = c
	writeDoctorJSON(t, filepath.Join(peer, "backlot.json"), m)
	attempt := newID()
	_, err = state.execution(prepared.Instance.ID, v1.Interrupted, &v1.ExecutionResult{Attempt: attempt, Outcome: v1.Interrupted, Components: []v1.ComponentResult{{Name: "browser", Status: "unknown"}}, CollectionFailure: "fixture collection gap", CleanupFailure: "fixture uncertain cleanup", NativeCleanup: []v1.NativeCleanupResult{{Component: "browser", Attempt: attempt, Confidence: v1.CleanupAuthorityLost, Reason: "guardian_authority_lost", Detail: "Synthetic retained unresolved cleanup; no process launched."}}})
	if err != nil {
		t.Fatal(err)
	}
	observe("retained interrupted consumer", true)
	instanceDoctor, err := s.doctor(context.Background(), v1.DoctorRequest{APIVersion: v1.Version, InstanceID: prepared.Instance.ID})
	if err != nil || instanceDoctor.Healthy {
		t.Fatal("instance doctor should expose retained cleanup failure", err, instanceDoctor)
	}
	c.Environment.Assign["TOKEN"] = v1.Value{Literal: ptrDoctorString("fixture-new"), Secret: true}
	m.Components["browser"] = c
	writeDoctorJSON(t, filepath.Join(peer, "backlot.json"), m)
	observe("interrupted consumer and protected input drift", true)
}
