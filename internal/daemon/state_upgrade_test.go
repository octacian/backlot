//go:build darwin || linux

package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	v1 "github.com/octacian/backlot/api/v1"
	"github.com/octacian/backlot/internal/plan"
	bolt "go.etcd.io/bbolt"
)

func stateContents(t *testing.T, s *store) map[string]map[string]string {
	t.Helper()
	result := map[string]map[string]string{}
	if err := s.db.View(func(tx *bolt.Tx) error {
		return tx.ForEach(func(name []byte, bucket *bolt.Bucket) error {
			contents := map[string]string{}
			result[string(name)] = contents
			return bucket.ForEach(func(k, v []byte) error { contents[string(k)] = string(v); return nil })
		})
	}); err != nil {
		t.Fatal(err)
	}
	return result
}

func TestLegacySnapshotAdoptionIsAtomicWithAcceptance(t *testing.T) {
	project := shortTemp(t)
	config := writeFixture(t, project)
	state, err := openStore(shortTemp(t))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := state.db.Close(); err != nil {
			t.Error(err)
		}
	}()
	svc := &service{store: state, lease: time.Minute}
	prepared, err := svc.prepare(context.Background(), request(project, config, "dev"))
	if err != nil {
		t.Fatal(err)
	}
	id := prepared.Instance.ID
	snap, err := state.snapshot(id)
	if err != nil {
		t.Fatal(err)
	}
	current := snap
	snap.PrivateProvenance, snap.Plan.EffectiveConfigDigest = nil, ""
	if err := state.replaceSnapshot(id, snap); err != nil {
		t.Fatal(err)
	}
	before := stateContents(t, state)
	if _, err := state.execution(id, v1.Failed, nil); err != nil {
		t.Fatal(err)
	}
	oldSnapshot, oldSecrets := storedBytes(t, state, "snapshots", id), storedBytes(t, state, "secrets", id)
	_, err = state.acceptExecutionSnapshot(id, runtimeRecord{Attempt: newID()}, &v1.ExecutionResult{Attempt: newID()}, false, &current)
	code(t, err, "restart_required")
	if !bytes.Equal(oldSnapshot, storedBytes(t, state, "snapshots", id)) || !bytes.Equal(oldSecrets, storedBytes(t, state, "secrets", id)) {
		t.Fatal("rejected acceptance published current provenance")
	}
	if err := state.db.Update(func(tx *bolt.Tx) error {
		return tx.Bucket([]byte("instances")).Put([]byte(id), []byte(before["instances"][id]))
	}); err != nil {
		t.Fatal(err)
	}
	accepted, err := state.acceptExecutionSnapshot(id, runtimeRecord{Attempt: newID()}, &v1.ExecutionResult{Attempt: newID()}, false, &current)
	if err != nil || accepted.Plan.EffectiveConfigDigest != current.Plan.EffectiveConfigDigest {
		t.Fatal("current plan not accepted", err)
	}
	adopted, err := state.snapshot(id)
	if err != nil || adopted.PrivateProvenance == nil || adopted.Plan.EffectiveConfigDigest == "" {
		t.Fatal("current provenance not adopted", err)
	}
}

func TestLegacyConfigurationDiagnosticsRemainUnrecorded(t *testing.T) {
	project := shortTemp(t)
	config := writeFixture(t, project)
	source, err := plan.Locate(project)
	if err != nil {
		t.Fatal(err)
	}
	launch, err := plan.Prepare(source, v1.PlanRequest{Scene: "dev", ConfigPath: config})
	if err != nil {
		t.Fatal(err)
	}
	launch.PrivateProvenance, launch.Plan.EffectiveConfigDigest = nil, ""
	var result v1.DoctorResponse
	addPrivatePath(&result, "launch_private_config", launch)
	checkCurrentComposition(&result, launch)
	found := map[string]string{}
	for _, path := range result.Paths {
		found[path.Name] = path.State
	}
	if found["launch_private_config"] != "unrecorded" || found["launch_effective_configuration"] != "unrecorded" || found["current_effective_configuration"] != "present" {
		t.Fatal("legacy/current provenance conflated", found)
	}
	if launch.PrivateProvenance != nil || launch.Plan.EffectiveConfigDigest != "" {
		t.Fatal("diagnostic backfilled launch provenance")
	}
}

