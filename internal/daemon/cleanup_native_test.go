//go:build darwin || linux

package daemon

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	v1 "github.com/octacian/backlot/api/v1"
)

func TestCleanupNativeCLI(t *testing.T) {
	binary := buildFixtureBinary(t, t.TempDir(), "../../cmd/backlot")
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", home)
	t.Setenv("BACKLOT_PROJECT_CONFIG_DIR", filepath.Join(home, "registry"))
	h := newNativeHarness(t, binary, "30s")
	h.manifest.Tools["shell"] = "/bin/sh"
	h.manifest.Resources["data"] = v1.Resource{Kind: "directory"}
	job := h.manifest.Components["job"]
	environment := map[string]v1.Value{}
	for k, v := range job.Environment.Assign {
		environment[k] = v
	}
	job.Environment.Assign = environment
	job.Command = &v1.Command{Tool: "shell", Args: v1.StringArguments("-c", `printf cleanup-fixture; printf retained > "$DATA/file"; exit 17`)}
	job.Resources = append(job.Resources, "data")
	job.Environment.Assign["DATA"] = v1.Value{Ref: &v1.Reference{Kind: "resource", Name: "data", Field: "path"}}
	h.manifest.Components["job"] = job
	scene := h.manifest.Scenes["test"]
	scene.Resources = append(scene.Resources, "data")
	h.manifest.Scenes["test"] = scene
	h.write()
	r := h.request("test")
	r.Options.KeepOnFailure = true
	first, err := h.cli.Run(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	h.state(first.Instance.ID, v1.Failed)
	if _, err = h.cli.StopExecution(context.Background(), first.Instance.ID); err != nil {
		t.Fatal(err)
	}
	export := filepath.Join(t.TempDir(), "report")
	if _, err = h.cli.Export(context.Background(), v1.ExportRequest{APIVersion: v1.Version, InstanceID: first.Instance.ID, Output: export}); err != nil {
		t.Fatal(err)
	}
	// A different state root hosts live work while this checkout is destroyed.
	peer := newNativeHarness(t, binary, "30s")
	other := peer.run("dev")
	peer.state(other.Instance.ID, v1.RuntimeReady)
	invoke := func(jsonMode bool, args ...string) ([]byte, error) {
		t.Helper()
		if jsonMode {
			args = append([]string{"--json"}, args...)
		}
		cmd := exec.Command(binary, args...)
		var diagnostic bytes.Buffer
		cmd.Stderr = &diagnostic
		data, err := cmd.Output()
		if err != nil {
			t.Log(diagnostic.String())
		}
		return data, err
	}
	args := []string{"checkout", "destroy", "--checkout", h.project, "--state-dir", h.dir}
	text, err := invoke(false, args...)
	if err != nil || strings.Contains(string(text), "Apply this cleanup?") || !strings.Contains(string(text), "--apply-preview") {
		t.Fatal("noninteractive preview prompted or omitted apply guidance", err, string(text))
	}
	raw, err := invoke(true, args...)
	if err != nil {
		t.Fatal(err)
	}
	decode := func(raw []byte) []v1.CleanupRecord {
		t.Helper()
		out := []v1.CleanupRecord{}
		for _, line := range bytes.Split(bytes.TrimSpace(raw), []byte("\n")) {
			var r v1.CleanupRecord
			if json.Unmarshal(line, &r) != nil || r.Validate() != nil {
				t.Fatal("invalid CLI stream", string(line))
			}
			out = append(out, r)
		}
		return out
	}
	preview := decode(raw)
	digest := preview[len(preview)-1].Summary.Preview
	extra, err := h.cli.Prepare(context.Background(), v1.PrepareRequest{APIVersion: v1.Version, Plan: r.Plan})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = invoke(true, append(args, "--apply-preview", digest)...); err == nil {
		t.Fatal("stale preview accepted")
	}
	raw, err = invoke(true, args...)
	if err != nil {
		t.Fatal(err)
	}
	preview = decode(raw)
	digest = preview[len(preview)-1].Summary.Preview
	raw, err = invoke(true, append(args, "--apply-preview", digest)...)
	if err != nil {
		t.Fatal(err, string(raw))
	}
	events := decode(raw)
	dataRemoved, evidenceRemoved := false, false
	for _, event := range events {
		if event.Resource != nil && event.Resource.Outcome == "removed" {
			dataRemoved = dataRemoved || event.Resource.Kind == "directory"
			evidenceRemoved = evidenceRemoved || event.Resource.Kind == "evidence"
		}
	}
	if !dataRemoved || !evidenceRemoved {
		t.Fatal("committed per-resource outcomes missing")
	}
	summary := events[len(events)-1].Summary
	if summary.Done != 2 || summary.Unresolved+summary.Failed != 0 {
		t.Fatal(summary)
	}
	for _, id := range []string{first.Instance.ID, extra.Instance.ID} {
		state, err := h.cli.Inspect(context.Background(), id)
		if err != nil || state.Instance.Status != v1.Destroyed {
			t.Fatal("instance retained", err, state.Instance.Status)
		}
	}
	entries, err := os.ReadDir(filepath.Join(h.dir, "data"))
	if err != nil || len(entries) != 0 {
		t.Fatal("owned directory or witness retained", err, len(entries))
	}
	if _, err = os.Stat(export); err != nil {
		t.Fatal("caller export removed", err)
	}
	peer.state(other.Instance.ID, v1.RuntimeReady)
	// Deleted-path cleanup uses the recorded checkout parent, without source access.
	pending, err := h.cli.Prepare(context.Background(), v1.PrepareRequest{APIVersion: v1.Version, Plan: r.Plan})
	if err != nil {
		t.Fatal(err)
	}
	if err = os.RemoveAll(h.project); err != nil {
		t.Fatal(err)
	}
	q := v1.CleanupRequest{APIVersion: v1.Version, Contract: v1.CleanupContract, Orphans: true}
	summaryValue, err := h.cli.Cleanup(context.Background(), q, nil)
	if err != nil || summaryValue.Targets != 1 {
		t.Fatal(summaryValue, err)
	}
	q.ApplyPreview = summaryValue.Preview
	if summaryValue, err = h.cli.Cleanup(context.Background(), q, nil); err != nil || summaryValue.Done != 1 {
		t.Fatal(summaryValue, err)
	}
	h.state(pending.Instance.ID, v1.Destroyed)
}

func TestCleanupCLISignals(t *testing.T) {
	binary := buildFixtureBinary(t, t.TempDir(), "../../cmd/backlot")
	for _, signal := range []syscall.Signal{syscall.SIGINT, syscall.SIGTERM} {
		t.Run(signal.String(), func(t *testing.T) {
			s, root, config := sceneFixture(t)
			cleanupPrepared(t, s, root, config, true)
			cleanupPrepared(t, s, root, config, true)
			q := v1.CleanupRequest{APIVersion: v1.Version, Contract: v1.CleanupContract, CheckoutPath: root}
			preview, err := s.cleanupPreview(context.Background(), q)
			if err != nil {
				t.Fatal(err)
			}
			listener, err := net.Listen("unix", SocketPath(s.directory))
			if err != nil {
				t.Fatal(err)
			}
			if err = os.Chmod(SocketPath(s.directory), 0600); err != nil {
				t.Fatal(err)
			}
			server := &http.Server{Handler: handler(s, v1.DaemonStatusResponse{}, func() {})}
			serveDone := make(chan error, 1)
			go func() { serveDone <- server.Serve(listener) }()
			defer func() { _ = server.Close(); <-serveDone }()
			lock := s.mutationLock(preview.targets[0].target.ID)
			lock.Lock()
			defer lock.Unlock()
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, binary, "checkout", "destroy", "--checkout", root, "--state-dir", s.directory, "--apply-preview", preview.digest, "--json")
			var diagnostic bytes.Buffer
			cmd.Stderr = &diagnostic
			stdout, err := cmd.StdoutPipe()
			if err != nil {
				t.Fatal(err)
			}
			if err = cmd.Start(); err != nil {
				t.Fatal(err)
			}
			joined := false
			defer func() {
				if !joined {
					_ = cmd.Process.Kill()
					_ = cmd.Wait()
				}
			}()
			scanner := bufio.NewScanner(stdout)
			records := []v1.CleanupRecord{}
			sent := false
			for scanner.Scan() {
				var record v1.CleanupRecord
				if err = json.Unmarshal(scanner.Bytes(), &record); err != nil || record.Validate() != nil {
					t.Fatal("invalid signal flow", string(scanner.Bytes()), err)
				}
				records = append(records, record)
				if record.Type == "progress" && !sent {
					if err = cmd.Process.Signal(signal); err != nil {
						t.Fatal(err)
					}
					sent = true
				}
			}
			if err = scanner.Err(); err != nil {
				t.Fatal(err)
			}
			err = cmd.Wait()
			joined = true
			if err == nil || ctx.Err() != nil || !sent || !strings.Contains(diagnostic.String(), "Cancellation acknowledged") {
				t.Fatal("signal cancellation not acknowledged", err, ctx.Err(), diagnostic.String())
			}
			last := records[len(records)-1]
			if last.Type != "summary" || !last.Summary.Cancelled || last.Summary.Done != 0 || last.Summary.Unattempted != 1 || last.Summary.Unresolved != 1 {
				t.Fatal(last)
			}
		})
	}
}

