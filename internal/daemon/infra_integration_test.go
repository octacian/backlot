//go:build darwin || linux

package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/gofrs/flock"
	v1 "github.com/octacian/backlot/api/v1"
	"github.com/octacian/backlot/internal/client"
	bolt "go.etcd.io/bbolt"
	berrors "go.etcd.io/bbolt/errors"
)

func infraCLI(t *testing.T, binary, dir string, success bool, args ...string) []byte {
	t.Helper()
	args = append(args, "--state-dir", dir, "--json")
	command := exec.Command(binary, args...)
	var diagnostic bytes.Buffer
	command.Stderr = &diagnostic
	output, err := command.Output()
	if (err == nil) != success {
		t.Fatalf("CLI %v: %v %s %s", args, err, output, diagnostic.String())
	}
	return output
}
func infraJoin(t *testing.T, h *nativeHarness) {
	t.Helper()
	if err := h.process.Wait(); err != nil {
		t.Fatal(err, h.daemonLog.String())
	}
	h.waited = true
	h.absent()
}

func TestInfraCLI(t *testing.T) {
	binary := buildFixtureBinary(t, t.TempDir(), "../../cmd/backlot")
	h := newNativeHarness(t, binary)
	config := filepath.Join(filepath.Dir(h.dir), "machine.json")
	if err := os.WriteFile(config, []byte(`{"version":"backlot/v1"}`), 0600); err != nil {
		t.Fatal(err)
	}
	run := func(scene string) v1.InstanceResponse {
		q := h.request(scene)
		q.Plan.ConfigPath = config
		out, err := h.cli.Run(context.Background(), q)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	// Every composing call uses explicit private inputs; this fixture does not need Docker.
	for range 2 {
		var out v1.InfraResponse
		if json.Unmarshal(infraCLI(t, binary, h.dir, true, "infra", "start", "--config", config), &out) != nil || !out.Ready || out.Daemon != "running" || out.Gateway.Mode != "none" {
			t.Fatal(out)
		}
	}
	// Reproduce the reported sequence on real native work: gateway stop is only a
	// blocker guard; normal daemon cleanup should remove workloads without restart.
	ready := run("dev")
	legacy := infraCLI(t, binary, h.dir, true, "daemon", "stop")
	if !bytes.Contains(legacy, []byte(`"stopping"`)) {
		t.Fatal(string(legacy))
	}
	infraJoin(t, h)
	h.start()
	infraCLI(t, binary, h.dir, true, "gateway", "stop")
	inspected, err := h.cli.Inspect(context.Background(), ready.Instance.ID)
	if err != nil || inspected.Instance.Execution.CleanupFailure != "" || inspected.Instance.Status != v1.Stopped {
		t.Fatal(inspected, err)
	}
	// Kept disposable work and persistent work share the existing shutdown path.
	run("dev")
	job := h.manifest.Components["job"]
	job.Command.Args = v1.StringArguments("-test.run=TestNativeFixtureProcess", "--", "job-fail")
	h.manifest.Components["job"] = job
	h.write()
	q := h.request("test")
	q.Plan.ConfigPath = config
	q.Options.KeepOnFailure = true
	kept, err := h.cli.Run(context.Background(), q)
	if err != nil {
		t.Fatal(err)
	}
	kept = h.state(kept.Instance.ID, v1.Failed)
	if kept.Instance.Execution == nil || !kept.Instance.Execution.Kept {
		t.Fatal("kept workload missing", kept.Instance.Status)
	}
	data := infraCLI(t, binary, h.dir, true, "infra", "stop")
	records := []v1.InfraStopRecord{}
	decoder := json.NewDecoder(bytes.NewReader(data))
	for decoder.More() {
		var r v1.InfraStopRecord
		if err := decoder.Decode(&r); err != nil {
			t.Fatal(err)
		}
		records = append(records, r)
	}
	if len(records) < 15 || records[0].Type != "accepted" || records[len(records)-2].Type != "cleanup" || records[len(records)-1].State != "complete" {
		t.Fatal(string(data))
	}
	infraJoin(t, h)
	absent := infraCLI(t, binary, h.dir, false, "infra", "stop")
	if !bytes.Contains(absent, []byte(`"state":"absent"`)) || !bytes.Contains(absent, []byte("cleanup_unknown")) {
		t.Fatal(string(absent))
	}
	infraCLI(t, binary, h.dir, true, "infra", "status")
	t.Log("real CLI legacy workaround needs no gateway restart; unified stream completed; absent cleanup stays unknown")
}

func TestInfraManagedRuntime(t *testing.T) {
	caddy := os.Getenv("BACKLOT_CADDY_TEST_BINARY")
	if caddy == "" {
		t.Skip("set BACKLOT_CADDY_TEST_BINARY for owned managed runtime")
	}
	binary := buildFixtureBinary(t, t.TempDir(), "../../cmd/backlot")
	h := newNativeHarness(t, binary)
	config := filepath.Join(filepath.Dir(h.dir), "machine.json")
	port, admin := managedFixturePort(t), managedFixturePort(t)
	machine := v1.MachineConfig{Version: v1.ManifestVersion, Caddy: &v1.CaddyConfig{Mode: "managed", Executable: caddy, Endpoint: "http://127.0.0.1:" + strconv.Itoa(admin), HTTPSPort: &port}}
	raw, _ := json.Marshal(machine)
	if err := os.WriteFile(config, raw, 0600); err != nil {
		t.Fatal(err)
	}
	blocker, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(port))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = blocker.Close() })
	failed := infraCLI(t, binary, h.dir, false, "infra", "start", "--config", config)
	var partial v1.InfraResponse
	if json.Unmarshal(failed, &partial) != nil || partial.Daemon != "running" || partial.Ready || partial.Error == nil {
		t.Fatal(string(failed))
	}
	if _, err := h.cli.Status(context.Background()); err != nil {
		t.Fatal("partial start rolled daemon back", err)
	}
	if err := blocker.Close(); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		infraCLI(t, binary, h.dir, true, "infra", "start", "--config", config)
	}
	_, _, storage := managedPaths(h.dir)
	rootPath := filepath.Join(storage, "pki", "authorities", "local", "root.crt")
	ca, err := os.ReadFile(rootPath)
	if err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(h.dir, "retained-data")
	if err := os.WriteFile(sentinel, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	status := infraCLI(t, binary, h.dir, true, "infra", "status")
	if !bytes.Contains(status, []byte(`"ready":true`)) {
		t.Fatal(string(status))
	}
	infraCLI(t, binary, h.dir, true, "infra", "stop")
	infraJoin(t, h)
	after, err := os.ReadFile(rootPath)
	if err != nil || !bytes.Equal(ca, after) {
		t.Fatal("CA changed", err)
	}
	if data, err := os.ReadFile(sentinel); err != nil || string(data) != "keep" {
		t.Fatal("retained data changed", err)
	}
	h.start()
	infraCLI(t, binary, h.dir, true, "infra", "start") // retained settings, no default file read
	changedPort := managedFixturePort(t)
	machine.Caddy.HTTPSPort = &changedPort
	raw, _ = json.Marshal(machine)
	if err := os.WriteFile(config, raw, 0600); err != nil {
		t.Fatal(err)
	}
	conflict := infraCLI(t, binary, h.dir, false, "infra", "start", "--config", config)
	if !bytes.Contains(conflict, []byte("gateway_config")) {
		t.Fatal(string(conflict))
	}
	infraCLI(t, binary, h.dir, true, "infra", "stop")
	infraJoin(t, h)
	t.Log("real Caddy conflict left daemon available; idempotence, retained configuration, drift rejection, listener readiness and CA/data retention passed")
}

