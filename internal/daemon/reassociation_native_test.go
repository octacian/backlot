//go:build darwin || linux

package daemon

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	v1 "github.com/octacian/backlot/api/v1"
	"github.com/octacian/backlot/internal/native"
	bolt "go.etcd.io/bbolt"
)

func repairNativeTarget(t *testing.T, f *repairFixture, peer bool) v1.Instance {
	t.Helper()
	if !peer {
		return f.instance
	}
	git(t, f.project, "add", "backlot.json")
	git(t, f.project, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "-qm", "Native repair fixture")
	path := filepath.Join(filepath.Dir(f.project), "peer")
	git(t, f.project, "worktree", "add", "-q", "-b", "peer", path)
	prepared, err := f.s.prepare(context.Background(), request(path, f.config, "dev"))
	if err != nil {
		t.Fatal(err)
	}
	return prepared.Instance
}

func TestReassociationH1PreviousBootRecovery(t *testing.T) {
	for _, scenario := range []struct {
		name       string
		peer       bool
		confidence v1.NativeCleanupConfidence
		reason     string
	}{
		{"selected-incomplete", false, v1.CleanupIncomplete, "cleanup_incomplete"},
		{"peer-authority-lost", true, v1.CleanupAuthorityLost, "guardian_authority_lost"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			f := newRepairFixture(t)
			target := repairNativeTarget(t, f, scenario.peer)
			boot, err := native.CurrentBoot()
			if err != nil {
				t.Fatal(err)
			}
			old := "00000000-0000-0000-0000-000000000001"
			if old == boot {
				old = "00000000-0000-0000-0000-000000000002"
			}
			attempt := newID()
			result := &v1.ExecutionResult{Attempt: attempt, TerminalExitCode: new(17), CollectionFailure: "original collection gap", CleanupFailure: "original incomplete cleanup", Components: []v1.ComponentResult{{Name: "job", Status: "unknown"}}, NativeCleanup: []v1.NativeCleanupResult{{Component: "job", Attempt: attempt, Confidence: scenario.confidence, Reason: scenario.reason, Detail: "Original unresolved observation."}}}
			if _, err := f.s.store.execution(target.ID, v1.RuntimeReady, result); err != nil {
				t.Fatal(err)
			}
			control := filepath.Join(shortTemp(t), "control.sock")
			listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: control, Net: "unix"})
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = listener.Close() }()
			identity := native.Identity{Protocol: 1, Purpose: "application", Component: "job", Attempt: attempt, PID: os.Getpid(), Birth: "synthetic-earlier-boot", Boot: old, Token: strings.Repeat("b", 64), Control: control}
			if err := f.s.store.saveRuntime(target.ID, runtimeRecord{Attempt: attempt, Groups: []native.Identity{identity}, LaunchPending: true, PendingBoot: old}); err != nil {
				t.Fatal(err)
			}
			if err := f.s.store.recoverExecution(target.ID); err != nil {
				t.Fatal(err)
			}
			recovered, err := f.s.store.inspect(target.ID)
			if err != nil {
				t.Fatal(err)
			}
			cleanup := recovered.Execution.NativeCleanup[0]
			if recovered.Status != v1.Interrupted || cleanup.Confidence != scenario.confidence || cleanup.ResourceRelease != "previous_boot" || cleanup.FencesResources(nil) || recovered.Execution.CleanupFailure != "" || recovered.Execution.CollectionFailure != result.CollectionFailure || *recovered.Execution.TerminalExitCode != 17 {
				t.Fatal("ordinary recovery did not retain history with independent release")
			}
			f.move(t, "moved")
			before := stateContents(t, f.s.store)
			p := f.preview(t)
			if len(p.Blockers) != 0 || !reflect.DeepEqual(before, stateContents(t, f.s.store)) {
				t.Fatal("earlier-boot repair blocked or preview mutated state", p.Blockers)
			}
			if err := f.apply(t, p); err != nil {
				t.Fatal(err)
			}
			if stateContents(t, f.s.store)["instances"][target.ID] != before["instances"][target.ID] {
				t.Fatal("repair rewrote confidence, collection gap or recorded history")
			}
			if err := listener.SetDeadline(time.Now().Add(15 * time.Millisecond)); err != nil {
				t.Fatal(err)
			}
			connection, acceptErr := listener.AcceptUnix()
			if connection != nil {
				_ = connection.Close()
				t.Fatal("earlier-boot recovery/preview/apply contacted native control")
			}
			if err, ok := acceptErr.(net.Error); !ok || !err.Timeout() {
				t.Fatal("native control absence observation failed", acceptErr)
			}
		})
	}
}

