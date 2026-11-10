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
			component := h.manifest.Components["server"]
			component.Resources = append(component.Resources, "data")
			component.Outputs = []string{"build"}
			component.Fixtures = map[string]v1.Fixture{"data": {Value: fixtureRef("resource", "data", "path")}}
			component.Readiness = &v1.Probe{Kind: "command", Timeout: "2s", Command: &v1.Command{Tool: "fixture", Args: v1.StringArguments("-test.run=TestNativeFixtureProcess", "--", "probe")}}
			h.manifest.Components["server"] = component
			for name, scene := range h.manifest.Scenes {
				scene.Resources = append(scene.Resources, "data")
				h.manifest.Scenes[name] = scene
			}
			h.write()
			ready := h.state(h.run("dev").Instance.ID, v1.RuntimeReady).Instance
			id := ready.ID
			h.retainGuardianCleanup(id)
			authorities := h.guardianCapabilities(id)
			if len(authorities) < 2 {
				t.Fatal("readiness and application authorities not exercised")
			}
			fixtures, err := h.cli.Fixtures(context.Background(), v1.FixturesRequest{APIVersion: v1.Version, InstanceID: id})
			if err != nil || len(fixtures.Fixtures) != 1 {
				t.Fatal("data fixture", err)
			}
			marker := filepath.Join(fixtures.Fixtures[0].Value, "retained")
			if err := os.WriteFile(marker, []byte("owned data"), 0600); err != nil {
				t.Fatal(err)
			}
			// Withhold an ACTUAL completed readiness receipt before faulting the daemon.
			// The application's later successful receipt must not prove this sibling empty.
			receipt := ""
			for _, authority := range authorities {
				path := filepath.Join(filepath.Dir(authority.Control), "receipt.json")
				if _, err := os.Stat(path); err == nil {
					receipt = path
					break
				}
			}
			if receipt == "" {
				t.Fatal("completed readiness receipt missing")
			}
			held := receipt + ".fixture-held"
			if err := os.Rename(receipt, held); err != nil {
				t.Fatal(err)
			}
			restored := false
			defer func() {
				if !restored {
					if err := os.Rename(held, receipt); err != nil {
						t.Error("owned readiness receipt restore failed", err)
					}
				}
			}()
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
			prior := *ready.Execution
			observation := v1.NativeCleanupResult{Component: "server", Attempt: prior.Attempt, Confidence: confidence, Reason: "cleanup_incomplete", Detail: "Historical session cleanup uncertainty."}
			if confidence == v1.CleanupAuthorityLost {
				observation.Reason = "guardian_authority_lost"
			}
			prior.NativeCleanup = []v1.NativeCleanupResult{observation}
			prior.CleanupFailure = observation.Detail
			if _, err := state.execution(id, ready.Status, &prior); err != nil {
				t.Fatal(err)
			}
			if err := state.recoverExecution(id); err != nil {
				t.Fatal(err)
			}
			partial, err := state.inspect(id)
			if err != nil || partial.Execution.CleanupFailure == "" || partial.Execution.Settlement == nil || partial.Execution.Settlement.State == v1.ConsumerSettled || len(partial.Execution.NativeCleanup) != 1 || partial.Execution.NativeCleanup[0] != observation {
				t.Fatal("one successful sibling masked missing readiness authority", err)
			}
			if err := state.outputConflict(partial.Plan); err == nil {
				t.Fatal("partial native release cleared output fence")
			}
			completion, gap := partial.Execution.CompletedAt, partial.Execution.CollectionFailure
			if completion == "" || gap == "" {
				t.Fatal("interruption outcome/collection gap missing")
			}
			if err := state.db.Close(); err != nil {
				t.Fatal(err)
			}
			h.start()
			_, err = h.cli.StopExecution(context.Background(), id)
			var detail *v1.PlanError
			if !errors.As(err, &detail) || detail.Code != "cleanup_failed" {
				t.Fatal("explicit stop bypassed withheld sibling receipt", err)
			}
			request := h.request("dev")
			request.InstanceID = id
			if _, err = h.cli.Restart(context.Background(), request); err == nil {
				t.Fatal("restart bypassed partial consumer fence")
			}
			if _, err = h.cli.Destroy(context.Background(), id); err == nil {
				t.Fatal("destroy bypassed partial consumer fence")
			}
			if data, err := os.ReadFile(marker); err != nil || string(data) != "owned data" {
				t.Fatal("partial cleanup changed data", err)
			}
			if err := os.Rename(held, receipt); err != nil {
				t.Fatal(err)
			}
			restored = true
			released, err := h.cli.StopExecution(context.Background(), id)
			if err != nil {
				t.Fatal("independent all-launch proof did not release", err)
			}
			current := released.Instance.Execution
			if current == nil || current.Settlement == nil || current.Settlement.State != v1.ConsumerSettled || current.Settlement.Cause != v1.SettlementExplicitStop || current.Settlement.ObservedAt == "" || current.CleanupFailure != "" || len(current.NativeCleanup) != 1 || current.NativeCleanup[0] != observation || current.CompletedAt != completion || current.CollectionFailure != gap || released.Instance.Status != v1.Interrupted || current.Outcome != v1.Interrupted || len(released.Instance.NativeOwnershipWarnings) != 0 {
				t.Fatal("verified current release rewrote history or required force")
			}
			for _, authority := range authorities {
				observed, err := native.StopResult(authority, 0)
				if err != nil || observed == nil || observed.Confidence != v1.ObservedEmpty {
					t.Fatal("exact launch receipt not independently verified", err)
				}
			}
			h.absent()
			doctor, err := h.cli.Doctor(context.Background(), v1.DoctorRequest{APIVersion: v1.Version, InstanceID: id})
			if err != nil {
				t.Fatal(err)
			}
			assertDoctorCheck(t, doctor, "consumer_settlement", v1.DoctorOK)
			assertDoctorCheck(t, doctor, "native_cleanup", v1.DoctorFailed)
			exported, err := h.cli.Export(context.Background(), v1.ExportRequest{APIVersion: v1.Version, InstanceID: id, Output: filepath.Join(t.TempDir(), "bundle"), AllowPartial: true})
			if err != nil || !exported.Published || exported.Result.Settlement == nil || exported.Result.Settlement.State != v1.ConsumerSettled || len(exported.Result.NativeCleanup) != 1 || exported.Result.NativeCleanup[0] != observation {
				t.Fatal("strict export lost independent release/history", err)
			}
			if _, err := h.cli.Destroy(context.Background(), id); err != nil {
				t.Fatal("verified fixture destroy", err)
			}
			if _, err := os.Stat(marker); !os.IsNotExist(err) {
				t.Fatal("explicit destruction did not erase owned data", err)
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
