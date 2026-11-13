package daemon

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestGatewayPublicCAProvenance(t *testing.T) {
	state, _, path := caReferenceState(t)
	service := &service{store: state, directory: filepath.Dir(state.db.Path())}
	ca, err := service.gatewayCA(context.Background())
	if err != nil || ca.Validate() != nil {
		t.Fatal(ca, err)
	}
	record, err := state.managed()
	if err != nil {
		t.Fatal(err)
	}
	wire, err := json.Marshal(ca)
	if err != nil || strings.Contains(string(wire), record.Scope) || strings.Contains(string(wire), "PRIVATE KEY") || ca.Fingerprint != record.RootHash {
		t.Fatal("private material or wrong pin", err)
	}
	rootBytes, err := os.ReadFile(path)
	if err != nil || ca.CertificatePEM != string(rootBytes) {
		t.Fatal("not the actual root", err)
	}
	record.RootHash = strings.Repeat("0", 64)
	if err = state.saveManaged(record); err != nil {
		t.Fatal(err)
	}
	if _, err = service.gatewayCA(context.Background()); err == nil {
		t.Fatal("changed pin accepted")
	}
	record.RootHash = ca.Fingerprint
	if err = state.saveManaged(record); err != nil {
		t.Fatal(err)
	}
	if err = os.Remove(strings.TrimSuffix(path, ".crt") + ".key"); err != nil {
		t.Fatal(err)
	}
	if _, err = service.gatewayCA(context.Background()); err == nil {
		t.Fatal("missing private provenance accepted")
	}
	// Retained public evidence remains valid after source removal.
	if err = ca.Validate(); err != nil {
		t.Fatal("public retained identity depends on keys", err)
	}
}

func TestTrustHintShellScope(t *testing.T) {
	directory := "/state/user's $(printf expanded) daemon"
	command := trustInstallCommand(directory, strings.Repeat("a", 64))
	output, err := exec.Command("/bin/sh", "-c", `backlot() { printf '%s' "$5"; }; `+command).Output()
	if err != nil || string(output) != directory {
		t.Fatal("hint changed scope in actual shell", string(output), err)
	}
}

func TestHTTPSProbeFreshTrustIgnoresSchemeCase(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	cert := &x509.Certificate{SerialNumber: big.NewInt(1), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour)}
	der, err := x509.CreateCertificate(rand.Reader, cert, cert, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	server.TLS = &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}, MinVersion: tls.VersionTLS12}
	server.StartTLS()
	defer server.Close()
	dir := t.TempDir()
	path := filepath.Join(dir, "roots.pem")
	t.Setenv("SSL_CERT_FILE", path)
	t.Setenv("SSL_CERT_DIR", dir)
	_, _ = x509.SystemCertPool()
	target := strings.Replace(server.URL, "https://", "HTTPS://", 1)
	if ready, err := probeDestination(context.Background(), "http", target, server.Listener.Addr().String()); err != nil || ready {
		t.Fatal("untrusted fixture became ready", ready, err)
	}
	if err = os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0600); err != nil {
		t.Fatal(err)
	}
	if ready, err := probeDestination(context.Background(), "http", target, server.Listener.Addr().String()); err != nil || !ready {
		t.Fatal("HTTPS probe did not refresh trust for a valid case-insensitive scheme", ready, err)
	}
}
