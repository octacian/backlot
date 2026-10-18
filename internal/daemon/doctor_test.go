package daemon

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	v1 "github.com/octacian/backlot/api/v1"
	"github.com/octacian/backlot/internal/plan"
)

func TestDoctorHostSelectionAndRedaction(t *testing.T) {
	dir := t.TempDir()
	state, err := openStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = state.db.Close() }()
	trapDir, err := os.MkdirTemp("/tmp", "bl-doctor-trap-")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(trapDir) }()
	trap, err := net.Listen("unix", filepath.Join(trapDir, "engine.sock"))
	if err != nil {
		t.Fatal(err)
	}
	var contacts atomic.Int64
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			c, err := trap.Accept()
			if err != nil {
				return
			}
			contacts.Add(1)
			_ = c.Close()
		}
	}()
	defer func() { _ = trap.Close(); <-done }()
	project := t.TempDir()
	// An unselected container and a configured endpoint must not trigger Docker.
	manifest := v1.Manifest{Version: v1.ManifestVersion, Project: "doctor", Tools: map[string]string{"true": "/usr/bin/true"}, Components: map[string]v1.Component{"job": {Kind: v1.Job, Runtime: v1.Native, Command: &v1.Command{Tool: "true"}, Policy: v1.EachStart, Environment: v1.Environment{Assign: map[string]v1.Value{"PASSWORD": {Literal: ptrDoctorString("credential-marker"), Secret: true}}}}, "unselected": {Kind: v1.Job, Runtime: v1.Container, Image: &v1.Value{Literal: ptrDoctorString("secret-image:marker")}, Policy: v1.EachStart}}, Scenes: map[string]v1.Scene{"dev": {Lifetime: v1.Persistent, Components: []string{"job"}}}}
	writeDoctorJSON(t, filepath.Join(project, "backlot.json"), manifest)
	config := filepath.Join(dir, "machine.json")
	writeDoctorJSON(t, config, v1.MachineConfig{Version: v1.ManifestVersion, Docker: &v1.DockerConfig{Endpoint: "unix://" + trap.Addr().String()}})
	svc := service{store: state, directory: dir}
	r, err := svc.doctor(context.Background(), v1.DoctorRequest{APIVersion: v1.Version, ProjectPath: project, Scene: "dev", ConfigPath: config})
	if err != nil || !r.Healthy || r.Validate() != nil {
		t.Fatal(err, r)
	}
	if contacts.Load() != 0 {
		t.Fatal("host-only scene contacted Docker")
	}
	encoded, _ := json.Marshal(r)
	if strings.Contains(string(encoded), "credential-marker") || strings.Contains(string(encoded), "secret-image") {
		t.Fatal("secret declaration escaped diagnostic")
	}
	assertDoctorCheck(t, r, "upstreams", v1.DoctorDeferred)
	// A selected runtime input failure is a typed failed result without exposing
	// secret values or raw parser/provider diagnostics.
	manifest.Components["job"] = v1.Component{Kind: v1.Job, Runtime: v1.Native, Policy: v1.EachStart, Command: &v1.Command{Tool: "missing"}}
	writeDoctorJSON(t, filepath.Join(project, "backlot.json"), manifest)
	r, err = svc.doctor(context.Background(), v1.DoctorRequest{APIVersion: v1.Version, ProjectPath: project, Scene: "dev", ConfigPath: config})
	if err != nil || r.Healthy {
		t.Fatal(err, r)
	}
	assertDoctorCheck(t, r, "runtime_inputs", v1.DoctorFailed)
	if contacts.Load() != 0 {
		t.Fatal("invalid input contacted Docker")
	}
}
func ptrDoctorString(s string) *string { return &s }
func writeDoctorJSON(t *testing.T, path string, value any) {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
}

