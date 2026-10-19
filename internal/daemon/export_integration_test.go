//go:build darwin || linux

package daemon

import (
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

func TestExportNativeFlow(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "backlot")
	if output, err := exec.Command("go", "build", "-race", "-o", binary, "../../cmd/backlot").CombinedOutput(); err != nil {
		t.Fatalf("build: %v %s", err, output)
	}
	h := newNativeHarness(t, binary, "3s")
	h.manifest.Tools["sh"] = "/bin/sh"
	job := h.manifest.Components["job"]
	job.Command = &v1.Command{Tool: "sh", Args: []string{"-c", "mkdir -p evidence; echo '<script>alert(1)</script>' > evidence/report.txt; echo retained-output; exit 23"}}
	job.Artifacts = map[string]v1.ArtifactSource{"report": {Path: "evidence/report.txt"}}
	h.manifest.Components["job"] = job
	h.write()
	git(t, h.project, "init", "-q", "-b", "main")
	git(t, h.project, "add", ".")
	git(t, h.project, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "-qm", "Launch source")
	checkout, err := identify(h.project)
	if err != nil {
		t.Fatal(err)
	}
	launchCommit := launchGitCommit(context.Background(), checkout)
	if len(launchCommit) != 40 {
		t.Fatal("missing normal-branch launch commit", launchCommit)
	}
	started, err := h.cli.Run(context.Background(), h.request("test"))
	if err != nil {
		t.Fatal(err)
	}
	instance := h.state(started.Instance.ID, v1.Failed).Instance
	git(t, h.project, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "--allow-empty", "-qm", "Move branch after launch")
	if current := launchGitCommit(context.Background(), checkout); current == launchCommit {
		t.Fatal("branch did not move")
	}
	destination := filepath.Join(t.TempDir(), "bundle")
	command := exec.Command(binary, "export", instance.ID, "--output", destination, "--wait", "--state-dir", h.dir, "--json")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("export: %v %s", err, output)
	}
	var exported v1.ExportResponse
	if err := json.Unmarshal(output, &exported); err != nil {
		t.Fatal(err)
	}
	if !exported.Published || !exported.Complete || exported.Result.TerminalExitCode == nil || *exported.Result.TerminalExitCode != 23 {
		t.Fatalf("result lost: %+v", exported)
	}
	if _, err := h.cli.Export(context.Background(), v1.ExportRequest{APIVersion: v1.Version, InstanceID: instance.ID, Output: destination}); err == nil {
		t.Fatal("replaced existing bundle")
	}
	for index, wait := range []string{"45", "45s"} {
		target := filepath.Join(filepath.Dir(destination), fmt.Sprintf("bounded-%d", index))
		cmd := exec.Command(binary, "export", instance.ID, "--output", target, "--wait="+wait, "--state-dir", h.dir, "--json")
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("bounded wait: %v %s", err, output)
		}
	}

	if _, err := h.cli.Destroy(context.Background(), instance.ID); err != nil {
		t.Fatal(err)
	}
	h.close()
	if err := os.RemoveAll(h.dir); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(h.project); err != nil {
		t.Fatal(err)
	}
	t.Logf("owned runtime and source removed before relocation: %s %s", h.dir, h.project)
	relocated := filepath.Join(t.TempDir(), "moved")
	if err := os.Rename(destination, relocated); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(relocated, "report.json"))
	if err != nil {
		t.Fatal(err)
	}
	var report v1.ExportReport
	if err := json.Unmarshal(data, &report); err != nil {
		t.Fatal(err)
	}
	if report.Source.GitHead != launchCommit {
		t.Fatalf("launch provenance changed after branch movement/relocation: %q want %q", report.Source.GitHead, launchCommit)
	}
	for _, file := range report.Evidence {
		if !filepath.IsLocal(file.Path) {
			t.Fatal("nonrelative inventory")
		}
		if _, err := os.ReadFile(filepath.Join(relocated, file.Path)); err != nil {
			t.Fatal(err)
		}
	}
	html, err := os.ReadFile(filepath.Join(relocated, "index.html"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(html), "<script") || strings.Contains(string(data), h.dir) {
		t.Fatal("private path or active summary script")
	}
}

