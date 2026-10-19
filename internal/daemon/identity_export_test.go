//go:build darwin || linux

package daemon

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLaunchGitCommit(t *testing.T) {
	repo := shortTemp(t)
	git(t, repo, "init", "-q", "-b", "main")
	checkout, err := identify(repo)
	if err != nil {
		t.Fatal(err)
	}
	if got := launchGitCommit(context.Background(), checkout); got != "" {
		t.Fatal("invented unborn revision", got)
	}
	git(t, repo, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "--allow-empty", "-qm", "First")
	ref, err := os.ReadFile(filepath.Join(repo, ".git/refs/heads/main"))
	if err != nil {
		t.Fatal(err)
	}
	want := strings.TrimSpace(string(ref))
	for _, mode := range []string{"branch", "packed", "linked-worktree", "detached"} {
		t.Run(mode, func(t *testing.T) {
			path := repo
			switch mode {
			case "packed":
				git(t, repo, "pack-refs", "--all")
			case "linked-worktree":
				path = filepath.Join(shortTemp(t), "linked")
				git(t, repo, "worktree", "add", "-q", "-b", "linked", path)
			case "detached":
				git(t, repo, "checkout", "-q", "--detach")
			}
			checkout, err := identify(path)
			if err != nil {
				t.Fatal(err)
			}
			if got := launchGitCommit(context.Background(), checkout); got != want {
				t.Fatalf("commit %q want %q", got, want)
			}
			// Branch/HEAD movement never authorizes another checkout identity.
			before := checkout.ID
			git(t, path, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "--allow-empty", "-qm", "Later")
			after, err := identify(path)
			if err != nil || after.ID != before {
				t.Fatal("HEAD affected ownership", err)
			}
			// Restore the exact ref for the next representation test.
			git(t, path, "reset", "--hard", want)
		})
	}
	for _, unknown := range []string{"", "ref: refs/heads/main", "main", strings.Repeat("z", 40)} {
		if immutableGitCommit(unknown) != "" {
			t.Fatal("historical non-revision accepted", unknown)
		}
	}
}
