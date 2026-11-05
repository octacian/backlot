//go:build darwin || linux

package daemon

import (
	"context"
	"encoding/json"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	v1 "github.com/octacian/backlot/api/v1"
)

func enableNativeTasks(h *nativeHarness, modes ...string) {
	h.t.Helper()
	scene := h.manifest.Scenes["dev"]
	for _, mode := range modes {
		c := h.manifest.Components["job"]
		command := *c.Command
		command.Args = v1.StringArguments("-test.run=TestNativeFixtureProcess", "--", mode)
		c.Command = &command
		h.manifest.Components[mode] = c
		scene.Tasks = append(scene.Tasks, mode)
	}
	h.manifest.Scenes["dev"] = scene
	h.write()
}

func runHarnessTask(h *nativeHarness, parent, name string) v1.InstanceResponse {
	h.t.Helper()
	r, err := h.cli.RunTask(context.Background(), v1.TaskRunRequest{APIVersion: v1.Version, Target: v1.SceneTarget{InstanceID: parent}, Task: name, Options: v1.ExecutionOptions{JobTimeout: "10s", StopGrace: "50ms"}})
	if err != nil {
		h.t.Fatal(err)
	}
	if r.Instance.ID == parent || r.Instance.Task == nil || r.LeaseToken == "" {
		h.t.Fatal("task identity/lease absent", r)
	}
	if h.tokens == nil {
		h.tokens = map[string]string{}
	}
	h.tokens[r.Instance.ID] = r.LeaseToken
	return r
}

