//go:build darwin || linux

package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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

func predecessorFixture(t *testing.T) (*service, v1.Instance) {
	t.Helper()
	s, instance, _ := exportFixture(t)
	snap, err := s.store.snapshot(instance.ID)
	if err != nil {
		t.Fatal(err)
	}
	snap.PrivateProvenance, snap.Plan.EffectiveConfigDigest = nil, ""
	if err := s.store.replaceSnapshot(instance.ID, snap); err != nil {
		t.Fatal(err)
	}
	if err := s.store.db.Update(func(tx *bolt.Tx) error {
		if err := tx.Bucket([]byte("meta")).Put([]byte("opaque"), []byte("retained metadata")); err != nil {
			return err
		}
		return tx.Bucket([]byte("meta")).Put([]byte("version"), []byte("6"))
	}); err != nil {
		t.Fatal(err)
	}
	return s, instance
}

func TestStateSixUpgradePreservesRecordsAndRollsBack(t *testing.T) {
	s, instance := predecessorFixture(t)
	// Missing boot and mismatched attempt are conservative recovery states, not
	// migration corruption. Waiver bytes must survive without being normalized.
	if err := s.store.saveRuntime(instance.ID, runtimeRecord{Attempt: newID(), LaunchPending: true}); err != nil {
		t.Fatal(err)
	}
	if err := s.store.db.Update(func(tx *bolt.Tx) error {
		r, err := load(tx, instance.ID)
		if err != nil {
			return err
		}
		r.NativeAbandonments = []nativeAbandonment{{Attempt: newID(), RecordedAt: time.Now().UTC().Format(time.RFC3339Nano), Reason: "operator force", Pending: true}}
		return put(tx, "instances", instance.ID, r)
	}); err != nil {
		t.Fatal(err)
	}
	retained := filepath.Join(s.directory, "retained-ca-config-data")
	if err := os.WriteFile(retained, []byte("private retained fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	before := stateContents(t, s.store)
	injected := errors.New("before commit")
	if err := s.store.db.Update(func(tx *bolt.Tx) error {
		if err := openStateTransaction(tx); err != nil {
			return err
		}
		if string(tx.Bucket([]byte("meta")).Get([]byte("version"))) != "8" {
			t.Fatal("no tentative fence")
		}
		return injected
	}); !errors.Is(err, injected) {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, stateContents(t, s.store)) {
		t.Fatal("rollback changed predecessor")
	}
	if err := s.store.db.Update(openStateTransaction); err != nil {
		t.Fatal(err)
	}
	before["meta"]["version"] = "8"
	if !reflect.DeepEqual(before, stateContents(t, s.store)) {
		t.Fatal("upgrade changed retained records")
	}
	if err := s.store.db.Update(openStateTransaction); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, stateContents(t, s.store)) {
		t.Fatal("reopening changed records")
	}
	if data, err := os.ReadFile(retained); err != nil || string(data) != "private retained fixture" {
		t.Fatal("retained files changed", err)
	}
	snap, err := s.store.snapshot(instance.ID)
	if err != nil || snap.PrivateProvenance != nil || snap.Plan.EffectiveConfigDigest != "" {
		t.Fatal("fabricated historical provenance", err)
	}
}

func TestStateSixAddressingFailuresRollback(t *testing.T) {
	cases := map[string]func(*bolt.Tx, v1.Instance) error{
		"checkout key": func(tx *bolt.Tx, i v1.Instance) error {
			return tx.Bucket([]byte("checkouts")).Put([]byte(newID()), tx.Bucket([]byte("checkouts")).Get([]byte(i.Checkout.ID)))
		},
		"checkout path": func(tx *bolt.Tx, i v1.Instance) error {
			return tx.Bucket([]byte("paths")).Put([]byte(i.Checkout.Path), []byte(newID()))
		},
		"reverse path": func(tx *bolt.Tx, i v1.Instance) error {
			return tx.Bucket([]byte("paths")).Put([]byte("/wrong"), []byte(i.Checkout.ID))
		},
		"persistent target": func(tx *bolt.Tx, i v1.Instance) error {
			return tx.Bucket([]byte("persistent")).Put([]byte("wrong/slot"), []byte(i.ID))
		},
		"snapshot parent": func(tx *bolt.Tx, i v1.Instance) error {
			return tx.Bucket([]byte("snapshots")).Put([]byte(newID()), tx.Bucket([]byte("snapshots")).Get([]byte(i.ID)))
		},
		"snapshot identity": func(tx *bolt.Tx, i v1.Instance) error {
			var snap plan.Snapshot
			if err := json.Unmarshal(tx.Bucket([]byte("snapshots")).Get([]byte(i.ID)), &snap); err != nil {
				return err
			}
			snap.Plan.Scene = "misaddressed"
			return put(tx, "snapshots", i.ID, snap)
		},
		"effect pair": func(tx *bolt.Tx, i v1.Instance) error { return tx.Bucket([]byte("secrets")).Delete([]byte(i.ID)) },
		"secret parent": func(tx *bolt.Tx, _ v1.Instance) error {
			return tx.Bucket([]byte("secrets")).Put([]byte(newID()), []byte(`{}`))
		},
		"runtime parent":  func(tx *bolt.Tx, _ v1.Instance) error { return put(tx, "runtime", newID(), runtimeRecord{}) },
		"resource parent": func(tx *bolt.Tx, _ v1.Instance) error { return put(tx, "resources", newID(), resourceGeneration{}) },
		"snapshot type": func(tx *bolt.Tx, i v1.Instance) error {
			return tx.Bucket([]byte("snapshots")).Put([]byte(i.ID), []byte(`{"plan":[]}`))
		},
		"secret type": func(tx *bolt.Tx, i v1.Instance) error {
			return tx.Bucket([]byte("secrets")).Put([]byte(i.ID), []byte(`{"value":23}`))
		},
		"missing bucket": func(tx *bolt.Tx, _ v1.Instance) error { return tx.DeleteBucket([]byte("resources")) },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			s, instance := predecessorFixture(t)
			if err := s.store.db.Update(func(tx *bolt.Tx) error { return mutate(tx, instance) }); err != nil {
				t.Fatal(err)
			}
			before := stateContents(t, s.store)
			err := s.store.db.Update(openStateTransaction)
			code(t, err, "state_corrupt")
			if !reflect.DeepEqual(before, stateContents(t, s.store)) {
				t.Fatal("invalid state changed")
			}
		})
	}
}