func TestExportNativeInterruption(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "backlot")
	if output, err := exec.Command("go", "build", "-race", "-o", binary, "../../cmd/backlot").CombinedOutput(); err != nil {
		t.Fatalf("build: %v %s", err, output)
	}
	h := newNativeHarness(t, binary)
	t.Logf("owned-native-root=%s", filepath.Dir(h.dir))
	started, err := h.cli.Run(context.Background(), h.request("dev"))
	if err != nil {
		t.Fatal(err)
	}
	id := started.Instance.ID
	h.state(id, v1.RuntimeReady)
	for _, mode := range []string{"SIGINT", "SIGTERM", "wait-deadline", "caller-cancel", "caller-deadline"} {
		t.Run(mode, func(t *testing.T) {
			destination := filepath.Join(t.TempDir(), "absent")
			if strings.HasPrefix(mode, "caller-") {
				ctx, cancel := context.WithCancel(context.Background())
				if mode == "caller-deadline" {
					ctx, cancel = context.WithTimeout(context.Background(), 200*time.Millisecond)
				}
				defer cancel()
				if mode == "caller-cancel" {
					cancel()
				}
				_, err := h.cli.Export(ctx, v1.ExportRequest{APIVersion: v1.Version, InstanceID: id, Output: destination, Wait: true})
				code(t, err, "export_interrupted")
			} else {
				wait := "--wait"
				if mode == "wait-deadline" {
					wait = "--wait=200ms"
				}
				command := exec.Command(binary, "export", id, "--output", destination, wait, "--state-dir", h.dir, "--json")
				var output, diagnostic bytes.Buffer
				command.Stdout = &output
				command.Stderr = &diagnostic
				done := make(chan error, 1)
				launched, joined := false, false
				// Retain the direct child and register its join before signal faults.
				t.Cleanup(func() {
					if launched && !joined {
						_ = command.Process.Signal(os.Interrupt)
						select {
						case <-done:
						case <-time.After(5 * time.Second):
							_ = command.Process.Kill()
							<-done
							t.Error("exporter required forced cleanup")
						}
					}
				})
				if err := command.Start(); err != nil {
					t.Fatal(err)
				}
				launched = true
				go func() { done <- command.Wait() }()
				if mode != "wait-deadline" {
					select {
					case err := <-done:
						joined = true
						t.Fatalf("exporter exited before interruption: %v %s", err, output.String())
					case <-time.After(300 * time.Millisecond):
					}
					sig := os.Signal(os.Interrupt)
					if mode == "SIGTERM" {
						sig = syscall.SIGTERM
					}
					if err := command.Process.Signal(sig); err != nil {
						t.Fatal(err)
					}
				}
				select {
				case err := <-done:
					joined = true
					if err == nil {
						t.Fatal("interrupted export succeeded")
					}
				case <-time.After(5 * time.Second):
					t.Fatal("exporter failed to join")
				}
				var response v1.ErrorResponse
				if err := json.Unmarshal(output.Bytes(), &response); err != nil {
					t.Fatalf("invalid interruption JSON: %v %s", err, output.String())
				}
				if response.Error.Code != "export_interrupted" || !strings.Contains(response.Error.Message, "run was not cancelled") {
					t.Fatalf("wrong diagnostic: %s", output.String())
				}
				t.Logf("%s joined with %s", mode, output.String())
			}
			current, err := h.cli.Inspect(context.Background(), id)
			if err != nil || current.Instance.Status != v1.RuntimeReady || current.Instance.Execution.Cancelled {
				t.Fatalf("export affected recorded execution: %+v %v", current, err)
			}
			if _, err := os.Lstat(destination); !os.IsNotExist(err) {
				t.Fatal("interrupted exporter published", err)
			}
		})
	}
}
