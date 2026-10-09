//go:build darwin || linux

package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	v1 "github.com/octacian/backlot/api/v1"
)

func TestDisposableEvidenceNative(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "backlot")
	build := exec.Command("go", "build", "-race", "-o", binary, "../../cmd/backlot")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v %s", err, output)
	}
	for _, mode := range []string{"success", "job-failure", "collection-failure", "cancellation", "client-loss", "shutdown", "crash"} {
		t.Run(mode, func(t *testing.T) {
			lease := "2s"
			if mode == "client-loss" {
				lease = "500ms"
			}
			h := newNativeHarness(t, binary, lease)
			h.manifest.Tools["sh"] = "/bin/sh"
			h.manifest.Resources["data"] = v1.Resource{Kind: "directory"}
			h.manifest.Resources["password"] = v1.Resource{Kind: "secret"}
			scene := h.manifest.Scenes["test"]
			scene.Resources = append(scene.Resources, "data", "password")
			h.manifest.Scenes["test"] = scene
			job := h.manifest.Components["job"]
			job.Resources = append(job.Resources, "data", "password")
			script := "mkdir -p evidence; printf '%s\\n' \"$@\" > evidence/report; echo job-stdout; echo job-stderr >&2"
			if mode == "job-failure" {
				script += "; exit 23"
			}
			if mode == "cancellation" || mode == "client-loss" || mode == "shutdown" || mode == "crash" {
				script += "; exec sleep 60"
			}
			job.Command = &v1.Command{Tool: "sh", Args: []string{"-c", script, "job"}}
			job.Artifacts = map[string]v1.ArtifactSource{"report": {Path: "evidence/report"}}
			if mode == "collection-failure" {
				job.Artifacts["missing"] = v1.ArtifactSource{Path: "missing-report"}
			}
			user := "fixture-user"
			job.Fixtures = map[string]v1.Fixture{"user": {Value: v1.Value{Literal: &user}}, "password": {Value: v1.Value{Ref: &v1.Reference{Kind: "resource", Name: "password", Field: "value"}}}, "data": {Value: v1.Value{Ref: &v1.Reference{Kind: "resource", Name: "data", Field: "path"}}}}
			h.manifest.Components["job"] = job
			h.write()
			request := h.request("test")
			request.Plan.TerminalArgs = []string{"argument with spaces", "--selector"}
			request.Options.KeepOnFailure = mode != "success" && mode != "shutdown" && mode != "crash"
			r, err := h.cli.Run(context.Background(), request)
			if err != nil {
				t.Fatal(err)
			}
			id := r.Instance.ID
			waitComponentLog(t, h, id, "job", "job-stdout")
			if mode == "cancellation" {
				if _, err := h.cli.StopExecution(context.Background(), id); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "shutdown" || mode == "crash" {
				if mode == "crash" {
					if err := h.process.Process.Kill(); err != nil {
						t.Fatal(err)
					}
				} else {
					if _, err := h.cli.Stop(context.Background()); err != nil {
						t.Fatal(err)
					}
				}
				waitErr := h.process.Wait()
				h.waited = true
				if mode == "shutdown" && waitErr != nil {
					t.Fatal(waitErr)
				}
				h.start()
			}
			want := v1.Succeeded
			switch mode {
			case "job-failure", "collection-failure":
				want = v1.Failed
			case "cancellation", "client-loss", "shutdown":
				want = v1.Cancelled
			case "crash":
				want = v1.Interrupted
			}
			final := h.state(id, want).Instance
			result := final.Execution
			if result == nil {
				t.Fatal("missing result")
			}
			if mode == "job-failure" && (result.TerminalExitCode == nil || *result.TerminalExitCode != 23 || result.Failure != "") {
				t.Fatalf("job status lost: %+v", result)
			}
			if mode == "collection-failure" && (result.CollectionFailure == "" || result.TerminalExitCode == nil || *result.TerminalExitCode != 0) {
				t.Fatalf("collection failure lost: %+v", result)
			}
			if mode == "cancellation" || mode == "client-loss" || mode == "shutdown" {
				if !result.Cancelled {
					t.Fatalf("cancellation lost: %+v", result)
				}
			}
			if mode == "crash" && result.CollectionFailure == "" {
				t.Fatal("crash gap omitted")
			}
			kept := request.Options.KeepOnFailure
			if result.Kept != kept {
				t.Fatalf("kept=%v want %v: %+v", result.Kept, kept, result)
			}
			if kept {
				connection, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", result.Ports["port"]), time.Second)
				if err != nil {
					t.Fatal("healthy dependency was stopped", err)
				}
				_ = connection.Close()
			}
			if mode != "crash" {
				if len(result.Artifacts) == 0 {
					t.Fatal("artifact missing")
				}
				artifact := filepath.Join(result.Artifacts[0].Path, "report")
				data, err := os.ReadFile(artifact)
				if err != nil || string(data) != "argument with spaces\n--selector\n" {
					t.Fatal(string(data), err)
				}
			}
			fixtures, err := h.cli.Fixtures(context.Background(), v1.FixturesRequest{APIVersion: v1.Version, InstanceID: id})
			if err != nil {
				t.Fatal(err)
			}
			var dataPath string
			for _, fixture := range fixtures.Fixtures {
				if fixture.Name == "password" && (!fixture.Sensitive || fixture.Value != "") {
					t.Fatal("secret exposed", fixture)
				}
				if fixture.Name == "data" {
					dataPath = fixture.Value
				}
			}
			if mode == "shutdown" || mode == "crash" || kept {
				if _, err := os.Stat(dataPath); err != nil {
					t.Fatal("investigation data removed", err)
				}
			}
			secret, err := h.cli.FixtureSecret(context.Background(), v1.FixturesRequest{APIVersion: v1.Version, InstanceID: id, Component: "job", SecretName: "password"})
			if err != nil || len(secret.Fixtures) != 1 || secret.Fixtures[0].Value == "" {
				t.Fatal("explicit secret unavailable", err)
			}
			encoded, err := json.Marshal(final)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(encoded), secret.Fixtures[0].Value) {
				t.Fatal("ordinary result leaked fixture secret")
			}
			if _, err := h.cli.Destroy(context.Background(), id); err != nil {
				t.Fatal(err)
			}
			if dataPath != "" {
				if _, err := os.Stat(dataPath); !os.IsNotExist(err) {
					t.Fatal("explicit destroy retained data", err)
				}
			}
			logs, err := h.cli.Logs(context.Background(), v1.LogsRequest{APIVersion: v1.Version, InstanceID: id, Component: "job"})
			if err != nil || len(logs.Records) == 0 {
				t.Fatal("historical output lost after destroy", logs, err)
			}
			for _, record := range logs.Records {
				if record.Attempt != result.Attempt || record.InstanceID != id || record.Component != "job" || record.Time == "" || record.Stream == "" {
					t.Fatal("log context lost", record)
				}
			}
		})
	}
	for _, signal := range []os.Signal{os.Interrupt, syscall.SIGTERM} {
		t.Run("cli-signal-"+signal.String(), func(t *testing.T) {
			h := newNativeHarness(t, binary)
			job := h.manifest.Components["job"]
			job.Command.Args[len(job.Command.Args)-1] = "never"
			h.manifest.Components["job"] = job
			h.write()
			command := exec.Command(binary, "run", "test", "--project", h.project, "--state-dir", h.dir, "--stop-grace", "50ms", "--json")
			var output strings.Builder
			command.Stdout = &output
			command.Stderr = &strings.Builder{}
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
			for time.Now().Before(deadline) {
				if _, err := os.Stat(h.pids); err == nil {
					break
				}
				time.Sleep(10 * time.Millisecond)
			}
			if err := command.Process.Signal(signal); err != nil {
				t.Fatal(err)
			}
			err := command.Wait()
			joined = true
			exit, ok := err.(*exec.ExitError)
			want := 130
			if signal == syscall.SIGTERM {
				want = 143
			}
			if !ok || exit.ExitCode() != want {
				t.Fatalf("signal exit %v want %d; inspect the private fixture state for details", err, want)
			}
			var final v1.InstanceResponse
			if err := json.Unmarshal([]byte(output.String()), &final); err != nil {
				t.Fatal(err)
			}
			if !final.Instance.Execution.Cancelled {
				t.Fatal("signal result omitted cancellation")
			}
		})
	}
}

func waitComponentLog(t *testing.T, h *nativeHarness, id, component, needle string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		response, err := h.cli.Logs(context.Background(), v1.LogsRequest{APIVersion: v1.Version, InstanceID: id, Component: component})
		if err != nil {
			t.Fatal(err)
		}
		for _, record := range response.Records {
			if strings.Contains(record.Message, needle) {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("job output did not arrive")
}
