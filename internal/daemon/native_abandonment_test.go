package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	v1 "github.com/octacian/backlot/api/v1"
	"github.com/octacian/backlot/internal/manifest"
	"github.com/octacian/backlot/internal/native"
	bolt "go.etcd.io/bbolt"
)

func TestNativeWaiverAtomicScopeAndRetention(t *testing.T) {
	s, instance, _ := exportFixture(t)
	target := instance.ID
	if err := s.store.db.Update(func(tx *bolt.Tx) error {
		r, err := load(tx, target)
		if err != nil {
			return err
		}
		r.Instance.Plan.Lifetime = v1.Persistent
		return put(tx, "instances", target, r)
	}); err != nil {
		t.Fatal(err)
	}
	original := *instance.Execution
	original.CollectionFailure = "lost original output"
	original.CleanupFailure = "native authority missing"
	original.TerminalExitCode = new(17)
	if _, err := s.store.execution(target, v1.Failed, &original); err != nil {
		t.Fatal(err)
	}
	journal, err := s.store.runtime(target)
	if err != nil {
		t.Fatal(err)
	}
	journal.Attempt = original.Attempt
	journal.LaunchPending = true
	journal.PendingBoot = ""
	if err := s.store.saveRuntime(target, journal); err != nil {
		t.Fatal(err)
	}
	// A failed/cancelled transaction cannot erase the exact pending intent.
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.store.abandonNative(cancelled, target, original.Attempt); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	after, _ := s.store.runtime(target)
	if !after.LaunchPending {
		t.Fatal("cancelled waiver released intent")
	}
	if _, err := s.store.abandonNative(context.Background(), target, newID()); err == nil {
		t.Fatal("later attempt waived by stale request")
	}
	forced, err := s.stopRuntimeRequest(context.Background(), v1.StopRequest{InstanceID: target, Force: true})
	if err != nil || len(forced.NativeOwnershipWarnings) != 1 || forced.Execution.CollectionFailure == "" || forced.Execution.TerminalExitCode == nil || *forced.Execution.TerminalExitCode != 17 {
		t.Fatal("waiver lost outcome", forced.Execution, err)
	}
	var retained record
	if err := s.store.db.View(func(tx *bolt.Tx) error { var err error; retained, err = load(tx, target); return err }); err != nil {
		t.Fatal(err)
	}
	if len(retained.NativeAbandonments) != 1 || !retained.NativeAbandonments[0].Pending || retained.NativeAbandonments[0].Result.CleanupFailure == "" || *retained.NativeAbandonments[0].Result.TerminalExitCode != 17 {
		t.Fatal("exact private history lost")
	}
	if _, err := s.stopRuntimeRequest(context.Background(), v1.StopRequest{InstanceID: target, Force: true}); err != nil {
		t.Fatal(err)
	}
	again, _ := s.store.inspect(target)
	if len(again.NativeOwnershipWarnings) != 1 {
		t.Fatal("waiver retry duplicated history")
	}
	code(t, s.store.guardAbandonedData(target), "native_ownership_relinquished")
	abandoned, exportErr := s.export(context.Background(), v1.ExportRequest{APIVersion: v1.Version, InstanceID: target, Output: filepath.Join(t.TempDir(), "abandoned")})
	code(t, exportErr, "export_incomplete")
	if abandoned.Complete || abandoned.Published || len(abandoned.Result.NativeOwnershipWarnings) != 1 {
		t.Fatal("strict export hid abandoned attempt", abandoned)
	}

	// Replacement changes latest attempt/result, never the durable private history.
	next := &v1.ExecutionResult{Attempt: newID(), TerminalExitCode: new(0), Components: []v1.ComponentResult{{Name: "test", Status: "completed", ExitCode: new(0)}}}
	if _, err := s.store.acceptExecution(target, runtimeRecord{Attempt: next.Attempt}, next, true); err != nil {
		t.Fatal(err)
	}
	replacement, _ := s.store.inspect(target)
	if len(replacement.NativeOwnershipWarnings) != 1 || replacement.Execution.CollectionFailure != "" {
		t.Fatal("replacement warning/gap semantics")
	}
	if _, err := s.store.execution(target, v1.Succeeded, next); err != nil {
		t.Fatal(err)
	}
	request := v1.ExportRequest{APIVersion: v1.Version, InstanceID: target, Output: filepath.Join(t.TempDir(), "report")}
	report, err := s.export(context.Background(), request)
	if err != nil || !report.Complete || len(report.Result.NativeOwnershipWarnings) != 1 {
		t.Fatal("latest clean export lost prior warning", report, err)
	}
	// A later boot supplies independent absence proof, retaining the audit.
	if err := s.store.db.Update(func(tx *bolt.Tx) error {
		r, err := load(tx, target)
		if err != nil {
			return err
		}
		r.NativeAbandonments[0].Boot = "00000000-0000-0000-0000-000000000001"
		return put(tx, "instances", target, r)
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.store.guardAbandonedData(target); err != nil {
		t.Fatal("later boot proof rejected", err)
	}
	final, _ := s.store.inspect(target)
	if len(final.NativeOwnershipWarnings) != 1 {
		t.Fatal("proof erased history")
	}
}

func TestNativeWaiverFailedJournalPreservesClaim(t *testing.T) {
	s, instance, _ := exportFixture(t)
	j := runtimeRecord{Attempt: instance.Execution.Attempt, LaunchPending: true}
	if err := s.store.saveRuntime(instance.ID, j); err != nil {
		t.Fatal(err)
	}
	// Simulate failure after both transactional writes; bbolt must roll back
	// the released journal and the history/public result together.
	s.store.waiverCheckpoint = func() error { return errors.New("injected waiver commit failure") }

	if _, err := s.store.abandonNative(context.Background(), instance.ID, j.Attempt); err == nil {
		t.Fatal("failed waiver transaction claimed success")
	}
	after, _ := s.store.runtime(instance.ID)
	if !after.LaunchPending {
		t.Fatal("failed journal released intent")
	}
	kept, err := s.store.inspect(instance.ID)
	if err != nil || len(kept.NativeOwnershipWarnings) != 0 {
		t.Fatal("failed transaction retained partial acknowledgement", err)
	}
	s.store.waiverCheckpoint = nil
	if _, err := s.store.abandonNative(context.Background(), instance.ID, j.Attempt); err != nil {
		t.Fatal("retry after rollback failed", err)
	}

}

func TestManagedWaiverFencesAndIdempotency(t *testing.T) {
	s, instance, _ := exportFixture(t)
	config := manifest.CaddyDefaults(v1.CaddyConfig{Mode: "managed", Executable: "/explicit/caddy"})
	r := managedRecord{Scope: newID(), Config: config, Pending: true, Status: "uncertain"}
	if err := s.store.saveManaged(r); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for _, action := range []string{"start", "restart", "status"} {
		_, err := s.gatewayControl(ctx, action, v1.GatewayRequest{Force: true})
		code(t, err, "invalid_request")
	}
	_, err := s.gatewayControl(ctx, "stop", v1.GatewayRequest{})
	code(t, err, "gateway_ownership")
	// Active published scenes stay a fence even for force.
	if err := s.store.db.Update(func(tx *bolt.Tx) error {
		r, err := load(tx, instance.ID)
		if err != nil {
			return err
		}
		r.Instance.Plan.Publish = &v1.Publication{}
		r.Instance.Status = v1.RuntimeReady
		r.Instance.Operation.Status = v1.RuntimeReady
		return put(tx, "instances", instance.ID, r)
	}); err != nil {
		t.Fatal(err)
	}
	_, err = s.gatewayControl(ctx, "stop", v1.GatewayRequest{Force: true})
	code(t, err, "gateway_busy")
	if _, err := s.store.execution(instance.ID, v1.Stopped, instance.Execution); err != nil {
		t.Fatal(err)
	}
	forced, err := s.gatewayControl(ctx, "stop", v1.GatewayRequest{Force: true})
	if err != nil || len(forced.NativeOwnershipWarnings) != 1 {
		t.Fatal(forced, err)
	}
	if _, err := s.gatewayControl(ctx, "stop", v1.GatewayRequest{Force: true}); err != nil {
		t.Fatal(err)
	}
	got, err := s.store.managed()
	if err != nil || len(got.NativeAbandonments) != 1 || !got.NativeAbandonments[0].Pending || got.NativeAbandonments[0].Failure == "" {
		t.Fatal("private history missing", err)
	}
	// Nothing about a gateway waiver creates a scene collection outcome.
	scene, _ := s.store.inspect(instance.ID)
	if len(scene.NativeOwnershipWarnings) != 0 {
		t.Fatal("gateway warning leaked into scene")
	}
}

func TestStateFiveAdditiveTransition(t *testing.T) {
	s, instance, _ := exportFixture(t)
	path := s.store.directory
	if err := s.store.db.Update(func(tx *bolt.Tx) error {
		if err := tx.Bucket([]byte("meta")).Put([]byte("version"), []byte("5")); err != nil {
			return err
		}
		return tx.Bucket([]byte("meta")).Put([]byte("preserved"), []byte("CA/config retained"))
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.store.db.Close(); err != nil {
		t.Fatal(err)
	}
	upgraded, err := openStore(path)
	if err != nil {
		t.Fatal(err)
	}
	s.store = upgraded
	t.Cleanup(func() {
		if err := upgraded.db.Close(); err != nil {
			t.Error(err)
		}
	})
	after, err := upgraded.inspect(instance.ID)
	if err != nil || after.ID != instance.ID {
		t.Fatal(err)
	}
	if err := upgraded.db.View(func(tx *bolt.Tx) error {
		meta := tx.Bucket([]byte("meta"))
		if string(meta.Get([]byte("version"))) != "6" || !bytes.Equal(meta.Get([]byte("preserved")), []byte("CA/config retained")) {
			t.Fatal("transition changed private records or failed older-reader fence")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestScenePendingBootRecovery(t *testing.T) {
	for _, boot := range []string{"", "corrupt", "00000000-0000-0000-0000-000000000001"} {
		t.Run(boot, func(t *testing.T) {
			s, instance, _ := exportFixture(t)
			j := runtimeRecord{Attempt: instance.Execution.Attempt, LaunchPending: true, PendingBoot: boot}
			if err := s.store.saveRuntime(instance.ID, j); err != nil {
				t.Fatal(err)
			}
			if _, err := s.store.execution(instance.ID, v1.RuntimeReady, instance.Execution); err != nil {
				t.Fatal(err)
			}
			if err := s.store.recoverExecution(instance.ID); err != nil {
				t.Fatal(err)
			}
			got, _ := s.store.inspect(instance.ID)
			if (got.Execution.CleanupFailure == "") != (strings.HasPrefix(boot, "0000")) || got.Execution.CollectionFailure == "" {
				t.Fatal("pending boot proof/collection conflated", got.Execution)
			}
		})
	}
}

func TestWaiverStatusContainsNoAuthority(t *testing.T) {
	s, instance, _ := exportFixture(t)
	group := native.Identity{PID: 42, Birth: "private-birth", Token: strings.Repeat("a", 64), Control: filepath.Join(s.directory, "private-control")}
	audit := nativeAbandonment{Attempt: instance.Execution.Attempt, RecordedAt: time.Now().UTC().Format(time.RFC3339Nano), Groups: []native.Identity{group}, Reason: nativeWaiverWarning}
	if err := s.store.db.Update(func(tx *bolt.Tx) error {
		r, err := load(tx, instance.ID)
		if err != nil {
			return err
		}
		r.NativeAbandonments = []nativeAbandonment{audit}
		return put(tx, "instances", instance.ID, r)
	}); err != nil {
		t.Fatal(err)
	}
	view, _ := s.store.inspect(instance.ID)
	data, _ := json.Marshal(publicInstance(view))
	if bytes.Contains(data, []byte(group.Token)) || bytes.Contains(data, []byte(group.Control)) || bytes.Contains(data, []byte(group.Birth)) {
		t.Fatal("private authority exposed")
	}
	if len(view.NativeOwnershipWarnings) != 1 {
		t.Fatal("warning omitted")
	}
	// A rejected destructive call must preserve retained files.
	sentinel := filepath.Join(s.directory, "retained")
	if err := os.WriteFile(sentinel, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	code(t, s.store.removeResources(context.Background(), instance.ID), "native_ownership_relinquished")
	if data, err := os.ReadFile(sentinel); err != nil || string(data) != "keep" {
		t.Fatal("destructive waiver bypass", err)
	}
}

func TestNativeWaiverCorruptionPreservesIntent(t *testing.T) {
	s, instance, _ := exportFixture(t)
	journal := runtimeRecord{Attempt: instance.Execution.Attempt, LaunchPending: true}
	if err := s.store.saveRuntime(instance.ID, journal); err != nil {
		t.Fatal(err)
	}
	if err := s.store.db.Update(func(tx *bolt.Tx) error {
		return tx.Bucket([]byte("instances")).Put([]byte(instance.ID), []byte("invalid"))
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.store.abandonNative(context.Background(), instance.ID, journal.Attempt); err == nil {
		t.Fatal("corrupt record waived")
	}
	after, _ := s.store.runtime(instance.ID)
	if !after.LaunchPending {
		t.Fatal("preflight corruption erased intent")
	}
}

func TestNativeWaiverOtherTargetPreserved(t *testing.T) {
	s, instance, _ := exportFixture(t)
	other := newID()
	second := instance
	second.ID = other
	if err := s.store.db.Update(func(tx *bolt.Tx) error { return put(tx, "instances", other, record{Instance: second}) }); err != nil {
		t.Fatal(err)
	}
	j := runtimeRecord{Attempt: instance.Execution.Attempt, LaunchPending: true}
	if err := s.store.saveRuntime(instance.ID, j); err != nil {
		t.Fatal(err)
	}
	if err := s.store.saveRuntime(other, j); err != nil {
		t.Fatal(err)
	}
	before, _ := s.store.inspect(other)
	if _, err := s.store.abandonNative(context.Background(), instance.ID, j.Attempt); err != nil {
		t.Fatal(err)
	}
	after, _ := s.store.inspect(other)
	a, _ := json.Marshal(before)
	b, _ := json.Marshal(after)
	journal, _ := s.store.runtime(other)
	if !bytes.Equal(a, b) || !journal.LaunchPending {
		t.Fatal("waiver affected unrelated target")
	}
}
