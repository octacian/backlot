//go:build darwin || linux

package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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

	t.Run("private-inputs-and-recorded-cleanup", func(t *testing.T) {
		t.Setenv("BACKLOT_PROJECT_CONFIG_DIR", filepath.Join(shortTemp(t), "registry"))
		h := newNativeHarness(t, binary)
		h.manifest.Inputs = map[string]v1.Input{"token": {Secret: true}}
		server := h.manifest.Components["server"]
		server.Environment.Assign["FIXTURE_TOKEN"] = v1.Value{Ref: &v1.Reference{Kind: "input", Name: "token"}}
		h.manifest.Components["server"] = server
		h.write()
		config := filepath.Join(h.project, "machine.json")
		start := func() ([]byte, error) {
			command := exec.Command(binary, "scene", "start", "--state-dir", h.dir, "--config", config, "--stop-grace", "50ms", "--json", "dev")
			command.Dir = h.project
			return command.CombinedOutput()
		}
		// Neither an old machine value nor missing private input may launch work.
		if err := os.WriteFile(config, []byte(`{"version":"backlot/v1","inputs":{}}`), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := start(); err == nil {
			t.Fatal("named start accepted machine inputs")
		}
		if err := os.WriteFile(config, []byte(`{"version":"backlot/v1"}`), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := start(); err == nil {
			t.Fatal("named start accepted missing private input")
		}
		listed, err := h.cli.List(context.Background(), v1.ListRequest{APIVersion: v1.Version})
		if err != nil || len(listed.Instances) != 0 {
			t.Fatal("rejected named start accepted work", err)
		}
		// Use the real noninteractive CLI transport; the value never enters argv.
		payload, err := json.Marshal(v1.PrivateConfig{Version: v1.ManifestVersion, Inputs: map[string]string{"token": h.controlToken}})
		if err != nil {
			t.Fatal(err)
		}
		setup := exec.Command(binary, "project", "setup", "--project", h.project, "--config", config, "--from", "-", "--json")
		setup.Stdin = bytes.NewReader(payload)
		out, err := setup.CombinedOutput()
		if err != nil {
			t.Fatal("private setup failed", err)
		}
		var configured v1.ProjectSetupResponse
		if err := json.Unmarshal(out, &configured); err != nil {
			t.Fatal(err)
		}
		out, err = start()
		if err != nil {
			t.Fatal("private named start failed", err)
		}
		if strings.Contains(string(out), h.controlToken) {
			t.Fatal("public named response leaked private input")
		}
		var started v1.SceneResponse
		if err := json.Unmarshal(out, &started); err != nil || started.Result == nil || started.Result.Instance.Status != v1.RuntimeReady {
			t.Fatal("named start did not become ready", err)
		}
		id := started.Result.Instance.ID
		// Authenticated fixture control requires the supplied input at runtime.
		h.fixtureAction("root-crash")
		h.state(id, v1.Failed)
		h.absent()
		restart := h.request("dev")
		restart.InstanceID, restart.Plan.ConfigPath = id, config
		if _, err := h.cli.Restart(context.Background(), restart); err != nil {
			t.Fatal("private explicit recovery failed", err)
		}
		h.state(id, v1.RuntimeReady)
		if err := os.WriteFile(configured.PrivatePath, []byte("broken"), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(h.project, "backlot.json"), []byte("broken"), 0600); err != nil {
			t.Fatal(err)
		}
		for _, action := range []string{"status", "stop"} {
			command := exec.Command(binary, "scene", action, "--state-dir", h.dir, "--json", "dev")
			command.Dir = h.project
			out, err := command.CombinedOutput()
			if err != nil {
				t.Fatal("recorded named operation required current files", action, err)
			}
			var response v1.SceneResponse
			if err := json.Unmarshal(out, &response); err != nil || response.Result == nil || response.Result.Instance.ID != id {
				t.Fatal("recorded named identity changed", err)
			}
			if strings.Contains(string(out), h.controlToken) {
				t.Fatal("recorded response leaked private input")
			}
		}
		h.absent()
	})
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
	for _, mode := range []string{"named", "id"} {
		for _, sig := range []os.Signal{os.Interrupt, syscall.SIGTERM} {
			args := []string{"scene", "status", "--state-dir", h.dir, "--json", "--follow", "--interval", "100ms", "--duration", "10s", "dev"}
			if mode == "id" {
				args = []string{"status", "--state-dir", h.dir, "--json", "--follow", "--interval", "100ms", "--duration", "10s", id}
			}
			observer := exec.Command(binary, args...)
			observer.Dir = h.project
			if mode == "id" {
				observer.Dir = t.TempDir() // Exact IDs need no checkout or manifest.
			}
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

func TestStartupDetailsCLI(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "backlot")
	if out, err := exec.Command("go", "build", "-race", "-o", binary, "../../cmd/backlot").CombinedOutput(); err != nil {
		t.Fatal(err, string(out))
	}
	t.Setenv("BACKLOT_PROJECT_CONFIG_DIR", filepath.Join(shortTemp(t), "registry"))
	h := newNativeHarness(t, binary)
	h.manifest.Inputs = map[string]v1.Input{"token": {Secret: true}}
	h.manifest.Resources["password"] = v1.Resource{Kind: "secret"}
	server := h.manifest.Components["server"]
	server.Resources = append(server.Resources, "password")
	server.Fixtures = map[string]v1.Fixture{
		"reminder":   {Value: v1.Value{Literal: ptr("original reminder")}},
		"input":      {Value: v1.Value{Ref: &v1.Reference{Kind: "input", Name: "token"}}},
		"generated":  {Value: v1.Value{Ref: &v1.Reference{Kind: "resource", Name: "password", Field: "value"}}},
		"literal":    {Value: v1.Value{Literal: ptr("literal-private-canary"), Secret: true}},
		"unselected": {Value: v1.Value{Literal: ptr("never-selected-canary"), Secret: true}},
	}
	h.manifest.Components["server"] = server
	scene := h.manifest.Scenes["dev"]
	scene.Resources = append(scene.Resources, "password")
	for _, name := range []string{"reminder", "input", "generated", "literal"} {
		scene.StartupDetails = append(scene.StartupDetails, v1.StartupDetailSelection{Component: "server", Fixture: name, Label: name})
	}
	h.manifest.Scenes["dev"] = scene
	disposable := h.manifest.Scenes["test"]
	disposable.Resources = append(disposable.Resources, "password")
	h.manifest.Scenes["test"] = disposable
	h.write()
	config := filepath.Join(h.project, "machine.json")
	if err := os.WriteFile(config, []byte(`{"version":"backlot/v1"}`), 0600); err != nil {
		t.Fatal(err)
	}
	execute := func(args ...string) ([]byte, error) {
		t.Helper()
		command := exec.Command(binary, args...)
		command.Dir = h.project
		var out, stderr bytes.Buffer
		command.Stdout = &out
		command.Stderr = &stderr
		err := command.Run()
		return out.Bytes(), err
	}
	configure := func(token string) {
		t.Helper()
		payload, err := json.Marshal(v1.PrivateConfig{Version: v1.ManifestVersion, Inputs: map[string]string{"token": token}})
		if err != nil {
			t.Fatal(err)
		}
		setup := exec.Command(binary, "project", "setup", "--project", h.project, "--config", config, "--from", "-", "--json")
		setup.Stdin = bytes.NewReader(payload)
		if _, err := setup.CombinedOutput(); err != nil {
			t.Fatal("private setup", err)
		}
	}
	configure("input-private-canary")
	read := func(args ...string) v1.InstanceResponse {
		t.Helper()
		out, err := execute(args...)
		if err != nil {
			t.Fatal("startup CLI failed", args[0], err)
		}
		var response v1.InstanceResponse
		if args[0] == "scene" {
			var named v1.SceneResponse
			if err := json.Unmarshal(out, &named); err != nil || named.Result == nil {
				t.Fatal("scene envelope", err)
			}
			response = *named.Result
		} else if err := json.Unmarshal(out, &response); err != nil {
			t.Fatal("finite JSON envelope", err)
		}
		if response.StartupDetails == nil || response.StartupDetails.Warning != nil || len(response.StartupDetails.Details) != 4 {
			t.Fatal("missing authorized summary")
		}
		if strings.Contains(string(out), "never-selected-canary") {
			t.Fatal("unselected credential exposed")
		}
		return response
	}
	runArgs := []string{"run", "dev", "--config", config, "--state-dir", h.dir, "--json", "--stop-grace", "50ms"}
	first := read(runArgs...)
	id := first.Instance.ID
	generated := first.StartupDetails.Details[2].Value
	if first.StartupDetails.Details[0].Value != "original reminder" || first.StartupDetails.Details[1].Value != "input-private-canary" || generated == "" || !first.StartupDetails.Details[2].Sensitive || first.StartupDetails.Details[3].Value != "literal-private-canary" {
		t.Fatal("fixture resolution or order changed")
	}
	// Changing current selection/value must not silently adopt it on repeated start.
	scene.StartupDetails[0].Label = "changed label"
	h.manifest.Scenes["dev"] = scene
	server.Fixtures["reminder"] = v1.Fixture{Value: v1.Value{Literal: ptr("changed reminder")}}
	h.manifest.Components["server"] = server
	h.write()
	configure("changed-input-canary")
	repeated := read(runArgs...)
	if !repeated.ManifestDrift || repeated.StartupDetails.Details[0].Label != "reminder" || repeated.StartupDetails.Details[0].Value != "original reminder" || repeated.StartupDetails.Details[2].Value != generated || repeated.StartupDetails.Details[1].Value != "input-private-canary" {
		t.Fatal("repeated start used unadopted values")
	}
	named := read("scene", "start", "--config", config, "--state-dir", h.dir, "--json", "dev")
	if named.Instance.ID != id || named.StartupDetails.Details[2].Value != generated {
		t.Fatal("named repeat lost retained values")
	}
	for _, action := range []string{"status", "inspect", "fixtures"} {
		out, err := execute(action, "--state-dir", h.dir, "--json", id)
		if err != nil {
			t.Fatal(action, err)
		}
		for _, secret := range []string{generated, "input-private-canary", "literal-private-canary", "never-selected-canary"} {
			if strings.Contains(string(out), secret) {
				t.Fatal("ordinary output leaked credentials", action)
			}
		}
		if strings.Contains(string(out), `"startup_details":{"attempt"`) {
			t.Fatal("startup-only result persisted")
		}
	}
	if _, err := execute("restart", id, "--state-dir", h.dir, "--json"); err == nil {
		t.Fatal("restart accepted changed sensitive input")
	}
	configure("input-private-canary")
	restarted := read("restart", id, "--state-dir", h.dir, "--json", "--stop-grace", "50ms")
	if restarted.StartupDetails.Details[2].Value != generated || restarted.StartupDetails.Details[0].Value != "changed reminder" {
		t.Fatal("restart did not preserve generation/adopt ordinary drift")
	}
	stale := v1.FixturesRequest{APIVersion: v1.Version, InstanceID: id, ExpectedAttempt: first.Instance.Execution.Attempt, Component: "server", SecretName: "generated"}
	if _, err := h.cli.FixtureSecret(context.Background(), stale); err == nil {
		t.Fatal("replaced attempt retrieved current secret")
	}
	reset := read("reset", id, "--state-dir", h.dir, "--json", "--stop-grace", "50ms")
	if reset.StartupDetails.Details[2].Value == generated {
		t.Fatal("reset retained generated credential")
	}
	if _, err := h.cli.StopExecution(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	retained := read(runArgs...)
	if retained.StartupDetails.Details[2].Value != reset.StartupDetails.Details[2].Value {
		t.Fatal("stop/run lost retained actual value")
	}
	if _, err := h.cli.StopExecution(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	// A new failed startup must not hydrate details or claim readiness.
	server.Command.Args = []string{"-test.run=TestNativeFixtureProcess", "--", "fail"}
	h.manifest.Components["server"] = server
	h.write()
	out, err := execute("restart", id, "--state-dir", h.dir, "--json", "--stop-grace", "50ms")
	var failed v1.InstanceResponse
	if err == nil || json.Unmarshal(out, &failed) != nil || failed.Instance.ID != id || failed.Instance.Status != v1.Failed || failed.Instance.Execution == nil || failed.Instance.Execution.Failure == "" || failed.StartupDetails != nil {
		t.Fatal("failed readiness emitted startup summary")
	}
}
