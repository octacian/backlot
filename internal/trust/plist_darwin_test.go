package trust

import (
	"context"
	"crypto/sha1" // Stock trust-settings certificate keys use SHA-1.
	"encoding/hex"
	"encoding/pem"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestStockFileOnlySSLSettings(t *testing.T) {
	ca := publicCA(t, false)
	dir := t.TempDir()
	cert, settings := filepath.Join(dir, "public.crt"), filepath.Join(dir, "settings.plist")
	if err := os.WriteFile(cert, []byte(ca.CertificatePEM), 0600); err != nil {
		t.Fatal(err)
	}
	// File-output mode serializes public settings without changing any trust domain or keychain.
	if output, err := exec.Command("/usr/bin/security", "add-trusted-cert", "-r", "trustRoot", "-p", "ssl", "-o", settings, cert).CombinedOutput(); err != nil {
		t.Fatalf("stock file-output settings: %v: %s", err, output)
	}
	data, err := os.ReadFile(settings)
	if err != nil {
		t.Fatal(err)
	}
	block, _ := pem.Decode([]byte(ca.CertificatePEM))
	hash := sha1.Sum(block.Bytes)
	entry, found, err := settingsEntry(data, hex.EncodeToString(hash[:]))
	if err != nil || !found || !exactSSLSettings(entry) {
		t.Fatalf("stock SSL trustRoot rejected: found=%v err=%v entry=%s", found, err, entry)
	}
	if strings.Contains(entry, "kSecTrustSettingsResult") {
		t.Fatal("stock default trustRoot unexpectedly serialized an explicit result", entry)
	}
	t.Log("stock file-only SSL trustRoot accepted with omitted default result:", entry)
}

func TestConfiguredTrustRequiresCurrentRoot(t *testing.T) {
	for _, tc := range []struct {
		name       string
		start, end time.Time
	}{
		{"expired", time.Now().Add(-time.Hour), time.Now().Add(-time.Minute)},
		{"future", time.Now().Add(time.Hour), time.Now().Add(2 * time.Hour)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ca := publicCAWithDates(t, tc.start, tc.end)
			if err := ca.Validate(); err != nil {
				t.Fatal("retained identity must remain valid", err)
			}
			if got := Observe(context.Background(), ca, true); got.Status != "untrusted" {
				t.Fatal("invalid dates allowed configured trust", got)
			}
		})
	}
}

func TestNativeEffectiveUnknownWithFileOverride(t *testing.T) {
	ca := publicCA(t, false)
	dir := t.TempDir()
	path := filepath.Join(dir, "public.crt")
	if err := os.WriteFile(path, []byte(ca.CertificatePEM), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SSL_CERT_FILE", "")
	t.Setenv("SSL_CERT_DIR", "")
	t.Setenv("GODEBUG", "x509sslcertoverrideplatform=0")
	if got := Observe(context.Background(), ca, false); got.Status != "unknown" {
		t.Fatal("absence of overrides inferred native acceptance", got)
	}
	t.Setenv("SSL_CERT_FILE", path)
	t.Setenv("SSL_CERT_DIR", dir)
	// Go policy, not the mere presence of SSL_CERT_FILE, chooses the verifier.
	got := Observe(context.Background(), ca, false)
	if got.Status != "unknown" || !strings.Contains(got.Message, "server leaf") {
		t.Fatal("native root limitation misclassified", got)
	}
	t.Setenv("GODEBUG", "x509sslcertoverrideplatform=1")
	if got := Observe(context.Background(), ca, false); got.Status != "trusted" {
		t.Fatal("fresh file override acceptance lost", got)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if got := Observe(context.Background(), ca, false); got.Status == "trusted" {
		t.Fatal("removed override remained trusted", got)
	}
}