func TestDoctorConfigProvenance(t *testing.T) {
	r := v1.DoctorResponse{Checks: []v1.DoctorCheck{}, Paths: []v1.DoctorPath{}}
	legacy := plan.Snapshot{Plan: v1.PlanResponse{ManifestPath: "/original/backlot.yaml", ManifestDigest: "launch-digest", ConfigDigest: "config-digest"}}
	addLaunchPaths(&r, legacy)
	if r.Paths[1].State != "unrecorded" || r.Paths[1].Path != "" {
		t.Fatal("invented legacy configuration path", r.Paths)
	}
	legacy.ConfigProvenance = &plan.ConfigProvenance{Path: "/default/config.yaml", Present: false}
	r.Paths = nil
	addConfigPath(&r, "launch_machine_config", legacy)
	if r.Paths[0].State != "absent" {
		t.Fatal("missing default represented as configured", r.Paths)
	}
	file := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(file, []byte("current"), 0600); err != nil {
		t.Fatal(err)
	}
	checkCurrentFile(&r, "current_config", file, "original-digest")
	if r.Paths[1].State != "changed" {
		t.Fatal("drift not identified", r.Paths)
	}
}

func TestDoctorCertificateDatesAndPublicOnly(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	for _, test := range []struct {
		name          string
		before, after time.Time
		valid         bool
	}{{"valid", now.Add(-time.Hour), now.Add(time.Hour), true}, {"expired", now.Add(-2 * time.Hour), now.Add(-time.Hour), false}, {"future", now.Add(time.Hour), now.Add(2 * time.Hour), false}} {
		t.Run(test.name, func(t *testing.T) {
			template := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "owned fixture"}, NotBefore: test.before, NotAfter: test.after, IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
			der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(t.TempDir(), "root.crt")
			data := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
			if err := os.WriteFile(path, data, 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := diagnosticCA(path); (err == nil) != test.valid {
				t.Fatalf("dates: %v", err)
			}
			data = append(data, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: []byte("key-marker")})...)
			if err := os.WriteFile(path, data, 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := diagnosticCA(path); err == nil {
				t.Fatal("accepted key input as public certificate")
			}
		})
	}
}

func assertDoctorCheck(t *testing.T, r v1.DoctorResponse, name string, status v1.DoctorStatus) {
	t.Helper()
	for _, c := range r.Checks {
		if c.Name == name {
			if c.Status != status {
				t.Fatalf("%s: got %s want %s: %s", name, c.Status, status, c.Message)
			}
			return
		}
	}
	t.Fatalf("missing check %s", name)
}

func TestDoctorNormallyVerifiedTLSRejectsWrongTrustAndHostname(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))
	defer server.Close()
	origin, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(server.Certificate())
	r := v1.DoctorResponse{Healthy: true, Checks: []v1.DoctorCheck{}}
	doctorTLS(context.Background(), &r, origin, pool, "correct", "fixture explicit root")
	assertDoctorCheck(t, r, "correct", v1.DoctorOK)
	doctorTLS(context.Background(), &r, origin, x509.NewCertPool(), "wrong_trust", "fixture wrong explicit root")
	assertDoctorCheck(t, r, "wrong_trust", v1.DoctorFailed)
	origin.Host = net.JoinHostPort("localhost", origin.Port())
	doctorTLS(context.Background(), &r, origin, pool, "wrong_hostname", "fixture explicit root")
	assertDoctorCheck(t, r, "wrong_hostname", v1.DoctorFailed)
}

func TestDoctorTLSHandshakeTimeoutIsBounded(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	accepted := make(chan net.Conn, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		c, err := listener.Accept()
		if err == nil {
			accepted <- c
		}
	}()
	defer func() {
		_ = listener.Close()
		<-done
		select {
		case c := <-accepted:
			_ = c.Close()
		default:
		}
	}()
	origin, err := url.Parse("https://" + listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	r := v1.DoctorResponse{Healthy: true, Checks: []v1.DoctorCheck{}}
	doctorTLS(ctx, &r, origin, nil, "timeout", "fixture system trust")
	assertDoctorCheck(t, r, "timeout", v1.DoctorFailed)
}
