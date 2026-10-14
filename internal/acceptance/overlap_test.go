//go:build darwin && arm64

package acceptance

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	v1 "github.com/octacian/backlot/api/v1"
)

type pendingRun struct {
	command *exec.Cmd
	stdout  bytes.Buffer
	done    chan error
	joined  bool
}

func (h *harness) launch(project, config, scene string) *pendingRun {
	h.t.Helper()
	p := &pendingRun{done: make(chan error, 1)}
	p.command = exec.Command(h.binary, "run", scene, "--project", project, "--config", config, "--state-dir", h.state, "--json", "--stop-grace", "1s", "--job-timeout", "120s")
	p.command.Stdout = &p.stdout
	if err := p.command.Start(); err != nil {
		h.t.Fatal(err)
	}
	go func() { p.done <- p.command.Wait() }()
	h.t.Cleanup(func() {
		if p.joined {
			return
		}
		// Only the retained direct client is signalled; its lease then expires.
		_ = p.command.Process.Kill()
		select {
		case <-p.done:
			p.joined = true
		case <-time.After(5 * time.Second):
			h.t.Error("owned acceptance client unjoined; preserve", h.root)
		}
	})
	return p
}
func (p *pendingRun) result(t *testing.T) (v1.Instance, error) {
	t.Helper()
	var err error
	select {
	case err = <-p.done:
		p.joined = true
	case <-time.After(30 * time.Second):
		t.Fatal("owned disposable client did not finish")
	}
	var response v1.InstanceResponse
	if json.Unmarshal(p.stdout.Bytes(), &response) != nil || response.Instance.ID == "" {
		t.Fatal("owned disposable client returned no instance")
	}
	return response.Instance, err
}
func (h *harness) started(project, scene, job, witness string) v1.Instance {
	h.t.Helper()
	project, _ = filepath.EvalSymlinks(project)
	deadline := time.Now().Add(45 * time.Second)
	for time.Now().Before(deadline) {
		instances, err := h.list()
		if err != nil {
			h.t.Fatal(err)
		}
		for _, summary := range instances {
			checkout, _ := filepath.EvalSymlinks(summary.Checkout)
			if summary.Scene != scene || checkout != project {
				continue
			}
			i := h.require("inspect", summary.ID)
			if i.Status == v1.Failed {
				h.t.Fatalf("disposable preparation failed: %s", detail(i, "failure"))
			}
			if component(i, job).Status != "running" {
				continue
			}
			logs, err := h.command("logs", "--state-dir", h.state, "--json", "--component", job, i.ID)
			if err == nil && bytes.Contains(logs, []byte(witness)) {
				return i
			}
		}
		time.Sleep(25 * time.Millisecond)
	}
	h.t.Fatal("public disposable job start witness absent")
	return v1.Instance{}
}

func (h *harness) publishedOverlap(one, two string, dev, production v1.Instance) {
	t := h.t
	sceneName := "published-overlap"
	for _, project := range []string{one, two} {
		path := filepath.Join(project, "backlot.json")
		original, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		defer func() {
			if err := os.WriteFile(path, original, 0600); err != nil {
				t.Error(err)
			}
		}()
		var m v1.Manifest
		if json.Unmarshal(original, &m) != nil {
			t.Fatal("overlap manifest malformed")
		}
		m.Tools["sh"] = "/bin/sh"
		m.Components["published-hold"] = v1.Component{Kind: v1.Job, Runtime: v1.Native, Policy: v1.EachStart, Command: &v1.Command{Tool: "sh", Args: []string{"-c", `mkdir -p reports; echo published-disposable-ready; echo holding > reports/published.txt; while [ ! -f reports/release ]; do sleep 0.05; done; echo published-disposable-complete; echo completed > reports/published.txt`}}, DependsOn: []v1.Dependency{{Component: "container-client", Condition: v1.Completed}}, Outputs: []string{"reports"}, Artifacts: map[string]v1.ArtifactSource{"report": {Path: "reports/published.txt"}}}
		scene := m.Scenes["ui-test"]
		scene.Components = append([]string(nil), scene.Components...)
		for n, name := range scene.Components {
			if name == "ui-test" {
				scene.Components[n] = "published-hold"
			}
		}
		scene.TerminalJob = "published-hold"
		m.Scenes[sceneName] = scene
		data, err := json.MarshalIndent(m, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
		if err = os.Remove(filepath.Join(project, "reports", "release")); err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
	}
	devValue := httpValue(t, dev.Execution.Origin, "")
	productionValue := httpValue(t, production.Execution.Origin, "")
	first := h.launch(one, h.config, sceneName)
	second := h.launch(two, h.config, sceneName)
	a := h.started(one, sceneName, "published-hold", "published-disposable-ready")
	b := h.started(two, sceneName, "published-hold", "published-disposable-ready")
	defer func() {
		for _, i := range []v1.Instance{a, b} {
			if _, err := h.instance("destroy", i.ID); err != nil {
				t.Error("overlap fixture destroy failed; preserve", h.root)
			}
		}
	}()
	if a.ID == b.ID || a.Execution.Origin == b.Execution.Origin {
		t.Fatal("independent disposable identities/origins collided")
	}
	for _, i := range []v1.Instance{a, b} {
		if i.Execution.Origin == dev.Execution.Origin || i.Execution.Origin == production.Execution.Origin || httpValue(t, i.Execution.Origin, "") != "fresh" {
			t.Fatal("disposable DB/origin not isolated")
		}
	}
	if httpValue(t, a.Execution.Origin, "overlap-one") != "overlap-one" || httpValue(t, b.Execution.Origin, "overlap-two") != "overlap-two" {
		t.Fatal("disposable mutation failed")
	}
	peer := h.require("inspect", b.ID)
	if component(h.require("inspect", a.ID), "published-hold").Status != "running" || component(peer, "published-hold").Status != "running" {
		t.Fatal("published stacks did not overlap")
	}
	t.Logf("published overlap witness at=%s first=%s second=%s first_origin=%s second_origin=%s; both terminal jobs running after completed real container HTTPS clients", time.Now().UTC().Format(time.RFC3339Nano), a.ID, b.ID, a.Execution.Origin, b.Execution.Origin)
	finish := func(project string, p *pendingRun, id string) {
		if err := os.WriteFile(filepath.Join(project, "reports", "release"), []byte("finish"), 0600); err != nil {
			t.Fatal(err)
		}
		i, err := p.result(t)
		if err != nil || i.ID != id || i.Status != v1.Succeeded || i.Execution.TerminalExitCode == nil || *i.Execution.TerminalExitCode != 0 || strings.TrimSpace(string(artifactFile(t, i, "published-hold", "report", "published.txt"))) != "completed" {
			t.Fatalf("overlap completion/artifact failed: failure=%s", detail(i, "failure"))
		}
		h.require("destroy", id)
	}
	finish(one, first, a.ID)
	if component(h.require("inspect", b.ID), "published-hold").Status != "running" || httpValue(t, b.Execution.Origin, "") != "overlap-two" {
		t.Fatal("destroying first stack disturbed live peer")
	}
	if httpValue(t, dev.Execution.Origin, "") != devValue || httpValue(t, production.Execution.Origin, "") != productionValue {
		t.Fatal("disposable overlap disturbed persistent workflows")
	}
	t.Logf("peer preservation witness at=%s destroyed=%s live=%s value=overlap-two; persistent dev/production values preserved", time.Now().UTC().Format(time.RFC3339Nano), a.ID, b.ID)
	finish(two, second, b.ID)
}
