//go:build darwin || linux

package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/mount"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	moby "github.com/moby/moby/client"
	v1 "github.com/octacian/backlot/api/v1"
)

// TestDoctorRuntime exercises actual CLI/API/daemon/provider/data observations.
// Private fixture trust is explicit; no resolver or machine/browser trust changes.
func TestDoctorRuntime(t *testing.T) {
	caddy, endpoint, image := os.Getenv("BACKLOT_CADDY_TEST_BINARY"), os.Getenv("BACKLOT_DOCKER_TEST_ENDPOINT"), os.Getenv("BACKLOT_DOCTOR_TEST_IMAGE")
	if caddy == "" || endpoint == "" || image == "" {
		t.Skip("requires explicit owned Caddy/Docker grant and preexisting shell/getent/timeout/OpenSSL diagnostic image")
	}
	build := t.TempDir()
	binary, app := filepath.Join(build, "backlot"), filepath.Join(build, "app")
	for _, target := range []struct{ out, source string }{{binary, "../../cmd/backlot"}, {app, "../gateway/testdata/fixture"}} {
		if data, err := exec.Command("go", "build", "-race", "-o", target.out, target.source).CombinedOutput(); err != nil {
			t.Fatal(err, string(data))
		}
	}
	trust := filepath.Join(build, "trust.pem")
	t.Setenv("SSL_CERT_FILE", trust)
	t.Setenv("SSL_CERT_DIR", build)
	h := newNativeHarness(t, binary, "30s")
	t.Log("owned fixture root", filepath.Dir(h.dir), "binary", binary, "probe image", image)
	configPath := filepath.Join(filepath.Dir(h.dir), "machine.json")
	port, admin := managedFixturePort(t), managedFixturePort(t)
	config := v1.MachineConfig{Version: v1.ManifestVersion, Docker: &v1.DockerConfig{Endpoint: endpoint}, Caddy: &v1.CaddyConfig{Mode: "managed", Executable: caddy, HTTPSBindAddress: "0.0.0.0", HTTPSPort: &port, Endpoint: "http://127.0.0.1:" + strconv.Itoa(admin)}}
	writeConfig := func() {
		data, _ := json.Marshal(config)
		if err := os.WriteFile(configPath, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	writeConfig()
	command := func(success bool, args ...string) []byte {
		t.Helper()
		args = append(args, "--state-dir", h.dir, "--json")
		data, err := exec.Command(binary, args...).Output()
		if (err == nil) != success {
			t.Fatalf("CLI %v: %v %s", args, err, data)
		}
		return data
	}
	t.Cleanup(func() {
		h.close()
		if h.daemonLog.Len() > 0 {
			t.Log("daemon output", h.daemonLog.String())
		}
		state, err := openStore(h.dir)
		if err == nil {
			err = errors.Join(state.recoverDiagnosticProbes(), state.recoverRuntime(), state.recoverManaged(), state.cleanupFailures())
			record, e := state.managed()
			err = errors.Join(err, e)
			if record.Pending || record.Identity != nil {
				err = errors.Join(err, errors.New("gateway not joined"))
			}
			err = errors.Join(err, state.db.Close())
		}
		if err != nil {
			h.cleanupUnverified = true
			t.Error("preserving uncertain fixture", h.dir, err)
		} else {
			t.Log("all diagnostic, scene, native guardian and gateway resources verified cleaned")
		}
	})
	command(true, "gateway", "start", "--config", configPath)
	caPath := filepath.Join(h.dir, "gateway", "data", "pki", "authorities", "local", "root.crt")
	ca, err := os.ReadFile(caPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(trust, ca, 0600); err != nil {
		t.Fatal(err)
	}
	portRef := fixtureRef("resource", "port", "port")
	originRef := fixtureRef("resource", "origin", "url")
	server := v1.Component{Kind: v1.Service, Runtime: v1.Native, Command: &v1.Command{Tool: "app"}, Resources: []string{"port", "origin", "data"}, Ports: map[string]v1.ServicePort{"http": {Resource: "port"}}, Environment: v1.Environment{Assign: map[string]v1.Value{"PORT": portRef, "ROLE": fixtureLiteral("doctor")}}, Readiness: &v1.Probe{Kind: "tcp", Target: &portRef, Timeout: "5s"}}
	consumerPort := fixtureRef("resource", "consumer-port", "port")
	consumer := v1.Component{Kind: v1.Service, Runtime: v1.Container, Image: ptrValue(fixtureLiteral("alpine:3.21")), Args: []string{"sh", "-c", "while true; do nc -l -p 9000; done"}, Resources: []string{"consumer-port", "origin"}, Ports: map[string]v1.ServicePort{"tcp": {Resource: "consumer-port", ContainerPort: 9000}}, HostMappings: []v1.HostMapping{{Origin: "origin", Host: "host-gateway"}}, Readiness: &v1.Probe{Kind: "tcp", Target: &consumerPort, Timeout: "10s"}}
	h.manifest = v1.Manifest{Version: v1.ManifestVersion, Project: "doctor-fixture", Tools: map[string]string{"app": app}, Resources: map[string]v1.Resource{"port": {Kind: "port"}, "consumer-port": {Kind: "port"}, "origin": {Kind: "origin"}, "data": {Kind: "directory"}}, Components: map[string]v1.Component{"server": server, "consumer": consumer}, Scenes: map[string]v1.Scene{"dev": {Lifetime: v1.Persistent, Components: []string{"server", "consumer"}, Resources: []string{"port", "consumer-port", "origin", "data"}, Publish: &v1.Publication{Resource: "origin", Routes: []v1.Route{{Path: "/", Service: "server", Port: "http", Prefix: "preserve"}}, Probe: v1.Probe{Kind: "http", Target: &originRef, Timeout: "10s"}}}}}
	server.Fixtures = map[string]v1.Fixture{"data": {Value: fixtureRef("resource", "data", "path")}}
	h.manifest.Components["server"] = server
	h.write()
	var launched v1.InstanceResponse
	if err := json.Unmarshal(command(true, "run", "--project", h.project, "--config", configPath, "dev"), &launched); err != nil {
		t.Fatal(err)
	}
	id := launched.Instance.ID
	t.Run("client-resolver-evidence", func(t *testing.T) {
		observeDoctorCurlResolver(t, endpoint, image, launched.Instance.Execution.Origin, caPath)
	})
	state, err := h.cli.Doctor(context.Background(), v1.DoctorRequest{APIVersion: v1.Version, InstanceID: id})
	if err != nil || !state.Healthy {
		t.Fatal(err, state)
	}
	doctor := func(success bool, extra ...string) v1.DoctorResponse {
		t.Helper()
		args := append([]string{"doctor", "--instance", id}, extra...)
		data := command(success, args...)
		var result v1.DoctorResponse
		if err := json.Unmarshal(data, &result); err != nil {
			t.Fatal(err, string(data))
		}
		if err := result.Validate(); err != nil {
			t.Fatal(err)
		}
		return result
	}
	good := doctor(true, "--container-probe-image", image, "--probe-public-ca")
	assertDoctorCheck(t, good, "container_dns", v1.DoctorOK)
	assertDoctorCheck(t, good, "container_https", v1.DoctorOK)
	assertDoctorCheck(t, good, "container_probe_cleanup", v1.DoctorOK)
	encoded, _ := json.Marshal(good)
	t.Log("early successful CLI doctor", string(encoded))
	for _, path := range good.Paths {
		if path.Name == "public_ca_certificate" && path.Path != caPath {
			t.Fatal("wrong public CA path", path)
		}
	}
	human, err := exec.Command(binary, "doctor", "--instance", id, "--state-dir", h.dir).Output()
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{"launch_machine_config", "current_machine_config", configPath, "public_ca_certificate", caPath, "daemon/host system trust"} {
		if !strings.Contains(string(human), required) {
			t.Fatal("human diagnostic path/context missing", required, string(human))
		}
	}
	unsupported := doctor(true, "--container-probe-image", "alpine:3.21")
	assertDoctorCheck(t, unsupported, "container_probe_contract", v1.DoctorUnsupported)
	assertDoctorCheck(t, unsupported, "container_probe_cleanup", v1.DoctorOK)
	bad := doctor(false, "--container-probe-image", image)
	assertDoctorCheck(t, bad, "container_https", v1.DoctorFailed)
	assertDoctorCheck(t, bad, "container_probe_cleanup", v1.DoctorOK)
	t.Log("early failure: container image system trust rejects private fixture CA; explicit CA recovery follows")
	doctor(true, "--container-probe-image", image, "--probe-public-ca")
	// Current-file failure cannot redirect recorded diagnostics or launch provenance.
	original, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	config.Docker.Endpoint = "unix:///nonexistent-doctor-fixture.sock"
	writeConfig()
	changed := doctor(false)
	assertDoctorCheck(t, changed, "current_machine_config", v1.DoctorFailed)
	assertDoctorCheck(t, changed, "docker_endpoint", v1.DoctorOK)
	if err := os.WriteFile(configPath, original, 0600); err != nil {
		t.Fatal(err)
	}
	doctor(true)
	// Scene selection validates inputs without allocating a runtime or asserting DNS/TLS.
	selected := command(true, "doctor", "--project", h.project, "--scene", "dev", "--config", configPath)
	if !strings.Contains(string(selected), "deferred") {
		t.Fatal("missing unavailable runtime deferral")
	}
	// Verify real persistent data remains intact across diagnostics.
	store, err := openReadOnlyFixtureResources(h, id)
	if err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(store, "doctor-marker")
	if err := os.WriteFile(marker, []byte("retained"), 0600); err != nil {
		t.Fatal(err)
	}
	doctor(true)
	data, err := os.ReadFile(marker)
	if err != nil || string(data) != "retained" {
		t.Fatal("diagnostics changed owned data", err)
	}
	command(true, "destroy", id)
	// Every created probe name must have disappeared from the actual engine.
	d, err := newDocker(endpoint)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = d.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	containers, err := d.ContainerList(ctx, moby.ContainerListOptions{All: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range containers.Items {
		for _, name := range c.Names {
			if strings.HasPrefix(name, "/backlot-doctor-") {
				observed, err := d.ContainerInspect(ctx, c.ID, moby.ContainerInspectOptions{})
				if err != nil {
					t.Fatal(err)
				}
				if observed.Container.Config != nil && slices.Contains(observed.Container.Config.Cmd, launched.Instance.Execution.Origin) {
					t.Fatal("this fixture origin has a diagnostic residual", name)
				}
			}
		}
	}
}

// The fixture's public data directory is located via the retained resource view,
// without opening a running daemon's database or disclosing resource capabilities.
func openReadOnlyFixtureResources(h *nativeHarness, id string) (string, error) {
	// The fixture declares a public non-secret path explicitly, then asks fixtures.
	response, err := h.cli.Fixtures(context.Background(), v1.FixturesRequest{APIVersion: v1.Version, InstanceID: id})
	if err != nil {
		return "", err
	}
	for _, f := range response.Fixtures {
		if f.Name == "data" {
			return f.Value, nil
		}
	}
	return "", errors.New("missing fixture data path")
}

// observeDoctorCurlResolver records a client-specific negative control with
// exact owned resources and full normal TLS; it is not the production probe.
func observeDoctorCurlResolver(t *testing.T, endpoint, image, origin, caPath string) {
	t.Helper()
	root, err := os.MkdirTemp("/tmp", "bl-doctor-curl-")
	if err != nil {
		t.Fatal(err)
	}
	state, err := openStore(root)
	if err != nil {
		t.Fatal(err)
	}
	d, err := newDocker(endpoint)
	if err != nil {
		t.Fatal(err)
	}
	key := newID()
	record := diagnosticProbeRecord{Endpoint: endpoint, Resource: ownedResource{Kind: "container", Name: "backlot-doctor-" + key, Token: newID()}, Network: ownedResource{Kind: "network", Name: "backlot-doctor-network-" + key, Token: newID()}}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		err := cleanupDiagnosticProbe(ctx, d, record)
		if err == nil {
			err = state.forgetDiagnosticProbe(key)
		}
		err = errors.Join(err, state.db.Close(), d.Close())
		if err != nil {
			t.Error("curl evidence fixture cleanup uncertain; preserved", root, err)
		} else if err := os.RemoveAll(root); err != nil {
			t.Error(err)
		}
	})
	if err := state.saveDiagnosticProbe(key, record); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	network, err := createDockerResource(ctx, d, record.Network)
	if err != nil {
		t.Fatal(err)
	}
	target, err := url.Parse(origin)
	if err != nil {
		t.Fatal(err)
	}
	created, err := d.ContainerCreate(ctx, moby.ContainerCreateOptions{Name: record.Resource.Name, Config: &container.Config{Image: image, Entrypoint: []string{"/bin/sh", "-c", `getent hosts "$1"; curl --noproxy '*' --cacert /public-ca.crt --max-time 2 --verbose --output /dev/null "$2"`, "resolver-evidence"}, Cmd: []string{target.Hostname(), origin}, Tty: true, Labels: resourceLabels(record.Resource)}, HostConfig: &container.HostConfig{NetworkMode: container.NetworkMode(network), ExtraHosts: []string{target.Hostname() + ":host-gateway"}, ReadonlyRootfs: true, Mounts: []mount.Mount{{Type: mount.TypeBind, Source: caPath, Target: "/public-ca.crt", ReadOnly: true}}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.ContainerStart(ctx, created.ID, moby.ContainerStartOptions{}); err != nil {
		t.Fatal(err)
	}
	for {
		out, err := d.ContainerInspect(ctx, created.ID, moby.ContainerInspectOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if !out.Container.State.Running {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("curl evidence timeout")
		case <-time.After(50 * time.Millisecond):
		}
	}
	logs, err := d.ContainerLogs(ctx, created.ID, moby.ContainerLogsOptions{ShowStdout: true, ShowStderr: true})
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(io.LimitReader(logs, 8192))
	closeErr := logs.Close()
	if err != nil || closeErr != nil {
		t.Fatal(err, closeErr)
	}
	// Only resolver/connection facts are emitted, never provider/raw TLS output.
	lines := strings.Split(string(data), "\n")
	for _, line := range lines {
		if strings.Contains(line, target.Hostname()) && !strings.HasPrefix(line, "*") {
			t.Log("getent canonical mapping", strings.TrimSpace(line))
		}
		if strings.Contains(line, "Trying ") {
			t.Log("curl canonical connection choice", strings.TrimSpace(line))
		}
	}
}
