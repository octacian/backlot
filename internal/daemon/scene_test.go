//go:build !windows

package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	v1 "github.com/octacian/backlot/api/v1"
	"github.com/octacian/backlot/internal/client"
	"github.com/octacian/backlot/internal/plan"
	bolt "go.etcd.io/bbolt"
)

func sceneFixture(t *testing.T) (*service, string, string) {
	t.Helper()
	root := shortTemp(t)
	config := writeFixture(t, root)
	git(t, root, "init", "-q")
	store, err := openStore(shortTemp(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := store.db.Close(); err != nil {
			t.Error(err)
		}
	})
	return &service{store: store, lease: time.Minute, generation: newID()}, root, config
}

func scenePrepare(t *testing.T, s *service, root, config string) v1.InstanceResponse {
	t.Helper()
	r, err := s.prepare(context.Background(), request(root, config, "dev"))
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestRecordedSceneLocationsAndAmbiguity(t *testing.T) {
	s, root, config := sceneFixture(t)
	target := v1.SceneTarget{CWD: root, Scene: "dev"}
	absent, err := s.scene(target, false)
	if err != nil || absent.Outcome != v1.SceneNotFound || absent.Checkout == nil {
		t.Fatal(absent, err)
	}
	outer := scenePrepare(t, s, root, config)
	nested := filepath.Join(root, "nested")
	if err := os.MkdirAll(filepath.Join(nested, "child"), 0700); err != nil {
		t.Fatal(err)
	}
	writeFixture(t, nested)
	if err := os.WriteFile(filepath.Join(nested, "backlot.json"), []byte(strings.Replace(fixtureManifest, `"project":"fixture"`, `"project":"nested"`, 1)), 0600); err != nil {
		t.Fatal(err)
	}
	inner := scenePrepare(t, s, nested, config)
	target.CWD = filepath.Join(nested, "child")
	r, err := s.scene(target, false)
	if err != nil || r.Result.Instance.ID != inner.Instance.ID {
		t.Fatal(r, err)
	}
	// Nearest recorded location is selected before filtering by scene.
	if err := os.WriteFile(filepath.Join(nested, "backlot.json"), []byte("broken"), 0600); err != nil {
		t.Fatal(err)
	}
	target.Scene = "absent"
	r, err = s.scene(target, false)
	if err != nil || r.Outcome != v1.SceneNotFound {
		t.Fatal(r, err)
	}
	target.Scene = "dev"
	if err := os.Remove(filepath.Join(nested, "backlot.json")); err != nil {
		t.Fatal(err)
	}
	r, err = s.scene(target, true)
	if err != nil || r.Outcome != v1.SceneAlreadyStopped || r.Result.Instance.Status != v1.Prepared {
		t.Fatal(r, err)
	}
	target.ProjectPath = filepath.Join(root, "backlot.json")
	r, err = s.scene(target, false)
	if err != nil || r.Result.Instance.ID != outer.Instance.ID {
		t.Fatal(r, err)
	}
	// Many historical records cannot hide a match behind public-list pagination.
	for range 105 {
		p := outer.Instance.Plan
		p.Scene = "other"
		if _, _, err := s.store.begin(plan.Snapshot{Plan: p}, outer.Instance.Checkout, time.Minute); err != nil {
			t.Fatal(err)
		}
		p.Project = "p" + newID()[:32]
		if _, _, err := s.store.begin(plan.Snapshot{Plan: p}, outer.Instance.Checkout, time.Minute); err != nil {
			t.Fatal(err)
		}
	}
	alias := filepath.Join(shortTemp(t), "alias")
	if err := os.Symlink(root, alias); err != nil {
		t.Fatal(err)
	}
	target = v1.SceneTarget{CWD: alias, Scene: "dev"}
	r, err = s.scene(target, false)
	if err != nil || r.Result.Instance.ID != outer.Instance.ID {
		t.Fatal(r, err)
	}
	data := strings.Replace(fixtureManifest, `"project":"fixture"`, `"project":"changed"`, 1)
	if err := os.WriteFile(filepath.Join(root, "backlot.json"), []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	changed := scenePrepare(t, s, root, config)
	_, err = s.scene(target, false)
	code(t, err, "ambiguous_target")
	r, err = s.scene(v1.SceneTarget{InstanceID: changed.Instance.ID}, false)
	if err != nil || r.Result.Instance.ID != changed.Instance.ID {
		t.Fatal(r, err)
	}
	// Destroy history still anchors nearest-project discovery but is not a named match.
	if _, err := s.destroy(context.Background(), inner.Instance.ID); err != nil {
		t.Fatal(err)
	}
	r, err = s.scene(v1.SceneTarget{CWD: nested, Scene: "dev"}, false)
	if err != nil || r.Outcome != v1.SceneNotFound {
		t.Fatal(r, err)
	}
}

func TestRecordedSceneIdentityAndExplicitEscape(t *testing.T) {
	s, root, config := sceneFixture(t)
	prepared := scenePrepare(t, s, root, config)
	moved := root + "-moved"
	if err := os.Rename(root, moved); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(moved); err != nil {
			t.Error(err)
		}
	})
	_, err := s.scene(v1.SceneTarget{CWD: moved, Scene: "dev"}, false)
	code(t, err, "checkout_moved")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	git(t, root, "init", "-q")
	_, err = s.scene(v1.SceneTarget{CWD: root, Scene: "dev"}, true)
	code(t, err, "checkout_replaced")
	r, err := s.scene(v1.SceneTarget{InstanceID: prepared.Instance.ID}, true)
	if err != nil || r.Outcome != v1.SceneAlreadyStopped || r.Result.Instance.Status != v1.Prepared {
		t.Fatal(r, err)
	}
	if err := os.RemoveAll(root); err != nil {
		t.Fatal(err)
	}
	r, err = s.scene(v1.SceneTarget{InstanceID: prepared.Instance.ID}, false)
	if err != nil || r.Result.Instance.ID != prepared.Instance.ID {
		t.Fatal(r, err)
	}
	_, err = s.scene(v1.SceneTarget{CWD: root, Scene: "dev"}, false)
	code(t, err, "checkout_identity")
}