func TestCleanupSharedAllocationIsolation(t *testing.T) {
	f := newSharedMariaDBFixture(t, true)
	one := f.run("host", f.h.project).Instance
	two := f.run("host", f.linked).Instance
	removed := f.credential(t, one.ID, "native")
	kept := f.credential(t, two.ID, "native")
	witness := independentSharedWitness(t, f.db(t, kept))
	before := f.shared(t)
	q := v1.CleanupRequest{APIVersion: v1.Version, Contract: v1.CleanupContract, CheckoutPath: f.h.project}
	preview, err := f.h.cli.Cleanup(context.Background(), q, nil)
	if err != nil {
		t.Fatal(err)
	}
	q.ApplyPreview = preview.Preview
	out, err := f.h.cli.Cleanup(context.Background(), q, nil)
	if err != nil || out.Done != 1 {
		t.Fatal(out, err)
	}
	after := f.shared(t)
	if after.ID != before.ID || after.Status != "running" || after.Allocations != 1 {
		t.Fatal("shared server or sibling allocation changed", after)
	}
	f.requireAbsent(t, removed)
	verifySharedWitness(t, f.db(t, kept), witness)
}

func TestCleanupCLICancelsBeforeAcceptance(t *testing.T) {
	binary := buildFixtureBinary(t, t.TempDir(), "../../cmd/backlot")
	s, root, config := sceneFixture(t)
	instance := cleanupPrepared(t, s, root, config, false)
	q := v1.CleanupRequest{APIVersion: v1.Version, Contract: v1.CleanupContract, CheckoutPath: root}
	preview, err := s.cleanupPreview(context.Background(), q)
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("unix", SocketPath(s.directory))
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Chmod(SocketPath(s.directory), 0600); err != nil {
		t.Fatal(err)
	}
	entered := make(chan struct{}, 1)
	realHandler := handler(s, v1.DaemonStatusResponse{}, func() {})
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case entered <- struct{}{}:
		default:
		}
		realHandler.ServeHTTP(w, r)
	})}
	served := make(chan error, 1)
	go func() { served <- server.Serve(listener) }()
	defer func() { _ = server.Close(); <-served }()
	s.store.bindingGate.Lock()
	locked := true
	defer func() {
		if locked {
			s.store.bindingGate.Unlock()
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, "checkout", "destroy", "--checkout", root, "--state-dir", s.directory, "--apply-preview", preview.digest, "--json")
	var out, diagnostic bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &diagnostic
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	joined := false
	defer func() {
		if !joined {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	}()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("apply request did not arrive")
	}
	if err = cmd.Process.Signal(syscall.SIGINT); err != nil {
		t.Fatal(err)
	}
	err = cmd.Wait()
	joined = true
	if err == nil || ctx.Err() != nil || !strings.Contains(diagnostic.String(), "before observed acceptance") {
		t.Fatal("pending application did not cancel", err, ctx.Err(), diagnostic.String(), out.String())
	}
	s.store.bindingGate.Unlock()
	locked = false
	shutdown, finish := context.WithTimeout(context.Background(), 5*time.Second)
	defer finish()
	if err = server.Shutdown(shutdown); err != nil {
		t.Fatal(err)
	}
	current, err := s.store.inspect(instance.ID)
	if err != nil || current.Status != v1.Prepared {
		t.Fatal("unaccepted cleanup had effects", err, current.Status)
	}
}

