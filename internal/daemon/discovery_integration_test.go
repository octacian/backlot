//go:build darwin || linux

package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	v1 "github.com/octacian/backlot/api/v1"
	"github.com/octacian/backlot/schemas"
)

func TestDiscoveryNativeCLI(t *testing.T) {
	binary := buildFixtureBinary(t, t.TempDir(), "../../cmd/backlot")
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", home)
	t.Setenv("BACKLOT_PROJECT_CONFIG_DIR", filepath.Join(home, "registry"))
	h := newNativeHarness(t, binary)
	invoke := func(want int, args ...string) []byte {
		t.Helper()
		command := exec.Command(binary, append([]string{"--json"}, args...)...)
		var diagnostic strings.Builder
		command.Stderr = &diagnostic
		data, err := command.Output()
		code := 0
		if err != nil {
			exit, ok := err.(*exec.ExitError)
			if !ok {
				t.Fatal(err)
			}
			code = exit.ExitCode()
		}
		if code != want {
			t.Fatalf("%v: exit %d want %d: %s %s", args, code, want, data, diagnostic.String())
		}
		t.Logf("backlot --json %s => exit %d", strings.Join(args, " "), code)
		return data
	}
	// Missing prerequisites in an unselected scene must not prevent discovery.
	h.manifest.Inputs = map[string]v1.Input{"credential": {Secret: true}}
	h.manifest.Tools["missing"] = "backlot-fixture-tool-does-not-exist"
	h.manifest.Components["offline"] = v1.Component{Kind: v1.Job, Runtime: v1.Native, Policy: v1.EachStart, Command: &v1.Command{Tool: "missing"}, Environment: v1.Environment{Seeds: []string{"absent.env"}, Assign: map[string]v1.Value{"CREDENTIAL": {Ref: &v1.Reference{Kind: "input", Name: "credential"}}}}}
	h.manifest.Scenes["offline"] = v1.Scene{Lifetime: v1.Disposable, Components: []string{"offline"}, TerminalJob: "offline"}
	h.write()
	var declared v1.SceneDescriptionResponse
	if json.Unmarshal(invoke(0, "scene", "describe", "offline", "--project", h.project), &declared) != nil || len(declared.Prerequisites.Inputs) != 1 || len(declared.Prerequisites.Seeds) != 1 {
		t.Fatal("missing declaration prerequisites", declared)
	}
	invoke(0, "scene", "list", "--project", h.project)
	invoke(1, "plan", "offline", "--project", h.project)
	delete(h.manifest.Inputs, "credential")
	delete(h.manifest.Components, "offline")
	delete(h.manifest.Scenes, "offline")
	delete(h.manifest.Tools, "missing")
	h.manifest.Tools["sh"] = "/bin/sh"
	peer := h.run("dev")
	h.state(peer.Instance.ID, v1.RuntimeReady)
	for _, code := range []int{0, 23} {
		job := h.manifest.Components["job"]
		job.Command = &v1.Command{Tool: "sh", Args: []string{"-c", fmt.Sprintf("printf discovery-witness; exit %d", code)}}
		h.manifest.Components["job"] = job
		h.write()
		var r v1.InstanceResponse
		if json.Unmarshal(invoke(code, "run", "test", "--project", h.project, "--state-dir", h.dir, "--stop-grace", "50ms"), &r) != nil || r.Instance.Execution == nil || r.Instance.Execution.TerminalExitCode == nil || *r.Instance.Execution.TerminalExitCode != code {
			t.Fatal("lost original exit", r)
		}
		var list v1.ListResponse
		if json.Unmarshal(invoke(0, "list", "--project", h.project, "--scene", "test", "--state", string(r.Instance.Status), "--limit", "1", "--state-dir", h.dir), &list) != nil || len(list.Instances) != 1 || list.Instances[0].ID != r.Instance.ID {
			t.Fatal("filtered page", list)
		}
		var page v1.LogPageResponse
		if json.Unmarshal(invoke(0, "logs", r.Instance.ID, "--page", "--attempt", r.Instance.Execution.Attempt, "--stream", "stdout", "--component", "job", "--limit", "1", "--state-dir", h.dir), &page) != nil || len(page.Records) != 1 || page.Continuation == "" {
			t.Fatal("finite log page", page)
		}
		invoke(0, "logs", r.Instance.ID, "--state-dir", h.dir)
	}
	var local v1.ConfigurationResponse
	if json.Unmarshal(invoke(0, "config", "show", "--project", h.project, "--state-dir", h.dir), &local) != nil || local.DaemonDirectory != h.dir || local.Defaults.RetentionBytes != DefaultRetentionBytes {
		t.Fatal("local config", local)
	}
	var policy v1.DaemonConfigurationResponse
	if json.Unmarshal(invoke(0, "daemon", "config", "--state-dir", h.dir), &policy) != nil || policy.Validate() != nil || policy.LeaseDuration != h.lease || policy.RetentionBytes != DefaultRetentionBytes {
		t.Fatal("daemon policy", policy)
	}
	// Start is deliberately non-reconfiguring; inspect the captured policy afterward.
	invoke(0, "daemon", "start", "--state-dir", h.dir, "--retention-bytes", "123")
	var unchanged v1.DaemonConfigurationResponse
	if json.Unmarshal(invoke(0, "daemon", "config", "--state-dir", h.dir), &unchanged) != nil || unchanged != policy {
		t.Fatal("start reconfigured active daemon", unchanged, policy)
	}
	var installed v1.ContractsResponse
	if json.Unmarshal(invoke(0, "contracts"), &installed) != nil || installed.Executable == nil || len(installed.Schemas) != 3 {
		t.Fatal(installed)
	}
	var capabilities v1.ContractsResponse
	if json.Unmarshal(invoke(0, "contracts", "--daemon", "--state-dir", h.dir), &capabilities) != nil || capabilities.ValidateDaemon() != nil || capabilities.Executable != nil || capabilities.DaemonGeneration != policy.DaemonGeneration {
		t.Fatal(capabilities)
	}
	for _, contract := range capabilities.Contracts {
		if contract.ID == "scene-declarations/v1" || contract.Mode != "finite" {
			t.Fatal("daemon advertised local-only contract or CLI framing", contract)
		}
	}

	for selector, name := range map[string]string{"manifest": "manifest-v1.schema.json", "machine": "machine-config-v1.schema.json", "private": "private-config-v1.schema.json"} {
		expected, err := schemas.Documents.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(invoke(0, "schema", selector), expected) {
			t.Fatal("installed schema differs", selector)
		}
	}

	if err := os.WriteFile(filepath.Join(h.project, "backlot.json"), []byte("broken manifest"), 0600); err != nil {
		t.Fatal(err)
	}
	invoke(0, "list", "--project", filepath.Join(h.project, "backlot.json"), "--scene", "dev", "--state-dir", h.dir)
	if r, err := h.cli.Inspect(context.Background(), peer.Instance.ID); err != nil || r.Instance.Status != v1.RuntimeReady {
		t.Fatal("peer changed", r, err)
	}
	if _, err := h.cli.StopExecution(context.Background(), peer.Instance.ID); err != nil {
		t.Fatal(err)
	}
	h.absent()
}
