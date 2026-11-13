//go:build darwin

package trust

import (
	"bytes"
	"context"
	"crypto/sha1" // The stock trust-settings dictionary uses SHA-1 certificate keys.
	"crypto/sha256"
	"encoding/hex"
	"encoding/pem"
	"os"
	"os/exec"

	v1 "github.com/octacian/backlot/api/v1"
)

const systemKeychain = "/Library/Keychains/System.keychain"

func macSettings(ctx context.Context, ca v1.GatewayCA) (string, bool, error) {
	file, err := os.CreateTemp("", "backlot-public-trust-*.plist")
	if err != nil {
		return "", false, err
	}
	defer func() { _ = os.Remove(file.Name()) }()
	if err = file.Close(); err != nil {
		return "", false, err
	}
	args := []string{"trust-settings-export", "-d", file.Name()}
	output, err := exec.CommandContext(ctx, "/usr/bin/security", args...).CombinedOutput()
	if err != nil {
		// This documented errSecNoTrustSettings code proves an empty domain only.
		if bytes.Contains(output, []byte("-25263")) {
			return "", false, nil
		}
		return "", false, problem("cannot observe selected macOS trust domain; preserve effects and inspect stock security tools")
	}
	data, err := os.ReadFile(file.Name())
	if err != nil || len(data) > 8<<20 {
		return "", false, problem("macOS trust-settings snapshot unavailable or too large")
	}
	block, _ := pem.Decode([]byte(ca.CertificatePEM))
	hash := sha1.Sum(block.Bytes)
	return settingsEntry(data, hex.EncodeToString(hash[:]))
}

func macEntry(ctx context.Context, ca v1.GatewayCA) (bool, error) {
	output, err := exec.CommandContext(ctx, "/usr/bin/security", "find-certificate", "-a", "-p", systemKeychain).Output()
	if err != nil {
		return false, problem("cannot observe System keychain; no exact entry authority")
	}
	expected, _ := pem.Decode([]byte(ca.CertificatePEM))
	expectedSHA1 := sha1.Sum(expected.Bytes)
	count := 0
	for len(bytes.TrimSpace(output)) != 0 {
		block, rest := pem.Decode(output)
		if block == nil || block.Type != "CERTIFICATE" {
			return false, problem("invalid keychain certificate observation")
		}
		output = rest
		if sha1.Sum(block.Bytes) == expectedSHA1 {
			if sha256.Sum256(block.Bytes) != sha256.Sum256(expected.Bytes) {
				return false, problem("ambiguous keychain certificate identity; preserved")
			}
			count++
		}
	}
	if count > 1 {
		return false, problem("duplicate exact keychain entries; manual targeting required")
	}
	return count == 1, nil
}

func selectedTrust(ctx context.Context, ca v1.GatewayCA) (bool, error) {
	entry, found, err := macSettings(ctx, ca)
	if err != nil || !found {
		return false, err
	}
	if !exactSSLSettings(entry) {
		return false, problem("selected admin trust settings are not the exact supported TLS root policy; inspect manually")
	}
	return macEntry(ctx, ca)
}

func install(ctx context.Context, path, staged string, r *receipt) error {
	entry, err := macEntry(ctx, r.CA)
	if err != nil {
		return err
	}
	before, settings, err := macSettings(ctx, r.CA)
	if err != nil {
		return err
	}
	r.Preexisting = entry || settings
	if err = writeReceipt(path, *r); err != nil {
		return err
	}
	// authz: any pre-existing settings, including deny or a different policy,
	// belong to their operator and are never overwritten or adopted.
	if settings {
		if !exactSSLSettings(before) {
			return problem("pre-existing admin trust settings preserved; manual policy resolution required")
		}
		return nil
	}
	if current, err := macEntry(ctx, r.CA); err != nil || current != entry {
		return problem("keychain changed before effect; preserved")
	}
	if _, found, err := macSettings(ctx, r.CA); err != nil || found {
		return problem("admin trust settings changed before effect; preserved")
	}
	if err = command(ctx, "/usr/bin/security", "add-trusted-cert", "-d", "-r", "trustRoot", "-p", "ssl", "-k", systemKeychain, staged); err != nil {
		return err
	}
	after, found, err := macSettings(ctx, r.CA)
	if err != nil || !found || !exactSSLSettings(after) {
		return problem("new admin TLS settings not exactly verified; partial effects require manual recovery")
	}
	if present, err := macEntry(ctx, r.CA); err != nil || !present {
		return problem("new exact System keychain entry not verified; effects remain unknown")
	}
	r.EntryCreated = !entry
	r.TrustCreated = true
	r.TrustSettings = after
	return writeReceipt(path, *r)
}

func retire(ctx context.Context, r *receipt) error {
	if !r.TrustCreated || !exactSSLSettings(r.TrustSettings) {
		return problem("no exact owned macOS TLS settings; manual retirement required")
	}
	current, found, err := macSettings(ctx, r.CA)
	if err != nil || !found || current != r.TrustSettings {
		return problem("admin trust settings changed or disappeared; preserved for manual retirement")
	}
	if present, err := macEntry(ctx, r.CA); err != nil || !present {
		return problem("System keychain entry changed or disappeared; preserve trust settings")
	}
	file, err := os.CreateTemp("", "backlot-public-root-*.crt")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(file.Name()) }()
	if _, err = file.WriteString(r.CA.CertificatePEM); err != nil {
		_ = file.Close()
		return err
	}
	if err = file.Close(); err != nil {
		return err
	}
	if err = command(ctx, "/usr/bin/security", "remove-trusted-cert", "-d", file.Name()); err != nil {
		return err
	}
	if _, found, err = macSettings(ctx, r.CA); err != nil || found {
		return problem("admin trust withdrawal not verified; do not delete entry")
	}
	if present, err := macEntry(ctx, r.CA); err != nil || !present {
		return problem("certificate entry changed during trust withdrawal; inspect the partial receipt and selected store")
	}
	if !r.EntryCreated {
		r.EntryOutcome = "external-entry-preserved"
		return nil
	}
	// authz: stock observations prove the exact settings effect, not stable
	// certificate-entry identity after an identical external re-import.
	r.EntryOutcome = "public-entry-preserved-manual-deletion"
	return nil
}

func validateEffects(ctx context.Context, r *receipt) error {
	present, err := macEntry(ctx, r.CA)
	if err != nil || !present {
		return problem("retained System keychain entry unavailable; preserve tracking and inspect drift")
	}
	if r.TrustCreated {
		current, found, err := macSettings(ctx, r.CA)
		if err != nil || !found || current != r.TrustSettings {
			return problem("retained admin trust settings changed; preserve tracking and inspect drift")
		}
	}
	return nil
}
