//go:build darwin || linux

package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	v1 "github.com/octacian/backlot/api/v1"
	"github.com/octacian/backlot/internal/native"
)

func TestNativeRecoveryRetainsObservations(t *testing.T) {
	binary := buildFixtureBinary(t, t.TempDir(), "../../cmd/backlot")
	for _, confidence := range []v1.NativeCleanupConfidence{v1.CleanupIncomplete, v1.CleanupAuthorityLost} {
		t.Run(string(confidence), func(t *testing.T) {
			h := newNativeHarness(t, binary)
			h.manifest.Resources["data"] = v1.Resource{Kind: "directory"}
			h.manifest.Outputs = map[string]v1.Output{"build": {Path: "build", ConcurrencyGroup: "build"}}
			server := h.manifest.Components["server"]
			server.Resources = append(server.Resources, "data")
			server.Outputs = []string{"build"}
			server.Fixtures = map[string]v1.Fixture{"data": {Value: fixtureRef("resource", "data", "path")}}
			server.Readiness = &v1.Probe{Kind: "command", Timeout: "2s", Command: &v1.Command{Tool: "fixture", Args: []string{"-test.run=TestNativeFixtureProcess", "--", "probe"}}}
			h.manifest.Components["server"] = server
			dev := h.manifest.Scenes["dev"]
			dev.Resources = append(dev.Resources, "data")
			h.manifest.Scenes["dev"] = dev
			testScene := h.manifest.Scenes["test"]
			testScene.Resources = append(testScene.Resources, "data")
			h.manifest.Scenes["test"] = testScene
			h.manifest.Scenes["peer"] = dev
			h.write()
			r := h.run("dev")
			ready := h.state(r.Instance.ID, v1.RuntimeReady).Instance
			id := ready.ID
			h.retainGuardianCleanup(id) // Both readiness and workload owners, before fault.
			original := h.guardianCapabilities(id)
			if len(original) < 2 {
				t.Fatal("readiness/workload authorities not exercised")
			}
			for _, authority := range original {
				if authority.Component != "server" || authority.Attempt != ready.Execution.Attempt {
					t.Fatal("fixture binding")
				}
			}
			fixtures, err := h.cli.Fixtures(context.Background(), v1.FixturesRequest{APIVersion: v1.Version, InstanceID: id})
			if err != nil || len(fixtures.Fixtures) != 1 {
				t.Fatal("data fixture", err, fixtures)
			}
			marker := filepath.Join(fixtures.Fixtures[0].Value, "retained")
			if err := os.WriteFile(marker, []byte("owned data"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := h.process.Process.Kill(); err != nil {
				t.Fatal(err)
			}
			_ = h.process.Wait()
			h.waited = true
			state, err := openStore(h.dir)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = state.db.Close() }()
			// Artificial persisted observation for a real unchanged attempt. The exact
			// original public fixture result and authenticated authorities are retained
			// solely for post-assertion teardown; production has no such bypass.
			prior := *ready.Execution
			observation := v1.NativeCleanupResult{Component: "server", Attempt: prior.Attempt, Confidence: confidence, Reason: "cleanup_incomplete", Detail: "Session cleanup or required collection/join could not be verified; lifecycle fences remain."}
			if confidence == v1.CleanupAuthorityLost {
				observation.Reason = "guardian_authority_lost"
				observation.Detail = "Original guardian authority or bound cleanup receipt unavailable; lifecycle fences remain."
			}
			prior.NativeCleanup = []v1.NativeCleanupResult{observation}
			prior.CleanupFailure = observation.Detail + "\nfixture transient non-native cleanup failure"
			prior.CleanupDetails = []v1.PlanError{{Code: "cleanup_failed", Message: observation.Detail}, {Code: "gateway_ownership", Message: "fixture transient non-native cleanup failure"}}
			if _, err := state.execution(id, ready.Status, &prior); err != nil {
				t.Fatal(err)
			}
			assertResult := func(result *v1.ExecutionResult) {
				t.Helper()
				if result.Attempt != prior.Attempt || len(result.NativeCleanup) != 1 || result.NativeCleanup[0] != observation || result.CleanupFailure == "" {
					t.Fatal("earlier native uncertainty lost", result)
				}
				if len(result.CleanupDetails) != 1 || result.CleanupDetails[0].Code != "cleanup_failed" || result.CleanupDetails[0].Message != observation.Detail || strings.Contains(result.CleanupFailure, "transient") {
					t.Fatal("native fence or transient retry semantics lost", result)
				}
			}
			for range 2 {
				if err := state.recoverExecution(id); err != nil {
					t.Fatal(err)
				}
				current, err := state.inspect(id)
				if err != nil {
					t.Fatal(err)
				}
				assertResult(current.Execution)
				if err := state.outputConflict(current.Plan); err == nil {
					t.Fatal("output fence released")
				}
			}
			for _, authority := range original {
				observed, err := native.StopResult(authority, 0)
				if err != nil || observed == nil || observed.Confidence != v1.ObservedEmpty {
					t.Fatal("later bound successful sibling receipt not exercised", observed, err)
				}
			}
			h.absent()
			_ = state.db.Close()
			h.start() // Third recovery through a real daemon restart and validated client.
			current, err := h.cli.Inspect(context.Background(), id)
			if err != nil {
				t.Fatal("inspect/client validation", err)
			}
			assertResult(current.Instance.Execution)
			doctor, err := h.cli.Doctor(context.Background(), v1.DoctorRequest{APIVersion: v1.Version, InstanceID: id})
			if err != nil || doctor.Healthy {
				t.Fatal("doctor uncertainty", err, doctor)
			}
			assertDoctorCheck(t, doctor, "native_cleanup", v1.DoctorFailed)
			destination := filepath.Join(t.TempDir(), "bundle")
			exported, err := h.cli.Export(context.Background(), v1.ExportRequest{APIVersion: v1.Version, InstanceID: id, Output: destination, AllowPartial: true})
			var exportError *v1.PlanError
			if !errors.As(err, &exportError) || exportError.Code != "export_active" || exported.Published {
				t.Fatal("uncertain recovered evidence escaped export fence", err, exported)
			}
			if _, err := os.Stat(destination); !os.IsNotExist(err) {
				t.Fatal("fenced export published output", err)
			}
			requireFence := func(err error) {
				t.Helper()
				var detail *v1.PlanError
				if !errors.As(err, &detail) || detail.Code != "cleanup_failed" {
					t.Fatal("expected lifecycle cleanup fence", err)
				}
			}
			_, err = h.cli.StopExecution(context.Background(), id)
			requireFence(err)
			request := h.request("dev")
			request.InstanceID = id
			_, err = h.cli.Restart(context.Background(), request)
			requireFence(err)
			_, err = h.cli.Reset(context.Background(), request)
			requireFence(err)
			_, err = h.cli.Destroy(context.Background(), id)
			requireFence(err)
			peer, err := h.cli.Run(context.Background(), h.request("peer"))
			if err != nil {
				requireFence(err)
			} else {
				failed := h.state(peer.Instance.ID, v1.Failed)
				if failed.Instance.Execution.FailureDetail == nil || failed.Instance.Execution.FailureDetail.Code != "cleanup_failed" {
					t.Fatal("output consumer launched through fence", failed)
				}
			}
			if data, err := os.ReadFile(marker); err != nil || string(data) != "owned data" {
				t.Fatal("fenced data changed", err)
			}
			current, err = h.cli.Inspect(context.Background(), id)
			if err != nil {
				t.Fatal(err)
			}
			assertResult(current.Instance.Execution)
			// The known artificial fixture state may be restored only after all policy
			// assertions and bound successful cleanup of every exact retained authority.
			for _, authority := range original {
				if err := native.Stop(authority, 0); err != nil {
					t.Fatal(err)
				}
			}
			h.expectShutdownFailureAfterCleanup("cleanup_failed", original)
			h.close()
			state, err = openStore(h.dir)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := state.execution(id, v1.Interrupted, ready.Execution); err != nil {
				t.Fatal(err)
			}
			if err := state.recoverExecution(id); err != nil {
				t.Fatal(err)
			}
			_ = state.db.Close()
			h.start()
			if _, err := h.cli.Destroy(context.Background(), id); err != nil {
				t.Fatal("authenticated fixture teardown", err)
			}
			if _, err := os.Stat(marker); !os.IsNotExist(err) {
				t.Fatal("fixture data removal", err)
			}
		})
	}
}

func TestNativeMixedRecoveryCleanupAfterFatal(t *testing.T) {
	binary := buildFixtureBinary(t, t.TempDir(), "../../cmd/backlot")
	record := filepath.Join(t.TempDir(), "record")
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, self, "-test.run=^TestNativeMixedRecoveryFailurePath$", "-test.v", "-test.timeout=40s")
	command.Env = append(os.Environ(), "BACKLOT_MIXED_FAILURE_BINARY="+binary, "BACKLOT_MIXED_FAILURE_RECORD="+record)
	output, err := command.CombinedOutput()
	if ctx.Err() != nil || err == nil || !strings.Contains(string(output), "intentional assertion after daemon kill and join") {
		t.Fatalf("intentional subprocess: %v %s", err, output)
	}
	for _, failure := range []string{"DATA RACE", "unverified", "survives acknowledged"} {
		if strings.Contains(string(output), failure) {
			t.Fatalf("cleanup failed: %s", output)
		}
	}
	data, err := os.ReadFile(record)
	if err != nil {
		t.Fatal(err)
	}
	var witnesses struct {
		Root string
		PIDs []int
	}
	if err := json.Unmarshal(data, &witnesses); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(witnesses.Root); !os.IsNotExist(err) {
		t.Fatal("owned state removed before verified cleanup", err, string(output))
	}
	if len(witnesses.PIDs) < 4 || witnesses.Root == "" {
		t.Fatal("missing retained owner/workload witnesses")
	}
	for _, pid := range witnesses.PIDs {
		if syscall.Kill(pid, 0) != syscall.ESRCH {
			t.Fatal("failure path owner/workload remains", pid)
		}
	}
	t.Log("intentional failure ran retained authenticated cleanup and joined all owners before removing state")
}

