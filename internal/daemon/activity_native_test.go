//go:build darwin || linux

package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	v1 "github.com/octacian/backlot/api/v1"
	"github.com/octacian/backlot/internal/gateway"
)

// This is deliberately the first feature gate: real public CLI, daemon, native
// cleanup and retained disposable data, before broader concurrency coverage.
func TestActivityBulkPublicNative(t *testing.T) {
	binary := buildFixtureBinary(t, t.TempDir(), "../../cmd/backlot")
	for _, mode := range []string{"success", "collection-partial", "kept"} {
		partial := mode == "collection-partial"
		t.Run(mode, func(t *testing.T) {
			h := newNativeHarness(t, binary, "30s")
			originalServer := h.manifest.Components["server"]
			h.manifest.Tools["sh"] = "/bin/sh"
			h.manifest.Resources = map[string]v1.Resource{"data": {Kind: "directory"}, "password": {Kind: "secret"}}
			ref := func(name, field string) v1.Value {
				return v1.Value{Ref: &v1.Reference{Kind: "resource", Name: name, Field: field}}
			}
			job := v1.Component{Kind: v1.Job, Runtime: v1.Native, Policy: v1.EachStart, Resources: []string{"data", "password"}, Command: &v1.Command{Tool: "sh", Args: v1.StringArguments("-c", "echo retained > \"$DATA/marker\"; echo started; exec sleep 60")}, Environment: v1.Environment{Assign: map[string]v1.Value{"DATA": ref("data", "path")}}, Fixtures: map[string]v1.Fixture{"data": {Value: ref("data", "path")}, "password": {Value: ref("password", "value")}}}
			if partial {
				job.Artifacts = map[string]v1.ArtifactSource{"missing": {Path: "absent-report"}}
			}
			h.manifest.Components = map[string]v1.Component{"job": job}
			h.manifest.Scenes = map[string]v1.Scene{"test": {Lifetime: v1.Disposable, Components: []string{"job"}, Resources: []string{"data", "password"}, TerminalJob: "job"}}
			if mode == "kept" {
				job.Command = &v1.Command{Tool: "sh", Args: v1.StringArguments("-c", "echo retained > \"$DATA/marker\"; echo started; exit 23")}
				job.DependsOn = []v1.Dependency{{Component: "server", Condition: v1.Ready}}
				h.manifest.Resources["port"] = v1.Resource{Kind: "port"}
				h.manifest.Components["server"] = originalServer
				h.manifest.Components["job"] = job
				scene := h.manifest.Scenes["test"]
				scene.Components = []string{"server", "job"}
				scene.Resources = append(scene.Resources, "port")
				h.manifest.Scenes["test"] = scene
			}
			h.write()
			q := h.request("test")
			q.Options.KeepOnFailure = true
			accepted, err := h.cli.Run(context.Background(), q)
			if err != nil {
				t.Fatal(err)
			}
			id := accepted.Instance.ID
			var peer *nativeHarness
			var peerID, peerAttempt string
			if mode == "success" {
				peer = newNativeHarness(t, binary, "30s")
				p := peer.run("dev")
				peer.state(p.Instance.ID, v1.RuntimeReady)
				peerID = p.Instance.ID
				peerAttempt = p.Instance.Execution.Attempt
			}
			waitComponentLog(t, h, id, "job", "started")
			if mode == "kept" {
				kept := h.state(id, v1.Failed)
				if !kept.Instance.Execution.Kept {
					t.Fatal("fixture never kept services")
				}
			}
			fixtures, err := h.cli.Fixtures(context.Background(), v1.FixturesRequest{APIVersion: v1.Version, InstanceID: id})
			if err != nil {
				t.Fatal(err)
			}
			path := ""
			for _, f := range fixtures.Fixtures {
				if f.Name == "data" {
					path = f.Value
				}
			}
			secret, err := h.cli.FixtureSecret(context.Background(), v1.FixturesRequest{APIVersion: v1.Version, InstanceID: id, Component: "job", SecretName: "password"})
			if err != nil {
				t.Fatal(err)
			}
			run := func(args ...string) ([]byte, error) {
				command := exec.Command(binary, args...)
				command.Dir = h.project
				return command.Output()
			}
			data, err := run("activity", "--state-dir", h.dir, "--json")
			if err != nil {
				t.Fatal(err, string(data))
			}
			var activity v1.ActivityResponse
			if json.Unmarshal(data, &activity) != nil || activity.Validate() != nil || len(activity.Entries) != 1 || activity.Entries[0].ID != id || activity.Entries[0].Owner != map[bool]string{true: "joined", false: "running"}[mode == "kept"] {
				t.Fatalf("activity: %s", data)
			}
			if strings.Contains(string(data), secret.Fixtures[0].Value) {
				t.Fatal("activity leaked private fixture")
			}
			command := exec.Command(binary, "stop", "--all", "--state-dir", h.dir, "--json")
			command.Dir = h.project
			stream, err := command.StdoutPipe()
			if err != nil {
				t.Fatal(err)
			}
			if err := command.Start(); err != nil {
				t.Fatal(err)
			}
			decoder := json.NewDecoder(stream)
			stopped := v1.StopAllResponse{Targets: []v1.StopAllTarget{}}
			var kinds []string
			for {
				var event v1.StopAllRecord
				if e := decoder.Decode(&event); errors.Is(e, io.EOF) {
					break
				} else if e != nil || event.Validate() != nil {
					t.Fatalf("stream: %v %#v", e, event)
				}
				kinds = append(kinds, event.Type)
				stopped.APIVersion, stopped.OperationID, stopped.DaemonGeneration = event.APIVersion, event.OperationID, event.DaemonGeneration
				if event.Target != nil {
					stopped.Targets = append(stopped.Targets, *event.Target)
				}
				if summary := event.Summary; summary != nil {
					stopped.AdmissionsJoined, stopped.SelectionComplete, stopped.SelectionFailure, stopped.Error = summary.AdmissionsJoined, summary.SelectionComplete, summary.SelectionFailure, summary.Error
				}
			}
			err = command.Wait()
			if !reflect.DeepEqual(kinds, []string{"accepted", "target", "summary"}) || stopped.Validate() != nil || len(stopped.Targets) != 1 || stopped.Targets[0].Settlement != v1.ConsumerSettled {
				t.Fatalf("bulk: %v %#v %#v", err, kinds, stopped)
			}
			if (err != nil) != partial || (stopped.Error != nil) != partial || stopped.Targets[0].CollectionFailed != partial {
				t.Fatalf("partial framing: %v %#v", err, stopped)
			}
			if stopped.Targets[0].Attempt != accepted.Instance.Execution.Attempt {
				t.Fatal("wrong selected attempt")
			}
			if peer != nil {
				p, err := peer.cli.Inspect(context.Background(), peerID)
				if err != nil || p.Instance.Status != v1.RuntimeReady || p.Instance.Execution.Attempt != peerAttempt {
					t.Fatal("bulk crossed state scope", err)
				}
				connection, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", p.Instance.Execution.Ports["port"]), time.Second)
				if err != nil {
					t.Fatal("peer listener lost", err)
				}
				_ = connection.Close()
			}
			final, err := h.cli.Inspect(context.Background(), id)
			if err != nil || final.Instance.Execution.Kept {
				t.Fatal("keep was not suppressed", err)
			}
			if marker, err := os.ReadFile(filepath.Join(path, "marker")); err != nil || string(marker) != "retained\n" {
				t.Fatal("disposable data lost", err)
			}
			after, err := h.cli.FixtureSecret(context.Background(), v1.FixturesRequest{APIVersion: v1.Version, InstanceID: id, Component: "job", SecretName: "password"})
			if err != nil || after.Fixtures[0].Value != secret.Fixtures[0].Value {
				t.Fatal("paired credentials changed", err)
			}
			if mode == "kept" && (final.Instance.Execution.Outcome != v1.Failed || final.Instance.Execution.TerminalExitCode == nil || *final.Instance.Execution.TerminalExitCode != 23) {
				t.Fatal("kept history changed")
			}
			if partial && final.Instance.Execution.CollectionFailure == "" {
				t.Fatal("historical collection failure lost")
			}
			if _, err := h.cli.Destroy(context.Background(), id); err != nil {
				t.Fatal("authenticated fixture cleanup", err)
			}
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Fatal("explicit fixture destroy failed", err)
			}
			empty, err := h.cli.Activity(context.Background(), v1.ActivityRequest{APIVersion: v1.Version})
			if err != nil || len(empty.Entries) != 0 || !empty.Complete {
				t.Fatal("erased history reported as current work", err)
			}
			t.Logf("public CLI flow: partial=%t outcome=%s settlement=%s data+credentials retained", partial, stopped.Targets[0].Outcome, stopped.Targets[0].Settlement)
		})
	}
}