func TestSceneNonGitAndRedaction(t *testing.T) {
	s, root, config := sceneFixture(t)
	if err := os.RemoveAll(filepath.Join(root, ".git")); err != nil {
		t.Fatal(err)
	}
	prepared := scenePrepare(t, s, root, config)
	child := filepath.Join(root, "child")
	if err := os.Mkdir(child, 0700); err != nil {
		t.Fatal(err)
	}
	r, err := s.scene(v1.SceneTarget{CWD: child, Scene: "dev"}, false)
	if err != nil || r.Result.Instance.ID != prepared.Instance.ID {
		t.Fatal(r, err)
	}
	recorder := httptest.NewRecorder()
	writeJSON(recorder, r)
	if strings.Contains(recorder.Body.String(), "ownership_token") || strings.Contains(recorder.Body.String(), "resolved-fixture-secret") {
		t.Fatal("wrapped scene leaked private data", recorder.Body.String())
	}
	var decoded v1.SceneResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Result.Instance.Operation.Allocations[0].OwnershipToken != "" {
		t.Fatal("redaction failed")
	}
	raw, err := s.store.inspect(prepared.Instance.ID)
	if err != nil || raw.Operation.Allocations[0].OwnershipToken == "" {
		t.Fatal("redaction altered durable ownership", err)
	}
}

