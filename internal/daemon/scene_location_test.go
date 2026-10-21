//go:build !windows

package daemon

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	v1 "github.com/octacian/backlot/api/v1"
	"github.com/octacian/backlot/internal/plan"
	bolt "go.etcd.io/bbolt"
)

func TestSceneLocationCollisionAtomicAcceptance(t *testing.T) {
	s, root, config := sceneFixture(t)
	a, b := filepath.Join(root, "a"), filepath.Join(root, "b")
	for _, project := range []string{a, b} {
		if err := os.Mkdir(project, 0700); err != nil {
			t.Fatal(err)
		}
		writeFixture(t, project)
	}
	gate := make(chan struct{})
	results := make(chan error, 2)
	var joined sync.WaitGroup
	for _, project := range []string{a, b} {
		joined.Go(func() {
			<-gate
			_, err := s.prepareSelected(context.Background(), request(project, config, "dev"), true)
			results <- err
		})
	}
	close(gate)
	joined.Wait()
	close(results)
	accepted, rejected := 0, 0
	for err := range results {
		if err == nil {
			accepted++
		} else {
			code(t, err, "conflict")
			rejected++
		}
	}
	if accepted != 1 || rejected != 1 {
		t.Fatal(accepted, rejected)
	}
	if err := s.store.db.View(func(tx *bolt.Tx) error {
		if tx.Bucket([]byte("instances")).Stats().KeyN != 1 || tx.Bucket([]byte("runtime")).Stats().KeyN != 0 {
			t.Fatal("collision accepted another intent or runtime")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	listed, err := s.store.list(v1.ListRequest{})
	if err != nil || len(listed.Instances) != 1 {
		t.Fatal(listed, err)
	}
	winner, err := s.store.inspect(listed.Instances[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	other := a
	if filepath.Dir(winner.Plan.ManifestPath) == a || filepath.Dir(winner.Plan.ManifestPath) == filepath.Join(winner.Checkout.Path, "a") {
		other = b
	}
	legacy, err := s.prepare(context.Background(), request(other, config, "dev"))
	if err != nil || legacy.Instance.ID != winner.ID || legacy.Instance.Plan.ManifestPath != winner.Plan.ManifestPath {
		t.Fatal("legacy logical-key reuse changed", legacy, err)
	}

}

func TestSceneLocationRecheckedBeforeExecution(t *testing.T) {
	s, root, config := sceneFixture(t)
	a, b := filepath.Join(root, "a"), filepath.Join(root, "b")
	for _, project := range []string{a, b} {
		if err := os.Mkdir(project, 0700); err != nil {
			t.Fatal(err)
		}
		writeFixture(t, project)
	}
	source, err := plan.Locate(b)
	if err != nil {
		t.Fatal(err)
	}
	moved, err := plan.Prepare(source, request(b, config, "dev").Plan)
	if err != nil {
		t.Fatal(err)
	}
	s.beforeExecution = func(id string) {
		if err := s.store.replaceSnapshot(id, moved); err != nil {
			t.Fatal(err)
		}
	}
	_, err = s.startScene(context.Background(), v1.SceneStartRequest{APIVersion: v1.Version, CWD: a, Plan: request(a, config, "dev").Plan})
	code(t, err, "conflict")
	if len(s.executions) != 0 {
		t.Fatal("named start joined/launched an execution at another location")
	}
	if err := s.store.db.View(func(tx *bolt.Tx) error {
		return tx.Bucket([]byte("instances")).ForEach(func(key, data []byte) error {
			var r record
			if err := decodeRecord(data, string(key), &r); err != nil {
				return err
			}
			if r.Instance.Status != v1.Prepared || r.Instance.Execution != nil {
				t.Fatal("runtime side effect before location recheck", r.Instance)
			}
			return nil
		})
	}); err != nil {
		t.Fatal(err)
	}
}

func TestSceneCorruptLocationCannotHideNearestProject(t *testing.T) {
	cases := map[string]func(*v1.Instance){
		"manifest-omitted":         func(i *v1.Instance) { i.Plan.ManifestPath = "" },
		"manifest-relative":        func(i *v1.Instance) { i.Plan.ManifestPath = "inner/backlot.json" },
		"manifest-escaping":        func(i *v1.Instance) { i.Plan.ManifestPath = "/unrelated/backlot.json" },
		"manifest-unclean":         func(i *v1.Instance) { i.Plan.ManifestPath = i.Checkout.Path + "/inner/../backlot.json" },
		"manifest-name":            func(i *v1.Instance) { i.Plan.ManifestPath = filepath.Join(i.Checkout.Path, "inner", "arbitrary.json") },
		"plan-checkout-omitted":    func(i *v1.Instance) { i.Plan.Checkout = "" },
		"plan-checkout-incoherent": func(i *v1.Instance) { i.Plan.Checkout = "/unrelated" },
		"checkout-omitted":         func(i *v1.Instance) { i.Checkout.Path = "" },
		"checkout-relative":        func(i *v1.Instance) { i.Checkout.Path = "relative" },
		"checkout-map-incoherent":  func(i *v1.Instance) { i.Checkout.Path = "/"; i.Plan.Checkout = "/" },
		"checkout-id-unrecorded":   func(i *v1.Instance) { i.Checkout.ID = strings.Repeat("a", 64) },
		"git-omitted":              func(i *v1.Instance) { i.Checkout.GitDirectory = "" },
		"git-relative":             func(i *v1.Instance) { i.Checkout.GitDirectory = "relative/.git" },
		"project-omitted":          func(i *v1.Instance) { i.Plan.Project = "" },
		"project-invalid":          func(i *v1.Instance) { i.Plan.Project = "Invalid" },
		"scene-omitted":            func(i *v1.Instance) { i.Plan.Scene = "" },
		"scene-invalid":            func(i *v1.Instance) { i.Plan.Scene = "../dev" },
		"destroyed-history": func(i *v1.Instance) {
			i.Status = v1.Destroyed
			i.Operation.Status = v1.Destroyed
			i.Plan.ManifestPath = ""
		},
		"disposable-history": func(i *v1.Instance) { i.Plan.Lifetime = v1.Disposable; i.Plan.ManifestPath = "" },
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
			text := strings.Replace(fixtureManifest, `"project":"fixture"`, `"project":"inner"`, 1)
			if err := os.WriteFile(filepath.Join(inner, "backlot.json"), []byte(text), 0600); err != nil {
				t.Fatal(err)
			}
			recorded := scenePrepare(t, s, inner, config)
			var original []byte
			if err := s.store.db.View(func(tx *bolt.Tx) error {
				original = append([]byte(nil), tx.Bucket([]byte("instances")).Get([]byte(recorded.Instance.ID))...)
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			// This fixture has no daemon or runtime owner. Restore is registered before
			// bounded offline state injection, and runs before the private DB closes.
			t.Cleanup(func() {
				if err := s.store.db.Update(func(tx *bolt.Tx) error {
					return tx.Bucket([]byte("instances")).Put([]byte(recorded.Instance.ID), original)
				}); err != nil {
					t.Error(err)
				}
			})
			if err := s.store.db.Update(func(tx *bolt.Tx) error {
				r, err := load(tx, recorded.Instance.ID)
				if err != nil {
					return err
				}
				damage(&r.Instance)
				return put(tx, "instances", recorded.Instance.ID, r)
			}); err != nil {
				t.Fatal(err)
			}
			for _, stop := range []bool{false, true} {
				response, err := s.scene(v1.SceneTarget{CWD: inner, Scene: "dev"}, stop)
				code(t, err, "state_corrupt")
				if response.Result != nil {
					t.Fatal("corrupt inner record exposed an outer target", response)
				}
			}
			unchanged, err := s.store.inspect(outer.Instance.ID)
			if err != nil || !reflect.DeepEqual(unchanged, outer.Instance) {
				t.Fatal("outer instance mutated under corrupt selection", err)
			}
		})
	}
}