func TestBulkOwnerlessAcceptedRecovery(t *testing.T) {
	s, id := historyFixture(t)
	journal := runtimeRecord{Attempt: newID(), NativeNotStarted: true}
	result := &v1.ExecutionResult{Attempt: journal.Attempt, Components: []v1.ComponentResult{}, CollectionFailure: "historical collection gap"}
	if _, err := s.store.acceptExecution(id, journal, result, false); err != nil {
		t.Fatal(err)
	}
	out, err := s.stopAll(context.Background(), bulkRequest())
	checkBulk(t, out, err)
	if len(out.Targets) != 1 || out.Targets[0].Settlement != v1.ConsumerSettled || !out.Targets[0].CollectionFailed || out.Error == nil {
		t.Fatal(out)
	}
	observed, err := s.store.inspect(id)
	if err != nil || observed.Status != v1.Interrupted || observed.Execution.Outcome != v1.Interrupted || observed.Execution.CollectionFailure != "historical collection gap" {
		t.Fatal("bulk erased interrupted history", err)
	}
}

func TestActivityExactSharedAndGatewayBlockers(t *testing.T) {
	f, r, j, g := settledConsumerFixture(t)
	j.Groups = nil
	r.Instance.Execution.NativeCleanup = nil
	saveConsumerFixture(t, f, r, j, g)
	if err := f.s.store.recordConsumerRelease(context.Background(), r.Instance.ID, j, v1.SettlementExplicitStop, true); err != nil {
		t.Fatal(err)
	}
	shared := sharedLockRecord(newID())
	shared.LastTransition = &v1.SharedTransition{From: "running", To: "stopped", Cause: "explicit_shared_stop", At: time.Now().UTC().Format(time.RFC3339Nano)}
	if err := f.s.store.saveSharedFixture(shared); err != nil {
		t.Fatal(err)
	}
	private := newID()
	allocation := sharedAllocation{ServiceID: shared.ID, Request: v1.ManagerRequest{Version: v1.ManagerProtocolVersion, Operation: "allocate", AllocationID: newID(), Ownership: private, Database: "bl" + private[:32], Username: "bl" + private[:32], Password: private}}
	if err := f.s.store.saveSharedAllocations(r.Instance.ID, map[string]sharedAllocation{"db": allocation}); err != nil {
		t.Fatal(err)
	}
	before := stateContents(t, f.s.store)
	page, err := f.s.activity(context.Background(), v1.ActivityRequest{APIVersion: v1.Version})
	if err != nil || len(page.Entries) != 2 {
		t.Fatal(page, err)
	}
	server, consumer := page.Entries[0], page.Entries[1]
	if server.LastTransition == nil || *server.LastTransition != *shared.LastTransition || len(consumer.Blockers) != 1 || consumer.Blockers[0].ServiceID != shared.ID || consumer.Blockers[0].Stop || !consumer.Blockers[0].Destroy {
		t.Fatal("allocation-only blocker inaccurate", page)
	}
	count, err := f.s.store.sharedDependents(shared.ID, false)
	if err != nil || count != 0 {
		t.Fatal("stop diagnosis differs from guard", count, err)
	}
	count, err = f.s.store.sharedDependents(shared.ID, true)
	if err != nil || count != 1 {
		t.Fatal("destroy diagnosis differs from guard", count, err)
	}
	raw, err := json.Marshal(page)
	if err != nil || strings.Contains(string(raw), private) || strings.Contains(string(raw), shared.AdminPassword) {
		t.Fatal("read leaked private authority", err)
	}
	if !reflect.DeepEqual(before, stateContents(t, f.s.store)) {
		t.Fatal("read changed transition or allocation")
	}
	// The real gateway guard and diagnostic share exact route-only recovery behavior.
	r.Instance.Plan.Publish = &v1.Publication{}
	r.Instance.Execution.CleanupFailure = "route removal failed"
	j, err = f.s.store.runtime(r.Instance.ID)
	if err != nil {
		t.Fatal(err)
	}
	j.Gateway = &gateway.Intent{ID: "owned-route", Endpoint: "http://127.0.0.1:1", Scope: "/config/apps/http/servers/fixture/routes", Host: "fixture.invalid", Route: json.RawMessage(`{}`)}
	j.GatewayOnlyCleanup = true
	saveConsumerFixture(t, f, r, j, g)
	if err := f.s.store.publishedActive(); err != nil {
		t.Fatal("route-only exception lost", err)
	}
	j.GatewayOnlyCleanup = false
	saveConsumerFixture(t, f, r, j, g)
	code(t, f.s.store.publishedActive(), "gateway_busy")
	page, err = f.s.activity(context.Background(), v1.ActivityRequest{APIVersion: v1.Version})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range page.Entries {
		for _, b := range e.Blockers {
			found = found || b.Kind == "gateway" && b.Stop && e.ID == r.Instance.ID
		}
	}
	if !found {
		t.Fatal("missing exact gateway blocker")
	}
}

