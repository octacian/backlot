//go:build darwin || linux

package daemon

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	v1 "github.com/octacian/backlot/api/v1"
	"github.com/octacian/backlot/internal/client"
	"github.com/octacian/backlot/internal/native"
)

// TestNativeFixtureProcess is an owned generic subprocess fixture, not a mock runtime.
func TestNativeFixtureProcess(t *testing.T) {
	if os.Getenv("BACKLOT_NATIVE_FIXTURE") != "1" {
		return
	}
	mode := ""
	for i, arg := range os.Args {
		if arg == "--" && i+1 < len(os.Args) {
			mode = os.Args[i+1]
			break
		}
	}
	if path := os.Getenv("JOBPIDFILE"); path != "" {
		if err := os.WriteFile(path, []byte(fmt.Sprintf("%d %d", os.Getpid(), syscall.Getpgrp())), 0600); err != nil {
			os.Exit(25)
		}
	}
	if value := os.Getenv("BACKEND_PORT"); value != "" {
		fmt.Println("BACKEND_PORT=" + value)
	}
	fmt.Println("fixture", mode, "stdout")
	fmt.Fprintln(os.Stderr, "fixture stderr")
	switch mode {
	case "child":
		signal.Ignore(syscall.SIGTERM)
		for {
			time.Sleep(time.Hour)
		}
	case "prep":
		os.Exit(0)
	case "fail":
		os.Exit(23)
	case "job-fail":
		os.Exit(17)
	case "never":
		for {
			time.Sleep(time.Hour)
		}
	case "job", "probe":
		connection, err := net.DialTimeout("tcp", "127.0.0.1:"+os.Getenv("PORT"), time.Second)
		if err != nil {
			os.Exit(19)
		}
		_ = connection.Close()
		os.Exit(0)
	}
	child := exec.Command(os.Args[0], "-test.run=TestNativeFixtureProcess", "--", "child")
	child.Env = os.Environ()
	child.Stdout = os.Stdout
	child.Stderr = os.Stderr
	if err := child.Start(); err != nil {
		os.Exit(20)
	}
	if err := os.WriteFile(os.Getenv("PIDFILE"), []byte(fmt.Sprintf("%d %d %d", os.Getpid(), child.Process.Pid, syscall.Getpgrp())), 0600); err != nil {
		os.Exit(21)
	}
	if mode == "parent-exit" {
		os.Exit(0)
	}
	if os.Getenv("DELAY_BIND") == "1" {
		time.Sleep(300 * time.Millisecond)
	}
	// Private fixture authority survives a deliberate guardian crash. Cleanup asks
	// this live member to kill its own group; the test never signals an observed PGID.
	if control := os.Getenv("FIXTURE_CONTROL"); control != "" {
		_ = os.Remove(control)
		socket, err := net.Listen("unix", control)
		if err != nil {
			os.Exit(24)
		}
		go func() {
			for {
				conn, err := socket.Accept()
				if err != nil {
					return
				}
				var request fixtureControl
				_ = json.NewDecoder(conn).Decode(&request)
				if request.Token == os.Getenv("FIXTURE_TOKEN") {
					switch request.Action {
					case "kill":
						_ = syscall.Kill(-syscall.Getpgrp(), syscall.SIGKILL)
					case "root-crash":
						if err := json.NewEncoder(conn).Encode(syscall.Getpgrp()); err != nil {
							_ = conn.Close()
							continue
						}
						_ = conn.Close()
						_ = syscall.Kill(os.Getpid(), syscall.SIGKILL)
						continue
					case "child-crash":
						if err := child.Process.Kill(); err != nil {
							_ = json.NewEncoder(conn).Encode(-1)
							_ = conn.Close()
							continue
						}
						if _, ok := child.Wait().(*exec.ExitError); !ok {
							_ = json.NewEncoder(conn).Encode(-1)
							_ = conn.Close()
							continue
						}
					}
					_ = json.NewEncoder(conn).Encode(syscall.Getpgrp())
				}
				_ = conn.Close()
			}
		}()
	}
	// The test owns the listener and releases binding by closing the accepted
	// connection. Startup output and the PID witness precede this handshake;
	// cancellation still terminates this blocked root through its guardian.
	if path := os.Getenv("FIXTURE_BIND_GATE"); path != "" {
		connection, err := net.DialTimeout("unix", path, time.Second)
		if err != nil {
			os.Exit(26)
		}
		var release [1]byte
		_, err = connection.Read(release[:])
		_ = connection.Close()
		if err != io.EOF {
			os.Exit(27)
		}
	}
	host := os.Getenv("BIND_HOST")
	if host == "" {
		host = "127.0.0.1"
	}
	listener, err := net.Listen("tcp", net.JoinHostPort(host, os.Getenv("PORT")))
	if err != nil {
		os.Exit(22)
	}
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if os.Getenv("HTTP_STATUS") == "503" {
			w.WriteHeader(http.StatusServiceUnavailable)
		}
		_, _ = io.WriteString(w, "ok")
	}), ReadHeaderTimeout: time.Second}
	_ = server.Serve(listener)
	os.Exit(0)
}

type fixtureControl struct{ Token, Action string }

type nativeHarness struct {
	t                          *testing.T
	binary, dir, project, pids string
	lease                      string
	stopGrace                  time.Duration
	cli                        *client.Client
	process                    *exec.Cmd
	waited                     bool
	cleanupUnverified          bool
	control, controlToken      string
	extraPIDFiles              []string
	faultMode                  bool
	manifest                   v1.Manifest
	tokens                     map[string]string
	daemonLog                  bytes.Buffer
}

