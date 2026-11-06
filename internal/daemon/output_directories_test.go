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
			snapshot := plan.Snapshot{Manifest: v1.Manifest{Outputs: outputs}, Plan: v1.PlanResponse{ManifestPath: source.ManifestPath, Checkout: identity.checkout.Path, Outputs: outputs}}
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
			state, err := openStore(shortTemp(t))
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = state.db.Close() }()
			err = (&service{directory: private, store: state}).prepareOutputDirectories(ctx, snapshot, identity, "")
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

func TestPrepareInitiallyAbsentOutputAliases(t *testing.T) {
	for _, mode := range []string{"nested", "mixed-parent", "mixed-child", "changed-group", "same-directory", "git-anchor"} {
		t.Run(mode, func(t *testing.T) {
			f := newControlledExpiry(t)
			// Observe this fixture's filesystem, without assuming macOS is case-insensitive.
			_, err := os.Stat(filepath.Join(f.root, "BACKLOT.JSON"))
			caseAliases := err == nil
			if err != nil && !os.IsNotExist(err) {
				t.Fatal(err)
			}
			stale := filepath.Join(f.root, "aaa", "stale")
			if err := os.Mkdir(filepath.Dir(stale), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(stale, []byte("keep on rejection"), 0600); err != nil {
				t.Fatal(err)
			}
			outputs := map[string]v1.Output{
				"stale":  {Path: "aaa", ConcurrencyGroup: "aaa", EmptyBeforeExecution: true},
				"parent": {Path: "reports", ConcurrencyGroup: "reports", EmptyBeforeExecution: true},
				"child":  {Path: "REPORTS/nested", ConcurrencyGroup: "reports", EmptyBeforeExecution: true},
			}
			switch mode {
			case "mixed-parent":
				outputs["parent"] = v1.Output{Path: "reports", ConcurrencyGroup: "reports"}
			case "mixed-child":
				outputs["child"] = v1.Output{Path: "REPORTS/nested", ConcurrencyGroup: "reports"}
			case "changed-group":
				outputs["child"] = v1.Output{Path: "REPORTS/nested", ConcurrencyGroup: "changed", EmptyBeforeExecution: true}
			case "same-directory":
				outputs["child"] = v1.Output{Path: "REPORTS", ConcurrencyGroup: "reports", EmptyBeforeExecution: true}
			case "git-anchor":
				delete(outputs, "parent")
				outputs["child"] = v1.Output{Path: ".GIT/nested", ConcurrencyGroup: "reports", EmptyBeforeExecution: true}
			}
			selected := map[string]v1.Output{}
			names := map[string]bool{}
			for name, output := range outputs {
				if output.EmptyBeforeExecution {
					selected[name] = output
					names[name] = true
				}
			}
			root, err := filepath.EvalSymlinks(f.root)
			if err != nil {
				t.Fatal(err)
			}
			if err := plan.ValidateOutputs(root, outputs, names); err != nil {
				t.Fatal("offline validation of absent paths", err)
			}
			for _, path := range []string{"reports", "REPORTS", ".GIT"} {
				if _, err := os.Stat(filepath.Join(f.root, path)); !os.IsNotExist(err) {
					t.Fatal("offline planning materialized output", path, err)
				}
			}
			source, err := plan.Locate(f.root)
			if err != nil {
				t.Fatal(err)
			}
			identity, err := captureSource(source)
			if err != nil {
				t.Fatal(err)
			}
			snapshot := plan.Snapshot{Manifest: v1.Manifest{Outputs: outputs}, Plan: v1.PlanResponse{ManifestPath: source.ManifestPath, Checkout: identity.checkout.Path, Outputs: selected}}
			err = f.s.prepareOutputDirectories(context.Background(), snapshot, identity, "")
			reject := caseAliases && mode != "same-directory"
			if (err != nil) != reject {
				t.Fatalf("caseAliases=%v reject=%v error=%v", caseAliases, reject, err)
			}
			if reject {
				data, err := os.ReadFile(stale)
				if err != nil || string(data) != "keep on rejection" {
					t.Fatal("rejection removed existing contents", string(data), err)
				}
				return
			}
			for _, output := range selected {
				entries, err := os.ReadDir(filepath.Join(f.root, output.Path))
				if err != nil || len(entries) != 0 {
					t.Fatal("selected output missing or not empty", output.Path, entries, err)
				}
			}
			if mode == "same-directory" {
				same, overlap, err := plan.OutputOverlap(filepath.Join(f.root, "reports"), filepath.Join(f.root, "REPORTS"))
				if err != nil || same != caseAliases || overlap != caseAliases {
					t.Fatal("same-directory alias identity", same, overlap, err)
				}
			}
		})
	}
}

func TestMaterializedOutputAliasesFenceRetainedConsumers(t *testing.T) {
	for _, test := range []struct{ mode, taskID string }{
		{"active", ""}, {"kept", ""}, {"uncertain", ""},
		{"active", "task-self"}, {"kept", "task-self"}, {"uncertain", "task-self"},
	} {
		mode := test.mode
		t.Run(mode+test.taskID, func(t *testing.T) {
			f := newControlledExpiry(t)
			prepared, err := f.s.prepare(context.Background(), request(f.root, f.config, "test"))
			if err != nil {
				t.Fatal(err)
			}
			snapshot, err := f.s.store.snapshot(prepared.Instance.ID)
			if err != nil {
				t.Fatal(err)
			}
			snapshot.Plan.Outputs = map[string]v1.Output{"old": {Path: "reports/nested", ConcurrencyGroup: "old"}}
			if err := f.s.store.replaceSnapshot(prepared.Instance.ID, snapshot); err != nil {
				t.Fatal(err)
			}
			status := v1.Starting
			result := &v1.ExecutionResult{Attempt: newID()}
			if mode != "active" {
				status = v1.Failed
				result.Kept = mode == "kept"
				if mode == "uncertain" {
					result.CleanupFailure = "fixture unjoined consumer"
				}
			}
			if _, err := f.s.store.execution(prepared.Instance.ID, status, result); err != nil {
				t.Fatal(err)
			}
			outputs := map[string]v1.Output{
				"new":   {Path: "REPORTS", ConcurrencyGroup: "changed", EmptyBeforeExecution: true},
				"stale": {Path: "aaa", ConcurrencyGroup: "aaa", EmptyBeforeExecution: true},
			}
			next := snapshot
			next.Plan.Outputs = outputs
			next.Manifest.Outputs = outputs
			if err := f.s.store.outputConflict(next.Plan); err != nil {
				t.Fatal("initially absent paths should defer alias resolution", err)
			}
			stale := filepath.Join(f.root, "aaa", "stale")
			if err := os.Mkdir(filepath.Dir(stale), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(stale, []byte("keep"), 0600); err != nil {
				t.Fatal(err)
			}
			source, err := plan.Locate(f.root)
			if err != nil {
				t.Fatal(err)
			}
			identity, err := captureSource(source)
			if err != nil {
				t.Fatal(err)
			}
			err = f.s.prepareOutputDirectories(context.Background(), next, identity, test.taskID)
			_, aliasErr := os.Stat(filepath.Join(f.root, "reports"))
			caseAliases := aliasErr == nil
			if (err != nil) != caseAliases {
				t.Fatalf("caseAliases=%v preparation error=%v", caseAliases, err)
			}
			if caseAliases {
				if data, err := os.ReadFile(stale); err != nil || string(data) != "keep" {
					t.Fatal("consumer conflict removed contents", string(data), err)
				}
			}
			// The synthetic observation owns no process; remove its fence for fixture cleanup.
			if _, err := f.s.store.execution(prepared.Instance.ID, v1.Failed, &v1.ExecutionResult{Attempt: result.Attempt}); err != nil {
				t.Fatal(err)
			}
		})
	}
}