func TestBulkAcceptedTaskJoinsBeforeParent(t *testing.T) {
	binary := buildFixtureBinary(t, t.TempDir(), "../../cmd/backlot")
	h := newNativeHarness(t, binary, "30s")
	h.manifest.Tools["sh"] = "/bin/sh"
	h.manifest.Resources["data"] = v1.Resource{Kind: "directory"}
	h.manifest.Resources["password"] = v1.Resource{Kind: "secret"}
	ref := func(name, field string) v1.Value {
		return v1.Value{Ref: &v1.Reference{Kind: "resource", Name: name, Field: field}}
	}
	server := h.manifest.Components["server"]
	server.Resources = append(server.Resources, "data", "password")
	server.Fixtures = map[string]v1.Fixture{"data": {Value: ref("data", "path")}, "password": {Value: ref("password", "value")}}
	h.manifest.Components["server"] = server
	h.manifest.Components["job"] = v1.Component{Kind: v1.Job, Runtime: v1.Native, Policy: v1.EachStart, Command: &v1.Command{Tool: "sh", Args: v1.StringArguments("-c", "echo task-retained > \"$DATA/marker\"; echo task-started; exec sleep 60")}, Resources: []string{"data", "password"}, Environment: v1.Environment{Assign: map[string]v1.Value{"DATA": ref("data", "path")}}, DependsOn: []v1.Dependency{{Component: "server", Condition: v1.Ready}}}
	h.manifest.Scenes = map[string]v1.Scene{"dev": {Lifetime: v1.Persistent, Components: []string{"server"}, Resources: []string{"port", "data", "password"}, Tasks: []string{"job"}}}
	h.write()
	parent := h.run("dev")
	h.state(parent.Instance.ID, v1.RuntimeReady)
	fixtures, err := h.cli.Fixtures(context.Background(), v1.FixturesRequest{APIVersion: v1.Version, InstanceID: parent.Instance.ID})
	if err != nil {
		t.Fatal(err)
	}
	dataPath := ""
	for _, f := range fixtures.Fixtures {
		if f.Name == "data" {
			dataPath = f.Value
		}
	}
	secret, err := h.cli.FixtureSecret(context.Background(), v1.FixturesRequest{APIVersion: v1.Version, InstanceID: parent.Instance.ID, Component: "server", SecretName: "password"})
	if err != nil {
		t.Fatal(err)
	}
	task, err := h.cli.RunTask(context.Background(), v1.TaskRunRequest{APIVersion: v1.Version, Target: v1.SceneTarget{InstanceID: parent.Instance.ID}, Task: "job", Options: v1.ExecutionOptions{StopGrace: "0s"}})
	if err != nil {
		t.Fatal(err)
	}
	waitComponentLog(t, h, task.Instance.ID, "job", "task-started")
	out, err := h.cli.StopAll(context.Background(), bulkRequest(), nil)
	if err != nil || out.Validate() != nil || len(out.Targets) != 2 || out.Targets[0].ID != task.Instance.ID || out.Targets[1].ID != parent.Instance.ID {
		t.Fatal("task ordering or settlement", out, err)
	}
	child, err := h.cli.Inspect(context.Background(), task.Instance.ID)
	if err != nil || child.Instance.Status != v1.Cancelled || child.Instance.Execution.Settlement.State != v1.ConsumerSettled {
		t.Fatal("dependent consumer did not join", err)
	}
	if data, err := os.ReadFile(filepath.Join(dataPath, "marker")); err != nil || string(data) != "task-retained\n" {
		t.Fatal("borrowed parent data lost", err)
	}
	after, err := h.cli.FixtureSecret(context.Background(), v1.FixturesRequest{APIVersion: v1.Version, InstanceID: parent.Instance.ID, Component: "server", SecretName: "password"})
	if err != nil || after.Fixtures[0].Value != secret.Fixtures[0].Value {
		t.Fatal("parent paired credential lost", err)
	}
	if _, err := h.cli.Destroy(context.Background(), parent.Instance.ID); err != nil {
		t.Fatal("authenticated parent/task cleanup", err)
	}
	if _, err := os.Stat(dataPath); !os.IsNotExist(err) {
		t.Fatal("explicit fixture cleanup retained data", err)
	}
	// Valid parent/task tombstones must not poison a later scope-wide stop.
	h.manifest.Scenes["other"] = h.manifest.Scenes["dev"]
	h.write()
	other := h.run("other")
	h.state(other.Instance.ID, v1.RuntimeReady)
	out, err = h.cli.StopAll(context.Background(), bulkRequest(), nil)
	if err != nil || out.Validate() != nil || !out.SelectionComplete || len(out.Targets) != 1 || out.Targets[0].ID != other.Instance.ID || out.Targets[0].Outcome != "stopped" {
		t.Fatal("erased scene/task tombstones blocked unrelated stop", out, err)
	}
	if _, err := h.cli.Destroy(context.Background(), other.Instance.ID); err != nil {
		t.Fatal("authenticated unrelated fixture cleanup", err)
	}

}
