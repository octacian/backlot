//go:build darwin || linux

package daemon

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	v1 "github.com/octacian/backlot/api/v1"
)

func TestRegistrationEarlyFlow(t *testing.T) {
	t.Setenv("BACKLOT_PROJECT_CONFIG_DIR", shortTemp(t))
	binary := buildFixtureBinary(t, t.TempDir(), "../../cmd/backlot")
	h := newNativeHarness(t, binary)
	h.manifest.Inputs = map[string]v1.Input{"token": {Secret: true}}
	server := h.manifest.Components["server"]
	server.Environment.Assign["PRIVATE_TOKEN"] = fixtureRef("input", "token", "")
	h.manifest.Components["server"] = server
	h.write()
	machine := filepath.Join(filepath.Dir(h.project), "machine.yaml")
	if err := os.WriteFile(machine, []byte("version: backlot/v1\n"), 0600); err != nil {
		t.Fatal(err)
	}
	command := func(input string, args ...string) []byte {
		t.Helper()
		cmd := exec.Command(binary, args...)
		cmd.Stdin = strings.NewReader(input)
		output, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("CLI failed: %v %s", err, output)
		}
		if strings.Contains(string(output), "private-early-token") {
			t.Fatal("private value leaked through CLI result")
		}
		return output
	}
	var setup v1.ProjectSetupResponse
	if err := json.Unmarshal(command("version: backlot/v1\ninputs: {token: private-early-token}\n", "project", "setup", "--project", h.project, "--config", machine, "--from", "-", "--json"), &setup); err != nil {
		t.Fatal(err)
	}
	if !setup.Registered || !setup.Changed {
		t.Fatal("setup did not publish")
	}
	privateBefore, err := os.ReadFile(setup.PrivatePath)
	if err != nil {
		t.Fatal(err)
	}
	var offline v1.PlanResponse
	if err := json.Unmarshal(command("", "plan", "dev", "--project", h.project, "--config", machine, "--json"), &offline); err != nil {
		t.Fatal(err)
	}
	request := v1.PrepareRequest{APIVersion: v1.Version, Plan: v1.PlanRequest{ProjectPath: h.project, ConfigPath: machine, Scene: "dev"}}
	prepared, err := h.cli.Prepare(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if prepared.Instance.Status != v1.Prepared || prepared.Instance.Plan.EffectiveConfigDigest != offline.EffectiveConfigDigest {
		t.Fatal("daemon did not accept offline private composition")
	}
	var inspected v1.ProjectSetupResponse
	if err := json.Unmarshal(command("", "project", "setup", "--project", h.project, "--config", machine, "--json"), &inspected); err != nil {
		t.Fatal(err)
	}
	if inspected.Association != setup.Association || inspected.PrivatePath != setup.PrivatePath || inspected.Changed {
		t.Fatal("ordinary selection rotated private association/generation")
	}
	common := filepath.Join(h.project, ".backlot-local", "backlot-common.json")
	marker, err := os.ReadFile(common)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(common); err != nil {
		t.Fatal(err)
	}
	_, err = h.cli.Prepare(context.Background(), request)
	code(t, err, "project_registration")
	if _, err := os.Stat(common); !os.IsNotExist(err) {
		t.Fatal("missing enrolled marker silently republished", err)
	}
	if err := os.WriteFile(common, marker, 0600); err != nil {
		t.Fatal(err)
	}
	again, err := h.cli.Prepare(context.Background(), request)
	if err != nil || again.Instance.ID != prepared.Instance.ID {
		t.Fatal("restored exact marker lost retained instance", err)
	}
	privateAfter, err := os.ReadFile(setup.PrivatePath)
	if err != nil || string(privateAfter) != string(privateBefore) {
		t.Fatal("private generation changed", err)
	}
	h.close()
	if !h.waited || h.cleanupUnverified {
		t.Fatal("fixture daemon cleanup unverified")
	}
	t.Log("production CLI setup/offline selection and typed daemon preparation/refusal passed; private generation and instance retained; owned daemon joined; no provider/application effects")
}
