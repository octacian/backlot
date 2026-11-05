//go:build darwin || linux

package daemon

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	v1 "github.com/octacian/backlot/api/v1"
)

func TestEmptyOutputDirectoryCLI(t *testing.T) {
	binary := buildFixtureBinary(t, t.TempDir(), "../../cmd/backlot")
	t.Setenv("BACKLOT_PROJECT_CONFIG_DIR", filepath.Join(shortTemp(t), "registry"))
	h := newNativeHarness(t, binary)
	config := filepath.Join(h.project, "machine.json")
	if err := os.WriteFile(config, []byte(`{"version":"backlot/v1"}`), 0600); err != nil {
		t.Fatal(err)
	}
	h.manifest.Tools["sh"] = "/bin/sh"
	h.manifest.Outputs = map[string]v1.Output{"reports": {Path: "reports", ConcurrencyGroup: "reports", EmptyBeforeExecution: true}}
	job := v1.Component{Kind: v1.Job, Runtime: v1.Native, Policy: v1.EachStart, Outputs: []string{"reports"}, Artifacts: map[string]v1.ArtifactSource{"reports": {Path: "reports"}}, Command: &v1.Command{Tool: "sh", Args: v1.StringArguments("-c", "echo original > reports/report")}}
	h.manifest.Components = map[string]v1.Component{"job": job}
	h.manifest.Scenes = map[string]v1.Scene{"test": {Lifetime: v1.Disposable, Components: []string{"job"}, TerminalJob: "job"}}
	invoke := func(wantExit int) v1.Instance {
		t.Helper()
		h.write()
		out, err := exec.Command(binary, "run", "test", "--project", h.project, "--config", config, "--state-dir", h.dir, "--json").Output()
		code := 0
		if err != nil {
			if exit, ok := err.(*exec.ExitError); ok {
				code = exit.ExitCode()
			} else {
				t.Fatal(err)
			}
		}
		var response v1.InstanceResponse
		if json.Unmarshal(out, &response) != nil || code != wantExit || response.Instance.Execution == nil {
			t.Fatalf("CLI exit=%d err=%v result=%s", code, err, out)
		}
		result := response.Instance.Execution
		if result.TerminalExitCode == nil || *result.TerminalExitCode != wantExit || result.CollectionFailure != "" || result.CleanupFailure != "" || len(result.Artifacts) != 1 {
			t.Fatalf("independent outcomes lost: %+v", result)
		}
		if response.Instance.Plan.Outputs["reports"].EmptyBeforeExecution != h.manifest.Outputs["reports"].EmptyBeforeExecution {
			t.Fatal("opt-in lost through API/snapshot")
		}
		return response.Instance
	}
	first := invoke(0)
	if first.Status != v1.Succeeded {
		t.Fatal(first.Status)
	}
	archive := filepath.Join(first.Execution.Artifacts[0].Path, "reports", "report")
	check := func(path, want string) {
		t.Helper()
		got, err := os.ReadFile(path)
		if err != nil || string(got) != want {
			t.Fatalf("%s: %q %v", path, got, err)
		}
	}
	check(archive, "original\n")
	job.Command.Args = v1.StringArguments("-c", "echo partial > reports/partial; exit 23")
	h.manifest.Components["job"] = job
	second := invoke(23)
	if second.Status != v1.Failed {
		t.Fatal(second.Status)
	}
	for _, root := range []string{filepath.Join(h.project, "reports"), filepath.Join(second.Execution.Artifacts[0].Path, "reports")} {
		if _, err := os.Stat(filepath.Join(root, "report")); !os.IsNotExist(err) {
			t.Fatal("stale report retained", root, err)
		}
		check(filepath.Join(root, "partial"), "partial\n")
	}
	check(archive, "original\n")
	t.Logf("CLI/API/daemon: A=%s succeeded; B=%s failed exit=23, only current partial evidence; A archive unchanged", first.ID, second.ID)
	output := h.manifest.Outputs["reports"]
	output.EmptyBeforeExecution = false
	h.manifest.Outputs["reports"] = output
	job.Command.Args = v1.StringArguments("-c", "echo next > reports/next")
	h.manifest.Components["job"] = job
	third := invoke(0)
	check(filepath.Join(third.Execution.Artifacts[0].Path, "reports", "partial"), "partial\n")
	check(archive, "original\n")

}

