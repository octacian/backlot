//go:build darwin || linux

package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	v1 "github.com/octacian/backlot/api/v1"
)

func TestWorkingDirectoryCLI(t *testing.T) {
	binary := buildFixtureBinary(t, t.TempDir(), "../../cmd/backlot")
	t.Setenv("BACKLOT_PROJECT_CONFIG_DIR", filepath.Join(shortTemp(t), "registry"))
	h := newNativeHarness(t, binary)
	config := filepath.Join(h.project, "machine.json")
	if err := os.WriteFile(config, []byte(`{"version":"backlot/v1"}`), 0600); err != nil {
		t.Fatal(err)
	}
	pkg := filepath.Join(h.project, "package")
	if err := os.Mkdir(pkg, 0700); err != nil {
		t.Fatal(err)
	}
	// A package-owned entry point consumes relative argv without a cd/spawn wrapper.
	if err := os.WriteFile(filepath.Join(pkg, "build.sh"), []byte("test -f build.sh || exit 22\nprintf 'package-build-ok\\n'\n"), 0600); err != nil {
		t.Fatal(err)
	}
	h.manifest.Tools["sh"] = "sh"
	h.manifest.Components["build"] = v1.Component{Kind: v1.Job, Runtime: v1.Native, Policy: v1.EachStart, Command: &v1.Command{Tool: "sh", Args: v1.StringArguments("build.sh"), WorkingDirectory: "package"}}
	h.manifest.Scenes["build"] = v1.Scene{Lifetime: v1.Disposable, Components: []string{"build"}, TerminalJob: "build"}
	h.write()
	invoke := func() ([]byte, error) {
		return exec.Command(binary, "run", "build", "--project", h.project, "--config", config, "--state-dir", h.dir, "--json").Output()
	}
	out, err := invoke()
	var success v1.InstanceResponse
	if err != nil || json.Unmarshal(out, &success) != nil || success.Instance.Status != v1.Succeeded {
		t.Fatalf("package command failed: %v %s", err, out)
	}
	expected, err := filepath.EvalSymlinks(pkg)
	if err != nil {
		t.Fatal(err)
	}
	if success.Instance.Plan.Components[0].WorkingDirectory != expected {
		t.Fatalf("wrong recorded cwd: %s", out)
	}
	t.Logf("real CLI success: status=%s cwd=%s", success.Instance.Status, pkg)
	build := h.manifest.Components["build"]
	build.Command.WorkingDirectory = "missing-package"
	h.manifest.Components["build"] = build
	h.write()
	out, err = invoke()
	var failure v1.ErrorResponse
	if err == nil || json.Unmarshal(out, &failure) != nil || failure.Error.Code != "invalid_working_directory" || !bytes.Contains(out, []byte("components.build.command.working_directory")) {
		t.Fatalf("missing directory did not fail before launch: %v %s", err, out)
	}
	t.Logf("real CLI failure: %s", out)
}

