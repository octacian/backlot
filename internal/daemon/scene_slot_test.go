//go:build !windows

package daemon

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	v1 "github.com/octacian/backlot/api/v1"
	bolt "go.etcd.io/bbolt"
)

func TestSceneCorruptSlotCannotHideNearestProject(t *testing.T) {
	cases := map[string]func(*bolt.Tx, *v1.Instance) error{
		"scene-valid-but-changed":       func(_ *bolt.Tx, i *v1.Instance) error { i.Plan.Scene = "other"; return nil },
		"project-valid-but-changed":     func(_ *bolt.Tx, i *v1.Instance) error { i.Plan.Project = "other-project"; return nil },
		"lifetime-disposable-with-slot": func(_ *bolt.Tx, i *v1.Instance) error { i.Plan.Lifetime = v1.Disposable; return nil },
		"missing-slot": func(tx *bolt.Tx, i *v1.Instance) error {
			return tx.Bucket([]byte("persistent")).Delete([]byte(sceneSlot(*i)))
		},
		"slot-missing-record": func(tx *bolt.Tx, i *v1.Instance) error {
			return tx.Bucket([]byte("persistent")).Put([]byte(sceneSlot(*i)), []byte(newID()))
		},
		"slot-extra-project": func(tx *bolt.Tx, i *v1.Instance) error {
			return tx.Bucket([]byte("persistent")).Put([]byte("other/"+i.Checkout.ID+"/dev"), []byte(i.ID))
		},
		"slot-other-record": func(tx *bolt.Tx, i *v1.Instance) error {
			bucket := tx.Bucket([]byte("persistent"))
			return bucket.Put([]byte(sceneSlot(*i)), bucket.Get([]byte("fixture/"+i.Checkout.ID+"/dev")))
		},
		"slot-wrong-scene": func(tx *bolt.Tx, i *v1.Instance) error {
			return tx.Bucket([]byte("persistent")).Put([]byte(i.Plan.Project+"/"+i.Checkout.ID+"/other"), []byte(i.ID))
		},
		"slot-wrong-checkout": func(tx *bolt.Tx, i *v1.Instance) error {
			return tx.Bucket([]byte("persistent")).Put([]byte(i.Plan.Project+"/"+newID()+"/dev"), []byte(i.ID))
		},
		"destroyed-wrong-slot": func(tx *bolt.Tx, i *v1.Instance) error {
			i.Status = v1.Destroyed
			i.Operation.Status = i.Status
			return tx.Bucket([]byte("persistent")).Put([]byte("other/"+i.Checkout.ID+"/dev"), []byte(i.ID))
		},
	}
	for _, status := range []v1.InstanceStatus{v1.Preparing, v1.Starting, v1.RuntimeReady, v1.Stopping, v1.Stopped, v1.Failed, v1.Cancelled, v1.Interrupted, v1.Succeeded} {
		cases["missing-slot-"+string(status)] = func(tx *bolt.Tx, i *v1.Instance) error {
			i.Status = status
			i.Operation.Status = status
			return tx.Bucket([]byte("persistent")).Delete([]byte(sceneSlot(*i)))
		}
	}
	for name, damage := range cases {
		t.Run(name, func(t *testing.T) {
			s, root, config := sceneFixture(t)
			outer := scenePrepare(t, s, root, config)
			inner := filepath.Join(root, "inner")
			if err := os.Mkdir(inner, 0700); err != nil {
				t.Fatal(err)
			}
			writeFixture(t, inner)
			if err := os.WriteFile(filepath.Join(inner, "backlot.json"), []byte(strings.Replace(fixtureManifest, `"project":"fixture"`, `"project":"inner"`, 1)), 0600); err != nil {
				t.Fatal(err)
			}
			recorded := scenePrepare(t, s, inner, config)
			// No daemon/runtime exists. Capture and register restoration of both touched
			// buckets before injecting the bounded inconsistency into this private store.
			saved := map[string]map[string][]byte{}
			if err := s.store.db.View(func(tx *bolt.Tx) error {
				for _, name := range []string{"instances", "persistent"} {
					saved[name] = map[string][]byte{}
					if err := tx.Bucket([]byte(name)).ForEach(func(k, v []byte) error { saved[name][string(k)] = append([]byte(nil), v...); return nil }); err != nil {
						return err
					}
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := s.store.db.Update(func(tx *bolt.Tx) error {
					for name, entries := range saved {
						if err := tx.DeleteBucket([]byte(name)); err != nil {
							return err
						}
						bucket, err := tx.CreateBucket([]byte(name))
						if err != nil {
							return err
						}
						for key, data := range entries {
							if err := bucket.Put([]byte(key), data); err != nil {
								return err
							}
						}
					}
					return nil
				}); err != nil {
					t.Error(err)
				}
			})
			if err := s.store.db.Update(func(tx *bolt.Tx) error {
				r, err := load(tx, recorded.Instance.ID)
				if err != nil {
					return err
				}
				if err := damage(tx, &r.Instance); err != nil {
					return err
				}
				return put(tx, "instances", r.Instance.ID, r)
			}); err != nil {
				t.Fatal(err)
			}
			for _, stop := range []bool{false, true} {
				response, err := s.scene(v1.SceneTarget{CWD: inner, Scene: "dev"}, stop)
				code(t, err, "state_corrupt")
				if response.Result != nil {
					t.Fatal("corruption exposed a target", response)
				}
			}
			_, err := s.prepareSelected(context.Background(), request(inner, config, "dev"), true)
			code(t, err, "state_corrupt")
			// Legacy explicit IDs remain the escape route and the outer record is intact.
			if _, err := s.store.inspect(recorded.Instance.ID); err != nil {
				t.Fatal(err)
			}
			unchanged, err := s.store.inspect(outer.Instance.ID)
			if err != nil || !reflect.DeepEqual(unchanged, outer.Instance) {
				t.Fatal("outer mutated", err)
			}
			if err := s.store.db.View(func(tx *bolt.Tx) error {
				if tx.Bucket([]byte("instances")).Stats().KeyN != 2 {
					t.Fatal("new named intent accepted")
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestSceneSlotLifecycleHistory(t *testing.T) {
	s, root, config := sceneFixture(t)
	original := scenePrepare(t, s, root, config)
	disposable, err := s.prepare(context.Background(), request(root, config, "test"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.store.execution(disposable.Instance.ID, v1.Destroyed, &v1.ExecutionResult{Attempt: original.Instance.Operation.ID, Components: []v1.ComponentResult{}}); err != nil {
		t.Fatal(err)
	}

	target := v1.SceneTarget{CWD: root, Scene: "dev"}
	// Exercise the real status writer with a retained logical slot. None of these
	// non-destroyed states releases the persistent identity.
	for _, status := range []v1.InstanceStatus{v1.Preparing, v1.Prepared, v1.Starting, v1.RuntimeReady, v1.Stopping, v1.Stopped, v1.Failed, v1.Cancelled, v1.Interrupted, v1.Succeeded} {
		if _, err := s.store.execution(original.Instance.ID, status, &v1.ExecutionResult{Attempt: original.Instance.Operation.ID, Components: []v1.ComponentResult{}}); err != nil {
			t.Fatal(err)
		}
		response, err := s.scene(target, false)
		if err != nil || response.Result == nil || response.Result.Instance.Status != status {
			t.Fatal(status, response, err)
		}
	}
	// Match destroy's first transaction: a destroyed owner may still have its exact
	// slot until the separate delete transaction, including after interruption.
	if _, err := s.store.execution(original.Instance.ID, v1.Destroyed, &v1.ExecutionResult{Attempt: original.Instance.Operation.ID, Components: []v1.ComponentResult{}}); err != nil {
		t.Fatal(err)
	}
	response, err := s.scene(target, false)
	if err != nil || response.Outcome != v1.SceneNotFound {
		t.Fatal(response, err)
	}
	if err := s.store.db.Update(func(tx *bolt.Tx) error {
		return tx.Bucket([]byte("persistent")).Delete([]byte(sceneSlot(original.Instance)))
	}); err != nil {
		t.Fatal(err)
	}
	recreated, err := s.prepareSelected(context.Background(), request(root, config, "dev"), true)
	if err != nil || recreated.Instance.ID == original.Instance.ID {
		t.Fatal(recreated, err)
	}
	response, err = s.scene(target, false)
	if err != nil || response.Result == nil || response.Result.Instance.ID != recreated.Instance.ID {
		t.Fatal(response, err)
	}
	// An independently accepted project-ID generation is valid state, yielding
	// ambiguity rather than being classified as a broken slot or newest-wins.
	if err := os.WriteFile(filepath.Join(root, "backlot.json"), []byte(strings.Replace(fixtureManifest, `"project":"fixture"`, `"project":"changed"`, 1)), 0600); err != nil {
		t.Fatal(err)
	}
	changed, err := s.prepareSelected(context.Background(), request(root, config, "dev"), true)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.scene(target, false)
	code(t, err, "ambiguous_target")
	response, err = s.scene(v1.SceneTarget{InstanceID: changed.Instance.ID}, false)
	if err != nil || response.Result.Instance.ID != changed.Instance.ID {
		t.Fatal(response, err)
	}
}
