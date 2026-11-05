//go:build darwin || linux

package daemon

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	v1 "github.com/octacian/backlot/api/v1"
	"github.com/octacian/backlot/internal/plan"
)

func TestPrepareOutputDirectories(t *testing.T) {
	for _, mode := range []string{"empty", "absent", "opt-out", "cancelled", "symlink", "internal-symlink", "file", "private-state", "git-marker", "git-directory", "git-common", "invalid-common", "changed-project", "unsafe-relative", "root", "all-targets-preflight"} {
		t.Run(mode, func(t *testing.T) {
			project := shortTemp(t)
			writeFixture(t, project)
			put := func(path, data string) {
				t.Helper()
				if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(data), 0600); err != nil {
					t.Fatal(err)
				}
			}
			put(filepath.Join(project, "reports", "stale"), "old")
			scratch := "reports"
			private := shortTemp(t)
			switch mode {
			case "git-marker":
				put(filepath.Join(project, ".git", "HEAD"), "ref: refs/heads/main\n")
				scratch = ".git"
			case "git-directory", "git-common", "invalid-common":
				put(filepath.Join(project, "admin", "HEAD"), "ref: refs/heads/main\n")
				put(filepath.Join(project, ".git"), "gitdir: admin\n")
				scratch = "admin"
				if mode != "git-directory" {
					if err := os.Mkdir(filepath.Join(project, "common"), 0700); err != nil {
						t.Fatal(err)
					}
					common := "../common"
					if mode == "invalid-common" {
						common = "../missing"
					}
					put(filepath.Join(project, "admin", "commondir"), common)
					scratch = "common"
				}
			case "symlink", "internal-symlink":
				destination := private
				if mode == "internal-symlink" {
					destination = filepath.Join(project, "reports")
				}
				if err := os.Symlink(destination, filepath.Join(project, "alias")); err != nil {
					t.Fatal(err)
				}
				scratch = "alias/subdir"
			case "file":
				put(filepath.Join(project, "file"), "keep")
				scratch = "file"
			case "private-state":
				private = filepath.Join(project, "private")
				put(filepath.Join(private, "data", "retained"), "keep")
				scratch = "private"
			case "absent":
				scratch = "missing/nested"
			case "unsafe-relative":
				scratch = "../reports"
			case "root":
				scratch = "."
			}
			source, err := plan.Locate(project)
			if err != nil {
				t.Fatal(err)
			}
			identity, err := captureSource(source)
			if err != nil {
				t.Fatal(err)
			}
			outputs := map[string]v1.Output{"reports": {Path: scratch, ConcurrencyGroup: "reports", EmptyBeforeExecution: mode != "opt-out"}}
			if mode == "all-targets-preflight" {
				outputs["invalid"] = v1.Output{Path: ".git", ConcurrencyGroup: "git", EmptyBeforeExecution: true}
			}
			snapshot := plan.Snapshot{Manifest: v1.Manifest{Outputs: outputs}, Plan: v1.PlanResponse{Outputs: outputs}}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if mode == "cancelled" {
				cancel()
			}
			if mode == "changed-project" {
				if err := os.Rename(project, project+"-old"); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = os.RemoveAll(project + "-old") })
				if err := os.Mkdir(project, 0700); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "empty" {
				// Tree links are unlinked, never traversed to their external referent.
				put(filepath.Join(private, "keep"), "outside")
				if err := os.Symlink(private, filepath.Join(project, "reports", "link")); err != nil {
					t.Fatal(err)
				}
				outputs["alias"] = outputs["reports"]
			}
			err = (&service{directory: private}).prepareOutputDirectories(ctx, snapshot, identity)
			success := mode == "empty" || mode == "absent" || mode == "opt-out"
			if (err == nil) != success {
				t.Fatalf("preparation error=%v, want success=%v", err, success)
			}
			if mode == "empty" || mode == "absent" {
				entries, err := os.ReadDir(filepath.Join(project, scratch))
				if err != nil || len(entries) != 0 {
					t.Fatal("output not empty", entries, err)
				}
			}
			if mode != "empty" && mode != "changed-project" {
				data, err := os.ReadFile(filepath.Join(project, "reports", "stale"))
				if err != nil || string(data) != "old" {
					t.Fatal("preflight changed scratch contents", string(data), err)
				}
			}
			if mode == "empty" {
				data, err := os.ReadFile(filepath.Join(private, "keep"))
				if err != nil || string(data) != "outside" {
					t.Fatal("followed tree symlink", err)
				}
			}
		})
	}
}

func TestEmptyOutputRestartCompatibility(t *testing.T) {
	var old plan.Snapshot
	if err := json.Unmarshal([]byte(`{"plan":{"outputs":{"reports":{"path":"reports","concurrency_group":"reports"}},"resources":{"data":{"kind":"directory"}},"components":[{"name":"init","runtime":"native","kind":"job","policy":"fresh-only","outputs":["reports"],"resources":["data"],"initializes":["data"]}]}}`), &old); err != nil {
		t.Fatal(err)
	}
	if old.Plan.Outputs["reports"].EmptyBeforeExecution {
		t.Fatal("historical output opted in")
	}
	next := old
	next.Plan.Outputs = map[string]v1.Output{"reports": {Path: "reports", ConcurrencyGroup: "reports", EmptyBeforeExecution: true}}
	if err := safeRestart(old, next); err != nil {
		t.Fatal("opt-in invalidated retained initialization", err)
	}
	if err := safeRestart(next, old); err != nil {
		t.Fatal("opt-out invalidated retained initialization", err)
	}
}
