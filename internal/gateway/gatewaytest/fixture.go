// Package gatewaytest provides an explicitly opted-in, privately owned real gateway.
package gatewaytest

import (
	"bytes"
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
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

// Fixture contains only public fixture connection information and local trust.
type Fixture struct {
	Endpoint string
	Port     int
	Cert     string
	Pool     *x509.CertPool
	Config   string
}

// Port observes a dynamic free port; callers must detect bind conflicts on launch.
func Port(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp4", "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	return port
}

// Start runs only an explicitly configured Caddy binary with private cert/config,
// dynamic listeners, isolated storage and cleanup installed before process start.
func Start(t *testing.T, domain string) Fixture {
	return start(t, domain, false)
}

// StartContainer runs the pinned, pre-pulled Caddy fixture in an owned container.
func StartContainer(t *testing.T, domain string) Fixture {
	return start(t, domain, true)
}

func start(t *testing.T, domain string, inContainer bool) Fixture {
	t.Helper()
	binary := os.Getenv("BACKLOT_CADDY_TEST_BINARY")
	if binary == "" && !inContainer {
		t.Skip("set BACKLOT_CADDY_TEST_BINARY for owned real Caddy HTTPS fixtures")
	}
	directory, err := os.MkdirTemp("", "backlot-https-")
	if err != nil {
		t.Fatal(err)
	}
	preserve := false
	t.Cleanup(func() {
		if preserve {
			t.Log("preserved uncertain private Caddy fixture state", directory)
		} else if err := os.RemoveAll(directory); err != nil {
			t.Error(err)
		}
	})
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 120))
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: serial, Subject: pkix.Name{CommonName: "Backlot isolated fixture"}, DNSNames: []string{domain, "*." + domain}, NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, IsCA: true, BasicConstraintsValid: true}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	certPath, keyPath := filepath.Join(directory, "cert.pem"), filepath.Join(directory, "key.pem")
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	for path, content := range map[string][]byte{certPath: certPEM, keyPath: pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})} {
		if err := os.WriteFile(path, content, 0600); err != nil {
			t.Fatal(err)
		}
	}
	admin, port := Port(t), Port(t)
	for port == admin {
		port = Port(t)
	}
	marker := "fixture-" + serial.Text(16)
	adminHost := "127.0.0.1"
	storage := filepath.Join(directory, "storage")
	if inContainer {
		adminHost = "0.0.0.0"
		storage = "/data/" + marker
	}
	config := map[string]any{
		"admin":   map[string]any{"listen": adminHost + ":" + strconv.Itoa(admin)},
		"storage": map[string]any{"module": "file_system", "root": storage},
		"apps": map[string]any{
			"tls": map[string]any{"certificates": map[string]any{"load_files": []any{map[string]any{"certificate": certPath, "key": keyPath}}}},
			"http": map[string]any{"servers": map[string]any{"fixture": map[string]any{
				"listen": []string{":" + strconv.Itoa(port)}, "automatic_https": map[string]any{"disable": true}, "tls_connection_policies": []any{map[string]any{}},
				"routes": []any{map[string]any{"match": []any{map[string]any{"host": []string{domain, "*." + domain}}}, "handle": []any{map[string]any{"@id": "backlot", "handler": "subroute", "routes": []any{}}}}, map[string]any{"@id": marker, "handle": []any{map[string]any{"handler": "static_response", "body": "unrelated sentinel"}}}},
			}}},
		},
	}
	data, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(directory, "caddy.json")
	if err := os.WriteFile(configPath, data, 0600); err != nil {
		t.Fatal(err)
	}
	if inContainer {
		endpoint := os.Getenv("BACKLOT_DOCKER_TEST_ENDPOINT")
		if endpoint == "" {
			t.Skip("container Caddy requires explicit BACKLOT_DOCKER_TEST_ENDPOINT")
		}
		image := "caddy@sha256:3422ce6de165df66534f9b9ba50efaf457114ec961763cc52f5dbdaac2972d73"
		DockerCommand(t, endpoint, "image", "inspect", image)
		name := marker
		t.Cleanup(func() {
			preserve = !CleanupContainer(t, endpoint, name, name)
		})
		DockerCommand(t, endpoint, "create", "--name", name, "--label", "backlot.fixture="+name, "-p", "127.0.0.1:"+strconv.Itoa(admin)+":"+strconv.Itoa(admin), "-p", strconv.Itoa(port)+":"+strconv.Itoa(port), "-v", directory+":"+directory+":ro", image, "caddy", "run", "--config", configPath)
		DockerCommand(t, endpoint, "start", name)
	} else {
		var logs bytes.Buffer
		command := exec.Command(binary, "run", "--config", configPath)
		command.Env = append(os.Environ(), "XDG_DATA_HOME="+directory, "XDG_CONFIG_HOME="+directory, "HOME="+directory)
		command.Stdout, command.Stderr = &logs, &logs
		started := false
		t.Cleanup(func() {
			if started {
				_ = command.Process.Kill()
				_ = command.Wait()
			}
			if t.Failed() {
				t.Log("private Caddy log:", logs.String())
			}
		})
		if err := command.Start(); err != nil {
			t.Fatal(err)
		}
		started = true
	}
	endpoint := "http://127.0.0.1:" + strconv.Itoa(admin)
	client := &http.Client{Timeout: time.Second}
	for deadline := time.Now().Add(10 * time.Second); ; {
		response, err := client.Get(endpoint + "/id/" + marker)
		if err == nil {
			var identity map[string]any
			decodeErr := json.NewDecoder(response.Body).Decode(&identity)
			_ = response.Body.Close()
			if response.StatusCode == 200 && decodeErr == nil && identity["@id"] == marker {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("private Caddy failed to start")
		}
		time.Sleep(20 * time.Millisecond)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(certPEM) {
		t.Fatal("fixture certificate")
	}
	t.Logf("Caddy private dynamic admin=%d HTTPS=%d; explicit fixture-only trust; cleanup retained direct child", admin, port)
	return Fixture{Endpoint: endpoint, Port: port, Cert: certPath, Pool: pool, Config: configPath}
}