func TestCleanupRuntimeResourceOutcomes(t *testing.T) {
	endpoint := os.Getenv("BACKLOT_DOCKER_TEST_ENDPOINT")
	if endpoint == "" {
		t.Skip("requires explicit Docker endpoint and pre-pulled alpine:3.21")
	}
	binary := buildFixtureBinary(t, t.TempDir(), "../../cmd/backlot")
	for _, failure := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "later-data-failure"}[failure], func(t *testing.T) {
			h := newDockerHarness(t, binary, endpoint)
			h.alpine()
			server := h.manifest.Components["server"]
			server.Fixtures = map[string]v1.Fixture{"directory": {Value: fixtureRef("resource", "directory", "path")}}
			h.manifest.Components["server"] = server
			h.write()
			instance := h.run("dev").Instance
			h.state(instance.ID, v1.RuntimeReady)
			var restoreProof func() error
			if failure {
				fixtures, err := h.cli.Fixtures(context.Background(), v1.FixturesRequest{APIVersion: v1.Version, InstanceID: instance.ID, Component: "server"})
				if err != nil || len(fixtures.Fixtures) != 1 {
					t.Fatal(fixtures, err)
				}
				marker := filepath.Join(fixtures.Fixtures[0].Value, ".backlot-owner")
				proof, err := os.ReadFile(marker)
				if err != nil {
					t.Fatal(err)
				}
				// Restore only this fixture's exact proof before harness reconciliation.
				restoreProof = func() error { return os.WriteFile(marker, proof, 0600) }
				t.Cleanup(func() {
					if err := restoreProof(); err != nil && !os.IsNotExist(err) {
						t.Error(err)
					}
				})
				if err = os.WriteFile(marker, []byte("injected-uncertainty"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			q := v1.CleanupRequest{APIVersion: v1.Version, Contract: v1.CleanupContract, CheckoutPath: h.project}
			var reviewed []v1.CleanupResource
			preview, err := h.cli.Cleanup(context.Background(), q, func(record v1.CleanupRecord) error {
				if record.Target != nil {
					reviewed = append(reviewed, record.Target.Resources...)
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			kinds := map[string]bool{}
			for _, resource := range reviewed {
				kinds[resource.Kind] = true
			}
			if !kinds["container"] || !kinds["network"] {
				t.Fatal("fixture lacks runtime resources", reviewed)
			}
			q.ApplyPreview = preview.Preview
			var records []v1.CleanupRecord
			out, err := h.cli.Cleanup(context.Background(), q, func(record v1.CleanupRecord) error {
				records = append(records, record)
				return nil
			})
			if failure {
				code(t, err, "partial_failure")
				if out.Failed != 1 {
					t.Fatal(out)
				}
			} else if err != nil || out.Done != 1 {
				t.Fatal(out, err)
			}
			requireCleanupRuntimeOutcomes(t, records, reviewed)
			if failure {
				if err := restoreProof(); err != nil {
					t.Fatal(err)
				}
				if _, err := h.cli.Destroy(context.Background(), instance.ID); err != nil {
					t.Fatal("exact retry after restored fixture proof", err)
				}
			}
		})
	}
}