func TestReassociationH1AbandonmentBootProof(t *testing.T) {
	boot, err := native.CurrentBoot()
	if err != nil {
		t.Fatal(err)
	}
	old := "00000000-0000-0000-0000-000000000001"
	if old == boot {
		old = "00000000-0000-0000-0000-000000000002"
	}
	for _, scenario := range []struct {
		name, boot  string
		peer, mixed bool
	}{
		{"selected-earlier", old, false, false}, {"peer-earlier", old, true, false},
		{"current-waiver", boot, false, false}, {"peer-current-waiver", boot, true, false},
		{"missing-boot", "", false, false}, {"malformed-boot", "invalid", false, false},
		{"mixed-history", old, true, true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			f := newRepairFixture(t)
			target := repairNativeTarget(t, f, scenario.peer)
			if err := f.s.store.db.Update(func(tx *bolt.Tx) error {
				r, err := load(tx, target.ID)
				if err != nil {
					return err
				}
				attempt := newID()
				original := &v1.ExecutionResult{Attempt: attempt, CollectionFailure: "historical collection gap", CleanupFailure: "original cleanup uncertainty", NativeCleanup: []v1.NativeCleanupResult{{Component: "job", Attempt: attempt, Confidence: v1.CleanupIncomplete, Reason: "cleanup_incomplete", Detail: "Original waived observation."}}}
				latest := *original
				latest.CleanupFailure = ""
				r.Instance.Execution = &latest
				r.Instance.Status, r.Instance.Operation.Status = v1.Stopped, v1.Stopped
				r.NativeAbandonments = []nativeAbandonment{{Attempt: attempt, RecordedAt: time.Now().UTC().Format(time.RFC3339Nano), Reason: nativeWaiverWarning, Boot: scenario.boot, Pending: true, Result: original}}
				if scenario.mixed {
					a := r.NativeAbandonments[0]
					a.Boot = boot
					r.NativeAbandonments = append(r.NativeAbandonments, a)
				}
				return put(tx, "instances", target.ID, r)
			}); err != nil {
				t.Fatal(err)
			}
			allowed := scenario.boot == old && !scenario.mixed
			// Pending unidentified work makes the current/unknown branches refuse without actuation.
			guard := f.s.store.guardAbandonedData(target.ID)
			if (guard == nil) != allowed {
				t.Fatal("fixture disagrees with inherited independent-absence guard", guard)
			}
			f.move(t, "moved")
			before := stateContents(t, f.s.store)
			p := f.preview(t)
			if (len(p.Blockers) == 0) != allowed || !reflect.DeepEqual(before, stateContents(t, f.s.store)) {
				t.Fatal("boot eligibility disagreed or preview mutated history", p.Blockers)
			}
			if allowed {
				if err := f.apply(t, p); err != nil {
					t.Fatal(err)
				}
				if stateContents(t, f.s.store)["instances"][target.ID] != before["instances"][target.ID] {
					t.Fatal("repair erased abandonment warning or collection history")
				}
				current, err := f.s.store.inspect(target.ID)
				if err != nil || len(current.NativeOwnershipWarnings) != 1 || current.Execution.CollectionFailure != "historical collection gap" || current.Execution.NativeCleanup[0].Confidence != v1.CleanupIncomplete {
					t.Fatal("public inspection lost historical warning, gap or confidence", err)
				}
			} else {
				code(t, f.apply(t, p), "reassociation_blocked")
				if !reflect.DeepEqual(before, stateContents(t, f.s.store)) {
					t.Fatal("refusal changed history")
				}
			}
		})
	}
}

func TestReassociationH1UncertainCleanupNotWaived(t *testing.T) {
	for _, kind := range []string{"current", "missing", "malformed", "waiver-only", "other-attempt-proof", "invalid-release"} {
		t.Run(kind, func(t *testing.T) {
			f := newRepairFixture(t)
			attempt := newID()
			boot, err := native.CurrentBoot()
			if err != nil {
				t.Fatal(err)
			}
			observation := v1.NativeCleanupResult{Component: "job", Attempt: attempt, Confidence: v1.CleanupIncomplete, Reason: "cleanup_incomplete", Detail: "Unresolved observation."}
			switch kind {
			case "missing":
				boot = ""
			case "malformed":
				boot = "invalid"
			case "invalid-release":
				observation.Confidence, observation.Reason, observation.ResourceRelease = v1.ObservedEmpty, "session_observed_empty", "previous_boot"
			}
			if _, err := f.s.store.execution(f.instance.ID, v1.Stopped, &v1.ExecutionResult{Attempt: attempt, NativeCleanup: []v1.NativeCleanupResult{observation}}); err != nil {
				t.Fatal(err)
			}
			if err := f.s.store.saveRuntime(f.instance.ID, runtimeRecord{Attempt: attempt, Groups: []native.Identity{{Boot: boot}}}); err != nil {
				t.Fatal(err)
			}
			if kind == "waiver-only" || kind == "other-attempt-proof" {
				acknowledgementBoot, acknowledgedAttempt := boot, attempt
				if kind == "other-attempt-proof" {
					acknowledgementBoot = "00000000-0000-0000-0000-000000000001"
					if acknowledgementBoot == boot {
						acknowledgementBoot = "00000000-0000-0000-0000-000000000002"
					}
					acknowledgedAttempt = newID()
				}
				if err := f.s.store.db.Update(func(tx *bolt.Tx) error {
					r, err := load(tx, f.instance.ID)
					if err != nil {
						return err
					}
					r.NativeAbandonments = []nativeAbandonment{{Attempt: acknowledgedAttempt, RecordedAt: time.Now().UTC().Format(time.RFC3339Nano), Reason: nativeWaiverWarning, Boot: acknowledgementBoot, Pending: true}}
					if kind == "waiver-only" && observation.FencesResources(abandonmentWarnings(r.NativeAbandonments)) {
						t.Fatal("waiver fixture did not discharge ordinary lifecycle fence")
					}
					return put(tx, "instances", f.instance.ID, r)
				}); err != nil {
					t.Fatal(err)
				}
			}
			f.move(t, "moved")
			before := stateContents(t, f.s.store)
			p := f.preview(t)
			if !strings.Contains(strings.Join(p.Blockers, "\n"), "native consumer cleanup remains uncertain") {
				t.Fatal("repair accepted unresolved or waiver-only cleanup", p.Blockers)
			}
			code(t, f.apply(t, p), "reassociation_blocked")
			if !reflect.DeepEqual(before, stateContents(t, f.s.store)) {
				t.Fatal("uncertain cleanup preview/refusal changed state")
			}
		})
	}
}
