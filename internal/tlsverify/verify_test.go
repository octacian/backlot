package tlsverify

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	if Helper() {
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestFreshTrustAndPinnedHostname(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	root := &x509.Certificate{Subject: pkix.Name{CommonName: "Backlot owned TLS verifier fixture"}, SerialNumber: big.NewInt(1), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour)}
	der, err := x509.CreateCertificate(rand.Reader, root, root, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	root, err = x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	rootPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	leaf := &x509.Certificate{SerialNumber: big.NewInt(2), DNSNames: []string{"fixture.invalid"}, NotBefore: root.NotBefore, NotAfter: root.NotAfter, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, KeyUsage: x509.KeyUsageDigitalSignature}
	leafDER, err := x509.CreateCertificate(rand.Reader, leaf, root, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Backlot-Instance", r.Host)
		w.Header().Set("Location", "https://elsewhere.invalid/")
		w.WriteHeader(http.StatusFound)
	}))
	server.TLS = &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{leafDER, der}, PrivateKey: key}}, MinVersion: tls.VersionTLS12}
	server.StartTLS()
	defer server.Close()
	dir := t.TempDir()
	path := filepath.Join(dir, "roots.pem")
	wrongKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	wrongTemplate := *root
	wrongTemplate.Subject = pkix.Name{CommonName: "Backlot unrelated fixture root"}
	wrongTemplate.SubjectKeyId = nil
	wrongTemplate.AuthorityKeyId = nil
	wrongTemplate.PublicKey = &wrongKey.PublicKey
	wrongDER, err := x509.CreateCertificate(rand.Reader, &wrongTemplate, &wrongTemplate, &wrongKey.PublicKey, wrongKey)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: wrongDER}), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SSL_CERT_FILE", path)
	t.Setenv("SSL_CERT_DIR", dir)
	// Prime this process's cache before adding the root; only fresh children see it.
	_, _ = x509.SystemCertPool()
	request := Request{URL: "https://fixture.invalid/", Pinned: server.Listener.Addr().String()}
	before, err := Run(context.Background(), request, false)
	if err != nil || before.Verified || before.Failure != "rejected" {
		t.Fatal(before, err)
	}
	if err = os.WriteFile(path, rootPEM, 0600); err != nil {
		t.Fatal(err)
	}
	after, err := Run(context.Background(), request, false)
	if err != nil || !after.Verified || after.StatusCode != 302 || after.Location != "https://elsewhere.invalid/" || after.Marker != "fixture.invalid" {
		t.Fatal(after, err)
	}
	exact, err := Run(context.Background(), Request{RootPEM: string(rootPEM)}, false)
	if err != nil || !exact.Verified {
		t.Fatal(exact, err)
	}
	osProof, err := Run(context.Background(), Request{RootPEM: string(rootPEM)}, true)
	if err != nil || osProof.Verified || osProof.Failure != "rejected" {
		t.Fatal("override counted as OS proof", osProof, err)
	}
	wrong, err := Run(context.Background(), Request{URL: "https://wrong.invalid", Pinned: request.Pinned}, false)
	if err != nil || wrong.Verified || wrong.Failure != "rejected" {
		t.Fatal("hostname bypass", wrong, err)
	}
	if err = os.Remove(path); err != nil {
		t.Fatal(err)
	}
	removed, err := Run(context.Background(), request, false)
	if err != nil || removed.Verified {
		t.Fatal("cached trust survived removal", removed, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	started := time.Now()
	if _, err = Run(ctx, request, false); err == nil || time.Since(started) > time.Second {
		t.Fatal("cancellation not joined", err)
	}
}