func (h *nativeHarness) start() {
	h.t.Helper()
	h.process = exec.Command(h.binary, "daemon", "serve", "--state-dir", h.dir, "--lease-duration", h.lease)
	h.process.Env = append(os.Environ(), "BACKLOT_NATIVE_TEST_FAULTS=0")
	if h.faultMode {
		h.process.Env = append(h.process.Env, "BACKLOT_NATIVE_TEST_FAULTS=1")
	}
	h.process.Stdout = io.Discard
	if strings.Contains(h.daemonLog.String(), "DATA RACE") {
		h.t.Fatal("race-instrumented daemon reported a data race", h.daemonLog.String())
	}
	h.daemonLog.Reset()
	h.process.Stderr = &h.daemonLog
	if err := h.process.Start(); err != nil {
		h.t.Fatal(err)
	}
	h.waited = false
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := h.cli.Status(context.Background()); err == nil {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	h.t.Fatal("daemon did not start")
}
func (h *nativeHarness) write() {
	h.t.Helper()
	data, err := json.Marshal(h.manifest)
	if err != nil {
		h.t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(h.project, "backlot.json"), data, 0600); err != nil {
		h.t.Fatal(err)
	}
}
func (h *nativeHarness) request(scene string) v1.RunRequest {
	grace := h.stopGrace
	if grace == 0 {
		grace = 50 * time.Millisecond
	}
	return v1.RunRequest{APIVersion: v1.Version, Plan: v1.PlanRequest{ProjectPath: h.project, Scene: scene}, Options: v1.ExecutionOptions{StartupTimeout: "3s", JobTimeout: "3s", StopGrace: grace.String()}}
}
func (h *nativeHarness) run(scene string) v1.InstanceResponse {
	h.t.Helper()
	response, err := h.cli.Run(context.Background(), h.request(scene))
	if err != nil {
		h.t.Fatal(err)
	}
	if response.LeaseToken != "" {
		if h.tokens == nil {
			h.tokens = map[string]string{}
		}
		h.tokens[response.Instance.ID] = response.LeaseToken
	}
	return response
}
func (h *nativeHarness) state(id string, want ...v1.InstanceStatus) v1.InstanceResponse {
	h.t.Helper()
	last, err := h.cli.Inspect(context.Background(), id)
	if err != nil {
		h.t.Fatal(err)
	}
	budget := 6 * time.Second
	for _, status := range want {
		if status == v1.Starting || status == v1.RuntimeReady {
			continue
		}
		options := h.request(last.Instance.Plan.Scene).Options
		startup, _ := time.ParseDuration(options.StartupTimeout)
		job, _ := time.ParseDuration(options.JobTimeout)
		grace, _ := time.ParseDuration(options.StopGrace)
		// native.Stop permits dial (1s), proof (grace+8s), authority exit
		// (2s); Group.Stop then joins the guardian (2s) and status (1s).
		// Jobs clean up sequentially before the final concurrent group cleanup.
		cleanup := grace + 14*time.Second
		budget = startup + cleanup
		for _, component := range last.Instance.Plan.Components {
			if component.Kind == v1.Job {
				budget += job + cleanup
			}
		}
		break
	}
	deadline := time.Now().Add(budget)
	for time.Now().Before(deadline) {
		last, err = h.cli.Inspect(context.Background(), id)
		if err != nil {
			h.t.Fatal(err)
		}
		for _, status := range want {
			if last.Instance.Status == status {
				return last
			}
		}
		switch last.Instance.Status {
		case v1.Succeeded, v1.Failed, v1.Cancelled, v1.Stopped, v1.Interrupted:
			h.t.Fatalf("wanted %v, unexpected terminal status %s execution=%+v", want, last.Instance.Status, last.Instance.Execution)
		}
		if token := h.tokens[id]; token != "" && (last.Instance.Status == v1.Starting || last.Instance.Status == v1.RuntimeReady) {
			if _, err := h.cli.Renew(context.Background(), id, token); err != nil {
				current, inspectErr := h.cli.Inspect(context.Background(), id)
				if inspectErr != nil || (current.Instance.Status == v1.Starting || current.Instance.Status == v1.RuntimeReady) {
					h.t.Fatal(err)
				}
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	h.t.Fatalf("wanted %v within %s, got %s execution=%+v", want, budget, last.Instance.Status, last.Instance.Execution)
	return last
}
func (h *nativeHarness) absent() {
	h.t.Helper()
	for _, path := range append([]string{h.pids}, h.extraPIDFiles...) {
		data, err := os.ReadFile(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			h.cleanupUnverified = true
			h.t.Error(err)
			continue
		}
		fields := strings.Fields(string(data))
		for _, text := range fields {
			pid, err := strconv.Atoi(text)
			if err != nil {
				h.cleanupUnverified = true
				h.t.Error(err)
				continue
			}
			deadline := time.Now().Add(2 * time.Second)
			for syscall.Kill(pid, 0) != syscall.ESRCH && time.Now().Before(deadline) {
				time.Sleep(20 * time.Millisecond)
			}
			if syscall.Kill(pid, 0) != syscall.ESRCH {
				h.cleanupUnverified = true
				h.t.Errorf("fixture PID %d survives acknowledged cleanup; preserving %s", pid, h.dir)
			}
		}
		if len(fields) > 0 {
			pgid, err := strconv.Atoi(fields[len(fields)-1])
			if err != nil || pgid <= 1 {
				h.cleanupUnverified = true
				h.t.Error("invalid fixture group witness", path)
				continue
			}
			if syscall.Kill(-pgid, 0) != syscall.ESRCH {
				h.cleanupUnverified = true
				h.t.Error("fixture group absence unverified", pgid, "preserving", h.dir)
			}
		}
	}
}

func (h *nativeHarness) close() {
	h.t.Helper()
	_, stopErr := h.cli.Stop(context.Background())
	if !h.waited {
		done := make(chan error, 1)
		go func() { done <- h.process.Wait() }()
		select {
		case err := <-done:
			if err != nil && stopErr == nil {
				h.t.Error("daemon failed after successful shutdown request", err)
			}
		case <-time.After(40 * time.Second):
			_ = h.process.Process.Kill()
			<-done
			h.t.Error("daemon shutdown exceeded budget")
		}
		h.waited = true
	}
	if strings.Contains(h.daemonLog.String(), "DATA RACE") {
		h.t.Error("race-instrumented daemon reported a data race", h.daemonLog.String())
	}
	h.cli.Close()
	h.absent()
}
func newNativeHarness(t *testing.T, binary string, lease ...string) *nativeHarness {
	t.Helper()
	budget := "2s"
	if len(lease) > 0 {
		budget = lease[0]
	}
	return newNativeHarnessConfigured(t, binary, budget, false)
}
func newNativeFaultHarness(t *testing.T, binary string) *nativeHarness {
	t.Helper()
	return newNativeHarnessConfigured(t, binary, "2s", true)
}
func newNativeHarnessConfigured(t *testing.T, binary, budget string, faults bool) *nativeHarness {
	t.Helper()
	root, err := os.MkdirTemp("/tmp", "bl-native-")
	if err != nil {
		t.Fatal(err)
	}
	var h *nativeHarness
	t.Cleanup(func() {
		if h != nil && h.cleanupUnverified {
			t.Log("preserved unverified fixture state", root)
			return
		}
		if err := os.RemoveAll(root); err != nil {
			t.Error(err)
		}
	})
	h = &nativeHarness{t: t, binary: binary, lease: budget, faultMode: faults, dir: filepath.Join(root, "state"), project: filepath.Join(root, "project"), pids: filepath.Join(root, "pids")}
	if err := os.Mkdir(h.project, 0700); err != nil {
		t.Fatal(err)
	}
	h.control = filepath.Join(root, "fixture.sock")
	h.controlToken = fmt.Sprintf("%x", mustFixtureToken(t))
	literal := func(s string) v1.Value { return v1.Value{Literal: &s} }
	port := v1.Value{Ref: &v1.Reference{Kind: "resource", Name: "port", Field: "port"}}
	command := func(mode string) *v1.Command {
		return &v1.Command{Tool: "fixture", Args: []string{"-test.run=TestNativeFixtureProcess", "--", mode}}
	}
	fixtureToken := literal(h.controlToken)
	fixtureToken.Secret = true
	env := v1.Environment{Assign: map[string]v1.Value{"BACKLOT_NATIVE_FIXTURE": literal("1"), "PORT": port, "PIDFILE": literal(h.pids), "FIXTURE_CONTROL": literal(h.control), "FIXTURE_TOKEN": fixtureToken}}
	server := v1.Component{Kind: v1.Service, Runtime: v1.Native, Command: command("server"), Resources: []string{"port"}, Ports: map[string]v1.ServicePort{"http": {Resource: "port"}}, Environment: env, Readiness: &v1.Probe{Kind: "tcp", Target: &port, Timeout: "2s"}}
	job := v1.Component{Kind: v1.Job, Runtime: v1.Native, Policy: v1.EachStart, Command: command("job"), Resources: []string{"port"}, Environment: env, DependsOn: []v1.Dependency{{Component: "server", Condition: v1.Ready}}}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	h.manifest = v1.Manifest{Version: v1.ManifestVersion, Project: "native-fixture", Tools: map[string]string{"fixture": self}, Resources: map[string]v1.Resource{"port": {Kind: "port"}}, Components: map[string]v1.Component{"server": server, "job": job}, Scenes: map[string]v1.Scene{"dev": {Lifetime: v1.Persistent, Components: []string{"server"}, Resources: []string{"port"}}, "test": {Lifetime: v1.Disposable, Components: []string{"server", "job"}, Resources: []string{"port"}, TerminalJob: "job"}}}
	h.write()
	h.cli = client.New(SocketPath(h.dir))
	h.start()
	t.Cleanup(h.close)
	return h
}

func TestNativeRuntime(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "backlot")
	build := exec.Command("go", "build", "-race", "-o", binary, "../../cmd/backlot")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v %s", err, output)
	}
	t.Run("uncertainty-cleanup-after-fatal", func(t *testing.T) {
		record := filepath.Join(t.TempDir(), "record")
		self, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		command := exec.Command(self, "-test.run=^TestNativeUncertaintyCleanupFailurePath$", "-test.v")
		command.Env = append(os.Environ(), "BACKLOT_FAILURE_BINARY="+binary, "BACKLOT_FAILURE_RECORD="+record)
		output, err := command.CombinedOutput()
		if err == nil || !strings.Contains(string(output), "intentional assertion after guardian fault") {
			t.Fatalf("expected intentional subprocess failure: %v %s", err, output)
		}
		if strings.Contains(string(output), "DATA RACE") || strings.Contains(string(output), "unverified") || strings.Contains(string(output), "survives acknowledged") {
			t.Fatalf("fixture cleanup failed: %s", output)
		}
		data, err := os.ReadFile(record)
		if err != nil {
			t.Fatal(err)
		}
		lines := strings.SplitN(string(data), "\n", 2)
		if _, err := os.Stat(lines[0]); !os.IsNotExist(err) {
			t.Fatal("verified fixture state not removed", err, string(output))
		}
		numbers := strings.Fields(lines[1])
		pgid, _ := strconv.Atoi(numbers[0])
		if syscall.Kill(-pgid, 0) != syscall.ESRCH {
			t.Fatal("failure path left group", pgid)
		}
		for _, text := range numbers[1:] {
			pid, _ := strconv.Atoi(text)
			if syscall.Kill(pid, 0) != syscall.ESRCH {
				t.Fatal("failure path left process", pid)
			}
		}
	})
	for _, action := range []string{"stop", "restart", "shutdown"} {
		t.Run("accepted-16s-grace-"+action, func(t *testing.T) {
			h := newNativeHarness(t, binary)
			request := h.request("dev")
			request.Options.StopGrace = "16s"
			r, err := h.cli.Run(context.Background(), request)
			if err != nil {
				t.Fatal(err)
			}
			h.state(r.Instance.ID, v1.RuntimeReady)
			oldPIDs, err := os.ReadFile(h.pids)
			if err != nil {
				t.Fatal(err)
			}
			started := time.Now()
			switch action {
			case "stop":
				_, err = h.cli.StopExecution(context.Background(), r.Instance.ID)
			case "restart":
				request.InstanceID = r.Instance.ID
				request.Options.StopGrace = "0s"
				_, err = h.cli.Restart(context.Background(), request)
			case "shutdown":
				_, err = h.cli.Stop(context.Background())
				if err == nil {
					err = h.process.Wait()
					h.waited = true
				}
			}
			elapsed := time.Since(started)
			if err != nil {
				t.Fatal(action, elapsed, err)
			}
			if elapsed < 16*time.Second || elapsed > 36*time.Second {
				t.Fatal("accepted grace not honored", elapsed)
			}
			for _, text := range strings.Fields(string(oldPIDs)) {
				pid, _ := strconv.Atoi(text)
				if syscall.Kill(pid, 0) != syscall.ESRCH {
					t.Fatal("old owned process remains", pid)
				}
			}
			if action == "restart" {
				h.state(r.Instance.ID, v1.RuntimeReady)
			}
		})
	}
	for _, kind := range []string{"tcp", "http"} {
		t.Run("dual-stack-wildcard-"+kind, func(t *testing.T) {
			h := newNativeHarness(t, binary)
			c := h.manifest.Components["server"]
			c.Environment.Assign["BIND_HOST"] = v1.Value{Literal: ptr("::")}
			if kind == "http" {
				listener, err := net.Listen("tcp4", "127.0.0.1:0")
				if err != nil {
					t.Fatal(err)
				}
				port := strconv.Itoa(listener.Addr().(*net.TCPAddr).Port)
				if err := listener.Close(); err != nil {
					t.Fatal(err)
				}
				c.Environment.Assign["PORT"] = v1.Value{Literal: ptr(port)}
				c.Readiness = &v1.Probe{Kind: "http", Target: &v1.Value{Literal: ptr("http://127.0.0.1:" + port + "/")}, Timeout: "2s"}
			}
			h.manifest.Components["server"] = c
			h.write()
			r := h.run("dev")
			h.state(r.Instance.ID, v1.RuntimeReady)
			if _, err := h.cli.StopExecution(context.Background(), r.Instance.ID); err != nil {
				t.Fatal(err)
			}
			h.absent()
		})
	}
	for _, kind := range []string{"service", "job"} {
		for _, reference := range []string{"resource", "service"} {
			t.Run("consumed-port-conflict-"+kind+"-"+reference, func(t *testing.T) { testConsumedPortConflict(t, binary, kind, reference) })
		}
	}
	for _, action := range []string{"expiry", "shutdown"} {
		t.Run("independent-expiry-"+action, func(t *testing.T) { testIndependentNativeExpiry(t, binary, action) })
	}
	t.Run("self-fault-rejects-disabled-and-mismatched-authority", func(t *testing.T) {
		disabled := newNativeHarness(t, binary)
		c := disabled.manifest.Components["server"]
		c.Environment.Assign["BACKLOT_NATIVE_TEST_FAULTS"] = v1.Value{Literal: ptr("1")}
		disabled.manifest.Components["server"] = c
		disabled.write()
		a := disabled.run("dev")
		disabled.state(a.Instance.ID, v1.RuntimeReady)
		enabled := newNativeFaultHarness(t, binary)
		b := enabled.run("dev")
		enabled.state(b.Instance.ID, v1.RuntimeReady)
		disabled.retainFixtureCleanup()
		enabled.retainFixtureCleanup()
		first := disabled.guardianCapability(a.Instance.ID)
		second := enabled.guardianCapability(b.Instance.ID)
		if err := native.CrashForTest(first); err == nil {
			t.Fatal("workload activation enabled default-disabled guardian fault")
		}
		attempts := []native.Identity{second, second, second, first}
		attempts[0].Token = first.Token
		attempts[1].Birth = "stale"
		attempts[2].PID = first.PID
		attempts[3].Control = second.Control
		for _, identity := range attempts {
			if err := native.CrashForTest(identity); err == nil {
				t.Fatal("mismatched fault authority accepted")
			}
		}
		for _, pair := range []struct {
			h  *nativeHarness
			id string
		}{{disabled, a.Instance.ID}, {enabled, b.Instance.ID}} {
			pair.h.state(pair.id, v1.RuntimeReady)
			pair.h.fixtureAction("inspect")
			if _, err := pair.h.cli.StopExecution(context.Background(), pair.id); err != nil {
				t.Fatal(err)
			}
			pair.h.absent()
		}
	})
	t.Run("persistent-concurrent-logs-stop-restart", func(t *testing.T) {
		h := newNativeHarness(t, binary)
		var wg sync.WaitGroup
		ids := make(chan string, 6)
		for range 6 {
			wg.Go(func() {
				r, err := h.cli.Run(context.Background(), h.request("dev"))
				if err != nil {
					t.Error(err)
					return
				}
				ids <- r.Instance.ID
			})
		}
		wg.Wait()
		close(ids)
		id := ""
		for next := range ids {
			if id != "" && next != id {
				t.Fatal("persistent duplicate")
			}
			id = next
		}
		h.state(id, v1.RuntimeReady)
		logs, err := h.cli.Logs(context.Background(), v1.LogsRequest{APIVersion: v1.Version, InstanceID: id})
		if err != nil || len(logs.Records) < 2 {
			t.Fatalf("logs %+v %v", logs, err)
		}
		if _, err := h.cli.StopExecution(context.Background(), id); err != nil {
			t.Fatal(err)
		}
		h.absent()
		historical, err := h.cli.Logs(context.Background(), v1.LogsRequest{APIVersion: v1.Version, InstanceID: id})
		if err != nil || len(historical.Records) < len(logs.Records) {
			t.Fatal("historical logs lost", err)
		}
		c := h.manifest.Components["server"]
		c.Environment.Assign["EXTRA"] = v1.Value{Literal: ptr("changed")}
		h.manifest.Components["server"] = c
		h.write()
		request := h.request("dev")
		request.InstanceID = id
		r, err := h.cli.Restart(context.Background(), request)
		if err != nil || r.Instance.ID != id {
			t.Fatal(r, err)
		}
		h.state(id, v1.RuntimeReady)
		page, err := h.cli.Logs(context.Background(), v1.LogsRequest{APIVersion: v1.Version, InstanceID: id, Offset: historical.NextOffset})
		if err != nil || len(page.Records) == 0 {
			t.Fatal("restart log cursor lost output", err)
		}
		for _, record := range page.Records {
			if record.Attempt == logs.Records[0].Attempt {
				t.Fatal("restart log cursor replayed old attempt")
			}
		}
		h.manifest.Resources["extra"] = v1.Resource{Kind: "port"}
		scene := h.manifest.Scenes["dev"]
		scene.Resources = append(scene.Resources, "extra")
		h.manifest.Scenes["dev"] = scene
		h.write()
		if _, err := h.cli.Restart(context.Background(), request); err == nil {
			t.Fatal("unsafe resource drift accepted")
		}
		h.state(id, v1.RuntimeReady)
	})
	t.Run("finite-original-status", func(t *testing.T) {
		h := newNativeHarness(t, binary)
		r := h.run("test")
		final := h.state(r.Instance.ID, v1.Succeeded)
		if code := final.Instance.Execution.Components[1].ExitCode; code == nil || *code != 0 {
			t.Fatal("missing job exit")
		}
		h.absent()
		job := h.manifest.Components["job"]
		job.Command.Args[len(job.Command.Args)-1] = "job-fail"
		h.manifest.Components["job"] = job
		h.write()
		r = h.run("test")
		final = h.state(r.Instance.ID, v1.Failed)
		if code := final.Instance.Execution.Components[1].ExitCode; code == nil || *code != 17 {
			t.Fatal("original failure status lost")
		}
		h.absent()
	})
	t.Run("startup-interruption-unlimited-command-probe", func(t *testing.T) {
		h := newNativeHarness(t, binary)
		c := h.manifest.Components["server"]
		c.Readiness = &v1.Probe{Kind: "command", Timeout: "0s", Command: &v1.Command{Tool: "fixture", Args: []string{"-test.run=TestNativeFixtureProcess", "--", "never"}}}
		h.manifest.Components["server"] = c
		h.write()
		request := h.request("dev")
		request.Options.StartupTimeout = "0s"
		r, err := h.cli.Run(context.Background(), request)
		if err != nil {
			t.Fatal(err)
		}
		time.Sleep(150 * time.Millisecond)
		if _, err := h.cli.StopExecution(context.Background(), r.Instance.ID); err != nil {
			t.Fatal(err)
		}
		h.state(r.Instance.ID, v1.Stopped)
		h.absent()
	})
	t.Run("lease-expiry-and-unlimited-job", func(t *testing.T) {
		h := newNativeHarness(t, binary, "400ms")
		job := h.manifest.Components["job"]
		job.Command.Args[len(job.Command.Args)-1] = "never"
		h.manifest.Components["job"] = job
		h.write()
		request := h.request("test")
		request.Options.JobTimeout = "0s"
		r, err := h.cli.Run(context.Background(), request)
		if err != nil {
			t.Fatal(err)
		}
		h.state(r.Instance.ID, v1.Cancelled)
		h.absent()
		r, err = h.cli.Run(context.Background(), request)
		if err != nil {
			t.Fatal(err)
		}
		for range 8 {
			time.Sleep(100 * time.Millisecond)
			if _, err := h.cli.Renew(context.Background(), r.Instance.ID, r.LeaseToken); err != nil {
				t.Fatal(err)
			}
		}
		h.state(r.Instance.ID, v1.Starting)
		if _, err := h.cli.StopExecution(context.Background(), r.Instance.ID); err != nil {
			t.Fatal(err)
		}
		h.absent()
	})
	t.Run("deadline-and-startup-failure", func(t *testing.T) {
		h := newNativeHarness(t, binary)
		c := h.manifest.Components["server"]
		c.Command.Args[len(c.Command.Args)-1] = "fail"
		h.manifest.Components["server"] = c
		h.write()
		r := h.run("dev")
		h.state(r.Instance.ID, v1.Failed)
		h.absent()
		h.manifest.Components["server"] = newNativeComponent(h)
		job := h.manifest.Components["job"]
		job.Command.Args[len(job.Command.Args)-1] = "never"
		h.manifest.Components["job"] = job
		h.write()
		request := h.request("test")
		request.Options.JobTimeout = "50ms"
		r, err := h.cli.Run(context.Background(), request)
		if err != nil {
			t.Fatal(err)
		}
		h.state(r.Instance.ID, v1.Failed)
		h.absent()
	})
	t.Run("daemon-crash-recovery", func(t *testing.T) {
		h := newNativeHarness(t, binary)
		r := h.run("dev")
		h.state(r.Instance.ID, v1.RuntimeReady)
		if err := h.process.Process.Kill(); err != nil {
			t.Fatal(err)
		}
		_ = h.process.Wait()
		h.waited = true
		h.start()
		final := h.state(r.Instance.ID, v1.Interrupted)
		if final.Instance.Execution.CollectionFailure == "" {
			t.Fatal("crash collection gap hidden")
		}
		h.absent()
	})
	t.Run("parent-exit-descendant-collection", func(t *testing.T) {
		h := newNativeHarness(t, binary)
		c := h.manifest.Components["server"]
		c.Command.Args[len(c.Command.Args)-1] = "parent-exit"
		h.manifest.Components["server"] = c
		h.write()
		r := h.run("dev")
		final := h.state(r.Instance.ID, v1.Failed)
		if final.Instance.Execution.Failure == "" {
			t.Fatal("parent exit hidden")
		}
		h.absent()
	})
	t.Run("http-and-command-readiness", func(t *testing.T) {
		for _, kind := range []string{"http", "command"} {
			t.Run(kind, func(t *testing.T) {
				h := newNativeHarness(t, binary)
				c := h.manifest.Components["server"]
				if kind == "command" {
					c.Readiness = &v1.Probe{Kind: "command", Timeout: "2s", Command: &v1.Command{Tool: "fixture", Args: []string{"-test.run=TestNativeFixtureProcess", "--", "probe"}}}
				} else {
					listener, err := net.Listen("tcp", "127.0.0.1:0")
					if err != nil {
						t.Fatal(err)
					}
					port := listener.Addr().(*net.TCPAddr).Port
					_ = listener.Close()
					text := strconv.Itoa(port)
					c.Environment.Assign["PORT"] = v1.Value{Literal: &text}
					target := v1.Value{Literal: ptr("http://127.0.0.1:" + text + "/")}
					c.Readiness = &v1.Probe{Kind: "http", Target: &target, Timeout: "2s"}
				}
				h.manifest.Components["server"] = c
				h.write()
				r := h.run("dev")
				h.state(r.Instance.ID, v1.RuntimeReady)
			})
		}
	})
	t.Run("uncertain-recovery-preserves-group", func(t *testing.T) {
		h := newNativeHarness(t, binary)
		r := h.run("dev")
		h.state(r.Instance.ID, v1.RuntimeReady)
		_ = h.process.Process.Kill()
		_ = h.process.Wait()
		h.waited = true
		store, err := openStore(h.dir)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = store.db.Close() }()
		journal, err := store.runtime(r.Instance.ID)
		if err != nil {
			t.Fatal(err)
		}
		original := journal.Groups[0]
		t.Cleanup(func() {
			if err := native.Stop(original, 0); err != nil {
				h.cleanupUnverified = true
				t.Error("retained fixture authority cleanup", err)
			}
			h.absent()
		})
		journal.Groups[0].Birth = "mismatch"
		if err := store.saveRuntime(r.Instance.ID, journal); err != nil {
			t.Fatal(err)
		}
		_ = store.db.Close()
		h.start()
		final := h.state(r.Instance.ID, v1.Interrupted)
		if final.Instance.Execution.CleanupFailure == "" {
			t.Fatal("ownership mismatch hidden")
		}
		if _, err := h.cli.StopExecution(context.Background(), r.Instance.ID); err == nil {
			t.Fatal("uncertain ownership stop returned success")
		}
		if syscall.Kill(original.PID, 0) != nil {
			t.Fatal("uncertain guardian was killed")
		}
		if err := native.Stop(original, 0); err != nil {
			t.Fatal(err)
		}
		h.absent()
	})
	t.Run("allocation-race-preserves-unrelated-listener", func(t *testing.T) {
		h := newNativeHarness(t, binary)
		c := h.manifest.Components["server"]
		c.Environment.Assign["DELAY_BIND"] = v1.Value{Literal: ptr("1")}
		h.manifest.Components["server"] = c
		h.write()
		r := h.run("dev")
		var port int
		deadline := time.Now().Add(time.Second)
		for port == 0 && time.Now().Before(deadline) {
			current, err := h.cli.Inspect(context.Background(), r.Instance.ID)
			if err != nil {
				t.Fatal(err)
			}
			port = current.Instance.Execution.Ports["port"]
			time.Sleep(5 * time.Millisecond)
		}
		sentinel, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = sentinel.Close() }()
		final := h.state(r.Instance.ID, v1.RuntimeReady)
		if final.Instance.Execution.Ports["port"] == port {
			t.Fatal("unrelated listener accepted as readiness")
		}
		connection, err := net.DialTimeout("tcp", sentinel.Addr().String(), time.Second)
		if err != nil {
			t.Fatal("unrelated listener lost", err)
		}
		_ = connection.Close()
	})
	t.Run("checkout-output-group-serialization", func(t *testing.T) {
		h := newNativeHarness(t, binary)
		h.manifest.Outputs = map[string]v1.Output{"build": {Path: "dist", ConcurrencyGroup: "build"}}
		c := h.manifest.Components["server"]
		c.Outputs = []string{"build"}
		h.manifest.Components["server"] = c
		h.manifest.Scenes["other"] = h.manifest.Scenes["dev"]
		h.write()
		first := h.run("dev")
		h.state(first.Instance.ID, v1.RuntimeReady)
		second := h.run("other")
		time.Sleep(150 * time.Millisecond)
		pending, err := h.cli.Inspect(context.Background(), second.Instance.ID)
		if err != nil || pending.Instance.Status != v1.Starting || len(pending.Instance.Execution.Ports) != 0 {
			t.Fatal("shared output group launched concurrently", pending, err)
		}
		if _, err := h.cli.StopExecution(context.Background(), first.Instance.ID); err != nil {
			t.Fatal(err)
		}
		h.state(second.Instance.ID, v1.RuntimeReady)
	})
	t.Run("completed-job-gate", func(t *testing.T) {
		h := newNativeHarness(t, binary)
		prep := h.manifest.Components["job"]
		prep.Command = &v1.Command{Tool: "fixture", Args: []string{"-test.run=TestNativeFixtureProcess", "--", "prep"}}
		h.manifest.Components["prep"] = prep
		job := h.manifest.Components["job"]
		job.DependsOn = []v1.Dependency{{Component: "prep", Condition: v1.Completed}}
		h.manifest.Components["job"] = job
		scene := h.manifest.Scenes["test"]
		scene.Components = []string{"server", "prep", "job"}
		h.manifest.Scenes["test"] = scene
		h.write()
		r := h.run("test")
		final := h.state(r.Instance.ID, v1.Succeeded)
		if len(final.Instance.Execution.Components) != 3 {
			t.Fatal("completed dependency missing")
		}
		h.absent()
	})
	t.Run("terminal-state-includes-cleanup-grace", func(t *testing.T) {
		h := newNativeHarness(t, binary)
		// The owned service descendant ignores TERM. Its valid cleanup grace
		// exceeds the old six-second state observation window.
		h.stopGrace = 7 * time.Second
		r := h.run("test")
		started := time.Now()
		final := h.state(r.Instance.ID, v1.Succeeded)
		if time.Since(started) < h.stopGrace {
			t.Fatal("terminal state preceded required descendant cleanup grace")
		}
		if final.Instance.Execution.CleanupFailure != "" {
			t.Fatal("cleanup failed", final.Instance.Execution.CleanupFailure)
		}
		h.absent()
	})
	t.Run("service-loss-cancels-terminal", func(t *testing.T) {
		h := newNativeHarness(t, binary)
		job := h.manifest.Components["job"]
		job.Command.Args[len(job.Command.Args)-1] = "never"
		h.manifest.Components["job"] = job
		h.write()
		r := h.run("test")
		deadline := time.Now().Add(time.Second)
		var current v1.InstanceResponse
		for time.Now().Before(deadline) {
			var err error
			current, err = h.cli.Inspect(context.Background(), r.Instance.ID)
			if err != nil {
				t.Fatal(err)
			}
			if len(current.Instance.Execution.Components) == 2 && current.Instance.Execution.Components[1].Status == "running" {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		h.retainGuardianCleanup(r.Instance.ID)
		h.fixtureAction("root-crash")
		final := h.state(r.Instance.ID, v1.Failed)
		if final.Instance.Execution.Components[1].ExitCode == nil {
			t.Fatal("cancelled job original status missing")
		}
		h.absent()
	})
	t.Run("guardian-crash-preserves-uncertain-descendants", func(t *testing.T) {
		h := newNativeFaultHarness(t, binary)
		r := h.run("dev")
		h.state(r.Instance.ID, v1.RuntimeReady)
		data, err := os.ReadFile(h.pids)
		if err != nil {
			t.Fatal(err)
		}
		root, _ := strconv.Atoi(strings.Fields(string(data))[0])
		pgid, err := syscall.Getpgid(root)
		if err != nil {
			t.Fatal(err)
		}
		identity := h.guardianCapability(r.Instance.ID)
		h.retainFixtureCleanup()
		if err := native.CrashForTest(identity); err != nil {
			t.Fatal(err)
		}
		final := h.state(r.Instance.ID, v1.Failed)
		if final.Instance.Execution.CleanupFailure == "" || final.Instance.Execution.CollectionFailure == "" {
			t.Fatal("guardian loss must retain independent cleanup and collection gaps")
		}
		for _, component := range final.Instance.Execution.Components {
			if component.ExitCode != nil {
				t.Fatal("lost native status invented an original exit")
			}
		}
		logs, err := h.cli.Logs(context.Background(), v1.LogsRequest{APIVersion: v1.Version, InstanceID: r.Instance.ID})
		if err != nil || logs.Gap == "" {
			t.Fatal("historical collection gap omitted", err)
		}
		following, err := h.cli.Logs(context.Background(), v1.LogsRequest{APIVersion: v1.Version, InstanceID: r.Instance.ID, Offset: logs.NextOffset})
		if err != nil || following.Gap != logs.Gap {
			t.Fatal("following collection gap omitted", err)
		}
		if syscall.Kill(root, 0) != nil {
			t.Fatal("uncertain descendant killed")
		}
		actual, err := syscall.Getpgid(root)
		if err != nil || actual != pgid {
			t.Fatal("fixture identity changed", err)
		}
		// Cleanup is registered before the fault and uses retained fixture authority.
	})
	t.Run("daemon-crash-during-startup", func(t *testing.T) {
		h := newNativeHarness(t, binary)
		c := h.manifest.Components["server"]
		c.Readiness = &v1.Probe{Kind: "command", Timeout: "0s", Command: &v1.Command{Tool: "fixture", Args: []string{"-test.run=TestNativeFixtureProcess", "--", "never"}}}
		h.manifest.Components["server"] = c
		h.write()
		r := h.run("dev")
		time.Sleep(150 * time.Millisecond)
		_ = h.process.Process.Kill()
		_ = h.process.Wait()
		h.waited = true
		h.start()
		h.state(r.Instance.ID, v1.Interrupted)
		h.absent()
	})
	t.Run("probe-timeout-with-unlimited-startup", func(t *testing.T) {
		h := newNativeHarness(t, binary)
		c := h.manifest.Components["server"]
		c.Readiness = &v1.Probe{Kind: "command", Timeout: "50ms", Command: &v1.Command{Tool: "fixture", Args: []string{"-test.run=TestNativeFixtureProcess", "--", "never"}}}
		h.manifest.Components["server"] = c
		h.write()
		request := h.request("dev")
		request.Options.StartupTimeout = "0s"
		r, err := h.cli.Run(context.Background(), request)
		if err != nil {
			t.Fatal(err)
		}
		h.state(r.Instance.ID, v1.Failed)
		h.absent()
	})

	t.Run("startup-budget-bounds-unlimited-probe", func(t *testing.T) {
		h := newNativeHarness(t, binary)
		c := h.manifest.Components["server"]
		c.Readiness = &v1.Probe{Kind: "command", Timeout: "0s", Command: &v1.Command{Tool: "fixture", Args: []string{"-test.run=TestNativeFixtureProcess", "--", "never"}}}
		h.manifest.Components["server"] = c
		h.write()
		request := h.request("dev")
		request.Options.StartupTimeout = "80ms"
		r, err := h.cli.Run(context.Background(), request)
		if err != nil {
			t.Fatal(err)
		}
		h.state(r.Instance.ID, v1.Failed)
		h.absent()
	})
	t.Run("inactive-guardian-stop-status", func(t *testing.T) {
		output, err := os.CreateTemp(t.TempDir(), "guardian-output")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = output.Close() })
		group, err := native.Start(binary, output, output)
		if group != nil {
			t.Cleanup(func() {
				if err := group.Stop(0); err != nil {
					t.Error("inactive guardian cleanup", err)
				}
			})
		}
		if err != nil {
			t.Fatal(err)
		}
		if err := group.Stop(0); err != nil {
			t.Fatal(err)
		}
		result := group.Result()
		if result == nil || result.CollectionFailure != "" || result.Code != -1 || group.PGID() != 0 || group.Alive() {
			t.Fatal("inactive cancellation lost status or launched work", result)
		}
	})
	t.Run("cli-sigint-sigterm-cancel-unlimited-terminal", func(t *testing.T) {
		for _, sig := range []syscall.Signal{syscall.SIGINT, syscall.SIGTERM} {
			t.Run(sig.String(), func(t *testing.T) {
				h := newNativeHarness(t, binary)
				job := h.manifest.Components["job"]
				job.Command.Args[len(job.Command.Args)-1] = "never"
				h.manifest.Components["job"] = job
				h.write()
				command := exec.Command(binary, "run", "test", "--state-dir", h.dir, "--project", h.project, "--json", "--job-timeout", "0s", "--stop-grace", "0s")
				var stdout strings.Builder
				command.Stdout = &stdout
				command.Stderr = io.Discard
				if err := command.Start(); err != nil {
					t.Fatal(err)
				}
				time.Sleep(400 * time.Millisecond)
				if err := command.Process.Signal(sig); err != nil {
					t.Fatal(err)
				}
				if err := command.Wait(); err == nil {
					t.Fatal("cancelled CLI returned success")
				}
				var final v1.InstanceResponse
				if err := json.Unmarshal([]byte(stdout.String()), &final); err != nil {
					t.Fatal(stdout.String(), err)
				}
				if final.Instance.Status != v1.Cancelled || !final.Instance.Execution.Cancelled {
					t.Fatal("cancellation state missing", stdout.String())
				}
				h.absent()
			})
		}
	})
	t.Run("native-private-generation-and-job-policies", func(t *testing.T) {
		h := newNativeHarness(t, binary)
		delete(h.manifest.Scenes, "test")
		h.manifest.Tools["sh"] = "sh"
		h.manifest.Resources["data"] = v1.Resource{Kind: "directory"}
		h.manifest.Resources["password"] = v1.Resource{Kind: "secret"}
		env := v1.Environment{Assign: map[string]v1.Value{"DATA": {Ref: &v1.Reference{Kind: "resource", Name: "data", Field: "path"}}, "SECRET": {Ref: &v1.Reference{Kind: "resource", Name: "password", Field: "value"}}}}
		prep := v1.Component{Kind: v1.Job, Runtime: v1.Native, Policy: v1.FreshOnly, Initializes: []string{"data"}, Resources: []string{"data", "password"}, Environment: env, Command: &v1.Command{Tool: "sh", Args: []string{"-c", "printf '%s\n' \"$SECRET\" >> \"$DATA/init\""}}}
		each := v1.Component{Kind: v1.Job, Runtime: v1.Native, Policy: v1.EachStart, Resources: []string{"data", "password"}, Environment: env, Command: &v1.Command{Tool: "sh", Args: []string{"-c", "echo start >> \"$DATA/starts\""}}, DependsOn: []v1.Dependency{{Component: "server", Condition: v1.Ready}}}
		server := h.manifest.Components["server"]
		server.DependsOn = []v1.Dependency{{Component: "prep", Condition: v1.Completed}}
		h.manifest.Components["server"] = server
		h.manifest.Components["prep"] = prep
		h.manifest.Components["each"] = each
		h.manifest.Scenes["dev"] = v1.Scene{Lifetime: v1.Persistent, Resources: []string{"port", "data", "password"}, Components: []string{"prep", "server", "each"}}
		h.write()
		id := h.run("dev").Instance.ID
		t.Cleanup(func() {
			if _, err := h.cli.Destroy(context.Background(), id); err != nil {
				h.cleanupUnverified = true
				t.Error(err)
			}
		})
		h.state(id, v1.RuntimeReady)
		contents := func() (string, string, string) {
			t.Helper()
			paths, err := filepath.Glob(filepath.Join(h.dir, "data", "*", "init"))
			if err != nil || len(paths) != 1 {
				t.Fatal("owned directory generation missing", err)
			}
			init, err := os.ReadFile(paths[0])
			if err != nil {
				t.Fatal(err)
			}
			starts, err := os.ReadFile(filepath.Join(filepath.Dir(paths[0]), "starts"))
			if err != nil {
				t.Fatal(err)
			}
			return paths[0], string(init), string(starts)
		}
		path, secret, starts := contents()
		if strings.Count(secret, "\n") != 1 || starts != "start\n" {
			t.Fatal("initial job policy failed")
		}
		req := h.request("dev")
		req.InstanceID = id
		if _, err := h.cli.Restart(context.Background(), req); err != nil {
			t.Fatal(err)
		}
		ready := h.state(id, v1.RuntimeReady)
		if ready.Instance.Execution.Components[0].Status != "skipped" {
			t.Fatal("native fresh-only not skipped")
		}
		_, retained, starts := contents()
		if retained != secret || starts != "start\nstart\n" {
			t.Fatal("native retained generation lost")
		}
		if _, err := h.cli.Reset(context.Background(), req); err != nil {
			t.Fatal(err)
		}
		h.state(id, v1.RuntimeReady)
		nextPath, nextSecret, starts := contents()
		if nextPath == path || nextSecret == secret || starts != "start\n" {
			t.Fatal("native reset did not replace generation")
		}
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatal("old owned directory survived reset", err)
		}
	})

	t.Run("selected-unsupported-rejected-before-launch", func(t *testing.T) {
		h := newNativeHarness(t, binary)
		h.manifest.Resources["data"] = v1.Resource{Kind: "origin"}
		scene := h.manifest.Scenes["dev"]
		scene.Resources = append(scene.Resources, "data")
		h.manifest.Scenes["dev"] = scene
		h.write()
		if _, err := h.cli.Run(context.Background(), h.request("dev")); err == nil {
			t.Fatal("publication resource executed")
		}
		if _, err := os.Stat(h.pids); !os.IsNotExist(err) {
			t.Fatal("preflight rejection launched fixture", err)
		}
	})

	t.Run("guardian-activation-cancellation-boundary", func(t *testing.T) {
		file, err := os.OpenFile(filepath.Join(t.TempDir(), "guardian.ndjson"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = file.Close() }()
		self, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		for range 3 {
			group, err := native.Start(binary, file, file)
			if err != nil {
				t.Fatal(err)
			}
			if err := group.Activate(native.Spec{Executable: self, Args: []string{"-test.run=TestNativeFixtureProcess", "--", "never"}, Environment: []string{"BACKLOT_NATIVE_FIXTURE=1"}, InstanceID: newID(), Component: "boundary", Attempt: newID()}); err != nil {
				t.Fatal(err)
			}
			if err := group.Stop(0); err != nil {
				t.Fatal("activation cancellation failed verified stop", err)
			}
		}
	})

	t.Run("descendant-exit-keeps-declared-root-health", func(t *testing.T) {
		h := newNativeHarness(t, binary)
		r := h.run("dev")
		h.state(r.Instance.ID, v1.RuntimeReady)
		h.retainGuardianCleanup(r.Instance.ID)
		h.fixtureAction("child-crash") // root owns and joins its unreaped direct child
		time.Sleep(100 * time.Millisecond)
		h.state(r.Instance.ID, v1.RuntimeReady)
		if _, err := h.cli.StopExecution(context.Background(), r.Instance.ID); err != nil {
			t.Fatal(err)
		}
		h.absent()
	})

	t.Run("absent-ownership-effect-preserved", func(t *testing.T) {
		h := newNativeHarness(t, binary)
		r := h.run("dev")
		h.state(r.Instance.ID, v1.RuntimeReady)
		_ = h.process.Process.Kill()
		_ = h.process.Wait()
		h.waited = true
		store, err := openStore(h.dir)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = store.db.Close() }()
		journal, err := store.runtime(r.Instance.ID)
		if err != nil {
			t.Fatal(err)
		}
		original := append([]native.Identity(nil), journal.Groups...)
		t.Cleanup(func() {
			for _, id := range original {
				if err := native.Stop(id, 0); err != nil {
					h.cleanupUnverified = true
					t.Error("retained fixture authority cleanup", err)
				}
			}
			h.absent()
		})
		journal.Groups = nil
		if err := store.saveRuntime(r.Instance.ID, journal); err != nil {
			t.Fatal(err)
		}
		_ = store.db.Close()
		h.start()
		final := h.state(r.Instance.ID, v1.Interrupted)
		if final.Instance.Execution.CleanupFailure == "" {
			t.Fatal("absent ownership effect hidden")
		}
		for _, id := range original {
			if syscall.Kill(id.PID, 0) != nil {
				t.Fatal("process with absent ownership proof was killed")
			}
			if err := native.Stop(id, 0); err != nil {
				t.Fatal(err)
			}
		}
		h.absent()
	})

	t.Run("default-stop-grace-retains-logs-and-joins", func(t *testing.T) {
		h := newNativeHarness(t, binary)
		request := h.request("dev")
		request.Options.StopGrace = ""
		r, err := h.cli.Run(context.Background(), request)
		if err != nil {
			t.Fatal(err)
		}
		h.state(r.Instance.ID, v1.RuntimeReady)
		time.Sleep(100 * time.Millisecond)
		start := time.Now()
		final, err := h.cli.StopExecution(context.Background(), r.Instance.ID)
		if err != nil {
			t.Fatal(err)
		}
		if time.Since(start) > 15*time.Second {
			t.Fatal("default stop exceeded bounded grace/verification")
		}
		if final.Instance.Execution.CollectionFailure != "" || final.Instance.Execution.CleanupFailure != "" {
			t.Fatal("graceful stop lost collection", final.Instance.Execution)
		}
		h.absent()
	})

	t.Run("inactive-guardian-verified-stop", func(t *testing.T) {
		file, err := os.OpenFile(filepath.Join(t.TempDir(), "inactive.ndjson"), os.O_CREATE|os.O_WRONLY, 0600)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = file.Close() }()
		for _, direct := range []bool{false, true} {
			group, err := native.Start(binary, file, file)
			if err != nil {
				t.Fatal(err)
			}
			start := time.Now()
			if direct {
				err = native.Stop(group.Identity, 0)
			} else {
				err = group.Stop(0)
			}
			if err != nil {
				t.Fatal("never-activated stop failed", direct, err)
			}
			if time.Since(start) > 4*time.Second {
				t.Fatal("inactive cancellation did not join promptly")
			}
			if group.Alive() {
				t.Fatal("inactive guardian remained alive")
			}
		}
	})

}
func ptr(text string) *string { return &text }
func newNativeComponent(h *nativeHarness) v1.Component {
	c := h.manifest.Components["server"]
	c.Command.Args[len(c.Command.Args)-1] = "server"
	return c
}

