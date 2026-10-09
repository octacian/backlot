//go:build darwin && arm64

package acceptance

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	v1 "github.com/octacian/backlot/api/v1"
)

func literal(text string) v1.Value { return v1.Value{Literal: &text} }
func reference(name, field string) v1.Value {
	return v1.Value{Ref: &v1.Reference{Kind: "resource", Name: name, Field: field}}
}

func (h *harness) writeManifest(m v1.Manifest) {
	h.t.Helper()
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		h.t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(h.project, "backlot.json"), data, 0600); err != nil {
		h.t.Fatal(err)
	}
}
func (h *harness) await(id string, status v1.InstanceStatus) v1.Instance {
	h.t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	var i v1.Instance
	for time.Now().Before(deadline) {
		i = h.require("inspect", id)
		if i.Status == status {
			return i
		}
		time.Sleep(25 * time.Millisecond)
	}
	h.t.Fatalf("instance %s did not reach %s: status=%s failure=%s", id, status, i.Status, detail(i, "failure"))
	return i
}
func component(i v1.Instance, name string) v1.ComponentResult {
	if i.Execution != nil {
		for _, c := range i.Execution.Components {
			if c.Name == name {
				return c
			}
		}
	}
	return v1.ComponentResult{}
}
func (h *harness) fixtures(id, component string) v1.FixturesResponse {
	data, err := h.command("fixtures", "--state-dir", h.state, "--json", "--component", component, id)
	var result v1.FixturesResponse
	if err != nil || json.Unmarshal(data, &result) != nil {
		h.t.Fatal("fixture discovery failed")
	}
	return result
}
func (h *harness) negativeWorkflows() {
	t := h.t
	path := filepath.Join(h.project, "backlot.json")
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
		t.Fatal("fixture manifest malformed")
	}
	m.Tools["sh"] = "/bin/sh"
	m.Resources["witness-port"] = v1.Resource{Kind: "port"}
	m.Resources["witness-data"] = v1.Resource{Kind: "directory"}
	m.Outputs["negative"] = v1.Output{Path: "negative", ConcurrencyGroup: "negative"}
	env := v1.Environment{Assign: map[string]v1.Value{"PORT": reference("witness-port", "port")}}
	target := reference("witness-port", "port")
	server := v1.Component{Kind: v1.Service, Runtime: v1.Native, Command: &v1.Command{Tool: "fixture", Args: []string{"witness-server"}}, Resources: []string{"witness-port"}, Environment: env, Ports: map[string]v1.ServicePort{"http": {Resource: "witness-port"}}, Readiness: &v1.Probe{Kind: "tcp", Target: &target, Timeout: "3s"}}
	m.Components["witness"] = server
	m.Scenes["witness"] = v1.Scene{Lifetime: v1.Persistent, Components: []string{"witness"}, Resources: []string{"witness-port"}}
	containerServer := server
	containerServer.Runtime = v1.Container
	containerServer.Command = nil
	image := v1.Value{Ref: &v1.Reference{Kind: "input", Name: "image"}}
	containerServer.Image = &image
	containerServer.Args = []string{"witness-server"}
	containerServer.Environment = v1.Environment{Assign: map[string]v1.Value{"PORT": literal("8080")}}
	containerServer.Ports = map[string]v1.ServicePort{"http": {Resource: "witness-port", ContainerPort: 8080}}
	m.Components["container-witness"] = containerServer
	m.Scenes["container-witness"] = v1.Scene{Lifetime: v1.Persistent, Components: []string{"container-witness"}, Resources: []string{"witness-port"}}
	missing := containerServer
	missingImage := literal("backlot-acceptance-missing:" + filepath.Base(h.root))
	missing.Image = &missingImage
	m.Components["missing-image"] = missing
	m.Scenes["missing-image"] = v1.Scene{Lifetime: v1.Persistent, Components: []string{"missing-image"}, Resources: []string{"witness-port"}}
	shell := func(script string) v1.Component {
		return v1.Component{Kind: v1.Job, Runtime: v1.Native, Policy: v1.EachStart, Command: &v1.Command{Tool: "sh", Args: []string{"-c", script}}}
	}
	fresh := shell(`if [ ! -f "$DATA/attempted" ]; then echo attempt >"$DATA/attempted"; echo fresh-first-failure; exit 19; fi; echo success >>"$DATA/completions"; echo fresh-success`)
	fresh.Policy = v1.FreshOnly
	fresh.Resources = []string{"witness-data"}
	fresh.Initializes = []string{"witness-data"}
	fresh.Environment = v1.Environment{Assign: map[string]v1.Value{"DATA": reference("witness-data", "path")}}
	fresh.Fixtures = map[string]v1.Fixture{"data": {Value: reference("witness-data", "path")}}
	m.Components["fresh-witness"] = fresh
	dependent := server
	dependent.DependsOn = []v1.Dependency{{Component: "fresh-witness", Condition: v1.Completed}}
	m.Components["fresh-service"] = dependent
	m.Scenes["fresh-retry"] = v1.Scene{Lifetime: v1.Persistent, Components: []string{"fresh-witness", "fresh-service"}, Resources: []string{"witness-data", "witness-port"}}
	gate := shell(`mkdir -p negative; echo "$PORT" > negative/port; while [ ! -f negative/release ]; do sleep 0.05; done`)
	gate.Environment = env
	gate.Resources = []string{"witness-port"}
	gate.Outputs = []string{"negative"}
	m.Components["port-gate"] = gate
	conflict := server
	conflict.DependsOn = []v1.Dependency{{Component: "port-gate", Condition: v1.Completed}}
	m.Components["conflict-service"] = conflict
	m.Scenes["port-conflict"] = v1.Scene{Lifetime: v1.Persistent, Components: []string{"port-gate", "conflict-service"}, Resources: []string{"witness-port"}}
	collect := shell(`mkdir -p negative; echo retained-evidence >negative/report; echo collection-original-exit; exit 17`)
	collect.Outputs = []string{"negative"}
	collect.Artifacts = map[string]v1.ArtifactSource{"present": {Path: "negative/report"}, "missing": {Path: "negative/never-created"}}
	m.Components["collection"] = collect
	m.Scenes["collection-failure"] = v1.Scene{Lifetime: v1.Disposable, Components: []string{"collection"}, TerminalJob: "collection"}
	clean := shell(`echo cleanup-started; while [ ! -f "$DATA/release" ]; do sleep 0.05; done; echo cleanup-original-zero`)
	clean.Resources = []string{"witness-data"}
	clean.Environment = fresh.Environment
	clean.Fixtures = fresh.Fixtures
	m.Components["cleanup"] = clean
	m.Scenes["cleanup-failure"] = v1.Scene{Lifetime: v1.Disposable, Components: []string{"cleanup"}, Resources: []string{"witness-data"}, TerminalJob: "cleanup"}
	h.writeManifest(m)
	h.test("public-provider-diagnostic", func(t *testing.T) {
		i, err := h.instance("run", "missing-image")
		if err == nil || i.Status != v1.Failed || !strings.Contains(i.Execution.Failure, "use external Docker tooling to pull/build the declared image") || i.Execution.CleanupFailure != "" {
			t.Fatal("missing local image diagnostic/cleanup incorrect")
		}
		h.require("destroy", i.ID)
	})
	h.test("fresh-only-failure-retry-and-skip", func(t *testing.T) {
		failed, err := h.instance("run", "fresh-retry")
		if err == nil || failed.Status != v1.Failed || component(failed, "fresh-witness").ExitCode == nil || *component(failed, "fresh-witness").ExitCode != 19 || component(failed, "fresh-service").Name != "" {
			t.Fatal("failed initialization launched dependent or lost exit")
		}
		ready := h.require("restart", failed.ID)
		if component(ready, "fresh-witness").Status != "completed" {
			t.Fatal("failed fresh initializer was not retried")
		}
		fixtures := h.fixtures(ready.ID, "fresh-witness")
		if len(fixtures.Fixtures) != 1 {
			t.Fatal("initialization data fixture missing")
		}
		dir := fixtures.Fixtures[0].Value
		h.require("stop", ready.ID)
		again := h.require("run", "fresh-retry")
		if component(again, "fresh-witness").Status != "skipped" {
			t.Fatal("successful initialization repeated")
		}
		data, err := os.ReadFile(filepath.Join(dir, "completions"))
		if err != nil || string(data) != "success\n" {
			t.Fatal("initialization completion not exactly once")
		}
		h.require("destroy", ready.ID)
	})
	h.test("deterministic-consumed-port-conflict", func(t *testing.T) {
		dir := filepath.Join(h.project, "negative")
		_ = os.RemoveAll(dir)
		done := make(chan struct{})
		var i v1.Instance
		var runErr error
		go func() { i, runErr = h.instance("run", "port-conflict"); close(done) }()
		// Always release and join the owned public invocation, even on assertion failure.
		defer func() {
			_ = os.WriteFile(filepath.Join(dir, "release"), nil, 0600)
			select {
			case <-done:
			case <-time.After(20 * time.Second):
				t.Error("conflict invocation unjoined")
			}
		}()
		var data []byte
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			data, err = os.ReadFile(filepath.Join(dir, "port"))
			if err == nil {
				break
			}
			time.Sleep(25 * time.Millisecond)
		}
		port, err := strconv.Atoi(strings.TrimSpace(string(data)))
		if err != nil || port == 0 {
			t.Fatal("consumed allocation witness absent")
		}
		listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = listener.Close() }()
		if err = os.WriteFile(filepath.Join(dir, "release"), nil, 0600); err != nil {
			t.Fatal(err)
		}
		select {
		case <-done:
		case <-time.After(20 * time.Second):
			t.Fatal("conflict run timed out")
		}
		if runErr == nil || i.Status != v1.Failed || i.Execution.FailureDetail == nil || i.Execution.FailureDetail.Code != "port_conflict" || i.Execution.Ports["witness-port"] != port {
			t.Fatalf("consumed port conflict not deterministic: status=%s failure=%s", i.Status, detail(i, "failure"))
		}
		connection, err := net.DialTimeout("tcp", listener.Addr().String(), time.Second)
		if err != nil {
			t.Fatal("unrelated listener altered")
		}
		_ = connection.Close()
		h.require("destroy", i.ID)
	})
	h.test("unexpected-service-exit-and-live-log-framing", func(t *testing.T) {
		for _, name := range []string{"witness", "container-witness"} {
			i := h.require("run", name)
			port := i.Execution.Ports["witness-port"]
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, h.binary, "logs", "--state-dir", h.state, "--json", "--follow", i.ID)
			pipe, err := command.StdoutPipe()
			if err != nil {
				t.Fatal(err)
			}
			if err = command.Start(); err != nil {
				t.Fatal(err)
			}
			records := make(chan v1.LogRecord, 20)
			decodeErrors := make(chan error, 1)
			go func() {
				defer close(records)
				scanner := bufio.NewScanner(pipe)
				for scanner.Scan() {
					var record v1.LogRecord
					if err := json.Unmarshal(scanner.Bytes(), &record); err != nil {
						decodeErrors <- err
						return
					}
					records <- record
				}
				decodeErrors <- scanner.Err()
			}()
			defer func() { cancel(); _ = command.Wait() }()
			get := func(path string) {
				response, err := (&http.Client{Timeout: time.Second}).Get(fmt.Sprintf("http://127.0.0.1:%d/%s", port, path))
				if err == nil {
					_ = response.Body.Close()
				}
			}
			witnessed := false
			timer := time.NewTimer(10 * time.Second)
			defer timer.Stop()
			for !witnessed {
				select {
				case record, ok := <-records:
					if !ok {
						t.Fatal("live stream ended before witness")
					}
					if record.InstanceID != i.ID || record.Attempt != i.Execution.Attempt || record.Component != name || record.Stream != "stdout" || record.Time == "" {
						t.Fatal("invalid NDJSON framing")
					}
					if strings.Contains(record.Message, "witness server starting") {
						get("witness")
					}
					if strings.Contains(record.Message, "live-request-witness") {
						witnessed = true
					}
				case <-timer.C:
					t.Fatal("new live log witness absent")
				}
			}
			get("exit")
			failed := h.await(i.ID, v1.Failed)
			if !strings.Contains(failed.Execution.Failure, "exit 23") || component(failed, name).ExitCode == nil || *component(failed, name).ExitCode != 23 {
				t.Fatal("unexpected exit diagnostic/result lost")
			}
			h.require("destroy", i.ID)
			// Follow exits after completion and emits only complete record frames.
			for range records {
			}
			if err := <-decodeErrors; err != nil {
				t.Fatal("live framing decode", err)
			}
		}
	})
	h.test("collection-failure-retains-original-exit", func(t *testing.T) {
		i, err := h.instance("run", "collection-failure")
		if err == nil || i.Status != v1.Failed || i.Execution.TerminalExitCode == nil || *i.Execution.TerminalExitCode != 17 || i.Execution.CollectionFailure == "" || i.Execution.CleanupFailure != "" {
			t.Fatal("collection and original job failure conflated")
		}
		h.collectionWitness = i.ID
		if string(artifactFile(t, i, "collection", "present", "report")) != "retained-evidence\n" {
			t.Fatal("available evidence not retained")
		}
		h.require("destroy", i.ID)
	})
	h.test("uncertain-ownership-cleanup-and-retry", func(t *testing.T) {
		known, err := h.list()
		if err != nil {
			t.Fatal(err)
		}
		old := map[string]bool{}
		for _, i := range known {
			old[i.ID] = true
		}
		done := make(chan struct{})
		var final v1.Instance
		var runErr error
		go func() { final, runErr = h.instance("run", "cleanup-failure"); close(done) }()
		id := ""
		directory := ""
		deadline := time.Now().Add(15 * time.Second)
		for time.Now().Before(deadline) {
			list, err := h.list()
			if err != nil {
				t.Fatal(err)
			}
			for _, i := range list {
				if !old[i.ID] && i.Scene == "cleanup-failure" {
					id = i.ID
				}
			}
			if id != "" {
				i := h.require("inspect", id)
				if component(i, "cleanup").Status == "running" {
					f := h.fixtures(id, "cleanup")
					if len(f.Fixtures) == 1 {
						directory = f.Fixtures[0].Value
						break
					}
				}
			}
			time.Sleep(25 * time.Millisecond)
		}
		if directory == "" {
			t.Fatal("cleanup terminal fixture not running")
		}
		marker := filepath.Join(directory, ".backlot-owner")
		proof, err := os.ReadFile(marker)
		if err != nil {
			t.Fatal(err)
		}
		// Retain exact owned proof before the fault, restoring before any explicit cleanup.
		defer func() {
			_ = os.WriteFile(marker, proof, 0600)
			_ = os.WriteFile(filepath.Join(directory, "release"), nil, 0600)
			select {
			case <-done:
			case <-time.After(20 * time.Second):
				t.Error("cleanup invocation unjoined")
			}
			if _, err := h.instance("destroy", id); err != nil {
				t.Error("restored cleanup retry failed")
			}
		}()
		if err = os.WriteFile(marker, []byte("fixture-uncertain-owner"), 0600); err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(filepath.Join(directory, "release"), nil, 0600); err != nil {
			t.Fatal(err)
		}
		select {
		case <-done:
		case <-time.After(20 * time.Second):
			t.Fatal("cleanup result timed out")
		}
		if runErr == nil || final.ID != id || final.Status != v1.Failed || final.Execution.TerminalExitCode == nil || *final.Execution.TerminalExitCode != 0 || final.Execution.CleanupFailure == "" {
			t.Fatal("uncertain cleanup was reported as successful")
		}
		if data, err := os.ReadFile(marker); err != nil || string(data) != "fixture-uncertain-owner" {
			t.Fatal("uncertain resource modified")
		}
		if _, err := h.instance("destroy", id); err == nil {
			t.Fatal("uncertain destroy succeeded")
		}
		if err = os.WriteFile(marker, proof, 0600); err != nil {
			t.Fatal(err)
		}
		recovered := h.require("destroy", id)
		if recovered.Execution.CleanupFailure != "" {
			t.Fatal("verified cleanup did not resolve failure")
		}
	})
}

