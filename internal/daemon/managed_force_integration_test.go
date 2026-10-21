//go:build darwin || linux

package daemon

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"

	v1 "github.com/octacian/backlot/api/v1"
	"github.com/octacian/backlot/internal/native"
)

func TestManagedForceRestart(t *testing.T) {
	caddy := os.Getenv("BACKLOT_CADDY_TEST_BINARY")
	if caddy == "" {
		t.Skip("explicit native Caddy required")
	}
	binary := filepath.Join(t.TempDir(), "backlot")
	if output, err := exec.Command("go", "build", "-race", "-o", binary, "../../cmd/backlot").CombinedOutput(); err != nil {
		t.Fatal(err, string(output))
	}
	h := newNativeHarness(t, binary)
	ctx := context.Background()
	port, admin := managedFixturePort(t), managedFixturePort(t)
	config := v1.MachineConfig{Version: v1.ManifestVersion, Caddy: &v1.CaddyConfig{Mode: "managed", Executable: caddy, Endpoint: "http://127.0.0.1:" + strconv.Itoa(admin), HTTPSPort: &port}}
	path := filepath.Join(h.project, "machine.json")
	data, _ := json.Marshal(config)
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	// Retain normal authenticated gateway cleanup before allocation/faults.
	t.Cleanup(func() {
		if _, err := h.cli.Gateway(ctx, "stop", v1.GatewayRequest{APIVersion: v1.Version}); err != nil {
			h.cleanupUnverified = true
			t.Error("managed cleanup", err)
		}
	})
	if _, err := h.cli.Gateway(ctx, "start", v1.GatewayRequest{APIVersion: v1.Version, ConfigPath: path}); err != nil {
		t.Fatal(err)
	}
	_, saved, storage := managedPaths(h.dir)
	caPath := filepath.Join(storage, "pki", "authorities", "local", "root.crt")
	ca, err := os.ReadFile(caPath)
	if err != nil {
		t.Fatal(err)
	}
	savedBefore, err := os.ReadFile(saved)
	if err != nil {
		t.Fatal(err)
	}
	h.close()
	state, err := openStore(h.dir)
	if err != nil {
		t.Fatal(err)
	}
	r, err := state.managed()
	if err != nil {
		t.Fatal(err)
	}
	// Shutdown already authenticated cleanup. Reconstruct the installed bootless
	// lost-receipt record with a separate real inactive guardian, cleaned before
	// removing its control files. No process observation authorizes this actuation.
	log, err := os.Create(filepath.Join(h.project, "guardian-log"))
	if err != nil {
		t.Fatal(err)
	}
	group, err := native.Start(binary, log, log)
	if err != nil {
		t.Fatal(err)
	}
	if err := group.Stop(0); err != nil {
		t.Fatal(err)
	}
	if err := log.Close(); err != nil {
		t.Fatal(err)
	}
	identity := group.Identity
	if err := os.RemoveAll(filepath.Dir(identity.Control)); err != nil {
		t.Fatal(err)
	}
	identity.Boot = ""
	r.Identity = &identity
	r.Status = "uncertain"
	if err := state.saveManaged(r); err != nil {
		t.Fatal(err)
	}
	if err := state.db.Close(); err != nil {
		t.Fatal(err)
	}
	h.start()
	if _, err := h.cli.Gateway(ctx, "stop", v1.GatewayRequest{APIVersion: v1.Version}); err == nil {
		t.Fatal("bootless unknown authority released")
	}
	output, err := exec.Command(binary, "gateway", "stop", "--force", "--state-dir", h.dir, "--json").CombinedOutput()
	if err != nil {
		t.Fatal(err, string(output))
	}
	var forced v1.GatewayResponse
	if err := json.Unmarshal(output, &forced); err != nil || len(forced.NativeOwnershipWarnings) != 1 {
		t.Fatal("warning missing", err, string(output))
	}
	blocker, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(port))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = blocker.Close() })
	_, err = h.cli.Gateway(ctx, "start", v1.GatewayRequest{APIVersion: v1.Version})
	code(t, err, "gateway_port_conflict")
	if err := blocker.Close(); err != nil {
		t.Fatal(err)
	}
	started, err := h.cli.Gateway(ctx, "start", v1.GatewayRequest{APIVersion: v1.Version})
	if err != nil || len(started.NativeOwnershipWarnings) != 1 {
		t.Fatal("explicit resume lost warning", err)
	}
	after, err := os.ReadFile(caPath)
	if err != nil || string(after) != string(ca) {
		t.Fatal("CA changed", err)
	}
	after, err = os.ReadFile(saved)
	if err != nil || string(after) != string(savedBefore) {
		t.Fatal("saved config changed", err)
	}
	h.close()
	h.start()
	status, err := h.cli.Gateway(ctx, "status", v1.GatewayRequest{APIVersion: v1.Version})
	if err != nil || len(status.NativeOwnershipWarnings) != 1 {
		t.Fatal("history lost after daemon restart", err)
	}
}
