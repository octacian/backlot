//go:build darwin || linux

package daemon

import (
	"github.com/octacian/backlot/internal/plan"
	bolt "go.etcd.io/bbolt"
	"os"
	"path/filepath"
	"testing"
)

func TestSharedProjectIdentity(t *testing.T) {
	root := shortTemp(t)
	repo := filepath.Join(root, "repo")
	if err := os.Mkdir(repo, 0700); err != nil {
		t.Fatal(err)
	}
	git(t, repo, "init", "-q", "-b", "main")
	git(t, repo, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "--allow-empty", "-qm", "Fixture")
	identify := func(checkout, project string) sharedProjectIdentity {
		t.Helper()
		id, err := identifyShared(plan.Source{CheckoutPath: checkout, ProjectPath: project}, "same-project")
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	original := identify(repo, repo)
	linked := filepath.Join(root, "linked")
	git(t, repo, "worktree", "add", "-q", "-b", "linked", linked)
	if next := identify(linked, linked); next != original {
		t.Fatal("linked worktrees did not share common-directory identity")
	}
	clone := filepath.Join(root, "clone")
	git(t, root, "clone", "-q", repo, clone)
	if next := identify(clone, clone); next.ID == original.ID {
		t.Fatal("independent clone merged")
	}
	for _, checkout := range []string{repo, linked} {
		if err := os.Mkdir(filepath.Join(checkout, "nested"), 0700); err != nil {
			t.Fatal(err)
		}
	}
	nested := identify(repo, filepath.Join(repo, "nested"))
	if nested.ID == original.ID || identify(linked, filepath.Join(linked, "nested")) != nested {
		t.Fatal("relative project location not part of shared identity")
	}
	nongit := filepath.Join(root, "plain")
	if err := os.Mkdir(nongit, 0700); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(root, "alias")
	if err := os.Symlink(nongit, alias); err != nil {
		t.Fatal(err)
	}
	if identify(nongit, nongit) != identify(alias, alias) {
		t.Fatal("non-Git canonical aliases diverged")
	}
	state, err := openStore(shortTemp(t))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = state.db.Close() }()
	register := func(id sharedProjectIdentity) error {
		return state.db.Update(func(tx *bolt.Tx) error { return registerSharedProject(tx, id) })
	}
	if err = register(original); err != nil {
		t.Fatal(err)
	}
	moved := original
	moved.Anchor = filepath.Join(root, "moved")
	code(t, register(moved), "project_moved")
	replaced := original
	replaced.ID = hash("replacement")
	code(t, register(replaced), "project_replaced")
	if err = register(original); err != nil {
		t.Fatal("rejection mutated original identity", err)
	}
}