func TestSceneStopRechecksSelectionAndReportsCollectionFailure(t *testing.T) {
	s, root, config := sceneFixture(t)
	prepared := scenePrepare(t, s, root, config)
	reached := make(chan struct{})
	proceed := make(chan struct{})
	s.beforeSceneMutation = func(string) { close(reached); <-proceed }
	result := make(chan error, 1)
	go func() { _, err := s.scene(v1.SceneTarget{CWD: root, Scene: "dev"}, true); result <- err }()
	<-reached
	if _, err := s.destroy(context.Background(), prepared.Instance.ID); err != nil {
		t.Fatal(err)
	}
	replacement := scenePrepare(t, s, root, config)
	close(proceed)
	code(t, <-result, "conflict")
	current, err := s.store.inspect(replacement.Instance.ID)
	if err != nil || current.Status != v1.Prepared {
		t.Fatal(current, err)
	}
	s.beforeSceneMutation = nil
	_, err = s.store.execution(replacement.Instance.ID, v1.Failed, &v1.ExecutionResult{Attempt: newID(), Components: []v1.ComponentResult{}, CollectionFailure: "collection failed"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.scene(v1.SceneTarget{InstanceID: replacement.Instance.ID}, true)
	code(t, err, "collection_failed")
}

func TestSceneStartRejectsDisposableBeforeIntentEvenWhenInputsChange(t *testing.T) {
	s, root, config := sceneFixture(t)
	_, err := s.startScene(context.Background(), v1.SceneStartRequest{APIVersion: v1.Version, CWD: root, Plan: v1.PlanRequest{Scene: "test", ProjectPath: root, ConfigPath: config}})
	code(t, err, "invalid_lifetime")
	s.resolve = func(source plan.Source, request v1.PlanRequest) (plan.Snapshot, error) {
		snapshot, err := plan.Prepare(source, request)
		snapshot.Plan.Lifetime = v1.Disposable
		return snapshot, err
	}
	_, err = s.startScene(context.Background(), v1.SceneStartRequest{APIVersion: v1.Version, CWD: root, Plan: v1.PlanRequest{Scene: "dev", ProjectPath: root, ConfigPath: config}})
	code(t, err, "invalid_lifetime")
	if err := s.store.db.View(func(tx *bolt.Tx) error {
		if tx.Bucket([]byte("instances")).Stats().KeyN != 0 {
			t.Fatal("disposable intent allocated")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestSceneFollowInitialAbsenceRejectsReplacementThroughRealDaemon(t *testing.T) {
	root := shortTemp(t)
	git(t, root, "init", "-q")
	cli, _ := startFixture(t, filepath.Join(shortTemp(t), "state"), time.Minute)
	moved := root + "-original"
	t.Cleanup(func() {
		if err := os.RemoveAll(moved); err != nil {
			t.Error(err)
		}
	})
	var frames []v1.SceneFollowRecord
	err := cli.FollowScene(context.Background(), v1.SceneRequest{APIVersion: v1.Version, Target: v1.SceneTarget{CWD: root, Scene: "dev"}}, client.SceneFollowOptions{Interval: 100 * time.Millisecond, Duration: time.Second}, func(record v1.SceneFollowRecord) error {
		frames = append(frames, record)
		if record.Snapshot != nil {
			if record.Snapshot.Outcome != v1.SceneNotFound || record.Snapshot.Checkout == nil {
				t.Fatal("initial absence lacked independent identity", record)
			}
			if err := os.Rename(root, moved); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(root, 0700); err != nil {
				t.Fatal(err)
			}
			git(t, root, "init", "-q")
		}
		return nil
	})
	code(t, err, "checkout_changed")
	if len(frames) != 2 || frames[1].Type != "error" {
		t.Fatal(frames)
	}
}

func TestSceneFollowRealDaemonCreationAndRecreation(t *testing.T) {
	root := shortTemp(t)
	config := writeFixture(t, root)
	git(t, root, "init", "-q")
	cli, _ := startFixture(t, filepath.Join(shortTemp(t), "state"), time.Minute)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var ids []string
	missing := 0
	err := cli.FollowScene(ctx, v1.SceneRequest{APIVersion: v1.Version, Target: v1.SceneTarget{CWD: root, Scene: "dev"}}, client.SceneFollowOptions{Interval: 100 * time.Millisecond, Duration: 2 * time.Second}, func(record v1.SceneFollowRecord) error {
		if record.Snapshot == nil {
			return nil
		}
		if record.Snapshot.Outcome == v1.SceneNotFound {
			missing++
			prepareFixture(t, cli, request(root, config, "dev"))
		} else {
			ids = append(ids, record.Snapshot.Result.Instance.ID)
			if len(ids) == 1 {
				if _, err := cli.Destroy(ctx, ids[0]); err != nil {
					t.Fatal(err)
				}
			} else {
				cancel()
			}
		}
		return nil
	})
	if !errors.Is(err, context.Canceled) || missing != 2 || len(ids) != 2 || ids[0] == ids[1] {
		t.Fatal(err, missing, ids)
	}
}

func TestSceneUnknownNonGitRootAndUnavailableRecordedGit(t *testing.T) {
	s, root, config := sceneFixture(t)
	if err := os.RemoveAll(filepath.Join(root, ".git")); err != nil {
		t.Fatal(err)
	}
	_, err := s.scene(v1.SceneTarget{CWD: root, Scene: "dev"}, false)
	code(t, err, "checkout_identity")
	response, err := s.scene(v1.SceneTarget{CWD: root, ProjectPath: root, Scene: "dev"}, false)
	if err != nil || response.Outcome != v1.SceneNotFound || response.Checkout == nil {
		t.Fatal(response, err)
	}
	git(t, root, "init", "-q")
	nested := filepath.Join(root, "nested")
	if err := os.Mkdir(nested, 0700); err != nil {
		t.Fatal(err)
	}
	writeFixture(t, nested)
	scenePrepare(t, s, nested, config)
	if err := os.RemoveAll(filepath.Join(root, ".git")); err != nil {
		t.Fatal(err)
	}
	// Unavailable recorded checkout identity is an error even when the scene is absent.
	_, err = s.scene(v1.SceneTarget{CWD: nested, Scene: "absent"}, false)
	code(t, err, "checkout_changed")
}

func TestSceneFollowFreezesCanonicalAliases(t *testing.T) {
	for _, mode := range []string{"cwd", "project", "missing-manifest"} {
		t.Run(mode, func(t *testing.T) {
			root := shortTemp(t)
			git(t, root, "init", "-q")
			a, b := filepath.Join(root, "a"), filepath.Join(root, "b")
			for _, project := range []string{a, b} {
				if err := os.Mkdir(project, 0700); err != nil {
					t.Fatal(err)
				}
				writeFixture(t, project)
				text := strings.Replace(fixtureManifest, `"project":"fixture"`, `"project":"`+filepath.Base(project)+`"`, 1)
				if err := os.WriteFile(filepath.Join(project, "backlot.json"), []byte(text), 0600); err != nil {
					t.Fatal(err)
				}
			}
			// Configuration is external to the mutable selector and remains read-only.
			config := writeFixture(t, shortTemp(t))
			cli, _ := startFixture(t, filepath.Join(shortTemp(t), "state"), time.Minute)
			first := prepareFixture(t, cli, request(a, config, "dev"))
			peer := prepareFixture(t, cli, request(b, config, "dev"))
			alias := filepath.Join(root, "alias")
			if err := os.Symlink(a, alias); err != nil {
				t.Fatal(err)
			}
			target := v1.SceneTarget{Scene: "dev", CWD: alias}
			if mode != "cwd" {
				target.CWD = root
				target.ProjectPath = alias
			}
			original, err := os.ReadFile(filepath.Join(a, "backlot.json"))
			if err != nil {
				t.Fatal(err)
			}
			if mode == "missing-manifest" {
				target.ProjectPath = filepath.Join(alias, "backlot.json")
				if err := os.Remove(filepath.Join(a, "backlot.json")); err != nil {
					t.Fatal(err)
				}
			}
			canonicalA, err := filepath.EvalSymlinks(a)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			snapshots := 0
			recreated := ""
			err = cli.FollowScene(ctx, v1.SceneRequest{APIVersion: v1.Version, Target: target}, client.SceneFollowOptions{Interval: 100 * time.Millisecond, Duration: 3 * time.Second}, func(record v1.SceneFollowRecord) error {
				if record.Snapshot == nil {
					return nil
				}
				snapshots++
				snapshot := record.Snapshot
				if snapshot.CanonicalTarget == nil || snapshot.CanonicalTarget.CWD != canonicalA {
					t.Fatal("canonical selector not frozen", snapshot.CanonicalTarget)
				}
				if mode != "cwd" && snapshot.CanonicalTarget.ProjectPath != canonicalA {
					t.Fatal("manifest selector not normalized", snapshot.CanonicalTarget)
				}
				if snapshot.Result != nil && snapshot.Result.Instance.ID == peer.Instance.ID {
					t.Fatal("observer silently retargeted peer project")
				}
				switch snapshots {
				case 1:
					if snapshot.Result == nil || snapshot.Result.Instance.ID != first.Instance.ID {
						t.Fatal("wrong initial target", snapshot)
					}
					if err := os.Remove(alias); err != nil {
						t.Fatal(err)
					}
					if err := os.Symlink(b, alias); err != nil {
						t.Fatal(err)
					}
					if _, err := cli.Destroy(context.Background(), first.Instance.ID); err != nil {
						t.Fatal(err)
					}
				case 2:
					if snapshot.Outcome != v1.SceneNotFound {
						t.Fatal("original project gap not observed", snapshot)
					}
					if err := os.WriteFile(filepath.Join(a, "backlot.json"), original, 0600); err != nil {
						t.Fatal(err)
					}
					recreated = prepareFixture(t, cli, request(a, config, "dev")).Instance.ID
				case 3:
					if snapshot.Result == nil || snapshot.Result.Instance.ID != recreated || recreated == first.Instance.ID {
						t.Fatal("same-project recreation not observed", snapshot)
					}
					cancel()
				default:
					t.Fatal("unexpected extra snapshot", snapshot)
				}
				return nil
			})
			if !errors.Is(err, context.Canceled) || snapshots != 3 {
				t.Fatal(err, snapshots)
			}
		})
	}
}

func TestSceneNestedGitBoundaryRootChildAndExplicitParity(t *testing.T) {
	s, root, config := sceneFixture(t)
	scenePrepare(t, s, root, config)
	nested := filepath.Join(root, "nested-git")
	child := filepath.Join(nested, "child")
	if err := os.MkdirAll(child, 0700); err != nil {
		t.Fatal(err)
	}
	git(t, nested, "init", "-q")
	nestedIdentity, err := identify(nested)
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range []v1.SceneTarget{{CWD: nested, Scene: "dev"}, {CWD: child, Scene: "dev"}, {CWD: root, ProjectPath: nested, Scene: "dev"}, {CWD: child, ProjectPath: filepath.Join(nested, "backlot.json"), Scene: "dev"}} {
		response, err := s.scene(target, false)
		if err != nil || response.Outcome != v1.SceneNotFound || response.Checkout == nil || response.Checkout.ID != nestedIdentity.ID {
			t.Fatal(target, response, err)
		}
	}
}

// Exact-ID observation keeps inspect's lifetime breadth, needs no checkout files,
// and must not adopt a replacement logical scene or renew a disposable lease.
func TestSceneFollowExactIDLifetimeAndHistory(t *testing.T) {
	for _, scene := range []string{"dev", "test"} {
		t.Run(scene, func(t *testing.T) {
			root := shortTemp(t)
			config := writeFixture(t, root)
			cli, _ := startFixture(t, filepath.Join(shortTemp(t), "state"), time.Minute)
			initial := prepareFixture(t, cli, request(root, config, scene))
			id := initial.Instance.ID
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if err := os.Remove(filepath.Join(root, "backlot.json")); err != nil {
				t.Fatal(err)
			}
			var frames []v1.SceneFollowRecord
			var replacement string
			err := cli.FollowScene(ctx, v1.SceneRequest{APIVersion: v1.Version, Target: v1.SceneTarget{InstanceID: id}}, client.SceneFollowOptions{Interval: 100 * time.Millisecond, Duration: 2 * time.Second}, func(record v1.SceneFollowRecord) error {
				frames = append(frames, record)
				if record.Snapshot == nil {
					return nil
				}
				snapshot := record.Snapshot
				if snapshot.Checkout != nil || snapshot.CanonicalTarget != nil || snapshot.Result == nil || snapshot.Result.Instance.ID != id || snapshot.Result.LeaseToken != "" {
					t.Fatal("exact-ID observation changed selection or leaked capability", snapshot)
				}
				if len(frames) == 1 {
					if snapshot.Result.Instance.Status != v1.Prepared || snapshot.Result.Instance.LeaseExpiresAt != initial.Instance.LeaseExpiresAt {
						t.Fatal("observation changed prepared state or lease", snapshot)
					}
					if _, err := cli.Destroy(ctx, id); err != nil {
						t.Fatal(err)
					}
					writeFixture(t, root)
					replacement = prepareFixture(t, cli, request(root, config, scene)).Instance.ID
					if err := os.RemoveAll(root); err != nil {
						t.Fatal(err)
					}
				} else if snapshot.Result.Instance.Status == v1.Destroyed {
					cancel()
				}
				return nil
			})
			if !errors.Is(err, context.Canceled) || len(frames) != 3 || replacement == "" || replacement == id || frames[2].Reason != "cancelled" {
				t.Fatal(err, replacement, frames)
			}
			if _, err := cli.Destroy(context.Background(), replacement); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestSceneExactIDObservationDoesNotRequireNamedLocation(t *testing.T) {
	s, root, config := sceneFixture(t)
	prepared := scenePrepare(t, s, root, config)
	id := prepared.Instance.ID
	var original record
	if err := s.store.db.View(func(tx *bolt.Tx) error {
		var err error
		original, err = load(tx, id)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	// Register restore before damaging metadata in this offline owned database.
	t.Cleanup(func() {
		if err := s.store.db.Update(func(tx *bolt.Tx) error { return put(tx, "instances", id, original) }); err != nil {
			t.Error(err)
		}
	})
	if err := s.store.db.Update(func(tx *bolt.Tx) error {
		r, err := load(tx, id)
		if err != nil {
			return err
		}
		r.Instance.Plan.ManifestPath = ""
		return put(tx, "instances", id, r)
	}); err != nil {
		t.Fatal(err)
	}
	response, err := s.scene(v1.SceneTarget{InstanceID: id}, false)
	if err != nil || response.Result == nil || response.Result.Instance.ID != id {
		t.Fatal(response, err)
	}
	_, err = s.scene(v1.SceneTarget{InstanceID: id}, true)
	code(t, err, "state_corrupt")
}

func TestSceneExactIDDisposableStatusKeepsStopRestriction(t *testing.T) {
	s, root, config := sceneFixture(t)
	prepared, err := s.prepare(context.Background(), request(root, config, "test"))
	if err != nil {
		t.Fatal(err)
	}
	target := v1.SceneTarget{InstanceID: prepared.Instance.ID}
	response, err := s.scene(target, false)
	if err != nil || response.Result == nil || response.Result.Instance.Plan.Lifetime != v1.Disposable || response.Result.LeaseToken != "" {
		t.Fatal(response, err)
	}
	_, err = s.scene(target, true)
	code(t, err, "invalid_lifetime")
	current, err := s.store.inspect(prepared.Instance.ID)
	if err != nil || current.Status != v1.Prepared || current.LeaseExpiresAt != prepared.Instance.LeaseExpiresAt {
		t.Fatal(current, err)
	}
}