func TestTaskNativeLifecycle(t *testing.T) {
	binary := buildFixtureBinary(t, t.TempDir(), "../../cmd/backlot")
	t.Run("exits drift legacy join and destroy", func(t *testing.T) {
		h := newNativeHarness(t, binary)
		enableNativeTasks(h, "job", "fail", "never")
		parent := h.state(h.run("dev").Instance.ID, v1.RuntimeReady).Instance
		success := runHarnessTask(h, parent.ID, "job")
		h.state(success.Instance.ID, v1.Succeeded)
		failure := runHarnessTask(h, parent.ID, "fail")
		result := h.state(failure.Instance.ID, v1.Failed).Instance
		if result.Execution.TerminalExitCode == nil || *result.Execution.TerminalExitCode != 23 || result.Execution.Failure != "" || result.Execution.CleanupFailure != "" {
			t.Fatal("lost app exit", result.Execution)
		}
		current := h.state(parent.ID, v1.RuntimeReady).Instance
		if current.Execution.Attempt != parent.Execution.Attempt {
			t.Fatal("task replaced parent")
		}
		c := h.manifest.Components["fail"]
		command := *c.Command
		command.Args = append(slices.Clone(command.Args), v1.StringArguments("changed")...)
		c.Command = &command
		h.manifest.Components["fail"] = c
		h.write()
		if _, err := h.cli.RunTask(context.Background(), v1.TaskRunRequest{APIVersion: v1.Version, Target: v1.SceneTarget{InstanceID: parent.ID}, Task: "job"}); err != nil {
			t.Fatal("unrelated drift refused", err)
		}
		_, err := h.cli.RunTask(context.Background(), v1.TaskRunRequest{APIVersion: v1.Version, Target: v1.SceneTarget{InstanceID: parent.ID}, Task: "fail"})
		code(t, err, "restart_required")
		h.manifest.Components["newtask"] = h.manifest.Components["job"]
		scene := h.manifest.Scenes["dev"]
		scene.Tasks = append(scene.Tasks, "newtask")
		h.manifest.Scenes["dev"] = scene
		h.write()
		joined := h.run("dev")
		if joined.Instance.Execution.Attempt != parent.Execution.Attempt {
			t.Fatal("join replaced parent")
		}
		_, err = h.cli.RunTask(context.Background(), v1.TaskRunRequest{APIVersion: v1.Version, Target: v1.SceneTarget{InstanceID: parent.ID}, Task: "newtask"})
		code(t, err, "restart_required")
		running := runHarnessTask(h, parent.ID, "never")
		if _, err := h.cli.Renew(context.Background(), running.Instance.ID, running.LeaseToken); err != nil {
			t.Fatal(err)
		}
		if _, err := h.cli.StopExecution(context.Background(), running.Instance.ID); err != nil {
			t.Fatal(err)
		}
		h.state(parent.ID, v1.RuntimeReady)
		another := runHarnessTask(h, parent.ID, "never")
		if _, err := h.cli.Destroy(context.Background(), parent.ID); err != nil {
			t.Fatal(err)
		}
		for _, id := range []string{success.Instance.ID, failure.Instance.ID, running.Instance.ID, another.Instance.ID} {
			r, err := h.cli.Inspect(context.Background(), id)
			if err != nil || r.Instance.Status != v1.Destroyed || r.Instance.Execution != nil {
				t.Fatal("dependent diagnosis not erased", err, r.Instance)
			}
		}
	})
	t.Run("exact ID refuses replacement checkout", func(t *testing.T) {
		h := newNativeHarness(t, binary)
		enableNativeTasks(h, "job")
		parent := h.state(h.run("dev").Instance.ID, v1.RuntimeReady).Instance
		success := runHarnessTask(h, parent.ID, "job")
		h.state(success.Instance.ID, v1.Succeeded)
		original := h.project + "-original"
		if err := os.Rename(h.project, original); err != nil {
			t.Fatal(err)
		}
		// Restore the accepted directory before the harness joins its owned parent.
		t.Cleanup(func() {
			if err := os.RemoveAll(h.project); err != nil {
				t.Error(err)
			}
			if err := os.Rename(original, h.project); err != nil {
				t.Error(err)
			}
		})
		if err := os.Mkdir(h.project, 0700); err != nil {
			t.Fatal(err)
		}
		h.write()
		before, err := h.cli.List(context.Background(), v1.ListRequest{APIVersion: v1.Version})
		if err != nil {
			t.Fatal(err)
		}
		command := exec.Command(binary, "task", "run", "job", "--instance", parent.ID, "--state-dir", h.dir, "--json")
		data, err := command.Output()
		if err == nil || !strings.Contains(string(data), `"code":"checkout_changed"`) {
			t.Fatalf("replacement checkout accepted: %v %s", err, data)
		}
		after, err := h.cli.List(context.Background(), v1.ListRequest{APIVersion: v1.Version})
		if err != nil || len(before.Instances) != len(after.Instances) {
			t.Fatal("task admitted on replacement", err)
		}
		h.state(parent.ID, v1.RuntimeReady)
	})
	t.Run("lease expiry leaves parent ready", func(t *testing.T) {
		h := newNativeHarness(t, binary, "500ms")
		enableNativeTasks(h, "never")
		parent := h.state(h.run("dev").Instance.ID, v1.RuntimeReady).Instance
		task := runHarnessTask(h, parent.ID, "never")
		delete(h.tokens, task.Instance.ID) // Exercise actual client loss rather than renewing this lease.
		h.state(task.Instance.ID, v1.Cancelled)
		h.state(parent.ID, v1.RuntimeReady)
	})
	t.Run("outputs immediate conflicts and disjoint concurrency", func(t *testing.T) {
		h := newNativeHarness(t, binary)
		enableNativeTasks(h, "never", "job", "fail")
		h.manifest.Outputs = map[string]v1.Output{"held": {Path: "held", ConcurrencyGroup: "parent"}, "one": {Path: "one", ConcurrencyGroup: "one"}, "two": {Path: "two", ConcurrencyGroup: "two"}}
		server := h.manifest.Components["server"]
		server.Outputs = []string{"held"}
		h.manifest.Components["server"] = server
		for name, output := range map[string]string{"never": "one", "job": "one", "fail": "two"} {
			c := h.manifest.Components[name]
			c.Outputs = []string{output}
			h.manifest.Components[name] = c
		}
		h.write()
		parent := h.state(h.run("dev").Instance.ID, v1.RuntimeReady).Instance
		one := runHarnessTask(h, parent.ID, "never")
		started := time.Now()
		_, err := h.cli.RunTask(context.Background(), v1.TaskRunRequest{APIVersion: v1.Version, Target: v1.SceneTarget{InstanceID: parent.ID}, Task: "job"})
		code(t, err, "output_conflict")
		if time.Since(started) > time.Second {
			t.Fatal("conflict queued")
		}
		two := runHarnessTask(h, parent.ID, "fail")
		h.state(two.Instance.ID, v1.Failed)
		if _, err := h.cli.StopExecution(context.Background(), parent.ID); err != nil {
			t.Fatal(err)
		}
		h.state(one.Instance.ID, v1.Cancelled)
		h.absent()
	})
	t.Run("CLI literal arguments exact nonzero", func(t *testing.T) {
		h := newNativeHarness(t, binary)
		enableNativeTasks(h, "fail")
		parent := h.state(h.run("dev").Instance.ID, v1.RuntimeReady).Instance
		command := exec.Command(binary, "task", "run", "fail", "--instance", parent.ID, "--state-dir", h.dir, "--json", "--", "two words", "--scene", "literal")
		data, err := command.Output()
		if err == nil || !strings.Contains(err.Error(), "exit status 23") {
			t.Fatalf("CLI exit %v: %s", err, data)
		}
		var r v1.InstanceResponse
		if json.Unmarshal(data, &r) != nil || r.Instance.Execution == nil || r.Instance.Execution.TerminalExitCode == nil || *r.Instance.Execution.TerminalExitCode != 23 {
			t.Fatal("CLI result", string(data))
		}
		args := r.Instance.Plan.Components[0].Command.Args
		n := len(args)
		if n < 3 || args[n-3].Literal == nil || *args[n-3].Literal != "two words" || *args[n-2].Literal != "--scene" || *args[n-1].Literal != "literal" {
			t.Fatal("literal argv altered", args)
		}
		h.state(parent.ID, v1.RuntimeReady)
	})
	t.Run("shutdown and recovery never resume", func(t *testing.T) {
		h := newNativeHarness(t, binary)
		enableNativeTasks(h, "never")
		parent := h.state(h.run("dev").Instance.ID, v1.RuntimeReady).Instance
		child := runHarnessTask(h, parent.ID, "never")
		// Authenticated shutdown joins both owners before closing the store.
		if _, err := h.cli.Stop(context.Background()); err != nil {
			t.Fatal(err)
		}
		if err := h.process.Wait(); err != nil {
			t.Fatal(err)
		}
		h.waited = true
		h.start()
		p, err := h.cli.Inspect(context.Background(), parent.ID)
		if err != nil || p.Instance.Status == v1.RuntimeReady {
			t.Fatal("parent resumed", err)
		}
		c, err := h.cli.Inspect(context.Background(), child.Instance.ID)
		if err != nil || active(c.Instance.Status) {
			t.Fatal("task resumed", err)
		}
		if _, err := os.Stat(filepath.Join(h.dir, "state.db")); err != nil {
			t.Fatal(err)
		}
	})
}

