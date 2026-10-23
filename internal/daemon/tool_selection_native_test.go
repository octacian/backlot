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

func TestToolSelectionNativeCLI(t *testing.T) {
	binary := buildFixtureBinary(t, t.TempDir(), "../../cmd/backlot")
	t.Setenv("BACKLOT_PROJECT_CONFIG_DIR", filepath.Join(shortTemp(t), "registry"))
	a, b := shortTemp(t), shortTemp(t)
	for _, fixture := range []struct{ dir, name, script string }{{a, "bl-tool", "echo fixture-A"}, {b, "bl-tool", "echo fixture-B"}, {b, "launcher", "bl-tool"}} {
		if err := os.WriteFile(filepath.Join(fixture.dir, fixture.name), []byte("#!/bin/sh\n"+fixture.script+"\n"), 0700); err != nil {
			t.Fatal(err)
		}
	}
	baseline := os.Getenv("PATH")
	t.Setenv("PATH", a+":"+baseline)
	h := newNativeHarness(t, binary)
	t.Setenv("PATH", b+":"+baseline)
	h.manifest.Tools["selected"] = "bl-tool"
	h.manifest.Components["selected"] = v1.Component{Kind: v1.Job, Runtime: v1.Native, Policy: v1.EachStart, Command: &v1.Command{Tool: "selected"}}
	h.manifest.Scenes["test"] = v1.Scene{Lifetime: v1.Disposable, Components: []string{"selected"}, TerminalJob: "selected"}
	h.write()
	cli := func(args ...string) []byte {
		t.Helper()
		out, err := exec.Command(binary, args...).CombinedOutput()
		if err != nil {
			t.Fatalf("CLI %s failed: %v", args[0], err)
		}
		return out
	}
	out := cli("plan", "test", "--project", h.project)
	if !strings.Contains(string(out), "caller/offline selection") || !strings.Contains(string(out), filepath.Join(b, "bl-tool")) {
		t.Fatal("caller path not shown")
	}
	first := h.run("test")
	first = h.state(first.Instance.ID, v1.Succeeded)
	if first.Instance.Plan.Components[0].Executable != filepath.Join(a, "bl-tool") {
		t.Fatal("daemon did not use startup PATH")
	}
	assertLogs := func(id, marker string) {
		t.Helper()
		logs, err := h.cli.Logs(context.Background(), v1.LogsRequest{APIVersion: v1.Version, InstanceID: id})
		data, _ := json.Marshal(logs)
		if err != nil || !bytes.Contains(data, []byte(marker)) {
			t.Fatal("missing actual fixture behavior", err)
		}
	}
	assertLogs(first.Instance.ID, "fixture-A")
	// Current private override differs from the recorded successful selection.
	payload, _ := json.Marshal(v1.PrivateConfig{Version: v1.ManifestVersion, Tools: map[string]string{"selected": filepath.Join(b, "launcher")}})
	setup := exec.Command(binary, "project", "setup", "--project", h.project, "--from", "-", "--json")
	setup.Stdin = bytes.NewReader(payload)
	if _, err := setup.CombinedOutput(); err != nil {
		t.Fatal("private override failed", err)
	}
	out = cli("doctor", "--state-dir", h.dir, "--instance", first.Instance.ID, "--json")
	var doctor v1.DoctorResponse
	if err := json.Unmarshal(out, &doctor); err != nil || doctor.Validate() != nil {
		t.Fatal("invalid doctor", err)
	}
	encoded := string(out)
	for _, want := range []string{"current daemon lookup", "private source", filepath.Join(b, "launcher"), filepath.Join(a, "bl-tool"), "original source/candidate/lookup context unrecorded"} {
		if !strings.Contains(encoded, want) {
			t.Fatal("recorded/current detail missing", want)
		}
	}
	second := h.run("test")
	second = h.state(second.Instance.ID, v1.Succeeded)
	if second.Instance.Plan.Components[0].Executable != filepath.Join(b, "launcher") {
		t.Fatal("private launcher not selected")
	}
	assertLogs(second.Instance.ID, "fixture-A") // Absolute launcher still resolves its child through daemon PATH.
	// Readiness lookup fails safely in the daemon context, then recovers explicitly.
	server := h.manifest.Components["server"]
	original := server.Readiness
	server.Readiness = &v1.Probe{Kind: "command", Command: &v1.Command{Tool: "probe"}, Timeout: "1s"}
	h.manifest.Components["server"] = server
	h.manifest.Tools["probe"] = "bl-missing-probe"
	h.write()
	failed := exec.Command(binary, "doctor", "--state-dir", h.dir, "--project", h.project, "--scene", "dev", "--json")
	out, err := failed.CombinedOutput()
	if err == nil || !bytes.Contains(out, []byte("daemon/runtime server readiness")) || !bytes.Contains(out, []byte("bl-missing-probe")) {
		t.Fatal("readiness failure lost lookup detail")
	}
	server.Readiness = original
	h.manifest.Components["server"] = server
	h.write()
	ready := h.run("dev")
	h.state(ready.Instance.ID, v1.RuntimeReady)
	if _, err := h.cli.StopExecution(context.Background(), ready.Instance.ID); err != nil {
		t.Fatal(err)
	}
	h.state(ready.Instance.ID, v1.Stopped)
	// Stop/join then restart on the same state directory, adopting the new caller PATH.
	h.close()
	h.start()
	retained, err := h.cli.Inspect(context.Background(), first.Instance.ID)
	if err != nil || retained.Instance.Plan.Components[0].Executable != filepath.Join(a, "bl-tool") {
		t.Fatal("restart lost recorded selection", err)
	}
	assertLogs(first.Instance.ID, "fixture-A")
	out = cli("inspect", "--state-dir", h.dir, first.Instance.ID)
	if !bytes.Contains(out, []byte("recorded selection")) || !bytes.Contains(out, []byte(filepath.Join(a, "bl-tool"))) {
		t.Fatal("human inspection lost recorded path")
	}
	third := h.run("test")
	third = h.state(third.Instance.ID, v1.Succeeded)
	assertLogs(third.Instance.ID, "fixture-B")
	t.Log("real CLI/daemon selection: caller B, original daemon A, private absolute launcher with child A, readiness failure/recovery, retained path/logs after explicit stop/start, restarted daemon child B")
}
