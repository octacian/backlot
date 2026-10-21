//go:build darwin || linux

package daemon

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	v1 "github.com/octacian/backlot/api/v1"
	"github.com/octacian/backlot/internal/plan"
)

func TestConfigurationLayeringDocker(t *testing.T) {
	endpoint := os.Getenv("BACKLOT_DOCKER_TEST_ENDPOINT")
	if endpoint == "" {
		t.Skip("requires explicit owned Docker fixture endpoint")
	}
	binary := filepath.Join(t.TempDir(), "backlot")
	build := exec.Command("go", "build", "-race", "-o", binary, "../../cmd/backlot")
	if data, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v %s", err, data)
	}
	home := shortTemp(t)
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", home)
	h := newDockerHarness(t, binary, endpoint)
	h.alpine()
	h.manifest.Inputs = map[string]v1.Input{"token": {Secret: true}, "unused": {Secret: true}}
	server := h.manifest.Components["server"]
	server.Environment.Assign["PRIVATE_TOKEN"] = fixtureRef("input", "token", "")
	h.manifest.Components["server"] = server
	h.write()
	setup := func(token, unused string) v1.ProjectSetupResponse {
		t.Helper()
		s, err := plan.OpenSetup(v1.ProjectSetupRequest{ProjectPath: h.project, ConfigPath: h.config, Edit: true})
		if err != nil {
			t.Fatal(err)
		}
		r, err := s.Save(context.Background(), v1.PrivateConfig{Version: v1.ManifestVersion, Inputs: map[string]string{"token": token, "unused": unused}}, nil, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	setup("first-token", "unused-first")
	started := h.run("dev")
	id := started.Instance.ID
	ready := h.state(id, v1.RuntimeReady)
	original := h.container(id, "server")
	_, exit := h.exec(original, []string{"sh", "-c", `test "$PRIVATE_TOKEN" = first-token`})
	if exit != 0 {
		t.Fatal("container did not receive composed private value")
	}
	setup("first-token", "unused-next")
	repeated := h.run("dev")
	if !repeated.ConfigDrift || repeated.Instance.Plan.EffectiveConfigDigest != ready.Instance.Plan.EffectiveConfigDigest {
		t.Fatal("repeat replaced launch snapshot or hid effective drift")
	}
	compatible := h.restart(id, false)
	h.state(id, v1.RuntimeReady)
	if compatible.Instance.ID != id {
		t.Fatal("compatible restart lost identity")
	}
	beforeUnsafe := h.container(id, "server")
	latest := setup("next-token", "unused-next")
	request := h.request("dev")
	request.InstanceID = id
	_, err := h.cli.Restart(context.Background(), request)
	code(t, err, "unsafe_drift")
	if !h.engineAlive(beforeUnsafe) || h.container(id, "server").ID != beforeUnsafe.ID {
		t.Fatal("unsafe credential restart stopped recorded work")
	}
	h.restart(id, true)
	h.state(id, v1.RuntimeReady)
	reset := h.container(id, "server")
	_, exit = h.exec(reset, []string{"sh", "-c", `test "$PRIVATE_TOKEN" = next-token`})
	if exit != 0 {
		t.Fatal("reset did not apply new private value")
	}
	if err := os.Remove(latest.PrivatePath); err != nil {
		t.Fatal(err)
	}
	if _, err := h.cli.StopExecution(context.Background(), id); err != nil {
		t.Fatal("snapshot stop required current private file", err)
	}
	if _, err := h.cli.Destroy(context.Background(), id); err != nil {
		t.Fatal("snapshot destroy required current private file", err)
	}
	t.Log("real provider composition, immutable repeat, compatible restart, pre-stop sensitive drift rejection, explicit reset and missing-current cleanup passed; harness reconciles all owned resources")
}