func TestTaskNativeAcceptedCredentialBoundary(t *testing.T) {
	binary := buildFixtureBinary(t, t.TempDir(), "../../cmd/backlot")
	h := newNativeHarness(t, binary)
	if err := os.Mkdir(filepath.Join(h.project, "package"), 0700); err != nil {
		t.Fatal(err)
	}
	cwd, err := filepath.EvalSymlinks(filepath.Join(h.project, "package"))
	if err != nil {
		t.Fatal(err)
	}
	h.manifest.Resources["token"] = v1.Resource{Kind: "secret"}
	token := v1.Value{Ref: &v1.Reference{Kind: "resource", Name: "token", Field: "value"}}
	server := h.manifest.Components["server"]
	server.Resources = append(slices.Clone(server.Resources), "token")
	server.Environment.Assign = maps.Clone(server.Environment.Assign)
	server.Environment.Assign["TOKEN"] = token
	h.manifest.Components["server"] = server
	task := h.manifest.Components["job"]
	task.Resources = append(slices.Clone(task.Resources), "token")
	task.Environment.Assign = maps.Clone(task.Environment.Assign)
	task.Environment.Assign["TOKEN"] = token
	task.Environment.Assign["EXPECTED_CWD"] = v1.Value{Literal: &cwd}
	task.Environment.Assign["PARENT"] = v1.Value{Ref: &v1.Reference{Kind: "instance", Field: "id"}}
	task.Environment.Assign["URL"] = v1.Value{Format: &v1.FormatValue{Template: "http://%s", Values: []v1.Value{{HostPort: &v1.HostPortValue{Host: v1.Value{Ref: &v1.Reference{Kind: "service", Name: "server", Field: "host"}}, Port: v1.Value{Ref: &v1.Reference{Kind: "service", Name: "server", Field: "port", Port: "http"}}}}}}}
	task.Command = &v1.Command{Tool: "fixture", WorkingDirectory: "package", Args: append(v1.StringArguments("-test.run=TestNativeFixtureProcess", "--", "task-auth"), v1.Argument{Value: v1.Value{Ref: &v1.Reference{Kind: "instance", Field: "id"}}})}
	h.manifest.Components["admin"] = task
	scene := h.manifest.Scenes["dev"]
	scene.Resources = append(scene.Resources, "token")
	scene.Tasks = []string{"admin"}
	h.manifest.Scenes["dev"] = scene
	testScene := h.manifest.Scenes["test"]
	testScene.Resources = append(testScene.Resources, "token")
	h.manifest.Scenes["test"] = testScene
	h.write()
	parent := h.state(h.run("dev").Instance.ID, v1.RuntimeReady).Instance
	for _, args := range [][]string{{"two words", "--literal"}, {"fail"}} {
		r, err := h.cli.RunTask(context.Background(), v1.TaskRunRequest{APIVersion: v1.Version, Target: v1.SceneTarget{InstanceID: parent.ID}, Task: "admin", Args: args, Options: v1.ExecutionOptions{StopGrace: "50ms"}})
		if err != nil {
			t.Fatal(err)
		}
		if h.tokens == nil {
			h.tokens = map[string]string{}
		}
		h.tokens[r.Instance.ID] = r.LeaseToken
		want := v1.Succeeded
		if args[0] == "fail" {
			want = v1.Failed
		}
		final := h.state(r.Instance.ID, want).Instance
		if final.Execution.TerminalExitCode == nil || args[0] == "fail" && *final.Execution.TerminalExitCode != 23 {
			t.Fatal("app exit", final.Execution)
		}
		logs, err := h.cli.Logs(context.Background(), v1.LogsRequest{APIVersion: v1.Version, InstanceID: r.Instance.ID})
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, record := range logs.Records {
			if strings.Contains(record.Message, "authenticated task boundary") {
				found = true
				if record.InstanceID != r.Instance.ID || record.Attempt != final.Execution.Attempt {
					t.Fatal("log identity", record)
				}
			}
		}
		if !found {
			t.Fatal("app authentication did not run", logs)
		}
		h.state(parent.ID, v1.RuntimeReady)
	}
}