func TestEmptyOutputPersistentLifecycle(t *testing.T) {
	binary := buildFixtureBinary(t, t.TempDir(), "../../cmd/backlot")
	t.Setenv("BACKLOT_PROJECT_CONFIG_DIR", filepath.Join(shortTemp(t), "registry"))
	h := newNativeHarness(t, binary)
	h.manifest.Tools["sh"] = "/bin/sh"
	h.manifest.Resources["data"] = v1.Resource{Kind: "directory"}
	h.manifest.Outputs = map[string]v1.Output{"reports": {Path: "reports", ConcurrencyGroup: "reports"}}
	init := v1.Component{Kind: v1.Job, Runtime: v1.Native, Policy: v1.FreshOnly, Resources: []string{"data"}, Initializes: []string{"data"}, Outputs: []string{"reports"}, Command: &v1.Command{Tool: "sh", Args: v1.StringArguments("-c", "mkdir -p reports; echo initialized > reports/initialized")}}
	h.manifest.Components["init"] = init
	server := h.manifest.Components["server"]
	server.Outputs = []string{"reports"}
	server.DependsOn = []v1.Dependency{{Component: "init", Condition: v1.Completed}}
	h.manifest.Components["server"] = server
	scene := h.manifest.Scenes["dev"]
	scene.Components = []string{"init", "server"}
	scene.Resources = []string{"port", "data"}
	h.manifest.Scenes = map[string]v1.Scene{"dev": scene}
	h.write()
	first := h.run("dev")
	h.state(first.Instance.ID, v1.RuntimeReady)
	marker := filepath.Join(h.project, "reports", "initialized")
	if _, err := os.Stat(marker); err != nil {
		t.Fatal(err)
	}
	output := h.manifest.Outputs["reports"]
	output.EmptyBeforeExecution = true
	h.manifest.Outputs["reports"] = output
	h.write()
	prepared, err := h.cli.Prepare(context.Background(), v1.PrepareRequest{APIVersion: v1.Version, Plan: h.request("dev").Plan})
	if err != nil || prepared.Instance.ID != first.Instance.ID {
		t.Fatal("metadata prepare failed", err)
	}
	joined := h.run("dev")
	if !joined.ManifestDrift || joined.Instance.Execution.Attempt != first.Instance.Execution.Attempt || joined.Instance.Plan.Outputs["reports"].EmptyBeforeExecution {
		t.Fatal("healthy join changed accepted work")
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatal("join/metadata prepare deleted report", err)
	}
	request := h.request("dev")
	request.InstanceID = first.Instance.ID
	restarted, err := h.cli.Restart(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	ready := h.state(restarted.Instance.ID, v1.RuntimeReady)
	if ready.Instance.Execution.Attempt == first.Instance.Execution.Attempt || !ready.Instance.Plan.Outputs["reports"].EmptyBeforeExecution {
		t.Fatal("restart did not accept opt-in")
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("restart did not clear output or reran initializer", err)
	}
	if ready.Instance.Execution.Components[0].Name != "init" || ready.Instance.Execution.Components[0].Status != "skipped" {
		t.Fatal("fresh-only stamp lost", ready.Instance.Execution.Components)
	}
	if _, err := h.cli.StopExecution(context.Background(), first.Instance.ID); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(marker, []byte("recovery must preserve"), 0600); err != nil {
		t.Fatal(err)
	}
	h.close()
	h.start()
	if _, err := os.Stat(marker); err != nil {
		t.Fatal("daemon recovery replayed preparation", err)
	}
	next := h.run("dev")
	h.state(next.Instance.ID, v1.RuntimeReady)
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("explicit new start did not prepare", err)
	}
}

