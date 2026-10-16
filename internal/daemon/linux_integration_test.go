//go:build linux

package daemon

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	v1 "github.com/octacian/backlot/api/v1"
)

// TestLinuxInstallation exercises the installed CGO-disabled release executable,
// including resource-free persistent restart after its durable JSON round trip.
func TestLinuxInstallation(t *testing.T) {
	binary := os.Getenv("BACKLOT_INSTALLED_TEST_BINARY")
	if binary == "" {
		t.Skip("set BACKLOT_INSTALLED_TEST_BINARY to an installed Linux release binary")
	}
	if !filepath.IsAbs(binary) {
		t.Fatal("installed executable must be absolute")
	}
	h := newNativeHarness(t, binary)
	h.manifest = v1.Manifest{Version: v1.ManifestVersion, Project: "linux-installed-fixture", Tools: map[string]string{"sh": "/bin/sh"}, Components: map[string]v1.Component{
		"server": {Kind: v1.Service, Runtime: v1.Native, Command: &v1.Command{Tool: "sh", Args: []string{"-c", `sleep 600 & child=$!; printf '%s %s %s' "$$" "$child" "$(ps -o pgid= -p $$)" > "$PIDFILE"; wait`}}, Environment: v1.Environment{Assign: map[string]v1.Value{"PIDFILE": fixtureLiteral(h.pids)}}, Readiness: &v1.Probe{Kind: "command", Command: &v1.Command{Tool: "sh", Args: []string{"-c", `test -s "$PIDFILE"`}}, Timeout: "3s"}},
	}, Scenes: map[string]v1.Scene{"dev": {Lifetime: v1.Persistent, Components: []string{"server"}}}}
	h.write()
	if !Doctor(context.Background(), h.dir).Healthy {
		t.Fatal("installed daemon IPC doctor failed")
	}
	for path, mode := range map[string]os.FileMode{h.dir: 0700, SocketPath(h.dir): 0600, filepath.Join(h.dir, "state.db"): 0600, filepath.Join(h.dir, "daemon.lock"): 0600} {
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != mode {
			t.Fatalf("private installed state permissions: %s %v", path, err)
		}
	}
	id := h.run("dev").Instance.ID
	h.state(id, v1.RuntimeReady)
	if _, err := h.cli.StopExecution(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	h.absent()
	request := h.request("dev")
	request.InstanceID = id
	if _, err := h.cli.Restart(context.Background(), request); err != nil {
		t.Fatal("resource-free installed restart", err)
	}
	h.state(id, v1.RuntimeReady)
	if _, err := h.cli.Destroy(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	h.absent()
	t.Log("installed Linux release CLI: private IPC/state, resource-free restart and descendant cleanup verified")
}
