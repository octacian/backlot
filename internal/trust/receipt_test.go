package trust

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"

	v1 "github.com/octacian/backlot/api/v1"
	"github.com/octacian/backlot/internal/tlsverify"
)

func TestMain(m *testing.M) {
	if tlsverify.Helper() {
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func publicCA(t *testing.T, expired bool) v1.GatewayCA {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	end := time.Now().Add(time.Hour)
	if expired {
		end = time.Now().Add(-time.Minute)
	}
	cert := &x509.Certificate{SerialNumber: big.NewInt(1), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign, NotBefore: time.Now().Add(-time.Hour), NotAfter: end}
	der, err := x509.CreateCertificate(rand.Reader, cert, cert, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(der)
	state := filepath.Join(t.TempDir(), "daemon")
	return v1.GatewayCA{APIVersion: v1.Version, StateDirectory: state, CertificatePath: filepath.Join(state, "gateway/data/pki/authorities/local/root.crt"), CertificatePEM: string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})), Fingerprint: hex.EncodeToString(hash[:]), Provenance: "validated-managed-ca-pin"}
}

func TestReceiptAuthorityAndPreservation(t *testing.T) {
	ca := publicCA(t, true)
	if err := ca.Validate(); err != nil {
		t.Fatal("expired identity lost", err)
	}
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	path, _ := receiptPath(dir, ca.Fingerprint)
	r := receipt{Version: 1, CA: ca, Store: target(), Phase: "installed", Preexisting: true, Sources: []string{ca.StateDirectory}}
	if err := writeReceipt(path, r); err != nil {
		t.Fatal(err)
	}
	read, err := readReceipt(path)
	if err != nil || read.CA.Fingerprint != ca.Fingerprint {
		t.Fatal(read, err)
	}
	// No owned effects: acknowledge does not adopt an external identical root.
	result, err := Retire(context.Background(), dir, ca.Fingerprint, true, nil)
	if err != nil || result.Outcome != "preserved" || result.Entry != "external-preserved" {
		t.Fatal(result, err)
	}
	before, _ := os.ReadFile(path)
	denied, deniedErr := Retire(context.Background(), dir, ca.Fingerprint, false, nil)
	if deniedErr == nil || denied.Host.Validate() != nil || denied.Effective.Validate() != nil {
		t.Fatal("denial lost valid unknown observations", denied, deniedErr)
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(before, after) {
		t.Fatal("denial modified receipt")
	}
	for _, phase := range []string{"intent", "partial"} {
		r.Phase = phase
		r.EntryCreated = true
		if err = writeReceipt(path, r); err != nil {
			t.Fatal(err)
		}
		if _, err = Retire(context.Background(), dir, ca.Fingerprint, true, nil); err == nil {
			t.Fatal("incomplete proof authorized removal", phase)
		}
	}
	r.Version = 999
	if err = writeReceipt(path, r); err != nil {
		t.Fatal(err)
	}
	before, _ = os.ReadFile(path)
	if _, err = readReceipt(path); err == nil {
		t.Fatal("unsupported version accepted")
	}
	after, _ = os.ReadFile(path)
	if !bytes.Equal(before, after) {
		t.Fatal("unsupported version rewritten")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = Install(ctx, dir, ca, true); err == nil {
		t.Fatal("unsupported writer started clean tracking over a future receipt")
	}
	after, _ = os.ReadFile(path)
	backups, _ := filepath.Glob(path + ".preserved-*")
	if !bytes.Equal(before, after) || len(backups) != 0 {
		t.Fatal("future receipt moved or rewritten")
	}
	summaries, warnings := Retained(dir)
	if len(summaries) != 0 || len(warnings) != 1 {
		t.Fatal(summaries, warnings)
	}
	if _, err = receiptPath(dir, "../root"); err == nil {
		t.Fatal("path accepted as authority")
	}
}

func TestTrackingLockPermissionsAndCleanRecovery(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	ca := publicCA(t, false)
	path, _ := receiptPath(dir, ca.Fingerprint)
	lock, err := lockTracking(dir)
	if err != nil {
		t.Fatal(err)
	}
	if other, err := lockTracking(dir); err == nil {
		_ = other.Close()
		t.Fatal("concurrent writer admitted")
	}
	if err = lock.Close(); err != nil {
		t.Fatal(err)
	}
	damaged := []byte("damaged evidence")
	if err = os.WriteFile(path, damaged, 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = Install(ctx, dir, ca, false); err == nil {
		t.Fatal("damaged evidence silently replaced")
	}
	if _, err = Install(ctx, dir, ca, true); err == nil {
		t.Fatal("cancelled install claimed success")
	}
	backups, err := filepath.Glob(path + ".preserved-*")
	if err != nil || len(backups) != 1 {
		t.Fatal(backups, err)
	}
	data, _ := os.ReadFile(backups[0])
	if !bytes.Equal(data, damaged) {
		t.Fatal("damaged evidence not preserved")
	}
	current, err := readReceipt(path)
	if err != nil || current.EntryCreated || current.TrustCreated || current.Phase != "partial" {
		t.Fatal("clean recovery minted authority", current, err)
	}
	if err = os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err = readReceipt(path); err == nil {
		t.Fatal("unsafe receipt accepted")
	}
	if err = os.Chmod(path, 0600); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(dir, "alias.json")
	if err = os.Symlink(path, alias); err != nil {
		t.Fatal(err)
	}
	if _, err = readReceipt(alias); err == nil {
		t.Fatal("receipt symlink accepted")
	}
	wire, err := json.Marshal(current)
	if err != nil || bytes.Contains(wire, []byte("PRIVATE KEY")) {
		t.Fatal("private material in public receipt", err)
	}
}