// removeWorktrees releases only the complete expected private Git topology, after
// public destruction and retained daemon join have both been verified.
func (h *harness) removeWorktrees() {
	h.t.Helper()
	output, err := exec.Command("git", "-C", h.fixtureRepo, "worktree", "list", "--porcelain").Output()
	if err != nil {
		h.t.Errorf("fixture Git topology unavailable; preserved %s", h.root)
		return
	}
	want := map[string]bool{}
	for _, path := range append([]string{h.fixtureRepo}, h.worktrees...) {
		canonical, err := filepath.EvalSymlinks(path)
		if err != nil {
			h.t.Error("fixture path unresolved; preserved", path)
			return
		}
		want[canonical] = true
	}
	count := 0
	for _, line := range strings.Split(string(output), "\n") {
		if path, ok := strings.CutPrefix(line, "worktree "); ok {
			canonical, err := filepath.EvalSymlinks(path)
			if err != nil || !want[canonical] {
				h.t.Errorf("unexpected fixture Git topology; preserved %s", h.root)
				return
			}
			count++
		}
	}
	if count != len(want) {
		h.t.Error("fixture worktree inventory incomplete; preserved", h.root)
		return
	}
	for _, path := range h.worktrees {
		if err := exec.Command("git", "-C", h.fixtureRepo, "worktree", "remove", "--force", path).Run(); err != nil {
			h.t.Error("owned fixture worktree removal failed", path)
			return
		}
	}
	if err := os.RemoveAll(h.fixtureRepo); err != nil {
		h.t.Error("owned fixture repository removal failed", err)
	}
}