func TestWorkingDirectoryServiceProbesAndWorktrees(t *testing.T) {
	binary := buildFixtureBinary(t, t.TempDir(), "../../cmd/backlot")
	t.Setenv("BACKLOT_PROJECT_CONFIG_DIR", filepath.Join(shortTemp(t), "registry"))
	h := newNativeHarness(t, binary)
	config := filepath.Join(filepath.Dir(h.project), "machine.json")
	if err := os.WriteFile(config, []byte(`{"version":"backlot/v1"}`), 0600); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"package/service-marker", "probe/probe-marker", "root-marker"} {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(h.project, name)), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(h.project, name), []byte("fixture"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink("package", filepath.Join(h.project, "selected")); err != nil {
		t.Fatal(err)
	}
	h.manifest.Tools["sh"] = "sh"
	server := v1.Component{Kind: v1.Service, Runtime: v1.Native, Command: &v1.Command{Tool: "sh", WorkingDirectory: "selected", Args: v1.StringArguments("-c", "test -f service-marker || exit 23; pwd > started; exec sleep 60")}}
	h.manifest.Components = map[string]v1.Component{"server": server}
	h.manifest.Scenes = map[string]v1.Scene{"dev": {Lifetime: v1.Persistent, Components: []string{"server"}}}
	git(t, h.project, "init", "-q")
	var id string
	for _, tc := range []struct{ cwd, marker string }{{"", "service-marker"}, {".", "root-marker"}, {"probe", "probe-marker"}} {
		server.Readiness = &v1.Probe{Kind: "command", Timeout: "3s", Command: &v1.Command{Tool: "sh", WorkingDirectory: tc.cwd, Args: v1.StringArguments("-c", "test -f "+tc.marker+" && test -f "+map[string]string{"": "started", ".": "package/started", "probe": "../package/started"}[tc.cwd])}}
		h.manifest.Components["server"] = server
		h.write()
		req := h.request("dev")
		req.Plan.ConfigPath = config
		var response v1.InstanceResponse
		var err error
		if id == "" {
			response, err = h.cli.Run(context.Background(), req)
		} else {
			req.InstanceID = id
			response, err = h.cli.Restart(context.Background(), req)
		}
		if err != nil {
			t.Fatal("probe cwd failed", tc.cwd, err)
		}
		id = response.Instance.ID
		h.state(id, v1.RuntimeReady)
	}
	// A real linked worktree runs the same relative declaration in its own package.
	git(t, h.project, "add", "backlot.json", "package/service-marker", "probe/probe-marker", "root-marker", "selected")
	git(t, h.project, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "-qm", "fixture")
	peer := filepath.Join(filepath.Dir(h.project), "peer")
	git(t, h.project, "worktree", "add", "--detach", peer, "HEAD")
	req := h.request("dev")
	req.Plan.ProjectPath, req.Plan.ConfigPath = peer, config
	second, err := h.cli.Run(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	second = h.state(second.Instance.ID, v1.RuntimeReady)
	first := h.state(id, v1.RuntimeReady)
	if first.Instance.Checkout.ID == second.Instance.Checkout.ID {
		t.Fatal("worktrees shared identity")
	}
	for _, project := range []string{h.project, peer} {
		want, err := filepath.EvalSymlinks(filepath.Join(project, "package"))
		if err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(filepath.Join(project, "package", "started"))
		if err != nil || strings.TrimSpace(string(data)) != want {
			t.Fatalf("wrong actual worktree cwd: %s %v", data, err)
		}
	}
	// Retargeting a contained alias changes the effective cwd without editing YAML/JSON.
	if err := os.Mkdir(filepath.Join(h.project, "next"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(h.project, "next", "service-marker"), []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(h.project, "selected")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("next", filepath.Join(h.project, "selected")); err != nil {
		t.Fatal(err)
	}
	req.Plan.ProjectPath = h.project
	joined, err := h.cli.Run(context.Background(), req)
	if err != nil || !joined.ManifestDrift || joined.Instance.Plan.ManifestDigest != first.Instance.Plan.ManifestDigest || joined.Instance.Plan.Components[0].WorkingDirectory != first.Instance.Plan.Components[0].WorkingDirectory {
		t.Fatal("healthy join hid symlink drift or changed accepted cwd", err)
	}
	req.InstanceID = id
	restarted, err := h.cli.Restart(context.Background(), req)
	if err != nil {
		t.Fatal("compatible cwd restart rejected", err)
	}
	restarted = h.state(restarted.Instance.ID, v1.RuntimeReady)
	want := filepath.Join(filepath.Dir(first.Instance.Plan.ManifestPath), "next")
	if restarted.Instance.Plan.Components[0].WorkingDirectory != want || restarted.Instance.ID != id {
		t.Fatal("restart did not accept new cwd with retained identity")
	}
	data, err := os.ReadFile(filepath.Join(want, "started"))
	if err != nil || strings.TrimSpace(string(data)) != want {
		t.Fatalf("restart launched wrong actual cwd: %s %v", data, err)
	}

}
