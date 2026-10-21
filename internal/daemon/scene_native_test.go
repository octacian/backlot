//go:build darwin || linux

package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	v1 "github.com/octacian/backlot/api/v1"
)

func TestSceneNativeCLI(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "backlot")
	if out, err := exec.Command("go", "build", "-race", "-o", binary, "../../cmd/backlot").CombinedOutput(); err != nil {
		t.Fatal(err, string(out))
	}

	t.Run("location-collision-before-restart", func(t *testing.T) {
		h := newNativeHarness(t, binary)
		git(t, filepath.Dir(h.project), "init", "-q")
		other := filepath.Join(filepath.Dir(h.project), "other-project")
		if err := os.Mkdir(other, 0700); err != nil {
			t.Fatal(err)
		}
		data, err := json.Marshal(h.manifest)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(other, "backlot.json"), data, 0600); err != nil {
			t.Fatal(err)
		}
		running := h.run("dev")
		h.state(running.Instance.ID, v1.RuntimeReady)
		if _, err := h.cli.StopExecution(context.Background(), running.Instance.ID); err != nil {
			t.Fatal(err)
		}
		stopped := h.state(running.Instance.ID, v1.Stopped)
		h.absent()
		command := exec.Command(binary, "scene", "start", "--state-dir", h.dir, "--project", other, "--json", "dev")
		command.Dir = other
		var stdout, stderr bytes.Buffer
		command.Stdout = &stdout
		command.Stderr = &stderr
		if err := command.Run(); err == nil {
			t.Fatal("named start reused another location", stdout.String())
		}
		var failure v1.ErrorResponse
		if err := json.Unmarshal(stdout.Bytes(), &failure); err != nil || failure.Error.Code != "conflict" {
			t.Fatal(err, stdout.String(), stderr.String())
		}
		current := h.state(running.Instance.ID, v1.Stopped)
		if current.Instance.Execution.Attempt != stopped.Instance.Execution.Attempt {
			t.Fatal("conflicting start restarted another location")
		}
		h.absent()
	})
	t.Run("cancel-start", func(t *testing.T) {
		h := newNativeHarness(t, binary)
		server := h.manifest.Components["server"]
		server.Environment = cloneFixtureEnvironment(server.Environment)
		server.Environment.Assign["DELAY_BIND"] = v1.Value{Literal: ptr("1")}
		h.manifest.Components["server"] = server
		h.write()
		command := exec.Command(binary, "scene", "start", "--state-dir", h.dir, "--stop-grace", "50ms", "--json", "dev")
		command.Dir = h.project
		var stdout, stderr bytes.Buffer
		command.Stdout = &stdout
		command.Stderr = &stderr
		if err := command.Start(); err != nil {
			t.Fatal(err)
		}
		joined := false
		t.Cleanup(func() {
			if !joined {
				_ = command.Process.Kill()
				_ = command.Wait()
			}
		})
		deadline := time.Now().Add(5 * time.Second)
		accepted := false
		for time.Now().Before(deadline) {
			listed, err := h.cli.List(context.Background(), v1.ListRequest{APIVersion: v1.Version})
			if err != nil {
				t.Fatal(err)
			}
			if len(listed.Instances) > 0 {
				accepted = true
				break
			}
			time.Sleep(5 * time.Millisecond)
		}
		if !accepted {
			t.Fatal("startup not accepted")
		}
		if err := command.Process.Signal(syscall.SIGTERM); err != nil {
			t.Fatal(err)
		}
		err := command.Wait()
		joined = true
		exit, ok := err.(*exec.ExitError)
		if !ok || exit.ExitCode() != 143 {
			t.Fatal(err, stdout.String(), stderr.String())
		}
		var response v1.SceneResponse
		if err := json.Unmarshal(stdout.Bytes(), &response); err != nil || response.Result == nil || response.Result.Instance.Status != v1.Stopped || response.Checkout == nil {
			t.Fatal(err, stdout.String())
		}
		h.absent()
	})
	h := newNativeHarness(t, binary)
	invoke := func(action string, args ...string) v1.SceneResponse {
		t.Helper()
		argv := append([]string{"scene", action, "--state-dir", h.dir, "--json"}, args...)
		command := exec.Command(binary, argv...)
		command.Dir = h.project
		out, err := command.CombinedOutput()
		if err != nil {
			t.Fatal(err, string(out))
		}
		var response v1.SceneResponse
		if err := json.Unmarshal(out, &response); err != nil {
			t.Fatal(err, string(out))
		}
		return response
	}
	response := invoke("start", "--stop-grace", "50ms", "dev")
	if response.Result.Instance.Status != v1.RuntimeReady || response.Checkout == nil {
		t.Fatal(response)
	}
	id := response.Result.Instance.ID
	peer := h.run("dev")
	if peer.Instance.ID != id {
		t.Fatal("old run and new scene start diverged")
	}
	// Signals terminate only the observer, never the native scene.
	for _, sig := range []os.Signal{os.Interrupt, syscall.SIGTERM} {
		observer := exec.Command(binary, "scene", "status", "--state-dir", h.dir, "--json", "--follow", "--interval", "100ms", "--duration", "10s", "dev")
		observer.Dir = h.project
		stdout, err := observer.StdoutPipe()
		if err != nil {
			t.Fatal(err)
		}
		if err := observer.Start(); err != nil {
			t.Fatal(err)
		}
		// Retain direct-child ownership and join even if a later assertion fails.
		joined := false
		t.Cleanup(func() {
			if !joined {
				_ = observer.Process.Kill()
				_ = observer.Wait()
			}
		})
		decoder := json.NewDecoder(stdout)
		var record v1.SceneFollowRecord
		if err := decoder.Decode(&record); err != nil || record.Type != "snapshot" {
			t.Fatal(err, record)
		}
		if err := observer.Process.Signal(sig); err != nil {
			t.Fatal(err)
		}
		if err := decoder.Decode(&record); err != nil || record.Type != "complete" || record.Reason != "cancelled" {
			t.Fatal(err, record)
		}
		err = observer.Wait()
		joined = true
		exit, ok := err.(*exec.ExitError)
		want := 130
		if sig == syscall.SIGTERM {
			want = 143
		}
		if !ok || exit.ExitCode() != want {
			t.Fatal(err)
		}
		h.state(id, v1.RuntimeReady)
	}
	h.fixtureAction("root-crash")
	failed := h.state(id, v1.Failed)
	observed := invoke("status", "dev")
	if observed.Result.Instance.Status != v1.Failed || observed.Result.Instance.Execution.Failure == "" {
		t.Fatal(observed, failed)
	}
	h.absent()
	request := h.request("dev")
	request.InstanceID = id
	if _, err := h.cli.Restart(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	h.state(id, v1.RuntimeReady)
	if err := os.WriteFile(filepath.Join(h.project, "backlot.json"), []byte("broken"), 0600); err != nil {
		t.Fatal(err)
	}
	stopped := invoke("stop", "dev")
	if stopped.Outcome != v1.SceneStopped {
		t.Fatal(stopped)
	}
	h.absent()
	if err := os.Remove(filepath.Join(h.project, "backlot.json")); err != nil {
		t.Fatal(err)
	}
	if stopped := invoke("stop", "dev"); stopped.Outcome != v1.SceneAlreadyStopped {
		t.Fatal(stopped)
	}
	if missing := invoke("status", "absent"); missing.Outcome != v1.SceneNotFound {
		t.Fatal(missing)
	}
}