func TestTaskEmptyOutputDirectories(t *testing.T) {
	binary := buildFixtureBinary(t, t.TempDir(), "../../cmd/backlot")
	h := newNativeHarness(t, binary)
	h.manifest.Tools["sh"] = "/bin/sh"
	h.manifest.Outputs = map[string]v1.Output{
		"parent":  {Path: "parent-output", ConcurrencyGroup: "parent", EmptyBeforeExecution: true},
		"reports": {Path: "reports", ConcurrencyGroup: "reports", EmptyBeforeExecution: true},
	}
	server := h.manifest.Components["server"]
	server.Outputs = []string{"parent"}
	h.manifest.Components["server"] = server
	h.manifest.Components["report"] = v1.Component{Kind: v1.Job, Runtime: v1.Native, Policy: v1.EachStart,
		Outputs: []string{"reports"}, Artifacts: map[string]v1.ArtifactSource{"reports": {Path: "reports"}},
		Command: &v1.Command{Tool: "sh", Args: v1.StringArguments("-c", `test ! -e reports/stale && test ! -e reports/current || exit 42; echo current > reports/current; exit "$1"`, "report")},
	}
	scene := h.manifest.Scenes["dev"]
	blocked := h.manifest.Components["report"]
	blocked.Outputs = []string{"parent"}
	h.manifest.Components["blocked"] = blocked
	scene.Tasks = []string{"report", "blocked"}
	h.manifest.Scenes["dev"] = scene
	h.write()
	if err := os.Mkdir(filepath.Join(h.project, "reports"), 0700); err != nil {
		t.Fatal(err)
	}
	stale := filepath.Join(h.project, "reports", "stale")
	if err := os.WriteFile(stale, []byte("old"), 0600); err != nil {
		t.Fatal(err)
	}
	parent := h.state(h.run("dev").Instance.ID, v1.RuntimeReady).Instance
	if _, err := os.Stat(stale); err != nil {
		t.Fatal("parent startup prepared task-only output", err)
	}
	witness := filepath.Join(h.project, "parent-output", "witness")
	if err := os.WriteFile(witness, []byte("parent"), 0600); err != nil {
		t.Fatal(err)
	}
	_, err := h.cli.RunTask(context.Background(), v1.TaskRunRequest{APIVersion: v1.Version, Target: v1.SceneTarget{InstanceID: parent.ID}, Task: "blocked"})
	code(t, err, "output_conflict")
	if data, err := os.ReadFile(witness); err != nil || string(data) != "parent" {
		t.Fatal("rejected task prepared parent output", string(data), err)
	}
	var archive string
	for _, exit := range []string{"0", "23"} {
		r, err := h.cli.RunTask(context.Background(), v1.TaskRunRequest{APIVersion: v1.Version, Target: v1.SceneTarget{InstanceID: parent.ID}, Task: "report", Args: []string{exit}, Options: v1.ExecutionOptions{JobTimeout: "10s", StopGrace: "50ms"}})
		if err != nil {
			t.Fatal(err)
		}
		if h.tokens == nil {
			h.tokens = map[string]string{}
		}
		h.tokens[r.Instance.ID] = r.LeaseToken
		want := v1.Succeeded
		if exit == "23" {
			want = v1.Failed
		}
		final := h.state(r.Instance.ID, want).Instance
		result := final.Execution
		if result.Failure != "" || result.CollectionFailure != "" || result.CleanupFailure != "" || result.TerminalExitCode == nil || len(result.Artifacts) != 1 {
			t.Fatalf("task outcomes: %+v", result)
		}
		if exit == "23" && *result.TerminalExitCode != 23 {
			t.Fatal("lost task exit", result)
		}
		if _, err := os.Stat(stale); !os.IsNotExist(err) {
			t.Fatal("stale task report retained", err)
		}
		if archive == "" {
			archive = filepath.Join(result.Artifacts[0].Path, "reports", "current")
		}
		for path, want := range map[string]string{archive: "current\n", witness: "parent"} {
			data, err := os.ReadFile(path)
			if err != nil || string(data) != want {
				t.Fatal("archive/parent changed", path, string(data), err)
			}
		}
		current := h.state(parent.ID, v1.RuntimeReady).Instance
		if current.Execution.Attempt != parent.Execution.Attempt || current.Execution.Ports["port"] != parent.Execution.Ports["port"] {
			t.Fatal("task changed parent generation")
		}
	}
}
