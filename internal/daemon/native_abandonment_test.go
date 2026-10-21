package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"html"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
	original.NativeCleanup = []v1.NativeCleanupResult{{Component: "test", Attempt: original.Attempt, Confidence: v1.CleanupAuthorityLost, Reason: "guardian_authority_lost", Detail: "Historical guardian authority lost."}}
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
	// A sibling with its own pending intent must remain byte-for-byte unchanged.
	other := newID()
	second := instance
	second.ID = other
	if err := s.store.db.Update(func(tx *bolt.Tx) error { return put(tx, "instances", other, record{Instance: second}) }); err != nil {
		t.Fatal(err)
	}
	if err := s.store.saveRuntime(other, journal); err != nil {
		t.Fatal(err)
	}
	otherRecord := storedBytes(t, s.store, "instances", other)
	otherJournal := storedBytes(t, s.store, "runtime", other)
	forced, err := s.stopRuntimeRequest(context.Background(), v1.StopRequest{InstanceID: target, Force: true, ExpectedAttempt: original.Attempt})
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
	if len(forced.Execution.NativeCleanup) != 1 || forced.Execution.NativeCleanup[0] != original.NativeCleanup[0] || forced.Execution.NativeCleanup[0].FencesResources(forced.NativeOwnershipWarnings) {
		t.Fatal("waiver lost historical observation or kept native claim fenced")
	}
	if retained.NativeAbandonments[0].Result.NativeCleanup[0] != original.NativeCleanup[0] {
		t.Fatal("waiver rewrote original audit observation")
	}
	if _, err := s.stopRuntimeRequest(context.Background(), v1.StopRequest{InstanceID: target, Force: true, ExpectedAttempt: original.Attempt}); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(otherRecord, storedBytes(t, s.store, "instances", other)) || !bytes.Equal(otherJournal, storedBytes(t, s.store, "runtime", other)) {
		t.Fatal("waiver affected unrelated target")
	}
	if got, _ := s.store.inspect(target); len(got.NativeOwnershipWarnings) != 1 {
		t.Fatal("waiver retry duplicated history")
	}
	sentinel := filepath.Join(s.directory, "retained")
	if err := os.WriteFile(sentinel, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	code(t, s.store.removeResources(context.Background(), target), "native_ownership_relinquished")
	if data, err := os.ReadFile(sentinel); err != nil || string(data) != "keep" {
		t.Fatal("destructive waiver bypass", err)
	}
	abandoned, exportErr := s.export(context.Background(), v1.ExportRequest{APIVersion: v1.Version, InstanceID: target, Output: filepath.Join(t.TempDir(), "abandoned")})
	code(t, exportErr, "export_incomplete")
	if abandoned.Complete || abandoned.Published || len(abandoned.Result.NativeOwnershipWarnings) != 1 {
		t.Fatal("strict export hid abandoned attempt", abandoned)
	}

	// Private authority sentinels stay in retained history, never the portable report.
	// The unidentified-intent guard above and later-boot proof below never actuate them.
	privateAuthority := native.Identity{PID: 42, Token: newID(), Control: filepath.Join(s.directory, "private-control.sock"), Birth: "private-birth-sentinel"}
	if err := s.store.db.Update(func(tx *bolt.Tx) error {
		r, err := load(tx, target)
		if err != nil {
			return err
		}
		r.NativeAbandonments[0].Groups = []native.Identity{privateAuthority}
		// Synthetic hostile history fields exercise escaping through complete export.
		// The public warning message itself is a fixed product string.
		r.NativeAbandonments[0].Attempt += `<script>attempt</script>`
		r.NativeAbandonments[0].RecordedAt += `<script>time</script>`
		return put(tx, "instances", target, r)
	}); err != nil {
		t.Fatal(err)
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
	warning := v1.NativeOwnershipWarning{Message: nativeWaiverWarning, Attempt: original.Attempt + `<script>attempt</script>`, RecordedAt: retained.NativeAbandonments[0].RecordedAt + `<script>time</script>`}
	// Combined configuration/recovery reports must retain both independent facts.
	effectiveDigest := strings.Repeat("a", 64)
	if err := s.store.db.Update(func(tx *bolt.Tx) error {
		r, err := load(tx, target)
		if err != nil {
			return err
		}
		r.Instance.Plan.EffectiveConfigDigest = effectiveDigest
		return put(tx, "instances", target, r)
	}); err != nil {
		t.Fatal(err)
	}
	request := v1.ExportRequest{APIVersion: v1.Version, InstanceID: target, Output: filepath.Join(t.TempDir(), "report")}
	report, err := s.export(context.Background(), request)
	if err != nil || !report.Complete || len(report.Result.NativeOwnershipWarnings) != 1 || report.Result.NativeOwnershipWarnings[0] != warning {
		t.Fatal("latest clean export lost prior warning", report, err)
	}
	page, err := os.ReadFile(filepath.Join(request.Output, "index.html"))
	if err != nil {
		t.Fatal(err)
	}
	for _, visible := range []string{nativeWaiverWarning, html.EscapeString(warning.Attempt), html.EscapeString(warning.RecordedAt), `>Complete<`, `class="status succeeded"`} {
		if !bytes.Contains(page, []byte(visible)) {
			t.Fatalf("complete replacement HTML omitted %q", visible)
		}
	}
	jsonReport, err := os.ReadFile(filepath.Join(request.Output, "report.json"))
	if err != nil {
		t.Fatal(err)
	}
	if got := readExportReport(t, request.Output); len(got.Result.NativeOwnershipWarnings) != 1 || got.Result.NativeOwnershipWarnings[0] != warning {
		t.Fatal("agent warning text changed", got.Result.NativeOwnershipWarnings)
	}
	if got := readExportReport(t, request.Output); got.Source.EffectiveConfigDigest != effectiveDigest {
		t.Fatal("export lost effective configuration provenance", got.Source)
	}
	if bytes.Contains(page, []byte("<script")) {
		t.Fatal("ownership warning inserted unescaped markup")
	}
	view, err := json.Marshal(publicInstance(replacement))
	if err != nil {
		t.Fatal(err)
	}
	for _, private := range []string{privateAuthority.Token, privateAuthority.Control, privateAuthority.Birth} {
		if bytes.Contains(page, []byte(private)) || bytes.Contains(jsonReport, []byte(private)) || bytes.Contains(view, []byte(private)) {
			t.Fatal("portable report exposed private native authority")
		}
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

func TestManagedWaiverFencesAndIdempotency(t *testing.T) {
	s, instance, _ := exportFixture(t)
	config := manifest.CaddyDefaults(v1.CaddyConfig{Mode: "managed", Executable: "/explicit/caddy"})
	r := managedRecord{Scope: newID(), Config: config, Pending: true, Status: "uncertain"}
	if err := s.store.saveManaged(r); err != nil {
		t.Fatal(err)
	}
	r, err := s.store.managed()
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for _, action := range []string{"start", "restart", "status"} {
		_, err := s.gatewayControl(ctx, action, v1.GatewayRequest{Force: true, ExpectedClaimRevision: r.ClaimRevision})
		code(t, err, "invalid_request")
	}
	_, err = s.gatewayControl(ctx, "stop", v1.GatewayRequest{})
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
	_, err = s.gatewayControl(ctx, "stop", v1.GatewayRequest{Force: true, ExpectedClaimRevision: r.ClaimRevision})
	code(t, err, "gateway_busy")
	if _, err := s.store.execution(instance.ID, v1.Stopped, instance.Execution); err != nil {
		t.Fatal(err)
	}
	forced, err := s.gatewayControl(ctx, "stop", v1.GatewayRequest{Force: true, ExpectedClaimRevision: r.ClaimRevision})
	if err != nil || len(forced.NativeOwnershipWarnings) != 1 {
		t.Fatal(forced, err)
	}
	if _, err := s.gatewayControl(ctx, "stop", v1.GatewayRequest{Force: true, ExpectedClaimRevision: r.ClaimRevision}); err != nil {
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