func mustFixtureToken(t *testing.T) []byte {
	t.Helper()
	token := make([]byte, 32)
	if _, err := rand.Read(token); err != nil {
		t.Fatal(err)
	}
	return token
}

// Retain before injecting a fault, so Fatal and early returns still join cleanup.
func (h *nativeHarness) retainFixtureCleanup() int {
	h.t.Helper()
	request := func(action string) (int, error) {
		conn, err := net.DialTimeout("unix", h.control, time.Second)
		if err != nil {
			return 0, err
		}
		defer func() { _ = conn.Close() }()
		_ = conn.SetDeadline(time.Now().Add(time.Second))
		if err := json.NewEncoder(conn).Encode(fixtureControl{h.controlToken, action}); err != nil {
			return 0, err
		}
		var group int
		err = json.NewDecoder(conn).Decode(&group)
		return group, err
	}
	pgid, err := request("inspect")
	if err != nil || pgid <= 1 {
		h.t.Fatal("fixture cleanup authority unavailable", pgid, err)
	}
	h.t.Cleanup(func() {
		if syscall.Kill(-pgid, 0) != syscall.ESRCH {
			_, _ = request("kill")
		}
		deadline := time.Now().Add(3 * time.Second)
		for syscall.Kill(-pgid, 0) != syscall.ESRCH && time.Now().Before(deadline) {
			time.Sleep(20 * time.Millisecond)
		}
		if syscall.Kill(-pgid, 0) != syscall.ESRCH {
			h.cleanupUnverified = true
			h.t.Error("fixture group absence unverified; preserving", h.dir)
		}
		h.absent()
	})
	return pgid
}

