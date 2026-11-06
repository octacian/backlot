//go:build darwin || linux

package daemon

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	v1 "github.com/octacian/backlot/api/v1"
)

func TestAttachedNativeCLI(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "backlot")
	if out, err := exec.Command("go", "build", "-race", "-o", binary, "../../cmd/backlot").CombinedOutput(); err != nil {
		t.Fatal(err, string(out))
	}
	t.Setenv("BACKLOT_PROJECT_CONFIG_DIR", filepath.Join(shortTemp(t), "registry"))
	for _, entry := range []string{"scene", "run"} {
		for _, machine := range []bool{false, true} {
			t.Run(fmt.Sprintf("startup-details/%s/json=%t", entry, machine), func(t *testing.T) {
				h := newNativeHarness(t, binary)
				server := h.manifest.Components["server"]
				server.Fixtures = map[string]v1.Fixture{
					"password": {Value: v1.Value{Literal: ptr("attached-selected-canary"), Secret: true}},
					"other":    {Value: v1.Value{Literal: ptr("attached-unselected-canary"), Secret: true}},
				}
				h.manifest.Components["server"] = server
				scene := h.manifest.Scenes["dev"]
				scene.StartupDetails = []v1.StartupDetailSelection{{Component: "server", Fixture: "password", Label: "Demo password"}}
				h.manifest.Scenes["dev"] = scene
				h.write()
				config := filepath.Join(h.project, "machine.json")
				if err := os.WriteFile(config, []byte(`{"version":"backlot/v1"}`), 0600); err != nil {
					t.Fatal(err)
				}
				args := []string{entry}
				if entry == "scene" {
					args = append(args, "start")
				}
				args = append(args, "dev", "--attached", "--config", config, "--state-dir", h.dir, "--stop-grace", "50ms")
				if machine {
					args = append(args, "--json")
				}
				cmd := exec.Command(binary, args...)
				cmd.Dir = h.project
				reader, err := cmd.StdoutPipe()
				if err != nil {
					t.Fatal(err)
				}
				var out, diag bytes.Buffer
				cmd.Stderr = &diag
				if err := cmd.Start(); err != nil {
					t.Fatal(err)
				}
				done, ready := make(chan struct{}), make(chan struct{}, 1)
				var waitErr error
				go func() {
					defer close(done)
					scanner := bufio.NewScanner(reader)
					scanner.Buffer(make([]byte, 4096), 1<<20)
					for scanner.Scan() {
						line := scanner.Text()
						out.WriteString(line + "\n")
						if strings.Contains(line, "attached-selected-canary") {
							select {
							case ready <- struct{}{}:
							default:
							}
						}
					}
					waitErr = cmd.Wait()
				}()
				t.Cleanup(func() {
					select {
					case <-done:
					default:
						_ = cmd.Process.Kill()
						<-done
					}
				})
				select {
				case <-ready:
				case <-done:
					t.Fatal("summary missing", waitErr, diag.String())
				case <-time.After(20 * time.Second):
					t.Fatal("summary timeout")
				}
				if err := cmd.Process.Signal(os.Interrupt); err != nil {
					t.Fatal(err)
				}
				select {
				case <-done:
				case <-time.After(20 * time.Second):
					t.Fatal("cleanup timeout")
				}
				if exit, ok := waitErr.(*exec.ExitError); !ok || exit.ExitCode() != 130 {
					t.Fatal("interrupt outcome", waitErr)
				}
				if strings.Count(out.String(), "attached-selected-canary") != 1 || strings.Contains(out.String()+diag.String(), "attached-unselected-canary") {
					t.Fatal("wrong disclosure")
				}
				var id string
				if machine {
					for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
						var record v1.AttachedRecord
						if err := json.Unmarshal([]byte(line), &record); err != nil {
							t.Fatal(err)
						}
						if record.Result != nil {
							id = record.Result.Instance.ID
							if record.Result.StartupDetails != nil && record.Type != "ready" {
								t.Fatal("summary escaped ready")
							}
						}
					}
				} else if !strings.Contains(out.String(), "Demo password: attached-selected-canary") {
					t.Fatal("missing human label")
				}
				if id != "" {
					status, err := h.cli.Inspect(context.Background(), id)
					if err != nil || status.StartupDetails != nil {
						t.Fatal("summary persisted", err)
					}
				}
				h.absent()
			})
		}
	}
	for _, scenario := range []struct {
		name   string
		signal os.Signal
		exit   int
	}{
		{"scene", os.Interrupt, 130}, {"run", syscall.SIGTERM, 143}, {"failure", nil, 1}, {"loss", os.Kill, -1}, {"external-stop", nil, 0},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			h := newNativeHarness(t, binary, "2s")
			delete(h.manifest.Scenes, "test")
			h.manifest.Tools["sh"] = "sh"
			h.manifest.Resources["data"] = v1.Resource{Kind: "directory"}
			h.manifest.Resources["password"] = v1.Resource{Kind: "secret"}
			env := v1.Environment{Assign: map[string]v1.Value{"DATA": {Ref: &v1.Reference{Kind: "resource", Name: "data", Field: "path"}}, "SECRET": {Ref: &v1.Reference{Kind: "resource", Name: "password", Field: "value"}}}}
			h.manifest.Components["prep"] = v1.Component{Kind: v1.Job, Runtime: v1.Native, Policy: v1.FreshOnly, Initializes: []string{"data"}, Resources: []string{"data", "password"}, Environment: env, Command: &v1.Command{Tool: "sh", Args: v1.StringArguments("-c", "printf '%s' \"$SECRET\" > \"$DATA/init\"")}}
			h.manifest.Components["verify"] = v1.Component{Kind: v1.Job, Runtime: v1.Native, Policy: v1.EachStart, Resources: []string{"data", "password"}, Environment: env, Command: &v1.Command{Tool: "sh", Args: v1.StringArguments("-c", `test "$(cat "$DATA/init")" = "$SECRET"`)}, DependsOn: []v1.Dependency{{Component: "server", Condition: v1.Ready}}}
			server := h.manifest.Components["server"]
			server.DependsOn = []v1.Dependency{{Component: "prep", Condition: v1.Completed}}
			h.manifest.Components["server"] = server
			h.manifest.Components["peer"] = v1.Component{Kind: v1.Service, Runtime: v1.Native, Command: &v1.Command{Tool: "sh", Args: v1.StringArguments("-c", "exec sleep 120")}, Readiness: &v1.Probe{Kind: "command", Command: &v1.Command{Tool: "sh", Args: v1.StringArguments("-c", "true")}, Timeout: "2s"}}
			h.manifest.Scenes["peer"] = v1.Scene{Lifetime: v1.Persistent, Components: []string{"peer"}}
			scene := h.manifest.Scenes["dev"]
			scene.Components = append(scene.Components, "prep", "job", "verify")
			scene.Resources = append(scene.Resources, "data", "password")
			h.manifest.Scenes["dev"] = scene
			h.write()
			peer := h.run("peer").Instance.ID
			h.state(peer, v1.RuntimeReady)
			args := []string{"scene", "start"}
			if scenario.name == "run" {
				args = []string{"run"}
			}
			args = append(args, "--attached", "--json", "--state-dir", h.dir, "--stop-grace", "50ms", "dev")
			cmd := exec.Command(binary, args...)
			cmd.Dir = h.project
			reader, writer, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = reader.Close() }()
			var stderr bytes.Buffer
			cmd.Stdout, cmd.Stderr = writer, &stderr
			records := make(chan v1.AttachedRecord, 128)
			done := make(chan struct{})
			var waitErr error
			t.Cleanup(func() {
				select {
				case <-done:
				default:
					_ = cmd.Process.Kill()
					<-done
				}
			})
			if err := cmd.Start(); err != nil {
				_ = writer.Close()
				close(done)
				t.Fatal(err)
			}
			if err := writer.Close(); err != nil {
				t.Fatal(err)
			}
			go func() {
				scanner := bufio.NewScanner(reader)
				scanner.Buffer(make([]byte, 4096), 1<<20)
				for scanner.Scan() {
					var record v1.AttachedRecord
					if err := json.Unmarshal(scanner.Bytes(), &record); err != nil {
						continue
					}
					records <- record
				}
				waitErr = cmd.Wait()
				close(records)
				close(done)
			}()
			var id string
			logs := map[string]bool{}
			ready := false
			deadline := time.After(25 * time.Second)
			for !ready || !logs["server"] || !logs["job"] {
				select {
				case record, ok := <-records:
					if !ok {
						t.Fatal("attached exited before readiness/logs", waitErr, stderr.String())
					}
					if record.Result != nil {
						if record.Result.Attachment != nil || record.Result.LeaseToken != "" {
							t.Fatal("private authority emitted")
						}
						id = record.Result.Instance.ID
					}
					if record.Type == "ready" {
						ready = true
					}
					if record.Log != nil {
						logs[record.Log.Component] = true
					}
				case <-deadline:
					t.Fatal("attached readiness/log timeout", stderr.String())
				}
			}
			select {
			case <-done:
				t.Fatal("ready attached client exited")
			default:
			}
			paths, err := filepath.Glob(filepath.Join(h.dir, "data", "*", "init"))
			if err != nil || len(paths) != 1 {
				t.Fatal("retained data missing", err)
			}
			before, err := os.ReadFile(paths[0])
			if err != nil || len(before) == 0 {
				t.Fatal("paired credential not initialized", err)
			}
			if scenario.name == "external-stop" {
				if _, err := h.cli.StopExecution(context.Background(), id); err != nil {
					t.Fatal(err)
				}
			} else if scenario.signal != nil {
				if err := cmd.Process.Signal(scenario.signal); err != nil {
					t.Fatal(err)
				}
			} else {
				h.fixtureAction("root-crash")
			}
			select {
			case <-done:
			case <-time.After(25 * time.Second):
				t.Fatal("attached cleanup timed out")
			}
			exit, ok := waitErr.(*exec.ExitError)
			if scenario.exit == 0 && waitErr != nil || scenario.exit > 0 && (!ok || exit.ExitCode() != scenario.exit) || scenario.exit < 0 && (!ok || !exit.ProcessState.Sys().(syscall.WaitStatus).Signaled()) {
				t.Fatal("attached exit", waitErr, stderr.String())
			}
			terminal := ""
			for record := range records {
				if record.Type == "failure" || record.Type == "stopped" {
					terminal = record.Type
				}
			}
			want := "stopped"
			if scenario.name == "failure" {
				want = "failure"
			}
			if scenario.name != "loss" && terminal != want {
				t.Fatal("missing terminal record", terminal, want, stderr.String())
			}
			if scenario.name == "loss" {
				h.state(id, v1.Stopped)
			}
			h.absent()
			after, err := os.ReadFile(paths[0])
			if err != nil || !bytes.Equal(before, after) {
				t.Fatal("retained data/credential changed", err)
			}
			h.state(peer, v1.RuntimeReady)
			if scenario.name == "scene" || scenario.name == "loss" {
				h.run("dev")
				h.state(id, v1.RuntimeReady) // each-start verify proves retained data matches the paired credential
				if _, err := h.cli.StopExecution(context.Background(), id); err != nil {
					t.Fatal(err)
				}
				h.absent()
			}
			historical, err := h.cli.Logs(context.Background(), v1.LogsRequest{APIVersion: v1.Version, InstanceID: id})
			if err != nil || len(historical.Records) == 0 {
				t.Fatal("historical logs missing", err)
			}
		})
	}
	t.Run("startup-readiness-failure", func(t *testing.T) {
		h := newNativeHarness(t, binary)
		server := h.manifest.Components["server"]
		server.Environment.Assign["DELAY_BIND"] = fixtureLiteral("1")
		server.Readiness.Timeout = "100ms"
		server.Fixtures = map[string]v1.Fixture{"password": {Value: v1.Value{Literal: ptr("failed-start-private-canary"), Secret: true}}}
		scene := h.manifest.Scenes["dev"]
		scene.StartupDetails = []v1.StartupDetailSelection{{Component: "server", Fixture: "password", Label: "Password"}}
		h.manifest.Scenes["dev"] = scene
		h.manifest.Components["server"] = server
		h.write()
		config := filepath.Join(h.project, "machine.json")
		if err := os.WriteFile(config, []byte(`{"version":"backlot/v1"}`), 0600); err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command(binary, "run", "--config", config, "--attached", "--json", "--state-dir", h.dir, "--stop-grace", "50ms", "dev")
		cmd.Dir = h.project
		out, err := cmd.Output()
		if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != 1 {
			t.Fatal("startup failure exit", err)
		}
		if bytes.Contains(out, []byte("failed-start-private-canary")) {
			t.Fatal("failed readiness disclosed secret")
		}
		var failed bool
		for _, line := range bytes.Split(bytes.TrimSpace(out), []byte("\n")) {
			var record v1.AttachedRecord
			if err := json.Unmarshal(line, &record); err != nil {
				t.Fatal(err)
			}
			if record.Type == "ready" || record.Result != nil && record.Result.StartupDetails != nil {
				t.Fatal("alive process was reported ready")
			}
			if record.Type == "failure" {
				failed = true
			}
		}
		if !failed {
			t.Fatal("startup failure record missing")
		}
		h.absent()
	})

}
