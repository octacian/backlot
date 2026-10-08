//go:build darwin || linux

package daemon

import (
	"bytes"
	"context"
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
	if err := os.WriteFile(os.Getenv("PIDFILE"), []byte(fmt.Sprintf("%d %d", os.Getpid(), child.Process.Pid)), 0600); err != nil {
		os.Exit(21)
	}
	if mode == "parent-exit" {
		os.Exit(0)
	}
	if os.Getenv("DELAY_BIND") == "1" {
		time.Sleep(300 * time.Millisecond)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:"+os.Getenv("PORT"))
	if err != nil {
		os.Exit(22)
	}
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "ok") }), ReadHeaderTimeout: time.Second}
	_ = server.Serve(listener)
	os.Exit(0)
}

type nativeHarness struct {
	t                          *testing.T
	binary, dir, project, pids string
	lease                      string
	cli                        *client.Client
	process                    *exec.Cmd
	waited                     bool
	manifest                   v1.Manifest
	tokens                     map[string]string
	daemonLog                  bytes.Buffer
}

func (h *nativeHarness) start() {
	h.t.Helper()
	h.process = exec.Command(h.binary, "daemon", "serve", "--state-dir", h.dir, "--lease-duration", h.lease)
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
	return v1.RunRequest{APIVersion: v1.Version, Plan: v1.PlanRequest{ProjectPath: h.project, Scene: scene}, Options: v1.ExecutionOptions{StartupTimeout: "3s", JobTimeout: "3s", StopGrace: "50ms"}}
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
	deadline := time.Now().Add(6 * time.Second)
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
	h.t.Fatalf("wanted %v, got %+v execution=%+v", want, last.Instance, last.Instance.Execution)
	return last
}
func (h *nativeHarness) absent() {
	h.t.Helper()
	data, err := os.ReadFile(h.pids)
	if os.IsNotExist(err) {
		return
	}
	if err != nil {
		h.t.Fatal(err)
	}
	for _, text := range strings.Fields(string(data)) {
		pid, err := strconv.Atoi(text)
		if err != nil {
			h.t.Fatal(err)
		}
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			if syscall.Kill(pid, 0) == syscall.ESRCH {
				break
			}
			time.Sleep(20 * time.Millisecond)
		}
		if syscall.Kill(pid, 0) != syscall.ESRCH {
			h.t.Errorf("fixture PID %d survives acknowledged cleanup", pid)
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
		case <-time.After(16 * time.Second):
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
	root, err := os.MkdirTemp("/tmp", "bl-native-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(root); err != nil {
			t.Error(err)
		}
	})
	budget := "2s"
	if len(lease) > 0 {
		budget = lease[0]
	}
	h := &nativeHarness{t: t, binary: binary, lease: budget, dir: filepath.Join(root, "state"), project: filepath.Join(root, "project"), pids: filepath.Join(root, "pids")}
	if err := os.Mkdir(h.project, 0700); err != nil {
		t.Fatal(err)
	}
	literal := func(s string) v1.Value { return v1.Value{Literal: &s} }
	port := v1.Value{Ref: &v1.Reference{Kind: "resource", Name: "port", Field: "port"}}
	command := func(mode string) *v1.Command {
		return &v1.Command{Tool: "fixture", Args: []string{"-test.run=TestNativeFixtureProcess", "--", mode}}
	}
	env := v1.Environment{Assign: map[string]v1.Value{"BACKLOT_NATIVE_FIXTURE": literal("1"), "PORT": port, "PIDFILE": literal(h.pids)}}
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
		journal, err := store.runtime(r.Instance.ID)
		if err != nil {
			t.Fatal(err)
		}
		original := journal.Groups[0]
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
			if len(current.Instance.Execution.Components) == 2 {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		data, err := os.ReadFile(h.pids)
		if err != nil {
			t.Fatal(err)
		}
		pid, _ := strconv.Atoi(strings.Fields(string(data))[0])
		if err := syscall.Kill(pid, syscall.SIGKILL); err != nil {
			t.Fatal(err)
		}
		final := h.state(r.Instance.ID, v1.Failed)
		if final.Instance.Execution.Components[1].ExitCode == nil {
			t.Fatal("cancelled job original status missing")
		}
		h.absent()
	})
	t.Run("guardian-crash-preserves-uncertain-descendants", func(t *testing.T) {
		h := newNativeHarness(t, binary)
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
		parentData, err := exec.Command("/bin/ps", "-o", "ppid=", "-p", strconv.Itoa(pgid)).Output()
		if err != nil {
			t.Fatal(err)
		}
		guardian, err := strconv.Atoi(strings.TrimSpace(string(parentData)))
		if err != nil || guardian <= 1 {
			t.Fatal("fixture guardian unavailable", err)
		}
		if err := syscall.Kill(guardian, syscall.SIGKILL); err != nil {
			t.Fatal(err)
		}
		final := h.state(r.Instance.ID, v1.Failed)
		if final.Instance.Execution.CleanupFailure == "" {
			t.Fatal("guardian loss hidden")
		}
		if syscall.Kill(root, 0) != nil {
			t.Fatal("uncertain descendant killed")
		}
		actual, err := syscall.Getpgid(root)
		if err != nil || actual != pgid {
			t.Fatal("fixture identity changed", err)
		}
		if err := syscall.Kill(-pgid, syscall.SIGKILL); err != nil {
			t.Fatal(err)
		}
		h.absent()
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
	t.Run("selected-unsupported-rejected-before-launch", func(t *testing.T) {
		h := newNativeHarness(t, binary)
		h.manifest.Resources["data"] = v1.Resource{Kind: "directory"}
		scene := h.manifest.Scenes["dev"]
		scene.Resources = append(scene.Resources, "data")
		h.manifest.Scenes["dev"] = scene
		h.write()
		if _, err := h.cli.Run(context.Background(), h.request("dev")); err == nil {
			t.Fatal("non-port resource executed")
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
		data, err := os.ReadFile(h.pids)
		if err != nil {
			t.Fatal(err)
		}
		child, _ := strconv.Atoi(strings.Fields(string(data))[1])
		if err := syscall.Kill(child, syscall.SIGKILL); err != nil {
			t.Fatal(err)
		}
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
		journal, err := store.runtime(r.Instance.ID)
		if err != nil {
			t.Fatal(err)
		}
		original := append([]native.Identity(nil), journal.Groups...)
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
