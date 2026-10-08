//go:build darwin || linux

package daemon

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/containerd/errdefs"
	"github.com/moby/moby/api/pkg/stdcopy"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/mount"
	"github.com/moby/moby/client"
	v1 "github.com/octacian/backlot/api/v1"
	bolt "go.etcd.io/bbolt"
)

type dockerHarness struct {
	*nativeHarness
	engine                  *dockerEngine
	config, endpoint, token string
	ids                     []string
}

func fixtureLiteral(text string) v1.Value { return v1.Value{Literal: &text} }
func fixtureRef(kind, name, field string) v1.Value {
	return v1.Value{Ref: &v1.Reference{Kind: kind, Name: name, Field: field}}
}
func newDockerHarness(t *testing.T, binary, endpoint string) *dockerHarness {
	t.Helper()
	h := &dockerHarness{nativeHarness: newNativeHarness(t, binary, "30s"), endpoint: endpoint, token: newID()}
	var err error
	h.engine, err = newDocker(endpoint)
	if err != nil {
		t.Fatal(err)
	}
	h.tokens = map[string]string{}
	h.config = filepath.Join(filepath.Dir(h.dir), "machine.json")
	data, _ := json.Marshal(v1.MachineConfig{Version: v1.ManifestVersion, Docker: &v1.DockerConfig{Endpoint: endpoint}})
	if err = os.WriteFile(h.config, data, 0600); err != nil {
		t.Fatal(err)
	}
	h.manifest = v1.Manifest{Version: v1.ManifestVersion, Project: "docker-fixture", Resources: map[string]v1.Resource{"data": {Kind: "volume"}, "directory": {Kind: "directory"}, "secret": {Kind: "secret"}, "port": {Kind: "port"}}, Components: map[string]v1.Component{}, Scenes: map[string]v1.Scene{}}
	// Register full private-journal reconciliation before any test can invoke
	// the real CLI: acceptance can be durable even when its output fails.
	t.Cleanup(h.cleanup)

	return h
}
func fixtureInstanceIDs(state *store) ([]string, error) {
	var ids []string
	err := state.db.View(func(tx *bolt.Tx) error {
		return tx.Bucket([]byte("instances")).ForEach(func(key, _ []byte) error {
			id := string(key)
			if _, err := load(tx, id); err != nil {
				return err
			}
			ids = append(ids, id)
			return nil
		})
	})
	return ids, err
}
func (h *dockerHarness) cleanup() {
	h.t.Helper()
	// Join the retained daemon child before opening its private store. h.ids is
	// only observation bookkeeping, never the completeness/cleanup authority.
	h.close()
	state, err := openStore(h.dir)
	if err == nil {
		var ids []string
		ids, err = fixtureInstanceIDs(state)
		for _, id := range ids {
			if recoveryErr := state.recoverExecution(id); recoveryErr != nil {
				err = errors.Join(err, recoveryErr)
				continue
			}
			instance, inspectErr := state.inspect(id)
			if inspectErr != nil {
				err = errors.Join(err, inspectErr)
				continue
			}
			if instance.Execution != nil && instance.Execution.CleanupFailure != "" {
				err = errors.Join(err, errors.New(instance.Execution.CleanupFailure))
				continue
			}
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			err = errors.Join(err, state.removeResources(ctx, id))
			cancel()
		}
		err = errors.Join(err, state.db.Close())
	}
	if err != nil {
		h.cleanupUnverified = true
		h.t.Error("fixture journal reconciliation failed; preserving private state", h.dir, err)
	}
	if err := h.engine.Close(); err != nil {
		h.t.Error(err)
	}
}
func (h *dockerHarness) request(scene string) v1.RunRequest {
	request := h.nativeHarness.request(scene)
	request.Plan.ConfigPath = h.config
	request.Options.StartupTimeout = "90s"
	request.Options.JobTimeout = "90s"
	request.Options.StopGrace = "1s"
	return request
}
func (h *dockerHarness) run(scene string) v1.InstanceResponse {
	h.t.Helper()
	response, err := h.cli.Run(context.Background(), h.request(scene))
	if err != nil {
		h.t.Fatal(err)
	}
	h.ids = append(h.ids, response.Instance.ID)
	if response.LeaseToken != "" {
		if h.tokens == nil {
			h.tokens = map[string]string{}
		}
		h.tokens[response.Instance.ID] = response.LeaseToken
	}
	return response
}
func (h *dockerHarness) state(id string, want ...v1.InstanceStatus) v1.InstanceResponse {
	h.t.Helper()
	deadline := time.Now().Add(100 * time.Second)
	var last v1.InstanceResponse
	for time.Now().Before(deadline) {
		var err error
		last, err = h.cli.Inspect(context.Background(), id)
		if err != nil {
			h.t.Fatal(err)
		}
		for _, status := range want {
			if last.Instance.Status == status {
				return last
			}
		}
		if last.Instance.Status == v1.Failed || last.Instance.Status == v1.Interrupted {
			h.t.Fatalf("unexpected terminal state: %+v", last.Instance.Execution)
		}
		if token := h.tokens[id]; token != "" && (last.Instance.Status == v1.Starting || last.Instance.Status == v1.RuntimeReady) {
			if _, err = h.cli.Renew(context.Background(), id, token); err != nil {
				current, inspectErr := h.cli.Inspect(context.Background(), id)
				if inspectErr != nil || current.Instance.Status == v1.Starting || current.Instance.Status == v1.RuntimeReady {
					h.t.Fatal(err)
				}
			}
		}
		time.Sleep(25 * time.Millisecond)
	}
	h.t.Fatalf("wanted %v, got %s: %+v", want, last.Instance.Status, last.Instance.Execution)
	return last
}
func (h *dockerHarness) componentEnv() v1.Environment {
	return v1.Environment{Assign: map[string]v1.Value{"BACKLOT_FIXTURE": fixtureLiteral(h.token), "INSTANCE": fixtureRef("instance", "", "id"), "SECRET": fixtureRef("resource", "secret", "value")}}
}
func (h *dockerHarness) alpine() {
	env := h.componentEnv()
	init := v1.Component{Kind: v1.Job, Runtime: v1.Container, Image: ptrValue(fixtureLiteral("alpine:3.21")), Args: []string{"sh", "-c", "echo initialized >> /data/init; printf '%s' \"$SECRET\" > /private/secret"}, Policy: v1.FreshOnly, Initializes: []string{"data", "directory"}, Resources: []string{"data", "directory", "secret"}, Mounts: []v1.Mount{{Resource: "data", Target: "/data"}, {Resource: "directory", Target: "/private"}}, Environment: env}
	server := v1.Component{Kind: v1.Service, Runtime: v1.Container, Image: ptrValue(fixtureLiteral("alpine:3.21")), Args: []string{"sh", "-c", "while true; do nc -l -p 8080 < /dev/null; done"}, Resources: []string{"data", "directory", "secret", "port"}, Mounts: init.Mounts, Environment: env, Ports: map[string]v1.ServicePort{"http": {Resource: "port", ContainerPort: 8080}}, Readiness: &v1.Probe{Kind: "tcp", Target: ptrValue(fixtureRef("resource", "port", "port")), Timeout: "30s"}, DependsOn: []v1.Dependency{{Component: "init", Condition: v1.Completed}}}
	each := v1.Component{Kind: v1.Job, Runtime: v1.Container, Image: ptrValue(fixtureLiteral("alpine:3.21")), Args: []string{"sh", "-c", "test \"$(cat /private/secret)\" = \"$SECRET\"; echo start >> /data/starts; echo terminal-output"}, Policy: v1.EachStart, Resources: []string{"data", "directory", "secret"}, Mounts: init.Mounts, Environment: env, DependsOn: []v1.Dependency{{Component: "server", Condition: v1.Ready}}}
	h.manifest.Components = map[string]v1.Component{"init": init, "server": server, "each": each}
	resources := []string{"data", "directory", "secret", "port"}
	h.manifest.Scenes = map[string]v1.Scene{"dev": {Lifetime: v1.Persistent, Components: []string{"init", "server", "each"}, Resources: resources}, "test": {Lifetime: v1.Disposable, Components: []string{"init", "server", "each"}, Resources: resources, TerminalJob: "each"}}
	h.write()
}
func ptrValue(value v1.Value) *v1.Value { return &value }
func (h *dockerHarness) container(id, component string) ownedResource {
	h.t.Helper()
	list, err := h.engine.ContainerList(context.Background(), client.ContainerListOptions{All: true, Filters: client.Filters{}.Add("label", "io.backlot.owner")})
	if err != nil {
		h.t.Fatal(err)
	}
	for _, item := range list.Items {
		inspect, err := h.engine.ContainerInspect(context.Background(), item.ID, client.ContainerInspectOptions{})
		if errdefs.IsNotFound(err) {
			continue
		}
		if err != nil {
			h.t.Fatal(err)
		}
		c := inspect.Container
		if contains(c.Config.Env, "BACKLOT_FIXTURE="+h.token) && contains(c.Config.Env, "INSTANCE="+id) {
			// Services have a distinct command, so completed jobs cannot be mistaken for the target.
			if component == "db" && c.Config.Image != "mariadb:11.4" {
				continue
			}
			if component == "server" && !strings.Contains(strings.Join(c.Config.Cmd, " "), "nc -l") {
				continue
			}
			return ownedResource{Kind: "container", ID: c.ID, Token: c.Config.Labels["io.backlot.owner"]}
		}
	}
	h.t.Fatal("fixture owned service container missing", component)
	return ownedResource{}
}
func (h *dockerHarness) exec(r ownedResource, command []string) (string, int) {
	h.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := verifyDockerResource(ctx, h.engine, r); err != nil {
		h.t.Fatal(err)
	}
	created, err := h.engine.ExecCreate(ctx, r.ID, client.ExecCreateOptions{Cmd: command, AttachStdout: true, AttachStderr: true})
	if err != nil {
		h.t.Fatal(err)
	}
	attached, err := h.engine.ExecAttach(ctx, created.ID, client.ExecAttachOptions{})
	if err != nil {
		h.t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	_, err = stdcopy.StdCopy(&stdout, &stderr, attached.Reader)
	attached.Close()
	if err != nil {
		h.t.Fatal(err)
	}
	result, err := h.engine.ExecInspect(ctx, created.ID, client.ExecInspectOptions{})
	if err != nil {
		h.t.Fatal(err)
	}
	return stdout.String(), result.ExitCode
}
func (h *dockerHarness) sql(r ownedResource, query string) string {
	h.t.Helper()
	deadline := time.Now().Add(45 * time.Second)
	for time.Now().Before(deadline) {
		out, code := h.exec(r, []string{"sh", "-c", "exec mariadb -uroot -p\"$MARIADB_ROOT_PASSWORD\" -N -e \"$1\"", "fixture", query})
		if code == 0 {
			return strings.TrimSpace(out)
		}
		time.Sleep(100 * time.Millisecond)
	}
	h.t.Fatal("fixture SQL deadline")
	return ""
}
func (h *dockerHarness) crash() {
	h.t.Helper()
	if err := h.process.Process.Kill(); err != nil {
		h.t.Fatal(err)
	}
	_ = h.process.Wait()
	h.waited = true
}
func (h *dockerHarness) generation(id string) resourceGeneration {
	h.t.Helper()
	state, err := openStore(h.dir)
	if err != nil {
		h.t.Fatal(err)
	}
	defer func() { _ = state.db.Close() }()
	g, err := state.resources(id)
	if err != nil {
		h.t.Fatal(err)
	}
	return g
}
func (h *dockerHarness) updateGeneration(id string, g resourceGeneration) {
	h.t.Helper()
	state, err := openStore(h.dir)
	if err != nil {
		h.t.Fatal(err)
	}
	defer func() { _ = state.db.Close() }()
	if err = state.saveResources(id, g); err != nil {
		h.t.Fatal(err)
	}
}
func (h *dockerHarness) restart(id string, reset bool) v1.InstanceResponse {
	h.t.Helper()
	request := h.request("dev")
	request.InstanceID = id
	var response v1.InstanceResponse
	var err error
	if reset {
		response, err = h.cli.Reset(context.Background(), request)
	} else {
		response, err = h.cli.Restart(context.Background(), request)
	}
	if err != nil {
		h.t.Fatal(err)
	}
	return response
}

// fixturePrivateState observes only this fixture's verified container. Credentials
// are compared as fingerprints and never printed in failure diagnostics.
type fixturePrivateState struct {
	Volume, Directory string
	Secret            [32]byte
	Networks          map[string]bool
}

func (h *dockerHarness) privateState(r ownedResource) fixturePrivateState {
	h.t.Helper()
	if err := verifyDockerResource(context.Background(), h.engine, r); err != nil {
		h.t.Fatal(err)
	}
	out, err := h.engine.ContainerInspect(context.Background(), r.ID, client.ContainerInspectOptions{})
	if err != nil {
		h.t.Fatal(err)
	}
	state := fixturePrivateState{Networks: map[string]bool{}}
	for _, m := range out.Container.Mounts {
		if m.Destination == "/data" {
			state.Volume = m.Name
		}
		if m.Destination == "/private" {
			state.Directory = m.Source
		}
	}
	for _, env := range out.Container.Config.Env {
		if strings.HasPrefix(env, "SECRET=") {
			state.Secret = sha256.Sum256([]byte(env))
		}
	}
	for _, n := range out.Container.NetworkSettings.Networks {
		state.Networks[n.NetworkID] = true
	}
	if state.Volume == "" || state.Directory == "" || state.Secret == ([32]byte{}) || len(state.Networks) != 2 {
		h.t.Fatal("private fixture allocation missing")
	}
	return state
}
func assertPrivateIsolation(t *testing.T, a, b fixturePrivateState) {
	t.Helper()
	if a.Volume == b.Volume || a.Directory == b.Directory || a.Secret == b.Secret {
		t.Fatal("private storage/credential generation shared")
	}
	for id := range a.Networks {
		if b.Networks[id] {
			t.Fatal("private network shared")
		}
	}
}
func (h *dockerHarness) witness(r ownedResource, value string, write bool) {
	h.t.Helper()
	command := "test \"$(cat /data/witness)\" = \"$1\" && test \"$(cat /private/witness)\" = \"$1\" && test \"$(cat /private/secret)\" = \"$SECRET\""
	if write {
		command = "printf '%s' \"$1\" > /data/witness; printf '%s' \"$1\" > /private/witness"
	}
	if _, code := h.exec(r, []string{"sh", "-c", command, "fixture", value}); code != 0 {
		h.t.Fatal("private witness/credential mismatch", code)
	}
}
func (h *dockerHarness) waitTerminalRunning(id string) {
	h.t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		state, err := h.cli.Inspect(context.Background(), id)
		if err != nil {
			h.t.Fatal(err)
		}
		if state.Instance.Execution != nil {
			for _, c := range state.Instance.Execution.Components {
				if c.Name == "each" && c.Status == "running" {
					return
				}
			}
		}
		if state.Instance.Status == v1.Failed {
			h.t.Fatal("disposable fixture failed before overlap", state.Instance.Execution)
		}
		time.Sleep(25 * time.Millisecond)
	}
	h.t.Fatal("disposable terminal did not start")
}
func TestDockerRuntime(t *testing.T) {
	endpoint := os.Getenv("BACKLOT_DOCKER_TEST_ENDPOINT")
	if endpoint == "" {
		t.Skip("requires explicit BACKLOT_DOCKER_TEST_ENDPOINT and pre-pulled mariadb:11.4/alpine:3.21; no provider resources used by default")
	}
	binary := filepath.Join(t.TempDir(), "backlot")
	build := exec.Command("go", "build", "-race", "-o", binary, "../../cmd/backlot")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v %s", err, out)
	}
	t.Run("native-directory-configured-consumer-cleanup", func(t *testing.T) {
		h := newDockerHarness(t, binary, endpoint)
		tool, err := exec.LookPath("true")
		if err != nil {
			t.Fatal(err)
		}
		h.manifest.Tools = map[string]string{"true": tool}
		h.manifest.Components = map[string]v1.Component{"job": {Kind: v1.Job, Runtime: v1.Native, Policy: v1.EachStart, Command: &v1.Command{Tool: "true"}, Resources: []string{"directory"}}}
		h.manifest.Scenes = map[string]v1.Scene{"dev": {Lifetime: v1.Persistent, Components: []string{"job"}, Resources: []string{"directory"}}}
		h.write()
		id := h.run("dev").Instance.ID
		h.state(id, v1.RuntimeReady)
		paths, err := filepath.Glob(filepath.Join(h.dir, "data", "*"))
		if err != nil || len(paths) != 1 {
			t.Fatal("native private directory missing", err)
		}
		witness := filepath.Join(paths[0], "witness")
		if err = os.WriteFile(witness, []byte("keep-native-data"), 0600); err != nil {
			t.Fatal(err)
		}
		foreign := ownedResource{Kind: "container", Token: newID()}
		foreign.Name = "backlot-fixture-" + foreign.Token
		t.Cleanup(func() {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if err := removeContainer(ctx, h.engine, foreign); err != nil {
				h.cleanupUnverified = true
				t.Error("foreign fixture cleanup", err)
			}
		})
		out, err := h.engine.ContainerCreate(context.Background(), client.ContainerCreateOptions{Name: foreign.Name, Config: &container.Config{Image: "alpine:3.21", Cmd: []string{"sleep", "300"}, Labels: resourceLabels(foreign)}, HostConfig: &container.HostConfig{Mounts: []mount.Mount{{Type: mount.TypeBind, Source: paths[0], Target: "/used"}}}})
		if err != nil {
			t.Fatal(err)
		}
		foreign.ID = out.ID
		if _, err = h.engine.ContainerStart(context.Background(), foreign.ID, client.ContainerStartOptions{}); err != nil {
			t.Fatal(err)
		}
		req := h.request("dev")
		req.InstanceID = id
		for _, action := range []string{"reset", "destroy"} {
			if action == "reset" {
				_, err = h.cli.Reset(context.Background(), req)
			} else {
				_, err = h.cli.Destroy(context.Background(), id)
			}
			if err == nil {
				t.Fatal(action, "deleted native directory with live bind consumer")
			}
			data, readErr := os.ReadFile(witness)
			if readErr != nil || string(data) != "keep-native-data" || !h.engineAlive(foreign) {
				t.Fatal(action, "changed consumed native data/foreign container", readErr)
			}
		}
		if err = removeContainer(context.Background(), h.engine, foreign); err != nil {
			t.Fatal(err)
		}
		h.restart(id, true)
		h.state(id, v1.RuntimeReady)
		if _, err = os.Stat(paths[0]); !os.IsNotExist(err) {
			t.Fatal("reset did not remove old native generation", err)
		}
		if _, err = h.cli.Destroy(context.Background(), id); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("native-launch-does-not-contact-configured-docker", func(t *testing.T) {
		h := newDockerHarness(t, binary, endpoint)
		tool, err := exec.LookPath("true")
		if err != nil {
			t.Fatal(err)
		}
		h.manifest.Tools = map[string]string{"true": tool}
		h.manifest.Components = map[string]v1.Component{"job": {Kind: v1.Job, Runtime: v1.Native, Policy: v1.EachStart, Command: &v1.Command{Tool: "true"}, Resources: []string{"directory"}}}
		h.manifest.Scenes = map[string]v1.Scene{"dev": {Lifetime: v1.Persistent, Components: []string{"job"}, Resources: []string{"directory"}}}
		h.write()
		socket := filepath.Join(h.dir, "optional-docker.sock")
		parsed, err := url.Parse(endpoint)
		if err != nil {
			t.Fatal(err)
		}
		bridge := func() {
			t.Helper()
			if _, err := os.Lstat(socket); os.IsNotExist(err) {
				if err = os.Symlink(parsed.Path, socket); err != nil {
					h.cleanupUnverified = true
					t.Fatal(err)
				}
			}
		}
		// Establish the owned endpoint bridge for teardown even if an assertion
		// fails while the endpoint is intentionally unavailable.
		t.Cleanup(bridge)
		config, _ := json.Marshal(v1.MachineConfig{Version: v1.ManifestVersion, Docker: &v1.DockerConfig{Endpoint: "unix://" + socket}})
		if err = os.WriteFile(h.config, config, 0600); err != nil {
			t.Fatal(err)
		}
		prepared, err := h.cli.Prepare(context.Background(), v1.PrepareRequest{APIVersion: v1.Version, Plan: h.request("dev").Plan})
		if err != nil {
			t.Fatal("metadata required Docker access", err)
		}
		paths, err := filepath.Glob(filepath.Join(h.dir, "data", "*"))
		if err != nil || len(paths) != 0 {
			t.Fatal("metadata allocated private directory", err)
		}
		id := h.run("dev").Instance.ID
		if id != prepared.Instance.ID {
			t.Fatal("native metadata identity not reused")
		}
		h.state(id, v1.RuntimeReady)
		paths, err = filepath.Glob(filepath.Join(h.dir, "data", "*"))
		if err != nil || len(paths) != 1 {
			t.Fatal("native launch failed without Docker", err)
		}
		if _, err = h.cli.Destroy(context.Background(), id); err == nil {
			t.Fatal("cleanup without configured consumer proof accepted")
		}
		if _, err = os.Stat(paths[0]); err != nil {
			t.Fatal("unverifiable directory cleanup removed data", err)
		}
		bridge()
		if _, err = h.cli.Destroy(context.Background(), id); err != nil {
			t.Fatal("cleanup after configured engine became available", err)
		}
	})
	t.Run("implicit-network-endpoint-drift-preserves-live-service", func(t *testing.T) {
		h := newDockerHarness(t, binary, endpoint)
		env := v1.Environment{Assign: map[string]v1.Value{"BACKLOT_FIXTURE": fixtureLiteral(h.token), "INSTANCE": fixtureRef("instance", "", "id")}}
		h.manifest.Components = map[string]v1.Component{"server": {Kind: v1.Service, Runtime: v1.Container, Image: ptrValue(fixtureLiteral("alpine:3.21")), Args: []string{"sh", "-c", "while true; do nc -l -p 8080 < /dev/null; done"}, Resources: []string{"port"}, Ports: map[string]v1.ServicePort{"http": {Resource: "port", ContainerPort: 8080}}, Environment: env, Readiness: &v1.Probe{Kind: "tcp", Target: ptrValue(fixtureRef("resource", "port", "port")), Timeout: "15s"}}}
		h.manifest.Scenes = map[string]v1.Scene{"dev": {Lifetime: v1.Persistent, Components: []string{"server"}, Resources: []string{"port"}}}
		h.write()
		id := h.run("dev").Instance.ID
		ready := h.state(id, v1.RuntimeReady)
		original := h.container(id, "server")
		config, _ := json.Marshal(v1.MachineConfig{Version: v1.ManifestVersion, Docker: &v1.DockerConfig{Endpoint: "unix://" + filepath.Join(h.dir, "other-engine.sock")}})
		if err := os.WriteFile(h.config, config, 0600); err != nil {
			t.Fatal(err)
		}
		req := h.request("dev")
		req.InstanceID = id
		if _, err := h.cli.Restart(context.Background(), req); err == nil {
			t.Fatal("implicit retained network allowed endpoint drift")
		}
		current := h.state(id, v1.RuntimeReady)
		if !h.engineAlive(original) || h.container(id, "server").ID != original.ID || current.Instance.Execution.Attempt != ready.Instance.Execution.Attempt || current.Instance.Plan.ConfigDigest != ready.Instance.Plan.ConfigDigest {
			t.Fatal("endpoint rejection replaced old work/snapshot")
		}
		if repeated := h.run("dev"); !repeated.ConfigDrift {
			t.Fatal("repeated run omitted rejected endpoint drift")
		}
	})
	t.Run("failed-or-undecodable-cli-acceptance-reconciles-journal", func(t *testing.T) {
		for _, mode := range []string{"missing-image", "undecodable-output"} {
			t.Run(mode, func(t *testing.T) {
				engine, err := newDocker(endpoint)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = engine.Close() })
				var generation resourceGeneration
				var stateDir string
				// Independent parent capability cleanup is retained before launching
				// the failure-path fixture, without relying on its observed CLI ID.
				t.Cleanup(func() {
					ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
					defer cancel()
					for _, r := range generation.Containers {
						if err := removeContainer(ctx, engine, r); err != nil {
							t.Error(err)
						}
					}
					for _, r := range generation.Resources {
						if r.Kind == "volume" || r.Kind == "network" {
							if err := removeDockerResource(ctx, engine, r); err != nil {
								t.Error(err)
							}
						}
					}
				})
				t.Run("acceptance", func(t *testing.T) {
					h := newDockerHarness(t, binary, endpoint)
					stateDir = h.dir
					image := "alpine:3.21"
					if mode == "missing-image" {
						image = "backlot-fixture-absent:" + h.token
					}
					h.manifest.Components = map[string]v1.Component{"job": {Kind: v1.Job, Runtime: v1.Container, Policy: v1.EachStart, Image: ptrValue(fixtureLiteral(image)), Args: []string{"true"}, Resources: []string{"data"}}}
					h.manifest.Scenes = map[string]v1.Scene{"dev": {Lifetime: v1.Persistent, Components: []string{"job"}, Resources: []string{"data"}}}
					h.write()
					command := exec.Command(binary, "run", "dev", "--project", h.project, "--config", h.config, "--state-dir", h.dir, "--json")
					output, runErr := command.Output()
					if mode == "missing-image" && runErr == nil {
						t.Fatal("missing image did not fail")
					}
					if mode == "undecodable-output" {
						if runErr != nil {
							t.Fatal("successful acceptance fixture failed", runErr)
						}
						// Inject response corruption after real durable CLI acceptance.
						output = append([]byte("!"), output...)
						var response v1.InstanceResponse
						if err = json.Unmarshal(output, &response); err == nil {
							t.Fatal("output corruption not observed")
						}
					}
					if len(h.ids) != 0 {
						t.Fatal("failure fixture unexpectedly recorded a cleanup ID")
					}
					if _, err = h.cli.Stop(context.Background()); err != nil {
						t.Fatal(err)
					}
					if err = h.process.Wait(); err != nil {
						t.Fatal(err)
					}
					h.waited = true
					state, err := openStore(h.dir)
					if err != nil {
						t.Fatal(err)
					}
					ids, listErr := fixtureInstanceIDs(state)
					if listErr != nil || len(ids) != 1 {
						_ = state.db.Close()
						t.Fatal("accepted journal identity missing", listErr)
					}
					generation, err = state.resources(ids[0])
					_ = state.db.Close()
					if err != nil {
						t.Fatal(err)
					}
					if generation.Resources["data"].ID == "" || generation.Resources["@network"].ID == "" {
						t.Fatal("failure acceptance did not allocate representative resources")
					}
				})
				if _, err = os.Stat(stateDir); !os.IsNotExist(err) {
					t.Fatal("expected verified fixture reconciliation, journal remains", err)
				}
				for _, name := range []string{"data", "@network"} {
					if err = verifyDockerResource(context.Background(), engine, generation.Resources[name]); !errdefs.IsNotFound(err) {
						t.Fatal("allocation survived harness cleanup", name, err)
					}
				}
			})
		}
	})
	t.Run("mariadb-cli-persistence-reset-and-drift", func(t *testing.T) {
		h := newDockerHarness(t, binary, endpoint)
		env := h.componentEnv()
		env.Assign["MARIADB_ROOT_PASSWORD"] = fixtureRef("resource", "secret", "value")
		db := v1.Component{Kind: v1.Service, Runtime: v1.Container, Image: ptrValue(fixtureLiteral("mariadb:11.4")), Resources: []string{"data", "secret", "port"}, Environment: env, Mounts: []v1.Mount{{Resource: "data", Target: "/var/lib/mysql"}}, Ports: map[string]v1.ServicePort{"sql": {Resource: "port", ContainerPort: 3306}}, Readiness: &v1.Probe{Kind: "tcp", Target: ptrValue(fixtureRef("resource", "port", "port")), Timeout: "60s"}}
		h.manifest.Components = map[string]v1.Component{"db": db}
		h.manifest.Scenes = map[string]v1.Scene{"dev": {Lifetime: v1.Persistent, Components: []string{"db"}, Resources: []string{"data", "secret", "port"}}}
		h.write()
		command := exec.Command(binary, "run", "dev", "--project", h.project, "--config", h.config, "--state-dir", h.dir, "--json")
		out, err := command.Output()
		if err != nil {
			t.Fatal("real CLI run", err, string(out))
		}
		var response v1.InstanceResponse
		if err = json.Unmarshal(out, &response); err != nil {
			t.Fatal(err)
		}
		id := response.Instance.ID
		h.ids = append(h.ids, id)
		r := h.container(id, "db")
		h.sql(r, "CREATE DATABASE fixture; CREATE TABLE fixture.mutations (value INT); INSERT INTO fixture.mutations VALUES (42)")
		fingerprint := func(r ownedResource) [32]byte {
			inspect, err := h.engine.ContainerInspect(context.Background(), r.ID, client.ContainerInspectOptions{})
			if err != nil {
				t.Fatal(err)
			}
			for _, env := range inspect.Container.Config.Env {
				if strings.HasPrefix(env, "MARIADB_ROOT_PASSWORD=") {
					return sha256.Sum256([]byte(env))
				}
			}
			t.Fatal("credential absent")
			return [32]byte{}
		}
		secret := fingerprint(r)
		if _, err = h.cli.StopExecution(context.Background(), id); err != nil {
			t.Fatal(err)
		}
		h.run("dev")
		h.state(id, v1.RuntimeReady)
		r = h.container(id, "db")
		if h.sql(r, "SELECT value FROM fixture.mutations") != "42" || fingerprint(r) != secret {
			t.Fatal("stop/run lost data or rotated credentials")
		}
		db.Environment.Assign["ORDINARY"] = fixtureLiteral("updated")
		h.manifest.Components["db"] = db
		h.write()
		repeated := h.run("dev")
		if !repeated.ManifestDrift || repeated.Instance.Execution.Attempt != h.state(id, v1.RuntimeReady).Instance.Execution.Attempt {
			t.Fatal("repeated run failed to report drift/join")
		}
		h.restart(id, false)
		h.state(id, v1.RuntimeReady)
		r = h.container(id, "db")
		if h.sql(r, "SELECT value FROM fixture.mutations") != "42" {
			t.Fatal("restart lost mutation")
		}
		original := r.ID
		db.Environment.Assign["MARIADB_ROOT_PASSWORD"] = v1.Value{Literal: ptrString("changed-credential"), Secret: true}
		h.manifest.Components["db"] = db
		h.write()
		request := h.request("dev")
		request.InstanceID = id
		if _, err = h.cli.Restart(context.Background(), request); err == nil {
			t.Fatal("credential drift accepted")
		}
		if !h.engineAlive(r) || h.container(id, "db").ID != original {
			t.Fatal("unsafe drift stopped existing runtime")
		}
		db.Environment.Assign["MARIADB_ROOT_PASSWORD"] = fixtureRef("resource", "secret", "value")
		h.manifest.Components["db"] = db
		h.write()
		h.restart(id, true)
		h.state(id, v1.RuntimeReady)
		r = h.container(id, "db")
		if h.sql(r, "SELECT COUNT(*) FROM information_schema.schemata WHERE schema_name='fixture'") != "0" || fingerprint(r) == secret {
			t.Fatal("reset did not replace data and credential generation")
		}
		if _, err = h.cli.Destroy(context.Background(), id); err != nil {
			t.Fatal(err)
		}
		logs, err := h.cli.Logs(context.Background(), v1.LogsRequest{APIVersion: v1.Version, InstanceID: id})
		if err != nil || len(logs.Records) == 0 {
			t.Fatal("historical container logs missing", err)
		}
	})
	t.Run("fresh-only-failed-init-retry-and-each-start", func(t *testing.T) {
		h := newDockerHarness(t, binary, endpoint)
		h.alpine()
		init := h.manifest.Components["init"]
		init.Args = []string{"sh", "-c", "if ! test -f /private/attempt; then touch /private/attempt; echo failed-initialization; exit 19; fi; echo initialized >> /data/init; printf '%s' \"$SECRET\" > /private/secret"}
		h.manifest.Components["init"] = init
		h.write()
		id := h.run("dev").Instance.ID
		failed := h.state(id, v1.Failed)
		if len(failed.Instance.Execution.Components) == 0 {
			t.Fatalf("init never launched: %+v", failed.Instance.Execution)
		}
		if failed.Instance.Execution.Components[0].ExitCode == nil || *failed.Instance.Execution.Components[0].ExitCode != 19 {
			t.Fatal("original init failure status lost")
		}
		time.Sleep(150 * time.Millisecond)
		if state := h.state(id, v1.Failed); state.Instance.Execution.Attempt != failed.Instance.Execution.Attempt {
			t.Fatal("automatic retry")
		}
		h.restart(id, false)
		h.state(id, v1.RuntimeReady)
		r := h.container(id, "server")
		text, code := h.exec(r, []string{"sh", "-c", "wc -l < /data/init; wc -l < /data/starts"})
		if code != 0 || strings.TrimSpace(text) != "1\n1" {
			t.Fatal("initialization/each-start records", text, code)
		}
		h.restart(id, false)
		ready := h.state(id, v1.RuntimeReady)
		if ready.Instance.Execution.Components[0].Status != "skipped" {
			t.Fatal("fresh-only not skipped")
		}
		r = h.container(id, "server")
		text, code = h.exec(r, []string{"sh", "-c", "wc -l < /data/init; wc -l < /data/starts"})
		if code != 0 || strings.TrimSpace(text) != "1\n2" {
			t.Fatal("restart job policy", text, code)
		}
		init.Args = append(init.Args, "changed")
		h.manifest.Components["init"] = init
		h.write()
		req := h.request("dev")
		req.InstanceID = id
		if _, err := h.cli.Restart(context.Background(), req); err == nil {
			t.Fatal("init contract drift accepted")
		}
		if !h.engineAlive(r) {
			t.Fatal("unsafe init drift stopped service")
		}
	})
	t.Run("simultaneous-worktrees-disposable-output-groups", func(t *testing.T) {
		h := newDockerHarness(t, binary, endpoint)
		h.alpine()
		h.manifest.Resources["network"] = v1.Resource{Kind: "network"}
		server := h.manifest.Components["server"]
		server.Resources = append(server.Resources, "network")
		h.manifest.Components["server"] = server
		for name, scene := range h.manifest.Scenes {
			scene.Resources = append(scene.Resources, "network")
			h.manifest.Scenes[name] = scene
		}
		h.write()
		git := func(args ...string) {
			t.Helper()
			cmd := exec.Command("git", args...)
			cmd.Dir = h.project
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("git %v: %v %s", args, err, out)
			}
		}
		git("init", "-q")
		git("add", "backlot.json")
		git("-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "-qm", "fixture")
		other := filepath.Join(filepath.Dir(h.project), "other")
		git("worktree", "add", "-qb", "fixture-other", other)
		t.Cleanup(func() { git("worktree", "remove", "--force", other) })
		first := h.run("dev")
		req := h.request("dev")
		req.Plan.ProjectPath = other
		second, err := h.cli.Run(context.Background(), req)
		if err != nil {
			t.Fatal(err)
		}
		h.ids = append(h.ids, second.Instance.ID)
		a, b := h.state(first.Instance.ID, v1.RuntimeReady), h.state(second.Instance.ID, v1.RuntimeReady)
		if a.Instance.ID == b.Instance.ID || a.Instance.Checkout.ID == b.Instance.Checkout.ID || a.Instance.Execution.Ports["port"] == b.Instance.Execution.Ports["port"] {
			t.Fatal("worktree isolation failed")
		}
		ra, rb := h.container(a.Instance.ID, "server"), h.container(b.Instance.ID, "server")
		pa, pb := h.privateState(ra), h.privateState(rb)
		assertPrivateIsolation(t, pa, pb)
		h.witness(ra, "worktree-a", true)
		h.witness(rb, "worktree-b", true)
		h.witness(ra, "worktree-a", false)
		h.witness(rb, "worktree-b", false)
		h.restart(a.Instance.ID, true)
		h.state(a.Instance.ID, v1.RuntimeReady)
		fresh := h.privateState(h.container(a.Instance.ID, "server"))
		assertPrivateIsolation(t, pa, fresh)
		if !reflect.DeepEqual(pb, h.privateState(rb)) {
			t.Fatal("peer generation changed after reset")
		}
		h.witness(rb, "worktree-b", false)
		if _, err := h.cli.Destroy(context.Background(), a.Instance.ID); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(pb, h.privateState(rb)) {
			t.Fatal("peer generation changed after destroy")
		}
		h.witness(rb, "worktree-b", false)
		// Two disposable runs stay overlapping until the test releases each private
		// terminal. Their service mounts expose their independent retained state.
		each := h.manifest.Components["each"]
		each.Args = []string{"sh", "-c", "while [ ! -f /private/release ]; do sleep 0.1; done"}
		h.manifest.Components["each"] = each
		h.write()
		da, db := h.run("test"), h.run("test")
		h.waitTerminalRunning(da.Instance.ID)
		h.waitTerminalRunning(db.Instance.ID)
		dra, drb := h.container(da.Instance.ID, "server"), h.container(db.Instance.ID, "server")
		dpa, dpb := h.privateState(dra), h.privateState(drb)
		assertPrivateIsolation(t, dpa, dpb)
		assertPrivateIsolation(t, pb, dpa)
		assertPrivateIsolation(t, pb, dpb)
		h.witness(dra, "disposable-a", true)
		h.witness(drb, "disposable-b", true)
		h.witness(dra, "disposable-a", false)
		h.witness(drb, "disposable-b", false)
		if _, code := h.exec(dra, []string{"touch", "/private/release"}); code != 0 {
			t.Fatal("release first disposable", code)
		}
		h.state(da.Instance.ID, v1.Succeeded)
		if _, err := h.cli.Destroy(context.Background(), da.Instance.ID); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(dpb, h.privateState(drb)) {
			t.Fatal("disposable peer generation changed")
		}
		h.witness(drb, "disposable-b", false)
		if _, code := h.exec(drb, []string{"touch", "/private/release"}); code != 0 {
			t.Fatal("release second disposable", code)
		}
		h.state(db.Instance.ID, v1.Succeeded)
		// A separate scene writes an explicitly shared output through two concurrent
		// disposable runs. The atomic directory detects overlap inside the real job.
		h.manifest.Outputs = map[string]v1.Output{"build": {Path: "build", ConcurrencyGroup: "build"}}
		job := v1.Component{Kind: v1.Job, Runtime: v1.Container, Image: ptrValue(fixtureLiteral("alpine:3.21")), Policy: v1.EachStart, Args: []string{"sh", "-c", "mkdir /output/lock || exit 41; echo start; sleep 0.4; rmdir /output/lock; echo done"}, Outputs: []string{"build"}, Mounts: []v1.Mount{{Output: "build", Target: "/output"}}}
		h.manifest.Components["output"] = job
		h.manifest.Scenes["output"] = v1.Scene{Lifetime: v1.Disposable, Components: []string{"output"}, TerminalJob: "output"}
		h.write()
		var responses [2]v1.InstanceResponse
		var failures [2]error
		var joined sync.WaitGroup
		for i := range responses {
			joined.Go(func() { responses[i], failures[i] = h.cli.Run(context.Background(), h.request("output")) })
		}
		joined.Wait()
		for i, response := range responses {
			if failures[i] != nil {
				t.Fatal(failures[i])
			}
			h.ids = append(h.ids, response.Instance.ID)
			h.tokens[response.Instance.ID] = response.LeaseToken
			h.state(response.Instance.ID, v1.Succeeded)
		}
		if responses[0].Instance.ID == responses[1].Instance.ID {
			t.Fatal("disposable identities shared")
		}
	})
	t.Run("bounded-log-records-retained-after-destroy", func(t *testing.T) {
		h := newDockerHarness(t, binary, endpoint)
		job := v1.Component{Kind: v1.Job, Runtime: v1.Container, Policy: v1.EachStart, Image: ptrValue(fixtureLiteral("alpine:3.21")), Args: []string{"sh", "-c", "head -c 16384 /dev/zero; printf '\\377\\376'; printf 'ordinary-output\\n'"}}
		job.Environment = v1.Environment{Assign: map[string]v1.Value{"BACKLOT_FIXTURE": fixtureLiteral(h.token), "INSTANCE": fixtureRef("instance", "", "id")}}
		h.manifest.Components = map[string]v1.Component{"logs": job}
		h.manifest.Scenes = map[string]v1.Scene{"logs": {Lifetime: v1.Persistent, Components: []string{"logs"}}}
		h.write()
		id := h.run("logs").Instance.ID
		h.state(id, v1.RuntimeReady)
		r := h.container(id, "logs")
		reader, err := h.engine.ContainerLogs(context.Background(), r.ID, client.ContainerLogsOptions{ShowStdout: true})
		if err != nil {
			t.Fatal(err)
		}
		var supplied bytes.Buffer
		if _, err := stdcopy.StdCopy(&supplied, io.Discard, reader); err != nil {
			t.Fatal(err)
		}
		if err := reader.Close(); err != nil {
			t.Fatal(err)
		}
		if _, err := h.cli.Destroy(context.Background(), id); err != nil {
			t.Fatal(err)
		}
		command := exec.Command(binary, "logs", "--state-dir", h.dir, "--json", id)
		output, err := command.Output()
		if err != nil {
			t.Fatal("historical logs CLI failed", err)
		}
		decoder := json.NewDecoder(bytes.NewReader(output))
		var raw bytes.Buffer
		for {
			var record v1.LogRecord
			if err := decoder.Decode(&record); err == io.EOF {
				break
			} else if err != nil {
				t.Fatal("typed log NDJSON", err)
			}
			if record.InstanceID != id || record.Component != "logs" || record.Stream != "stdout" {
				t.Fatal("log identity mismatch")
			}
			if record.Data != "" {
				data, err := base64.StdEncoding.DecodeString(record.Data)
				if err != nil {
					t.Fatal(err)
				}
				raw.Write(data)
			} else {
				raw.WriteString(record.Message)
			}
		}
		// Some engine log drivers replace invalid UTF-8 before the API returns
		// it. Backlot must preserve exactly the bytes supplied by that boundary.
		if !bytes.Equal(raw.Bytes(), supplied.Bytes()) {
			t.Fatal("retained provider output bytes differ", raw.Len(), supplied.Len())
		}
		if !bytes.HasPrefix(raw.Bytes(), make([]byte, 16384)) || !bytes.HasSuffix(raw.Bytes(), []byte("ordinary-output\n")) {
			t.Fatal("control/ordinary output incomplete")
		}
	})
	t.Run("declared-native-docker-build-before-container", func(t *testing.T) {
		h := newDockerHarness(t, binary, endpoint)
		docker, err := exec.LookPath("docker")
		if err != nil {
			t.Fatal("declared external Docker tool unavailable", err)
		}
		image := "backlot-fixture-build:" + h.token
		// The build has a unique image capability and never pulls or removes its base.
		t.Cleanup(func() {
			for _, id := range h.ids {
				if _, err := h.cli.Destroy(context.Background(), id); err != nil {
					h.cleanupUnverified = true
					t.Error(err)
					return
				}
			}
			inspect, err := h.engine.ImageInspect(context.Background(), image)
			if errdefs.IsNotFound(err) {
				return
			}
			if err != nil {
				t.Error(err)
				return
			}
			if inspect.Config == nil || inspect.Config.Labels["io.backlot.fixture"] != h.token {
				t.Error("build image ownership uncertain; preserved")
				return
			}
			if _, err = h.engine.ImageRemove(context.Background(), image, client.ImageRemoveOptions{PruneChildren: false}); err != nil {
				t.Error(err)
			}
		})
		if err = os.WriteFile(filepath.Join(h.project, "Dockerfile"), []byte("FROM alpine:3.21\nCOPY witness /witness\n"), 0600); err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(filepath.Join(h.project, "witness"), []byte(h.token), 0600); err != nil {
			t.Fatal(err)
		}
		h.manifest.Tools = map[string]string{"docker": docker}
		build := v1.Component{Kind: v1.Job, Runtime: v1.Native, Policy: v1.EachStart, Command: &v1.Command{Tool: "docker", Args: []string{"--host", endpoint, "build", "--pull=false", "--network=none", "--label", "io.backlot.fixture=" + h.token, "--tag", image, "."}}}
		job := v1.Component{Kind: v1.Job, Runtime: v1.Container, Policy: v1.EachStart, Image: ptrValue(fixtureLiteral(image)), Args: []string{"sh", "-c", "test \"$(cat /witness)\" = \"$EXPECTED\" && echo built-image-consumed"}, Environment: v1.Environment{Assign: map[string]v1.Value{"EXPECTED": fixtureLiteral(h.token)}}, DependsOn: []v1.Dependency{{Component: "build", Condition: v1.Completed}}}
		h.manifest.Components = map[string]v1.Component{"build": build, "consume": job}
		h.manifest.Scenes = map[string]v1.Scene{"build": {Lifetime: v1.Disposable, Components: []string{"build", "consume"}, TerminalJob: "consume"}}
		h.write()
		id := h.run("build").Instance.ID
		state := h.state(id, v1.Succeeded)
		if len(state.Instance.Execution.Components) != 2 || state.Instance.Execution.Components[0].Name != "build" || state.Instance.Execution.Components[0].Status != "completed" || state.Instance.Execution.Components[1].ExitCode == nil || *state.Instance.Execution.Components[1].ExitCode != 0 {
			t.Fatal("build dependency did not complete before image consumption")
		}
	})
	t.Run("partial-allocation-intent-effect-and-idempotent-cleanup", func(t *testing.T) {
		root := shortTemp(t)
		config := filepath.Join(root, "machine.json")
		data, _ := json.Marshal(v1.MachineConfig{Version: v1.ManifestVersion, Docker: &v1.DockerConfig{Endpoint: endpoint}})
		if err := os.WriteFile(config, data, 0600); err != nil {
			t.Fatal(err)
		}
		m := v1.Manifest{Version: v1.ManifestVersion, Project: "partial", Resources: map[string]v1.Resource{"data": {Kind: "volume"}}, Components: map[string]v1.Component{"job": {Kind: v1.Job, Runtime: v1.Container, Policy: v1.EachStart, Image: ptrValue(fixtureLiteral("alpine:3.21"))}}, Scenes: map[string]v1.Scene{"dev": {Lifetime: v1.Persistent, Components: []string{"job"}, Resources: []string{"data"}}}}
		data, _ = json.Marshal(m)
		if err := os.WriteFile(filepath.Join(root, "backlot.json"), data, 0600); err != nil {
			t.Fatal(err)
		}
		directory := shortTemp(t)
		state, err := openStore(directory)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = state.db.Close() })
		svc := &service{store: state, directory: directory, lease: time.Minute}
		prepared, err := svc.prepare(context.Background(), v1.PrepareRequest{APIVersion: v1.Version, Plan: v1.PlanRequest{ProjectPath: root, ConfigPath: config, Scene: "dev"}})
		if err != nil {
			t.Fatal(err)
		}
		id := prepared.Instance.ID
		t.Cleanup(func() {
			if err := state.removeResources(context.Background(), id); err != nil {
				t.Error("partial cleanup", err)
			}
		})
		snap, err := state.snapshot(id)
		if err != nil {
			t.Fatal(err)
		}
		svc.checkpoint = func(stage string) error {
			if stage == "resource-effect:@network" {
				return errors.New("fixture interruption after network effect")
			}
			return nil
		}
		entry := &execution{}
		if err = svc.allocateResources(context.Background(), id, snap, entry); err == nil {
			t.Fatal("allocation interruption ignored")
		}
		if entry.docker != nil {
			_ = entry.docker.Close()
		}
		g, err := state.resources(id)
		if err != nil {
			t.Fatal(err)
		}
		if len(g.Resources) != 1 || g.Resources["@network"].ID == "" {
			t.Fatal("network intent/effect missing")
		}
		d, err := newDocker(endpoint)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = d.Close() }()
		// Simulate removal succeeding immediately before its durable effect. A second
		// cleanup must observe absence and finish, without touching unrelated objects.
		if err = removeDockerResource(context.Background(), d, g.Resources["@network"]); err != nil {
			t.Fatal(err)
		}
		if err = state.removeResources(context.Background(), id); err != nil {
			t.Fatal(err)
		}
		if err = state.removeResources(context.Background(), id); err != nil {
			t.Fatal("idempotent cleanup", err)
		}
		svc.checkpoint = func(stage string) error {
			if stage == "resource-intent:data" {
				return errors.New("fixture create/effect recording interruption")
			}
			return nil
		}
		entry = &execution{}
		if err = svc.allocateResources(context.Background(), id, snap, entry); err == nil {
			t.Fatal("intent interruption ignored")
		}
		if entry.docker != nil {
			_ = entry.docker.Close()
		}
		g, err = state.resources(id)
		if err != nil {
			t.Fatal(err)
		}
		pending := g.Resources["data"]
		if pending.ID != "" {
			t.Fatal("effect recorded before external create")
		}
		effect, err := createDockerResource(context.Background(), d, pending)
		if err != nil {
			t.Fatal(err)
		}
		mismatch := pending
		mismatch.Token = newID()
		if _, err = recoverResourceEffect(context.Background(), d, mismatch); err == nil {
			t.Fatal("mismatched volume ownership adopted")
		}
		svc.checkpoint = nil
		entry = &execution{}
		if err = svc.allocateResources(context.Background(), id, snap, entry); err != nil {
			t.Fatal("proven create effect not recovered", err)
		}
		if entry.docker != nil {
			_ = entry.docker.Close()
		}
		recovered, err := state.resources(id)
		if err != nil {
			t.Fatal(err)
		}
		if recovered.ID != g.ID || recovered.Resources["data"].ID != effect {
			t.Fatal("recovery created another generation or resource")
		}
	})
	t.Run("daemon-crash-recovery-and-uncertain-container", func(t *testing.T) {
		h := newDockerHarness(t, binary, endpoint)
		h.alpine()
		h.manifest.Outputs = map[string]v1.Output{"shared": {Path: "shared", ConcurrencyGroup: "shared"}}
		server := h.manifest.Components["server"]
		server.Outputs = []string{"shared"}
		server.Mounts = append(server.Mounts, v1.Mount{Output: "shared", Target: "/output"})
		h.manifest.Components["server"] = server
		h.manifest.Components["blocked"] = v1.Component{Kind: v1.Job, Runtime: v1.Container, Policy: v1.EachStart, Image: ptrValue(fixtureLiteral("alpine:3.21")), Args: []string{"true"}, Outputs: []string{"shared"}, Mounts: []v1.Mount{{Output: "shared", Target: "/output"}}}
		h.manifest.Scenes["blocked"] = v1.Scene{Lifetime: v1.Disposable, Components: []string{"blocked"}, TerminalJob: "blocked"}
		h.write()
		peer := h.manifest.Components["server"]
		peer.Args = []string{"sh", "-c", "echo recovery-peer; exec sleep 300"}
		peer.Ports = nil
		h.manifest.Tools = map[string]string{"true": "true"}
		peer.Readiness = &v1.Probe{Kind: "command", Command: &v1.Command{Tool: "true"}, Timeout: "3s"}
		peer.Resources = []string{"data", "directory", "secret"}
		h.manifest.Components["peer"] = peer
		scene := h.manifest.Scenes["dev"]
		scene.Components = append(scene.Components, "peer")
		h.manifest.Scenes["dev"] = scene
		h.write()
		id := h.run("dev").Instance.ID
		h.state(id, v1.RuntimeReady)
		r := h.container(id, "server")
		h.crash()
		g := h.generation(id)
		if !h.engineAlive(r) {
			t.Fatal("fixture did not survive daemon crash")
		}
		h.start()
		recovered := h.state(id, v1.Interrupted)
		if recovered.Instance.Execution.CleanupFailure != "" || recovered.Instance.Execution.CollectionFailure == "" {
			t.Fatal("recovery not truthful", recovered.Instance.Execution)
		}
		if err := verifyDockerResource(context.Background(), h.engine, r); !errdefs.IsNotFound(err) {
			t.Fatal("owned survivor not removed", err)
		}
		h.restart(id, false)
		h.state(id, v1.RuntimeReady)
		r = h.container(id, "server")
		text, code := h.exec(r, []string{"sh", "-c", "wc -l < /data/init; wc -l < /data/starts"})
		if code != 0 || strings.TrimSpace(text) != "1\n2" {
			t.Fatal("recovery lost initialization or data", text, code)
		}
		h.crash()
		current := h.generation(id)
		for i, c := range current.Containers {
			if c.ID == r.ID {
				current.Containers[i].Token = newID()
			}
		}
		var verifiedPeer ownedResource
		for i, c := range current.Containers {
			if c.Component == "peer" && !c.Removed {
				verifiedPeer = c
			}
			if c.ID == r.ID {
				current.Containers[0], current.Containers[i] = c, current.Containers[0]
			}
		}
		if verifiedPeer.ID == "" || !h.engineAlive(verifiedPeer) {
			t.Fatal("second owned survivor missing")
		}
		// Register restoration before the ownership fault so any assertion failure
		// still restores our exact retained capability and reconciles the fixture.
		t.Cleanup(func() {
			if !h.waited {
				h.crash()
			}
			restore := h.generation(id)
			for i, c := range restore.Containers {
				if c.ID == r.ID {
					restore.Containers[i].Token = r.Token
				}
			}
			h.updateGeneration(id, restore)
			h.start()
		})
		h.updateGeneration(id, current)
		h.start()
		interrupted := h.state(id, v1.Interrupted)
		if interrupted.Instance.Execution.CleanupFailure == "" || !h.engineAlive(r) {
			t.Fatal("uncertain ownership acted on")
		}
		if err := verifyDockerResource(context.Background(), h.engine, verifiedPeer); !errdefs.IsNotFound(err) {
			t.Fatal("independently owned survivor not collected/removed", err)
		}
		logs, err := h.cli.Logs(context.Background(), v1.LogsRequest{APIVersion: v1.Version, InstanceID: id})
		if err != nil || logs.Gap == "" {
			t.Fatal("uncertain recovery gap absent", err)
		}
		collectedPeer := false
		for _, record := range logs.Records {
			if record.Component == "peer" && strings.Contains(record.Message, "recovery-peer") {
				collectedPeer = true
			}
		}
		if !collectedPeer {
			t.Fatal("later owned survivor logs not retained")
		}
		blocked := h.run("blocked")
		blockedState := h.state(blocked.Instance.ID, v1.Failed)
		if blockedState.Instance.Execution == nil || !strings.Contains(blockedState.Instance.Execution.Failure, "uncertain owned consumer blocks checkout output group") || len(blockedState.Instance.Execution.Components) != 0 {
			t.Fatal("uncertain consumer released checkout output fence")
		}
		if _, err := h.cli.Destroy(context.Background(), id); err == nil {
			t.Fatal("uncertain live consumer allowed data deletion")
		}
		if err := verifyResource(context.Background(), h.engine, g.Resources["data"]); err != nil {
			t.Fatal("retained data changed", err)
		}
		h.crash()
		for i, c := range current.Containers {
			if c.ID == r.ID {
				current.Containers[i].Token = r.Token
			}
		}
		h.updateGeneration(id, current)
		h.start()
		h.state(id, v1.Interrupted)
	})
	foreignCase := func(t *testing.T, directoryOnly bool) {
		h := newDockerHarness(t, binary, endpoint)
		h.alpine()
		id := h.run("dev").Instance.ID
		h.state(id, v1.RuntimeReady)
		h.crash()
		g := h.generation(id)
		h.start()
		h.state(id, v1.Interrupted)
		foreign := ownedResource{Kind: "container", Token: newID()}
		foreign.Name = "backlot-fixture-" + foreign.Token
		mounts := []mount.Mount{{Type: mount.TypeVolume, Source: g.Resources["data"].Name, Target: "/data"}}
		if directoryOnly {
			sub := filepath.Join(g.Resources["directory"].Name, "sub")
			if err := os.Mkdir(sub, 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(sub, "witness"), []byte("preserve"), 0600); err != nil {
				t.Fatal(err)
			}
			mounts = []mount.Mount{{Type: mount.TypeBind, Source: sub, Target: "/used"}}
		}
		t.Cleanup(func() {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if err := removeContainer(ctx, h.engine, foreign); err != nil {
				h.cleanupUnverified = true
				t.Error("foreign fixture cleanup proof failed", err)
			}
		})
		out, err := h.engine.ContainerCreate(context.Background(), client.ContainerCreateOptions{Name: foreign.Name, Config: &container.Config{Image: "alpine:3.21", Cmd: []string{"sleep", "300"}, Labels: resourceLabels(foreign)}, HostConfig: &container.HostConfig{Mounts: mounts}})
		if err != nil {
			t.Fatal(err)
		}
		foreign.ID = out.ID

		if _, err = h.engine.ContainerStart(context.Background(), foreign.ID, client.ContainerStartOptions{}); err != nil {
			t.Fatal(err)
		}
		if _, err = h.cli.Destroy(context.Background(), id); err == nil {
			t.Fatal("retained resources deleted while consumer live")
		}
		if !h.engineAlive(foreign) {
			t.Fatal("foreign consumer mutated")
		}
		for _, name := range []string{"data", "directory"} {
			if err = verifyResource(context.Background(), h.engine, g.Resources[name]); err != nil {
				t.Fatal("consumed resource lost", err)
			}
		}
		if directoryOnly {
			data, err := os.ReadFile(filepath.Join(g.Resources["directory"].Name, "sub", "witness"))
			if err != nil || string(data) != "preserve" {
				t.Fatal("live subdirectory consumer data removed", err)
			}
		}
		unrelated := ownedResource{Kind: "volume", Token: newID()}
		unrelated.Name = "backlot-fixture-" + unrelated.Token
		unrelated.ID, err = createDockerResource(context.Background(), h.engine, unrelated)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := removeDockerResource(context.Background(), h.engine, unrelated); err != nil {
				t.Error(err)
			}
		})
		zero := 0
		if _, err = h.engine.ContainerStop(context.Background(), foreign.ID, client.ContainerStopOptions{Timeout: &zero}); err != nil {
			t.Fatal(err)
		}
		if _, err = h.engine.ContainerRemove(context.Background(), foreign.ID, client.ContainerRemoveOptions{}); err != nil {
			t.Fatal(err)
		}
		if _, err = h.cli.Destroy(context.Background(), id); err != nil {
			t.Fatal("explicit retry did not finish cleanup", err)
		}
		if err = verifyResource(context.Background(), h.engine, unrelated); err != nil {
			t.Fatal("unrelated volume changed", err)
		}
	}
	t.Run("foreign-consumer-and-unrelated-resources-preserved", func(t *testing.T) {
		t.Run("volume-only", func(t *testing.T) { foreignCase(t, false) })
		t.Run("directory-subpath-only", func(t *testing.T) { foreignCase(t, true) })
	})
}
func ptrString(text string) *string { return &text }
func (h *dockerHarness) engineAlive(r ownedResource) bool {
	h.t.Helper()
	if err := verifyDockerResource(context.Background(), h.engine, r); err != nil {
		return false
	}
	inspect, err := h.engine.ContainerInspect(context.Background(), r.ID, client.ContainerInspectOptions{})
	return err == nil && inspect.Container.State != nil && inspect.Container.State.Running
}
