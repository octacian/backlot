//go:build darwin || linux

package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

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
