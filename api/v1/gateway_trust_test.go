package v1

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"strings"
	"testing"
	"time"
)

func TestManagedPublicCAContract(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	root := &x509.Certificate{SerialNumber: big.NewInt(1), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(-time.Minute)}
	der, err := x509.CreateCertificate(rand.Reader, root, root, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(der)
	ca := GatewayCA{APIVersion: Version, StateDirectory: "/owned/state", CertificatePath: "/owned/state/gateway/data/pki/authorities/local/root.crt", CertificatePEM: string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})), Fingerprint: hex.EncodeToString(hash[:]), Provenance: "validated-managed-ca-pin"}
	wire, err := json.Marshal(ca)
	var decoded GatewayCA
	if err != nil || json.Unmarshal(wire, &decoded) != nil || decoded.Validate() != nil {
		t.Fatal("expired public provenance did not roundtrip", err)
	}
	for _, change := range []func(*GatewayCA){func(c *GatewayCA) { c.Fingerprint = strings.Repeat("0", 64) }, func(c *GatewayCA) { c.Provenance = "name-only" }, func(c *GatewayCA) { c.CertificatePath = "/arbitrary/root.crt" }, func(c *GatewayCA) { c.CertificatePEM += "PRIVATE KEY" }, func(c *GatewayCA) { c.DaemonEffective = TrustObservation{Status: "maybe"} }} {
		invalid := ca
		change(&invalid)
		if invalid.Validate() == nil {
			t.Fatal("invalid public authority accepted")
		}
	}
	for _, state := range []string{"trusted", "untrusted", "unknown"} {
		if err := (TrustObservation{Status: state, Store: "selected-store", Context: "os", Message: "bounded observation"}).Validate(); err != nil {
			t.Fatal(err)
		}
	}
}