func TestNativeMixedRecoveryFailurePath(t *testing.T) {
	binary := os.Getenv("BACKLOT_MIXED_FAILURE_BINARY")
	if binary == "" {
		return
	}
	h := newNativeHarness(t, binary)
	r := h.run("dev")
	h.state(r.Instance.ID, v1.RuntimeReady)
	h.retainGuardianCleanup(r.Instance.ID)
	identities := h.guardianCapabilities(r.Instance.ID)
	data, err := os.ReadFile(h.pids)
	if err != nil {
		t.Fatal(err)
	}
	witnesses := struct {
		Root string
		PIDs []int
	}{Root: filepath.Dir(h.dir)}
	for _, identity := range identities {
		witnesses.PIDs = append(witnesses.PIDs, identity.PID)
	}
	for _, field := range strings.Fields(string(data)) {
		var pid int
		if _, err := fmt.Sscan(field, &pid); err != nil {
			t.Fatal(err)
		}
		witnesses.PIDs = append(witnesses.PIDs, pid)
	}
	data, err = json.Marshal(witnesses)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(os.Getenv("BACKLOT_MIXED_FAILURE_RECORD"), data, 0600); err != nil {
		t.Fatal(err)
	}
	if err := h.process.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = h.process.Wait()
	h.waited = true
	// Deliberately before openStore or post-fault journal acquisition.
	t.Fatal("intentional assertion after daemon kill and join")
}
