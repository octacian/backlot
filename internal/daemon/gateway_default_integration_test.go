//go:build darwin || linux

package daemon

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	v1 "github.com/octacian/backlot/api/v1"
	"github.com/octacian/backlot/internal/plan"
)

func TestGatewayDefaultConfig(t *testing.T) {
	caddy := os.Getenv("BACKLOT_CADDY_TEST_BINARY")
	if caddy == "" {
		t.Skip("explicit native Caddy required")
	}
	binary := buildFixtureBinary(t, t.TempDir(), "../../cmd/backlot")
	for _, first := range []string{"start", "restart", "explicit"} {
		t.Run(first, func(t *testing.T) {
			h := newNativeHarness(t, binary)
			// Only CLI processes see this private default; the daemon keeps its own environment.
			t.Setenv("HOME", t.TempDir())
			t.Setenv("XDG_CONFIG_HOME", t.TempDir())
			path, err := plan.DefaultConfigPath()
			if err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
				t.Fatal(err)
			}
			run := func(action, wantSource string, args ...string) []byte {
				t.Helper()
				command := exec.Command(binary, append([]string{"gateway", action, "--state-dir", h.dir, "--json"}, args...)...)
				output, err := command.CombinedOutput()
				if wantSource == "error" {
					if err == nil {
						t.Fatalf("expected failure: %s", output)
					}
				} else {
					var response v1.GatewayResponse
					if err != nil || json.Unmarshal(output, &response) != nil || response.ConfigSource != wantSource {
						t.Fatalf("%s: source %s: %v %s", action, wantSource, err, output)
					}
				}
				return output
			}
			write := func(path string, data []byte) {
				t.Helper()
				if err := os.WriteFile(path, data, 0600); err != nil {
					t.Fatal(err)
				}
			}
			// Authenticated fixture daemon shutdown owns gateway cleanup even after assertions fail.
			run("status", "")
			run("stop", "")
			port, admin := managedFixturePort(t), managedFixturePort(t)
			config := v1.MachineConfig{Version: v1.ManifestVersion, Caddy: &v1.CaddyConfig{Mode: "managed", Executable: caddy, Endpoint: "http://127.0.0.1:" + strconv.Itoa(admin), HTTPSPort: &port}}
			data, _ := json.Marshal(config)
			if first == "explicit" {
				write(path, []byte("invalid default"))
				explicit := filepath.Join(h.project, "machine.json")
				write(explicit, data)
				// Preserve CLI-relative explicit-path resolution and override the invalid default.
				cwd, err := os.Getwd()
				if err != nil {
					t.Fatal(err)
				}
				relative, err := filepath.Rel(cwd, explicit)
				if err != nil {
					t.Fatal(err)
				}
				run("start", "explicit", "--config", relative)
				run("start", "explicit", "--config", explicit)
				run("restart", "explicit", "--config", explicit)
				run("status", "retained")
				return
			}
			output := run(first, "error")
			if !strings.Contains(string(output), path) || !strings.Contains(string(output), "--config") {
				t.Fatalf("missing first-start guidance: %s", output)
			}
			for _, invalid := range []struct{ name, data, code string }{
				{"malformed", "version: [", "invalid_document"},
				{"invalid", "version: wrong", "invalid_contract"},
				{"absent-mode", "version: backlot/v1", "gateway_external"},
				{"external", "version: backlot/v1\ncaddy:\n  mode: external\n  endpoint: http://127.0.0.1:2019\n  scope: backlot\n  domain_suffix: example.test\n  host_address: 127.0.0.1\n", "gateway_external"},
			} {
				t.Log("reject default", invalid.name)
				write(path, []byte(invalid.data))
				output := run(first, "error")
				if !strings.Contains(string(output), invalid.code) {
					t.Fatalf("unexpected diagnostic: %s", output)
				}
				run("status", "")
			}
			write(path, data)
			t.Run("unreadable", func(t *testing.T) {
				if os.Geteuid() == 0 {
					t.Skip("root bypasses file read permissions")
				}
				if err := os.Chmod(path, 0000); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() {
					if err := os.Chmod(path, 0600); err != nil {
						t.Error(err)
					}
				})
				if output := run(first, "error"); !strings.Contains(string(output), "cannot read machine configuration") {
					t.Fatalf("unexpected diagnostic: %s", output)
				}
			})
			run(first, "default")
			run("status", "retained")
			_, saved, storage := managedPaths(h.dir)
			caPath := filepath.Join(storage, "pki", "authorities", "local", "root.crt")
			read := func(path string) []byte {
				t.Helper()
				value, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				return value
			}
			ca, autosave := read(caPath), read(saved)
			// Drift in the default is ignored, while an explicit mismatch fences restart before stop.
			config.Caddy.HTTPSPort = new(9443)
			changed, _ := json.Marshal(config)
			write(path, changed)
			run("start", "retained")
			if output := run("restart", "error", "--config", path); !strings.Contains(string(output), "differs from retained") {
				t.Fatalf("missing explicit drift fence: %s", output)
			}
			run("status", "retained")
			run("restart", "retained")
			write(path, []byte("invalid changed default"))
			run("restart", "retained")
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			run("stop", "retained")
			run("start", "retained")
			// A daemon restart must retain settings and CA without persisting source provenance.
			h.close()
			h.start()
			run("status", "retained")
			run("restart", "retained")
			if !bytes.Equal(ca, read(caPath)) || !bytes.Equal(autosave, read(saved)) {
				t.Fatal("retained CA or saved configuration changed")
			}
		})
	}
}