// TestInfraOwnerProcess reproduces main's process exit boundary. Its private owner
// deliberately ignores cancellation until its retained release file is written.
func TestInfraOwnerProcess(t *testing.T) {
	directory := os.Getenv("BACKLOT_INFRA_OWNER_ROOT")
	if directory == "" {
		return
	}
	err := Serve(context.Background(), Options{Directory: directory, serviceReady: func(s *service) {
		_, finish, err := s.beginMutation(context.Background())
		if err != nil {
			os.Exit(4)
		}
		go func() {
			for {
				if _, err := os.Stat(filepath.Join(directory, "release")); err == nil {
					finish()
					return
				}
				time.Sleep(10 * time.Millisecond)
			}
		}()
	}})
	if err != nil {
		os.Exit(1)
	}
	os.Exit(0)
}

func TestInfraProcessRetainsUnjoinedOwner(t *testing.T) {
	root, err := os.MkdirTemp("/tmp", "bl-infra-owner-")
	if err != nil {
		t.Fatal(err)
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command(self, "-test.run=^TestInfraOwnerProcess$")
	command.Env = append(os.Environ(), "BACKLOT_INFRA_OWNER_ROOT="+root)
	var output bytes.Buffer
	command.Stdout, command.Stderr = &output, &output
	var done chan error
	// Retain release and direct-child join before starting the fault fixture.
	t.Cleanup(func() {
		_ = os.WriteFile(filepath.Join(root, "release"), nil, 0600)
		if done != nil {
			select {
			case <-done:
			case <-time.After(10 * time.Second):
				_ = command.Process.Kill()
				<-done
				t.Error("owner fixture needed kill; preserved", root)
				return
			}
		}
		_ = os.RemoveAll(root)
	})
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	done = waitFixtureChild(command)
	cli := client.New(SocketPath(root))
	defer cli.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()
	for {
		if _, err := cli.Status(ctx); err == nil {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(20 * time.Millisecond):
		}
	}
	started := time.Now()
	stopDone := make(chan error, 1)
	go func() { _, err := cli.Stop(ctx); stopDone <- err }()
	var terminal, completion v1.InfraStopRecord
	err = InfraStop(ctx, root, func(r v1.InfraStopRecord) error {
		if r.Type == "cleanup" {
			terminal = r
		}
		if r.Type == "completion" {
			completion = r
		}
		return nil
	})
	if err == nil || terminal.Error == nil || completion.State != "unknown" || time.Since(started) > 30*time.Second {
		t.Fatal("finite cleanup failure missing", terminal, completion, err)
	}
	if err := <-stopDone; err == nil {
		t.Fatal("legacy observer claimed success")
	}
	select {
	case err := <-done:
		t.Fatal("serving process returned with live owner", err, output.String())
	default:
	}
	lock := flock.New(filepath.Join(root, "daemon.lock"))
	defer func() { _ = lock.Close() }()
	held, err := lock.TryLock()
	if err != nil || held {
		t.Fatal("state lock released under live owner", held, err)
	}
	db, err := bolt.Open(filepath.Join(root, "state.db"), 0600, &bolt.Options{Timeout: 100 * time.Millisecond})
	if err == nil {
		_ = db.Close()
		t.Fatal("storage released under live owner")
	}
	if !errors.Is(err, berrors.ErrTimeout) {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "release"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		var exit *exec.ExitError
		if !errors.As(err, &exit) || exit.ExitCode() != 1 {
			t.Fatal(err, output.String())
		}
		done = nil
	case <-time.After(10 * time.Second):
		t.Fatal("process did not join released owner")
	}
	held, err = lock.TryLock()
	if err != nil || !held {
		t.Fatal("lock not released after owner joined", err)
	}
	if _, err := os.Lstat(SocketPath(root)); !os.IsNotExist(err) {
		t.Fatal("socket retained after join", err)
	}
}
