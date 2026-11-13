package client

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"math/big"
	"net/http"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	v1 "github.com/octacian/backlot/api/v1"
)

func TestGatewayTrustWireContract(t *testing.T) {
	pin := strings.Repeat("a", 64)
	observation := v1.TrustObservation{Status: "unknown", Context: "os", Store: "selected-store", Message: "snapshot unavailable"}
	valid := v1.GatewayResponse{APIVersion: v1.Version, Mode: "managed", Status: "running", RootFingerprint: pin, HostTrust: &observation, TrustCommand: "backlot gateway trust install --state-dir '/state/user'\\''s $(printf expanded) daemon' --fingerprint " + pin}
	for _, tc := range []struct {
		name   string
		change func(*v1.GatewayResponse)
		code   string
	}{
		{"valid unknown snapshot", func(*v1.GatewayResponse) {}, ""},
		{"plain state", func(r *v1.GatewayResponse) {
			r.TrustCommand = "backlot gateway trust install --state-dir '/owned/state' --fingerprint " + pin
		}, ""},
		{"legacy absent", func(r *v1.GatewayResponse) { r.HostTrust = nil; r.RootFingerprint = ""; r.TrustCommand = "" }, ""},
		{"trusted", func(r *v1.GatewayResponse) { r.HostTrust.Status = "trusted" }, ""},
		{"untrusted", func(r *v1.GatewayResponse) { r.HostTrust.Status = "untrusted" }, ""},
		{"state", func(r *v1.GatewayResponse) { r.HostTrust.Status = "maybe" }, "invalid_response"},
		{"context", func(r *v1.GatewayResponse) { r.HostTrust.Context = "invented" }, "invalid_response"},
		{"effective context", func(r *v1.GatewayResponse) { r.HostTrust.Context = "effective-environment" }, "invalid_response"},
		{"store", func(r *v1.GatewayResponse) { r.HostTrust.Store = "" }, "invalid_response"},
		{"message", func(r *v1.GatewayResponse) { r.HostTrust.Message = "" }, "invalid_response"},
		{"pin", func(r *v1.GatewayResponse) { r.RootFingerprint = "invalid" }, "invalid_response"},
		{"uppercase pin", func(r *v1.GatewayResponse) { r.RootFingerprint = strings.ToUpper(pin) }, "invalid_response"},
		{"missing observation", func(r *v1.GatewayResponse) { r.HostTrust = nil }, "invalid_response"},
		{"missing pin", func(r *v1.GatewayResponse) { r.RootFingerprint = "" }, "invalid_response"},
		{"missing hint", func(r *v1.GatewayResponse) { r.TrustCommand = "" }, "invalid_response"},
		{"arbitrary hint", func(r *v1.GatewayResponse) { r.TrustCommand = "touch /tmp/unwanted" }, "invalid_response"},
		{"mismatched pin", func(r *v1.GatewayResponse) { r.RootFingerprint = strings.Repeat("b", 64) }, "invalid_response"},
		{"shell suffix", func(r *v1.GatewayResponse) { r.TrustCommand += "; id" }, "invalid_response"},
		{"unquoted substitution", func(r *v1.GatewayResponse) {
			r.TrustCommand = "backlot gateway trust install --state-dir /state/$(id) --fingerprint " + pin
		}, "invalid_response"},
		{"quote breakout", func(r *v1.GatewayResponse) {
			r.TrustCommand = "backlot gateway trust install --state-dir '/state/'$(id)'x' --fingerprint " + pin
		}, "invalid_response"},
		{"relative state", func(r *v1.GatewayResponse) {
			r.TrustCommand = "backlot gateway trust install --state-dir 'relative' --fingerprint " + pin
		}, "invalid_response"},
		{"NUL state", func(r *v1.GatewayResponse) {
			r.TrustCommand = "backlot gateway trust install --state-dir '/state/\x00' --fingerprint " + pin
		}, "invalid_response"},
		{"body version", func(r *v1.GatewayResponse) { r.APIVersion = "v2" }, "invalid_response"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			response := valid
			o := observation
			response.HostTrust = &o
			tc.change(&response)
			c := gatewayWireClient(t, "/v1/gateway/status", response, 200)
			got, err := c.Gateway(context.Background(), "status", v1.GatewayRequest{APIVersion: v1.Version})
			assertGatewayWireError(t, err, tc.code)
			if err == nil && !reflect.DeepEqual(got, response) {
				t.Fatal("response changed", got)
			}
		})
	}
}

