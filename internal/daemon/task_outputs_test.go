//go:build darwin || linux

package daemon

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	v1 "github.com/octacian/backlot/api/v1"
	bolt "go.etcd.io/bbolt"
)

func TestTaskOutputAcceptedAndCurrentAliases(t *testing.T) {
	f := newControlledExpiry(t)
	parent, err := f.s.prepare(context.Background(), request(f.root, f.config, "dev"))
	if err != nil {
		t.Fatal(err)
	}
	root := parent.Instance.Plan.Checkout
	for _, name := range []string{"original", "retarget", "disjoint"} {
		if err := os.Mkdir(filepath.Join(root, name), 0700); err != nil {
			t.Fatal(err)
		}
	}
	alias := filepath.Join(root, "alias")
	if err := os.Symlink("original", alias); err != nil {
		t.Fatal(err)
	}
	p := parent.Instance.Plan
	p.Outputs = map[string]v1.Output{"owned": {Path: "alias", ConcurrencyGroup: "owner"}}
	paths, err := outputPaths(p)
	if err != nil {
		t.Fatal(err)
	}
	attempt := newID()
	if err := f.s.store.db.Update(func(tx *bolt.Tx) error {
		r, err := load(tx, parent.Instance.ID)
		if err != nil {
			return err
		}
		r.Instance.Plan = p
		r.Instance.Status = v1.RuntimeReady
		r.Instance.Operation.Status = v1.RuntimeReady
		r.Instance.Execution = &v1.ExecutionResult{Attempt: attempt}
		if err := put(tx, "runtime", r.Instance.ID, runtimeRecord{Attempt: attempt, OutputPaths: paths}); err != nil {
			return err
		}
		return put(tx, "instances", r.Instance.ID, r)
	}); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(alias); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("retarget", alias); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		path, group string
		conflict    bool
	}{{"original/nested", "renamed", true}, {"retarget/nested", "renamed", true}, {"disjoint", "owner", true}, {"disjoint", "different", false}} {
		next := p
		next.Outputs = map[string]v1.Output{"new": {Path: test.path, ConcurrencyGroup: test.group}}
		paths, err := outputPaths(next)
		if err != nil {
			t.Fatal(err)
		}
		err = f.s.store.db.View(func(tx *bolt.Tx) error { return outputConflictTx(tx, next, paths, "", true) })
		if (err != nil) != test.conflict {
			t.Fatalf("%s/%s: %v", test.path, test.group, err)
		}
	}
	// Avoid synthetic live process claims during ordinary fixture cleanup.
	if _, err := f.s.store.execution(parent.Instance.ID, v1.Stopped, &v1.ExecutionResult{Attempt: attempt}); err != nil {
		t.Fatal(err)
	}
}