func TestLegacyActivationSensitiveFenceBeforeEffects(t *testing.T) {
	for _, legacy := range []bool{true, false} {
		for _, status := range []v1.InstanceStatus{v1.Prepared, v1.Stopped, v1.Failed} {
			for _, named := range []bool{true, false} {
				t.Run(string(status)+map[bool]string{true: "/legacy", false: "/current"}[legacy]+map[bool]string{true: "/scene", false: "/run"}[named], func(t *testing.T) {
					project := shortTemp(t)
					config := writeFixture(t, project)
					manifestPath := filepath.Join(project, "backlot.json")
					var manifest v1.Manifest
					data, err := os.ReadFile(manifestPath)
					if err != nil {
						t.Fatal(err)
					}
					if err := json.Unmarshal(data, &manifest); err != nil {
						t.Fatal(err)
					}
					manifest.Resources = map[string]v1.Resource{"data": {Kind: "directory"}}
					scene := manifest.Scenes["dev"]
					scene.Resources = []string{"data"}
					manifest.Scenes["dev"] = scene
					data, err = json.Marshal(manifest)
					if err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(manifestPath, data, 0600); err != nil {
						t.Fatal(err)
					}
					state, err := openStore(shortTemp(t))
					if err != nil {
						t.Fatal(err)
					}
					defer func() {
						if err := state.db.Close(); err != nil {
							t.Error(err)
						}
					}()
					svc := &service{store: state, lease: time.Minute}
					prepared, err := svc.prepare(context.Background(), request(project, config, "dev"))
					if err != nil {
						t.Fatal(err)
					}
					id := prepared.Instance.ID
					snap, err := state.snapshot(id)
					if err != nil {
						t.Fatal(err)
					}
					if legacy {
						snap.PrivateProvenance, snap.Plan.EffectiveConfigDigest = nil, ""
						if err := state.replaceSnapshot(id, snap); err != nil {
							t.Fatal(err)
						}
					}
					if _, err := state.execution(id, status, nil); err != nil {
						t.Fatal(err)
					}
					before := storedBytes(t, state, "snapshots", id)
					fixtureInputs(t, project, config, map[string]string{"password": "changed-sensitive-value"})
					// Fail before execution effects even if a guard regresses: this checkpoint
					// runs after resolution/preparation but before snapshot selection/actuation.
					svc.beforeExecution = func(string) { svc.closing = true }
					run := v1.RunRequest{APIVersion: v1.Version, Plan: request(project, config, "dev").Plan}
					if named {
						_, err = svc.startScene(context.Background(), v1.SceneStartRequest{APIVersion: v1.Version, CWD: project, Plan: run.Plan})
					} else {
						_, err = svc.run(context.Background(), run)
					}
					want := "shutting_down"
					if legacy {
						want = "unsafe_drift"
						if status == v1.Failed {
							want = "restart_required"
						}
					}
					code(t, err, want)
					if !bytes.Equal(before, storedBytes(t, state, "snapshots", id)) {
						t.Fatal("rejected activation changed historical snapshot")
					}
					generation, err := state.resources(id)
					if err != nil || generation.ID != "" {
						t.Fatal("rejected activation allocated resources", err)
					}
				})
			}
		}
	}
}

func TestStateMissingVersionPreserved(t *testing.T) {
	s, _, _ := exportFixture(t)
	if err := s.store.db.Update(func(tx *bolt.Tx) error { return tx.Bucket([]byte("meta")).Delete([]byte("version")) }); err != nil {
		t.Fatal(err)
	}
	before := stateContents(t, s.store)
	code(t, s.store.db.Update(openStateTransaction), "state_version")
	if !reflect.DeepEqual(before, stateContents(t, s.store)) {
		t.Fatal("versionless state adopted")
	}
}
