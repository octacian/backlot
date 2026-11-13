package trust

import (
	"crypto/sha1" // Stock trust-settings certificate keys use SHA-1.
	"encoding/hex"
	"encoding/pem"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
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