// This subprocess deliberately fails after fault injection. Its parent verifies
// that testing cleanup still runs and removes state only after process absence.
func TestNativeUncertaintyCleanupFailurePath(t *testing.T) {
	binary := os.Getenv("BACKLOT_FAILURE_BINARY")
	if binary == "" {
		return
	}
	h := newNativeFaultHarness(t, binary)
	r := h.run("dev")
	h.state(r.Instance.ID, v1.RuntimeReady)
	pgid := h.retainFixtureCleanup()
	data, err := os.ReadFile(h.pids)
	if err != nil {
		t.Fatal(err)
	}
	identity := h.guardianCapability(r.Instance.ID)
	guardian := identity.PID // absence witness only, never a signal target
	record := fmt.Sprintf("%s\n%d %d %s", filepath.Dir(h.dir), pgid, guardian, data)
	if err := os.WriteFile(os.Getenv("BACKLOT_FAILURE_RECORD"), []byte(record), 0600); err != nil {
		t.Fatal(err)
	}
	if err := native.CrashForTest(identity); err != nil {
		t.Fatal(err)
	}
	t.Fatal("intentional assertion after guardian fault")
}

func (h *nativeHarness) fixtureAction(action string) int {
	h.t.Helper()
	conn, err := net.DialTimeout("unix", h.control, time.Second)
	if err != nil {
		h.t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
	if err := json.NewEncoder(conn).Encode(fixtureControl{h.controlToken, action}); err != nil {
		h.t.Fatal(err)
	}
	var group int
	if err := json.NewDecoder(conn).Decode(&group); err != nil || group <= 1 {
		h.t.Fatal("fixture action rejected", action, err)
	}
	return group
}

// Tests settle the graph before copying private owner state. The copy may be
// stale or inconsistent and is never signal authority by itself: the live receiver
// must authenticate its full identity/capability before self-fault. A failed
// copy/read refuses injection; no observed process is a signal target.
func (h *nativeHarness) guardianCapability(id string) native.Identity {
	h.t.Helper()
	data, err := os.ReadFile(filepath.Join(h.dir, "state.db"))
	if err != nil {
		h.t.Fatal(err)
	}
	directory := h.t.TempDir()
	if err := os.WriteFile(filepath.Join(directory, "state.db"), data, 0600); err != nil {
		h.t.Fatal(err)
	}
	snapshot, err := openStore(directory)
	if err != nil {
		h.t.Fatal(err)
	}
	defer func() { _ = snapshot.db.Close() }()
	journal, err := snapshot.runtime(id)
	if err != nil || len(journal.Groups) < 1 {
		h.t.Fatal("private fixture guardian capability unavailable", err)
	}
	return journal.Groups[0]
}

func (h *nativeHarness) retainGuardianCleanup(id string) {
	h.t.Helper()
	identity := h.guardianCapability(id)
	h.t.Cleanup(func() {
		if err := native.Stop(identity, 0); err != nil {
			h.cleanupUnverified = true
			h.t.Error("retained fixture guardian cleanup", err)
		}
		h.absent()
	})
}
