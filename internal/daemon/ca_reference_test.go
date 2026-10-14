package daemon

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	v1 "github.com/octacian/backlot/api/v1"
	"github.com/octacian/backlot/internal/gateway"
	"github.com/octacian/backlot/internal/manifest"
	"github.com/octacian/backlot/internal/plan"
)

func caReferenceState(t *testing.T) (*store, plan.Snapshot, string) {
	t.Helper()
	directory := t.TempDir()
	if err := os.Chmod(directory, 0700); err != nil {
		t.Fatal(err)
	}
	state, err := openStore(directory)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := state.db.Close(); err != nil {
			t.Error(err)
		}
	})
	record := managedRecord{Scope: newID(), Initialized: true, Status: "stopped", Config: manifest.CaddyDefaults(v1.CaddyConfig{Mode: "managed", Executable: "/explicit/caddy"})}
	_, saved, storage := managedPaths(directory)
	caDir := filepath.Join(storage, "pki", "authorities", "local")
	for _, dir := range []string{filepath.Dir(saved), caDir} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	data, err := gateway.ManagedConfig(managedCaddy(record), storage)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(saved, data, 0600); err != nil {
		t.Fatal(err)
	}
	rootKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	root := &x509.Certificate{SerialNumber: big.NewInt(1), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour)}
	for _, name := range []string{"root", "intermediate"} {
		key := rootKey
		certificate := root
		if name == "intermediate" {
			key, err = ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
			if err != nil {
				t.Fatal(err)
			}
			clone := *root
			clone.SerialNumber = big.NewInt(2)
			certificate = &clone
		}
		der, err := x509.CreateCertificate(rand.Reader, certificate, root, &key.PublicKey, rootKey)
		if err != nil {
			t.Fatal(err)
		}
		encoded, err := x509.MarshalECPrivateKey(key)
		if err != nil {
			t.Fatal(err)
		}
		for ext, block := range map[string]*pem.Block{".crt": {Type: "CERTIFICATE", Bytes: der}, ".key": {Type: "EC PRIVATE KEY", Bytes: encoded}} {
			if err := os.WriteFile(filepath.Join(caDir, name+ext), pem.EncodeToMemory(block), 0600); err != nil {
				t.Fatal(err)
			}
		}
	}
	record.RootHash, err = validateCA(storage, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := state.saveManaged(record); err != nil {
		t.Fatal(err)
	}
	reference := v1.PlannedValue{Symbolic: &v1.Reference{Kind: "resource", Name: "origin", Field: "ca_cert_path"}}
	snapshot := plan.Snapshot{Caddy: &record.Config, Secrets: map[string]v1.PlannedValue{}, Plan: v1.PlanResponse{Publish: &v1.Publication{Resource: "origin"}, Resources: map[string]v1.Resource{"origin": {Kind: "origin"}}, Components: []v1.PlannedComponent{{Name: "client", Runtime: v1.Native, Environment: map[string]v1.PlannedValue{"CA": reference}}}}}
	return state, snapshot, filepath.Join(caDir, "root.crt")
}

func TestConsumerCACertPath(t *testing.T) {
	for _, name := range []string{"valid", "secret mapping", "no mapping", "external", "no publication", "container", "no pin", "changed pin", "missing root", "missing key", "root symlink", "directory symlink", "unsafe permissions", "private PEM appended"} {
		t.Run(name, func(t *testing.T) {
			state, snapshot, path := caReferenceState(t)
			switch name {
			case "secret mapping":
				snapshot.Secrets["client/environment/CA"] = snapshot.Plan.Components[0].Environment["CA"]
				snapshot.Plan.Components[0].Environment["CA"] = v1.PlannedValue{Redacted: true}
			case "no mapping":
				snapshot.Plan.Components[0].Environment = nil
			case "external":
				snapshot.Caddy = &v1.CaddyConfig{Mode: "external"}
			case "no publication":
				snapshot.Plan.Publish = nil
			case "container":
				snapshot.Plan.Components[0].Runtime = v1.Container
			case "no pin", "changed pin":
				record, err := state.managed()
				if err != nil {
					t.Fatal(err)
				}
				record.RootHash = ""
				if name == "changed pin" {
					record.RootHash = strings.Repeat("0", 64)
				}
				if err := state.saveManaged(record); err != nil {
					t.Fatal(err)
				}
			case "missing root":
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			case "missing key":
				if err := os.Remove(strings.TrimSuffix(path, ".crt") + ".key"); err != nil {
					t.Fatal(err)
				}
			case "root symlink", "directory symlink":
				target := path
				if name == "directory symlink" {
					target = filepath.Dir(path)
				}
				if err := os.Rename(target, target+".held"); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target+".held", target); err != nil {
					t.Fatal(err)
				}
			case "unsafe permissions":
				if err := os.Chmod(path, 0644); err != nil {
					t.Fatal(err)
				}
			case "private PEM appended":
				data, err := os.ReadFile(strings.TrimSuffix(path, ".crt") + ".key")
				if err != nil {
					t.Fatal(err)
				}
				f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
				if err != nil {
					t.Fatal(err)
				}
				_, writeErr := f.Write(data)
				closeErr := f.Close()
				if writeErr != nil || closeErr != nil {
					t.Fatal(writeErr, closeErr)
				}
			}
			actual, err := state.consumerCACertPath(snapshot)
			valid := name == "valid" || name == "secret mapping" || name == "no mapping"
			if (err == nil) != valid {
				t.Fatal(actual, err)
			}
			if name == "no mapping" {
				if actual != "" {
					t.Fatal("implicit trust path exposed")
				}
				return
			}
			if !valid {
				if actual != "" {
					t.Fatal("invalid CA exposed", actual)
				}
				return
			}
			if actual != path || strings.Contains(actual, ".key") {
				t.Fatal("did not resolve existing public root", actual)
			}
			value := snapshot.Plan.Components[0].Environment["CA"]
			resolved, err := resolvedValue(value, "client/environment/CA", "id", snapshot, &v1.ExecutionResult{}, &execution{caCertPath: actual}, v1.Native)
			if err != nil || resolved != path {
				t.Fatal(resolved, err)
			}
			if _, err := resolvedValue(value, "client/environment/CA", "id", snapshot, &v1.ExecutionResult{}, &execution{caCertPath: actual}, v1.Container); err == nil {
				t.Fatal("container consumed native CA path")
			}
		})
	}
}
