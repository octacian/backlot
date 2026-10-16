//go:build darwin || linux

package gateway

import (
	"bytes"
	"encoding/json"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	v1 "github.com/octacian/backlot/api/v1"
	"github.com/octacian/backlot/internal/manifest"
)

// TestManagedListenerExposure inspects stock Caddy's real socket families, not
// just its configured strings. Cleanup retains the direct child before startup.
func TestManagedListenerExposure(t *testing.T) {
	binary := os.Getenv("BACKLOT_CADDY_TEST_BINARY")
	if binary == "" {
		t.Skip("set BACKLOT_CADDY_TEST_BINARY for real managed listener exposure")
	}
	for _, bind := range []string{"", "0.0.0.0"} {
		name := "legacy-loopback"
		if bind != "" {
			name = "ipv4-wildcard"
		}
		t.Run(name, func(t *testing.T) {
			root, err := os.MkdirTemp("", "backlot-managed-listeners-")
			if err != nil {
				t.Fatal(err)
			}
			preserve := false
			t.Cleanup(func() {
				if preserve {
					t.Log("preserved uncertain Caddy fixture state", root)
					return
				}
				if err := os.RemoveAll(root); err != nil {
					t.Error(err)
				}
			})
			port := func() int {
				l, err := net.Listen("tcp4", "127.0.0.1:0")
				if err != nil {
					t.Fatal(err)
				}
				p := l.Addr().(*net.TCPAddr).Port
				_ = l.Close()
				return p
			}
			admin, https := port(), port()
			for admin == https {
				https = port()
			}
			c := manifest.CaddyDefaults(v1.CaddyConfig{Mode: "managed", Executable: binary, Endpoint: "http://127.0.0.1:" + strconv.Itoa(admin), HTTPSPort: &https, HTTPSBindAddress: bind})
			data, err := ManagedConfig(c, filepath.Join(root, "data"))
			if err != nil {
				t.Fatal(err)
			}
			config := filepath.Join(root, "config.json")
			if err := os.WriteFile(config, data, 0600); err != nil {
				t.Fatal(err)
			}
			var logs bytes.Buffer
			command := exec.Command(binary, "run", "--config", config)
			command.Env = append(os.Environ(), "HOME="+root, "XDG_CONFIG_HOME="+root, "XDG_DATA_HOME="+root)
			command.Stdout, command.Stderr = &logs, &logs
			started := false
			t.Cleanup(func() {
				if started {
					_ = command.Process.Signal(os.Interrupt)
					done := make(chan error, 1)
					go func() { done <- command.Wait() }()
					select {
					case err := <-done:
						if err != nil {
							t.Error("owned Caddy shutdown", err)
						}
					case <-time.After(10 * time.Second):
						_ = command.Process.Kill()
						select {
						case <-done:
							t.Error("owned Caddy required forced shutdown")
						case <-time.After(5 * time.Second):
							preserve = true
							t.Error("owned Caddy exit unverified; private state preserved", root)
						}
					}
				}
				if t.Failed() && !preserve {
					t.Log("private Caddy log", logs.String())
				}
			})
			if err := command.Start(); err != nil {
				t.Fatal(err)
			}
			started = true
			client := &http.Client{Timeout: time.Second}
			ready := false
			for end := time.Now().Add(10 * time.Second); time.Now().Before(end); {
				response, err := client.Get(c.Endpoint + "/config/")
				if err == nil {
					_ = response.Body.Close()
					if response.StatusCode == http.StatusOK {
						ready = true
						break
					}
				}
				time.Sleep(20 * time.Millisecond)
			}
			if !ready {
				t.Fatal("owned Caddy admin unavailable")
			}
			for _, host := range []string{"127.0.0.1", "::1"} {
				listening := false
				for end := time.Now().Add(10 * time.Second); time.Now().Before(end); {
					conn, err := net.DialTimeout("tcp", net.JoinHostPort(host, strconv.Itoa(https)), time.Second)
					if err == nil {
						_ = conn.Close()
						listening = true
						break
					}
					time.Sleep(20 * time.Millisecond)
				}
				if !listening {
					t.Fatal("owned Caddy HTTPS listener unavailable", host)
				}
			}
			observe := func(p int) []string {
				t.Helper()
				output, err := exec.Command("lsof", "-nP", "-a", "-iTCP:"+strconv.Itoa(p), "-sTCP:LISTEN", "-Fptn").CombinedOutput()
				if err != nil {
					t.Fatalf("observe actual owned listeners: %v %s", err, output)
				}
				t.Logf("actual Caddy listeners port=%d:\n%s", p, output)
				family := ""
				var bindings []string
				for _, line := range strings.Split(string(output), "\n") {
					if strings.HasPrefix(line, "t") {
						family = line[1:]
					}
					if strings.HasPrefix(line, "n") {
						bindings = append(bindings, family+" "+line[1:])
					}
				}
				return bindings
			}
			expectedV4 := "127.0.0.1"
			if bind != "" {
				expectedV4 = "*"
			}
			expected := map[string]bool{"IPv4 " + net.JoinHostPort(expectedV4, strconv.Itoa(https)): false, "IPv6 " + net.JoinHostPort("::1", strconv.Itoa(https)): false}
			actual := observe(https)
			for _, binding := range actual {
				if _, ok := expected[binding]; !ok {
					t.Fatal("unexpected HTTPS family/exposure", binding)
				}
				expected[binding] = true
			}
			for binding, found := range expected {
				if !found {
					t.Fatal("configured HTTPS binding missing", binding)
				}
			}
			adminBindings := observe(admin)
			if len(adminBindings) != 1 || adminBindings[0] != "IPv4 "+net.JoinHostPort("127.0.0.1", strconv.Itoa(admin)) {
				t.Fatal("admin exposure widened", adminBindings)
			}
			// Both listeners are real and accepting, including retained IPv6 loopback.
			for _, host := range []string{"127.0.0.1", "::1"} {
				conn, err := net.DialTimeout("tcp", net.JoinHostPort(host, strconv.Itoa(https)), time.Second)
				if err != nil {
					t.Fatal(err)
				}
				_ = conn.Close()
			}
			saved := filepath.Join(root, "caddy", "autosave.json")
			savedData, err := os.ReadFile(saved)
			if err != nil {
				t.Fatal(err)
			}
			if err := ValidateManagedConfig(savedData, c, filepath.Join(root, "data")); err != nil {
				t.Fatal("stock autosave rejected", err)
			}
			var object map[string]any
			if err := json.Unmarshal(data, &object); err != nil {
				t.Fatal(err)
			}
			u, _ := url.Parse(c.Endpoint)
			if object["admin"].(map[string]any)["listen"] != u.Host {
				t.Fatal("admin configuration changed")
			}
			t.Logf("stock Caddy exact family policy verified bind=%q", bind)
		})
	}
}