func TestTaskParentMutationFences(t *testing.T) {
	binary := buildFixtureBinary(t, t.TempDir(), "../../cmd/backlot")
	for _, action := range []string{"restart", "reset", "attached-cancel", "parent-failure", "crash-recovery"} {
		t.Run(action, func(t *testing.T) {
			h := newNativeHarness(t, binary, "10s")
			enableNativeTasks(h, "never")
			request := h.request("dev")
			request.Attached = action == "attached-cancel"
			started, err := h.cli.Run(context.Background(), request)
			if err != nil {
				t.Fatal(err)
			}
			parent := h.state(started.Instance.ID, v1.RuntimeReady).Instance
			task := runHarnessTask(h, parent.ID, "never")
			// Observe the application effect before cancellation or fault injection.
			deadline := time.Now().Add(5 * time.Second)
			launched := false
			for time.Now().Before(deadline) {
				r, err := h.cli.Inspect(context.Background(), task.Instance.ID)
				if err != nil {
					t.Fatal(err)
				}
				if len(r.Instance.Execution.Components) == 1 && r.Instance.Execution.Components[0].Status == "running" {
					launched = true
					break
				}
				time.Sleep(20 * time.Millisecond)
			}
			if !launched {
				t.Fatal("task never launched")
			}
			switch action {
			case "restart", "reset":
				request.InstanceID = parent.ID
				var response v1.InstanceResponse
				if action == "reset" {
					response, err = h.cli.Reset(context.Background(), request)
				} else {
					response, err = h.cli.Restart(context.Background(), request)
				}
				if err != nil {
					t.Fatal(err)
				}
				next := h.state(response.Instance.ID, v1.RuntimeReady).Instance
				if next.Execution.Attempt == parent.Execution.Attempt {
					t.Fatal("parent not replaced")
				}
				h.state(task.Instance.ID, v1.Cancelled)
			case "attached-cancel":
				if _, err := h.cli.Attached(context.Background(), v1.AttachedRequest{APIVersion: v1.Version, InstanceID: parent.ID, Owner: started.Attachment.Owner}, true); err != nil {
					t.Fatal(err)
				}
				h.state(task.Instance.ID, v1.Cancelled)
				h.state(parent.ID, v1.Stopped)
			case "parent-failure":
				// The fixture authenticates and acts on its own group, never an observed PID.
				h.fixtureAction("kill")
				h.state(parent.ID, v1.Failed)
				h.state(task.Instance.ID, v1.Cancelled)
			case "crash-recovery":
				h.retainGuardianCleanup(parent.ID)
				h.retainGuardianCleanup(task.Instance.ID)
				if err := h.process.Process.Kill(); err != nil {
					t.Fatal(err)
				}
				_ = h.process.Wait()
				h.waited = true
				h.start()
				h.state(task.Instance.ID, v1.Interrupted)
				h.state(parent.ID, v1.Interrupted)
			}
		})
	}
}