func TestGatewayCAPublicWireContract(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	// Expiry does not erase the retained root's identity or retirement authority.
	root := &x509.Certificate{SerialNumber: big.NewInt(1), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(-time.Minute)}
	der, err := x509.CreateCertificate(rand.Reader, root, root, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(der)
	valid := v1.GatewayCA{APIVersion: v1.Version, StateDirectory: "/owned/state", CertificatePath: filepath.Join("/owned/state", "gateway/data/pki/authorities/local/root.crt"), CertificatePEM: string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})), Fingerprint: hex.EncodeToString(hash[:]), Provenance: "validated-managed-ca-pin", DaemonEffective: v1.TrustObservation{Status: "unknown", Context: "effective-environment", Store: "selected-store", Message: "expired root"}}
	for _, tc := range []struct {
		name   string
		change func(*v1.GatewayCA)
		code   string
	}{
		{"valid expired public snapshot", func(*v1.GatewayCA) {}, ""},
		{"provenance", func(c *v1.GatewayCA) { c.Provenance = "name-only" }, "gateway_ca"},
		{"pin", func(c *v1.GatewayCA) { c.Fingerprint = strings.Repeat("0", 64) }, "gateway_ca"},
		{"source", func(c *v1.GatewayCA) { c.CertificatePath = "/arbitrary/root.crt" }, "gateway_ca"},
		{"missing effective", func(c *v1.GatewayCA) { c.DaemonEffective = v1.TrustObservation{} }, "invalid_response"},
		{"invalid effective", func(c *v1.GatewayCA) { c.DaemonEffective.Status = "maybe" }, "gateway_trust"},
		{"OS context", func(c *v1.GatewayCA) { c.DaemonEffective.Context = "os" }, "invalid_response"},
		{"retained context", func(c *v1.GatewayCA) { c.DaemonEffective.Context = "daemon-effective" }, "invalid_response"},
		{"body version", func(c *v1.GatewayCA) { c.APIVersion = "v2" }, "gateway_ca"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			response := valid
			tc.change(&response)
			c := gatewayWireClient(t, "/v1/gateway/ca", response, 200)
			got, err := c.GatewayCA(context.Background())
			assertGatewayWireError(t, err, tc.code)
			if err == nil && !reflect.DeepEqual(got, response) {
				t.Fatal("snapshot changed", got)
			}
		})
	}
	t.Run("source error", func(t *testing.T) {
		c := gatewayWireClient(t, "/v1/gateway/ca", v1.ErrorResponse{APIVersion: v1.Version, Error: v1.PlanError{Code: "gateway_ca", Message: "source unavailable"}}, 409)
		_, err := c.GatewayCA(context.Background())
		assertGatewayWireError(t, err, "gateway_ca")
		if err.Error() != "source unavailable" {
			t.Fatal(err)
		}
	})
}

func gatewayWireClient(t *testing.T, path string, payload any, status int) *Client {
	t.Helper()
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	c := New("/unused/socket")
	t.Cleanup(c.Close)
	c.http.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodPost || r.URL.Path != path || r.Header.Get("X-Backlot-API-Version") != v1.Version || r.Header.Get("Content-Type") != "application/json" {
			t.Fatal("unexpected request", r.Method, r.URL.Path)
		}
		var request v1.GatewayRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil || request.APIVersion != v1.Version {
			t.Fatal("invalid request", request, err)
		}
		h := make(http.Header)
		h.Set("Content-Type", "application/json")
		h.Set("X-Backlot-API-Version", v1.Version)
		return &http.Response{StatusCode: status, Header: h, Body: io.NopCloser(strings.NewReader(string(data)))}, nil
	})
	return c
}

func assertGatewayWireError(t *testing.T, err error, code string) {
	t.Helper()
	if code == "" {
		if err != nil {
			t.Fatal(err)
		}
		return
	}
	var detail *v1.PlanError
	if !errors.As(err, &detail) || detail.Code != code {
		t.Fatalf("want payload error %s, got %v", code, err)
	}
}
