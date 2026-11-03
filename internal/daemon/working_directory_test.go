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

func TestWorkingDirectoryRetainedCompatibility(t *testing.T) {
	var old plan.Snapshot
	// Pre-feature JSON has neither declaration nor effective directory fields.
	if err := json.Unmarshal([]byte(`{"plan":{"resources":{"data":{"kind":"directory"}},"components":[{"name":"init","runtime":"native","kind":"job","policy":"fresh-only","command":{"tool":"sh"},"resources":["data"],"initializes":["data"]}]}}`), &old); err != nil {
		t.Fatal(err)
	}
	next := old
	next.Plan.Components = append([]v1.PlannedComponent{}, old.Plan.Components...)
	next.Plan.Components[0].Command = &v1.Command{Tool: "sh", WorkingDirectory: "."}
	if err := safeRestart(old, next); err != nil {
		t.Fatal("explicit default invalidated old initializer", err)
	}
	next.Plan.Components[0].WorkingDirectory = "/project/package"
	code(t, safeRestart(old, next), "unsafe_drift")
	for _, snap := range []*plan.Snapshot{&old, &next} {
		snap.Plan.Components[0].Policy = v1.EachStart
		snap.Plan.Components[0].Initializes = nil
	}
	if err := safeRestart(old, next); err != nil {
		t.Fatal("ordinary command cwd drift rejected", err)
	}
}

func TestWorkingDirectoryRetargetBeforeLaunch(t *testing.T) {
	project := shortTemp(t)
	config := writeFixture(t, project)
	for _, name := range []string{"a", "b"} {
		if err := os.Mkdir(filepath.Join(project, name), 0700); err != nil {
			t.Fatal(err)
		}
	}
	alias := filepath.Join(project, "selected")
	if err := os.Symlink("a", alias); err != nil {
		t.Fatal(err)
	}
	source, err := plan.Locate(project)
	if err != nil {
		t.Fatal(err)
	}
	snap, err := plan.Prepare(source, v1.PlanRequest{ProjectPath: project, ConfigPath: config, Scene: "dev"})
	if err != nil {
		t.Fatal(err)
	}
	captured, err := captureSource(source)
	if err != nil {
		t.Fatal(err)
	}
	selected, err := plan.WorkingDirectory(source.ProjectPath, "selected")
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"b", t.TempDir(), "missing"} {
		if err := os.Remove(alias); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, alias); err != nil {
			t.Fatal(err)
		}
		s := &service{directory: shortTemp(t)}
		journal := runtimeRecord{}
		entry := &execution{source: captured}
		_, err := s.launch(context.Background(), "unused", "check", "/bin/sh", v1.Command{Tool: "sh", WorkingDirectory: "selected"}, selected, nil, snap, nil, entry, &journal)
		want := "invalid_working_directory"
		if target == "b" {
			want = "working_directory_changed"
		}
		code(t, err, want)
		if journal.LaunchPending || len(entry.groups) != 0 {
			t.Fatal("invalid cwd launched a guardian")
		}
	}
}
