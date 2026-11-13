//go:build darwin || linux

package daemon

import (
	"bytes"
	"context"
	"crypto/sha1" // security delete-certificate selects the retained exact public DER by SHA-1.
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	v1 "github.com/octacian/backlot/api/v1"
	"github.com/octacian/backlot/internal/client"
)

// TestOwnedHostTrust is destructive acceptance ONLY on explicitly granted,
// disposable GitHub-hosted runners. Ordinary repository checks never opt in.
func TestOwnedHostTrust(t *testing.T) {
	if os.Getenv("BACKLOT_HOST_TRUST_TEST") == "" {
		t.Skip("disposable host trust acceptance not enabled")
	}
	if os.Getenv("BACKLOT_HOST_TRUST_TEST") != "install-status-retire" || os.Getenv("BACKLOT_DISPOSABLE_HOST_GRANT") != "github-hosted-ephemeral" || os.Getenv("GITHUB_ACTIONS") != "true" || os.Getenv("RUNNER_ENVIRONMENT") != "github-hosted" {
		t.Fatal("host trust requires exact opt-in and disposable GitHub-hosted grant")
	}
	if os.Geteuid() == 0 {
		t.Fatal("acceptance must exercise production sudo from an unprivileged CLI")
	}
	switch runtime.GOOS {
	case "darwin":
		if runtime.GOARCH != "arm64" || os.Getenv("RUNNER_OS") != "macOS" {
			t.Fatal("requires macOS ARM64")
		}
	case "linux":
		release, err := os.ReadFile("/etc/os-release")
		if err != nil || runtime.GOARCH != "amd64" || os.Getenv("RUNNER_OS") != "Linux" || !bytes.Contains(release, []byte("ID=ubuntu\n")) || !bytes.Contains(release, []byte("VERSION_ID=\"24.04\"")) {
			t.Fatal("requires Ubuntu 24.04 amd64", err)
		}
	default:
		t.Fatal("unsupported disposable host")
	}
	stock := func(args ...string) ([]byte, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()
		return exec.CommandContext(ctx, args[0], args[1:]...).CombinedOutput()
	}
	mustStock := func(args ...string) []byte {
		t.Helper()
		out, err := stock(args...)
		if err != nil {
			t.Fatalf("stock %s: %v %s", args[0], err, out)
		}
		return out
	}
	mustStock("/usr/bin/sudo", "-n", "true")
	caddy := os.Getenv("BACKLOT_CADDY_TEST_BINARY")
	if !filepath.IsAbs(caddy) || !strings.HasPrefix(string(mustStock(caddy, "version")), "v2.11.7") {
		t.Fatal("requires explicit stock Caddy 2.11.7")
	}
	if runtime.GOOS == "darwin" {
		mustStock("/usr/bin/security", "list-keychains", "-d", "system")
	} else {
		for _, path := range []string{"/usr/sbin/update-ca-certificates", "/usr/bin/install", "/usr/bin/openssl"} {
			info, err := os.Stat(path)
			if err != nil || info.Mode()&0111 == 0 {
				t.Fatal("missing stock tool", path, err)
			}
		}
	}
	// No file override can make either context pass instead of actual OS trust.
	t.Setenv("SSL_CERT_FILE", "")
	t.Setenv("SSL_CERT_DIR", "")
	binary := buildFixtureBinary(t, t.TempDir(), "../../cmd/backlot")
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	h := newNativeHarness(t, binary, "30s")
	var ca v1.GatewayCA
	var sourceInfo, linkInfo os.FileInfo
	var source, link, public string
	var hook, hookLog, hookFail string
	var hookInfo os.FileInfo
	var hookBytes []byte
	// Register recovery before any gateway or OS effect. Public cleanup authority
	// comes only from this fixture's initially absent exact DER/files and handles.
	t.Cleanup(func() {
		if ca.Fingerprint != "" {
			out, err := stock(binary, "gateway", "trust", "retire", "--fingerprint", ca.Fingerprint, "--acknowledge-impact", "--json")
			t.Logf("cleanup product retirement exit=%v: %s", err, out)
			if runtime.GOOS == "darwin" {
				// Product deliberately preserves the public entry; fixture grant permits
				// exact manual removal, never deletion by certificate name.
				out, err = stock("/usr/bin/sudo", "-n", "/usr/bin/security", "remove-trusted-cert", "-d", public)
				if err != nil {
					t.Logf("manual settings cleanup (may already be absent): %s", out)
				}
				block, _ := pem.Decode([]byte(ca.CertificatePEM))
				sha := sha1.Sum(block.Bytes)
				if present, e := ownedHostMacEntry(ca); e != nil {
					t.Error(e)
				} else if present {
					out, err = stock("/usr/bin/sudo", "-n", "/usr/bin/security", "delete-certificate", "-Z", hex.EncodeToString(sha[:]), "/Library/Keychains/System.keychain")
					if err != nil {
						t.Errorf("exact public entry residue: %v %s", err, out)
					}
				}
				if present, e := ownedHostMacEntry(ca); e != nil || present {
					t.Error("System public entry residue", present, e)
				}
			} else {
				for _, item := range []struct {
					path     string
					identity os.FileInfo
				}{{source, sourceInfo}, {link, linkInfo}} {
					info, e := os.Lstat(item.path)
					if os.IsNotExist(e) {
						continue
					}
					if e != nil || item.identity == nil || !os.SameFile(info, item.identity) {
						t.Error("unproven host residue preserved", item.path, e)
						continue
					}
					if item.path == source {
						data, e := os.ReadFile(source)
						if e != nil || string(data) != ca.CertificatePEM {
							t.Error("changed source preserved", e)
							continue
						}
					} else {
						target, e := os.Readlink(link)
						if e != nil || target != source {
							t.Error("changed link preserved", e)
							continue
						}
					}
					out, e := stock("/usr/bin/sudo", "-n", "/usr/bin/rm", "--", item.path)
					if e != nil {
						t.Errorf("host residue %s: %v %s", item.path, e, out)
					}
				}
				out, err = stock("/usr/bin/sudo", "-n", "/usr/sbin/update-ca-certificates")
				if err != nil {
					t.Errorf("cleanup refresh: %v %s", err, out)
				}
				out, err = stock("/usr/bin/sudo", "-n", "/usr/bin/openssl", "rehash", "/etc/ssl/certs")
				if err != nil {
					t.Errorf("cleanup rehash: %v %s", err, out)
				}
			}
			observed, err := stock(binary, "gateway", "trust", "status", "--fingerprint", ca.Fingerprint, "--json")
			var result v1.GatewayTrustResult
			if err != nil || json.Unmarshal(observed, &result) != nil || result.Host.Status != "untrusted" {
				t.Errorf("cleanup root rejection unproven: %v %s", err, observed)
			}
		}
		if hook != "" {
			info, err := os.Lstat(hook)
			data, readErr := os.ReadFile(hook)
			if err != nil || readErr != nil || hookInfo == nil || !os.SameFile(info, hookInfo) || !bytes.Equal(data, hookBytes) {
				t.Error("changed hook preserved", err, readErr)
			} else {
				out, err := stock("/usr/bin/sudo", "-n", "/usr/bin/rm", "--", hook)
				if err != nil {
					t.Errorf("hook residue: %v %s", err, out)
				}
			}
			if _, err := os.Lstat(hook); !os.IsNotExist(err) {
				t.Error("hook removal unproven", err)
			}
		}
		h.close()
		state, err := openStore(h.dir)
		if err == nil {
			err = errors.Join(state.recoverRuntime(), state.recoverManaged(), state.cleanupFailures())
			record, readErr := state.managed()
			err = errors.Join(err, readErr)
			if record.Pending || record.Identity != nil || record.Status == "uncertain" {
				err = errors.Join(err, errors.New("managed cleanup uncertain"))
			}
			err = errors.Join(err, state.db.Close())
		}
		if err != nil {
			h.cleanupUnverified = true
			t.Error("owned gateway residue", err)
		}
	})
	command := func(ok bool, args ...string) []byte {
		t.Helper()
		args = append(args, "--state-dir", h.dir, "--json")
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		cmd := exec.CommandContext(ctx, binary, args...)
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		out, err := cmd.Output()
		// Capture exact identities immediately after fixture effects, including failure.
		if source != "" {
			if info, e := os.Lstat(source); e == nil && sourceInfo == nil {
				sourceInfo = info
			}
			if info, e := os.Lstat(link); e == nil && linkInfo == nil {
				linkInfo = info
			}
		}
		t.Logf("CLI %s exit=%v stderr=%s result=%s", strings.Join(args[:len(args)-4], " "), err, stderr.String(), out)
		if (err == nil) != ok {
			t.Fatalf("unexpected CLI outcome: %v %s", err, out)
		}
		return out
	}
	trust := func(ok bool, args ...string) v1.GatewayTrustResult {
		t.Helper()
		out := command(ok, append([]string{"gateway", "trust"}, args...)...)
		var r v1.GatewayTrustResult
		if err := json.Unmarshal(out, &r); err != nil {
			t.Fatal(err, string(out))
		}
		return r
	}
	port, admin := managedFixturePort(t), managedFixturePort(t)
	for port == admin {
		admin = managedFixturePort(t)
	}
	config := v1.MachineConfig{Version: v1.ManifestVersion, Caddy: &v1.CaddyConfig{Mode: "managed", Executable: caddy, Endpoint: "http://127.0.0.1:" + strconv.Itoa(admin), HTTPSPort: &port}}
	data, _ := json.Marshal(config)
	configPath := filepath.Join(t.TempDir(), "machine.json")
	if err := os.WriteFile(configPath, data, 0600); err != nil {
		t.Fatal(err)
	}
	command(true, "gateway", "start", "--config", configPath)
	before := trust(true, "status")
	if before.CA.Validate() != nil || before.Host.Status != "untrusted" || before.Effective.Status != "untrusted" {
		t.Fatal("fresh owned root unexpectedly trusted", before)
	}
	public = filepath.Join(t.TempDir(), "public.crt")
	if err := os.WriteFile(public, []byte(before.CA.CertificatePEM), 0600); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS == "darwin" {
		if present, err := ownedHostMacEntry(before.CA); err != nil || present {
			t.Fatal("root entry must initially be absent", present, err)
		}
	} else {
		source = "/usr/local/share/ca-certificates/backlot-" + before.CA.Fingerprint + ".crt"
		link = "/etc/ssl/certs/backlot-" + before.CA.Fingerprint + ".pem"
		for _, path := range []string{source, link} {
			if _, err := os.Lstat(path); !os.IsNotExist(err) {
				t.Fatal("fixture effect already exists", path, err)
			}
		}
	}
	ca = before.CA // Original public DER, fingerprint and validated provenance retained.
	if runtime.GOOS == "linux" {
		hook = "/etc/ca-certificates/update.d/backlot-fixture-" + ca.Fingerprint
		if _, err := os.Lstat(hook); !os.IsNotExist(err) {
			t.Fatal("hook already exists", err)
		}
		hookLog, hookFail = filepath.Join(t.TempDir(), "hook.log"), filepath.Join(t.TempDir(), "fail")
		// Paths are generated by TempDir; quote rather than interpolate shell input.
		hookBytes = []byte("#!/bin/sh\ncat >> '" + hookLog + "'\nprintf 'invoked\\n' >> '" + hookLog + "'\nif test -f '" + hookFail + "'; then printf 'controlled-exit-7\\n' >> '" + hookLog + "'; exit 7; fi\n")
		staged := filepath.Join(t.TempDir(), "hook")
		if err := os.WriteFile(staged, hookBytes, 0700); err != nil {
			t.Fatal(err)
		}
		mustStock("/usr/bin/sudo", "-n", "/usr/bin/install", "-m", "0755", staged, hook)
		var err error
		hookInfo, err = os.Lstat(hook)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(hookFail, nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	if out := command(false, "gateway", "trust", "install", "--fingerprint", strings.Repeat("0", 64)); !bytes.Contains(out, []byte("gateway_ca")) {
		t.Fatal("wrong pin not rejected", string(out))
	}
	installed := trust(true, "install", "--fingerprint", ca.Fingerprint)
	if installed.Outcome != "installed" || installed.Host.Status != "trusted" || installed.Effective.Status != "trusted" {
		t.Fatal("actual sudo install/fresh verification failed", installed)
	}
	daemon := h.process // Observation must refresh within this same live daemon.
	live := trust(true, "status")
	if live.Host.Status != "trusted" || live.Effective.Status != "trusted" || h.process != daemon {
		t.Fatal("live daemon did not refresh", live)
	}
	if runtime.GOOS == "linux" {
		log, err := os.ReadFile(hookLog)
		if err != nil || !bytes.Contains(log, []byte("controlled-exit-7\n")) || !bytes.Contains(log, []byte("+"+link)) {
			t.Fatal("nonempty normal distro hook effect unproven", string(log), err)
		}
		t.Log("stock refresh accepted bounded exit-7 hook; no command-failure/partial claim is inferred from hook failure")
		if err := os.Remove(hookFail); err != nil {
			t.Fatal(err)
		}
	}
	denied := trust(false, "retire", "--fingerprint", ca.Fingerprint)
	if denied.Error == nil || trust(true, "status").Host.Status != "trusted" {
		t.Fatal("missing acknowledgement changed trust")
	}
	command(true, "gateway", "stop")
	h.close() // Retire with only retained public proof, no running source daemon.
	retired := trust(true, "retire", "--fingerprint", ca.Fingerprint, "--acknowledge-impact")
	if retired.Outcome != "retired" || retired.Host.Status != "untrusted" {
		t.Fatal("owned withdrawal/root rejection failed", retired)
	}
	if runtime.GOOS == "darwin" {
		if retired.Entry != "public-entry-preserved-manual-deletion" {
			t.Fatal("public entry residue misreported", retired.Entry)
		}
		if present, err := ownedHostMacEntry(ca); err != nil || !present {
			t.Fatal("product deleted public entry", present, err)
		}
	} else {
		for _, path := range []string{source, link} {
			if _, err := os.Lstat(path); !os.IsNotExist(err) {
				t.Fatal("owned source/link residue", path, err)
			}
		}
		sourceInfo, linkInfo = nil, nil
		log, err := os.ReadFile(hookLog)
		if err != nil || bytes.Count(log, []byte("invoked\n")) < 2 {
			t.Fatal("hook recovery invocation missing", err)
		}
	}
	// Reinstall the identical public root as an explicitly fixture-owned external
	// administrator effect. Fresh product tracking must never adopt its ownership.
	if runtime.GOOS == "darwin" {
		mustStock("/usr/bin/sudo", "-n", "/usr/bin/security", "add-trusted-cert", "-d", "-r", "trustRoot", "-p", "ssl", "-k", "/Library/Keychains/System.keychain", public)
	} else {
		mustStock("/usr/bin/sudo", "-n", "/usr/bin/install", "-m", "0644", public, source)
		mustStock("/usr/bin/sudo", "-n", "/usr/sbin/update-ca-certificates")
		sourceInfo, _ = os.Lstat(source)
		linkInfo, _ = os.Lstat(link)
	}
	h.cli = client.New(SocketPath(h.dir))
	h.start()
	external := trust(true, "install", "--fingerprint", ca.Fingerprint, "--new-tracking")
	if external.Outcome != "installed" || external.Host.Status != "trusted" {
		t.Fatal("external identical root not observed", external)
	}
	preserved := trust(true, "retire", "--fingerprint", ca.Fingerprint, "--acknowledge-impact")
	if preserved.Outcome != "preserved" || preserved.Entry != "external-preserved" || preserved.Host.Status != "trusted" {
		t.Fatal("external exact effect was adopted or removed", preserved)
	}
	t.Log("PASS: actual stock sudo install/status/receipt-only retirement, live refresh, rejected pin/ack, identical external-effect preservation; cleanup still required")
}

func ownedHostMacEntry(ca v1.GatewayCA) (bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "/usr/bin/security", "find-certificate", "-a", "-p", "/Library/Keychains/System.keychain").Output()
	if err != nil {
		return false, err
	}
	expected, _ := pem.Decode([]byte(ca.CertificatePEM))
	sha := sha1.Sum(expected.Bytes)
	count := 0
	for len(bytes.TrimSpace(out)) > 0 {
		block, rest := pem.Decode(out)
		if block == nil {
			return false, errors.New("invalid System public certificate observation")
		}
		out = rest
		if sha1.Sum(block.Bytes) == sha {
			if sha256.Sum256(block.Bytes) != sha256.Sum256(expected.Bytes) {
				return false, errors.New("ambiguous public certificate hash; preserve")
			}
			count++
		}
	}
	if count > 1 {
		return false, errors.New("duplicate public certificate; preserve")
	}
	return count == 1, nil
}