func TestStateSixIntentAndDestroyedHistoryRemainSupported(t *testing.T) {
	for _, intent := range []bool{false, true} {
		t.Run(map[bool]string{false: "destroyed", true: "intent"}[intent], func(t *testing.T) {
			s, instance := predecessorFixture(t)
			if err := s.store.db.Update(func(tx *bolt.Tx) error {
				r, err := load(tx, instance.ID)
				if err != nil {
					return err
				}
				r.Instance.Status, r.Instance.Operation.Status = v1.Destroyed, v1.Destroyed
				if intent {
					r.Instance.Status, r.Instance.Operation.Status = v1.Interrupted, v1.Interrupted
					r.Instance.Operation.Allocations[0].Phase = "intent"
					if err := tx.Bucket([]byte("snapshots")).Delete([]byte(instance.ID)); err != nil {
						return err
					}
					if err := tx.Bucket([]byte("secrets")).Delete([]byte(instance.ID)); err != nil {
						return err
					}
				}
				return put(tx, "instances", instance.ID, r)
			}); err != nil {
				t.Fatal(err)
			}
			if err := s.store.db.Update(openStateTransaction); err != nil {
				t.Fatal(err)
			}
		})
	}
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

func TestStateSixMissingVersionPreserved(t *testing.T) {
	s, _ := predecessorFixture(t)
	if err := s.store.db.Update(func(tx *bolt.Tx) error { return tx.Bucket([]byte("meta")).Delete([]byte("version")) }); err != nil {
		t.Fatal(err)
	}
	before := stateContents(t, s.store)
	code(t, s.store.db.Update(openStateTransaction), "state_version")
	if !reflect.DeepEqual(before, stateContents(t, s.store)) {
		t.Fatal("versionless state adopted")
	}
}

func TestStateSixDestroyedBeforeSlotRemovalRemainsSupported(t *testing.T) {
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
	// destroy commits terminal state before deleting its index in a separate
	// transaction. A crash in that gap leaves a valid, correctly addressed slot.
	if _, err := state.execution(prepared.Instance.ID, v1.Destroyed, nil); err != nil {
		t.Fatal(err)
	}
	if err := state.db.Update(func(tx *bolt.Tx) error { return tx.Bucket([]byte("meta")).Put([]byte("version"), []byte("6")) }); err != nil {
		t.Fatal(err)
	}
	before := stateContents(t, state)
	if err := state.db.Update(openStateTransaction); err != nil {
		t.Fatal(err)
	}
	before["meta"]["version"] = "8"
	if !reflect.DeepEqual(before, stateContents(t, state)) {
		t.Fatal("destroy gap history or slot changed")
	}
}

func TestStateSixRequiresNonDestroyedPersistentSlot(t *testing.T) {
	for _, status := range []v1.InstanceStatus{v1.Prepared, v1.Stopped, v1.Interrupted, v1.Destroyed} {
		t.Run(string(status), func(t *testing.T) {
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
			if _, err := state.execution(prepared.Instance.ID, status, nil); err != nil {
				t.Fatal(err)
			}
			if err := state.db.Update(func(tx *bolt.Tx) error {
				if err := tx.Bucket([]byte("meta")).Put([]byte("version"), []byte("6")); err != nil {
					return err
				}
				return tx.Bucket([]byte("persistent")).Delete([]byte(sceneSlot(prepared.Instance)))
			}); err != nil {
				t.Fatal(err)
			}
			before := stateContents(t, state)
			path := filepath.Join(state.directory, "state.db")
			physical, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			err = state.db.Update(openStateTransaction)
			if status == v1.Destroyed {
				if err != nil {
					t.Fatal("slotless destroyed persistent history rejected", err)
				}
				before["meta"]["version"] = "8"
			} else {
				code(t, err, "state_corrupt")
				after, err := os.ReadFile(path)
				if err != nil || !bytes.Equal(physical, after) {
					t.Fatal("rejected predecessor database changed", err)
				}
			}
			if !reflect.DeepEqual(before, stateContents(t, state)) {
				t.Fatal("predecessor records changed")
			}
		})
	}
}
