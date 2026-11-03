package plan

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	v1 "github.com/octacian/backlot/api/v1"
)

func TestWorkingDirectoryBoundaries(t *testing.T) {
	root, err := canonical(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(root, "file"), []byte("file"))
	for _, name := range []string{"package", "package/nested", "other"} {
		if err := os.MkdirAll(filepath.Join(root, name), 0700); err != nil {
			t.Fatal(err)
		}
	}
	for name, target := range map[string]string{"alias": "package", "root": ".", "escape": t.TempDir(), "broken": "missing"} {
		if err := os.Symlink(target, filepath.Join(root, name)); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct{ path, want string }{{"", ""}, {".", ""}, {"root", ""}, {"package", filepath.Join(root, "package")}, {"alias", filepath.Join(root, "package")}, {"package/nested", filepath.Join(root, "package/nested")}} {
		got, err := WorkingDirectory(root, tc.path)
		if err != nil || got != tc.want {
			t.Fatalf("%q: got %q, %v; want %q", tc.path, got, err, tc.want)
		}
	}
	for _, path := range []string{"/tmp", "..", "../outside", "package/../other", "./package", "package/", "package//nested", "C:/dir", `package\nested`, "nul\x00", "missing", "file", "escape", "broken"} {
		if _, err := WorkingDirectory(root, path); err == nil {
			t.Fatalf("accepted invalid directory %q", path)
		}
	}
}

func TestWorkingDirectoryPlanning(t *testing.T) {
	noDefaultConfig(t)
	root := fixture(t)
	for _, dir := range []string{"package", "probe"} {
		if err := os.Mkdir(filepath.Join(root, dir), 0700); err != nil {
			t.Fatal(err)
		}
	}
	// The selected tool would leave a witness if offline planning executed it.
	write(t, filepath.Join(root, "tool"), []byte("#!/bin/sh\ntouch '"+filepath.Join(root, "executed")+"'\n"))
	if err := os.Chmod(filepath.Join(root, "tool"), 0700); err != nil {
		t.Fatal(err)
	}
	m := v1.Manifest{Version: v1.ManifestVersion, Project: "cwd", Tools: map[string]string{"tool": "./tool"}, Outputs: map[string]v1.Output{"out": {Path: "out", ConcurrencyGroup: "out"}}, Components: map[string]v1.Component{}, Scenes: map[string]v1.Scene{"dev": {Lifetime: v1.Persistent, Components: []string{"server"}}}}
	server := v1.Component{Kind: v1.Service, Runtime: v1.Native, Command: &v1.Command{Tool: "tool", WorkingDirectory: "package"}, Outputs: []string{"out"}, Artifacts: map[string]v1.ArtifactSource{"report": {Path: "package/report"}}, Environment: v1.Environment{Seeds: []string{"seed.env"}, Assign: map[string]v1.Value{"OUT": {Ref: &v1.Reference{Kind: "output", Name: "out", Field: "path"}}}}, Readiness: &v1.Probe{Kind: "command", Command: &v1.Command{Tool: "tool"}, Timeout: "1s"}}
	write(t, filepath.Join(root, "seed.env"), []byte("FROM_ROOT=root\n"))
	config := filepath.Join(root, "machine.json")
	write(t, config, []byte(`{"version":"backlot/v1","docker":{"endpoint":"unix:///tmp/not-contacted.sock"}}`))
	resolve := func(runtime v1.Runtime, override string) v1.PlanResponse {
		t.Helper()
		c := server
		c.Readiness = &v1.Probe{Kind: "command", Command: &v1.Command{Tool: "tool", WorkingDirectory: override}, Timeout: "1s"}
		c.Runtime = runtime
		if runtime == v1.Container {
			image := "fixture"
			c.Command, c.Image = nil, &v1.Value{Literal: &image}
			c.Artifacts = nil
		}
		m.Components["server"] = c
		data, err := json.Marshal(m)
		if err != nil {
			t.Fatal(err)
		}
		write(t, filepath.Join(root, "backlot.json"), data)
		if err := os.Remove(filepath.Join(root, "backlot.yaml")); err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
		p, err := Resolve(v1.PlanRequest{ProjectPath: root, ConfigPath: config, Scene: "dev"})
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	for _, runtime := range []v1.Runtime{v1.Native, v1.Container} {
		for _, override := range []string{"", ".", "probe"} {
			p := resolve(runtime, override)
			c := p.Components[0]
			want := ""
			if override == "probe" {
				want = filepath.Join(root, "probe")
			} else if override == "" && runtime == v1.Native {
				want = filepath.Join(root, "package")
			}
			if c.Readiness.WorkingDirectory != want || c.Readiness.Executable != filepath.Join(root, "tool") {
				t.Fatalf("%s override %q: wrong probe selection: %+v", runtime, override, c.Readiness)
			}
			if c.Readiness.Command.WorkingDirectory != override {
				t.Fatal("planner rewrote declaration")
			}
			if runtime == v1.Native && (c.WorkingDirectory != filepath.Join(root, "package") || c.Executable != filepath.Join(root, "tool") || c.Artifacts["report"].Path != "package/report") {
				t.Fatal("cwd rebased executable or artifact anchors")
			}
			if p.Outputs["out"].Path != "out" || *c.Environment["OUT"].Literal != filepath.Join(root, "out") {
				t.Fatal("cwd rebased output anchors")
			}
		}
	}
	if _, err := os.Stat(filepath.Join(root, "executed")); !os.IsNotExist(err) {
		t.Fatal("offline planning executed the selected tool")
	}
}

func TestWorkingDirectoryDrift(t *testing.T) {
	base := v1.PlanResponse{ManifestDigest: "same", Components: []v1.PlannedComponent{{Name: "server", WorkingDirectory: "/project/a", Readiness: &v1.PlannedProbe{WorkingDirectory: "/project/a"}}}}
	for _, probe := range []bool{false, true} {
		next := base
		next.Components = append([]v1.PlannedComponent{}, base.Components...)
		if probe {
			next.Components[0].Readiness = &v1.PlannedProbe{WorkingDirectory: "/project/b"}
		} else {
			next.Components[0].WorkingDirectory = "/project/b"
		}
		if !ManifestDrift(base, next) {
			t.Fatal("unchanged manifest hid effective cwd drift")
		}
	}
	if ManifestDrift(base, base) {
		t.Fatal("unchanged selections drifted")
	}
}
