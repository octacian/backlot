//go:build darwin || linux

package daemon

import (
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

func TestReassociationH1PreviousBootRecovery(t *testing.T) {
	f := newRepairFixture(t)
	target := f.preparePeer(t)
	boot, err := native.CurrentBoot()
	if err != nil {
		t.Fatal(err)
	}
	old := "00000000-0000-0000-0000-000000000001"
	if old == boot {
		old = "00000000-0000-0000-0000-000000000002"
	}
	attempt := newID()
	result := &v1.ExecutionResult{Attempt: attempt, TerminalExitCode: new(17), CollectionFailure: "original collection gap", CleanupFailure: "original incomplete cleanup", Components: []v1.ComponentResult{{Name: "job", Status: "unknown"}}, NativeCleanup: []v1.NativeCleanupResult{{Component: "job", Attempt: attempt, Confidence: v1.CleanupAuthorityLost, Reason: "guardian_authority_lost", Detail: "Original unresolved observation."}}}
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
	if recovered.Status != v1.Interrupted || cleanup.ResourceRelease != "previous_boot" || cleanup.FencesResources(nil) {
		t.Fatal("ordinary recovery did not establish independent release")
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
		{"peer-earlier", old, true, false},
		{"current-waiver", boot, false, false},
		{"mixed-history", old, true, true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			f := newRepairFixture(t)
			target := f.instance
			if scenario.peer {
				target = f.preparePeer(t)
			}
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
	for _, kind := range []string{"waiver-only", "invalid-release"} {
		t.Run(kind, func(t *testing.T) {
			f := newRepairFixture(t)
			attempt := newID()
			boot, err := native.CurrentBoot()
			if err != nil {
				t.Fatal(err)
			}
			observation := v1.NativeCleanupResult{Component: "job", Attempt: attempt, Confidence: v1.CleanupIncomplete, Reason: "cleanup_incomplete", Detail: "Unresolved observation."}
			if kind == "invalid-release" {
				observation.Confidence, observation.Reason, observation.ResourceRelease = v1.ObservedEmpty, "session_observed_empty", "previous_boot"
			}
			if _, err := f.s.store.execution(f.instance.ID, v1.Stopped, &v1.ExecutionResult{Attempt: attempt, NativeCleanup: []v1.NativeCleanupResult{observation}}); err != nil {
				t.Fatal(err)
			}
			if err := f.s.store.saveRuntime(f.instance.ID, runtimeRecord{Attempt: attempt, Groups: []native.Identity{{Boot: boot}}}); err != nil {
				t.Fatal(err)
			}
			if kind == "waiver-only" {
				if err := f.s.store.db.Update(func(tx *bolt.Tx) error {
					r, err := load(tx, f.instance.ID)
					if err != nil {
						return err
					}
					r.NativeAbandonments = []nativeAbandonment{{Attempt: attempt, RecordedAt: time.Now().UTC().Format(time.RFC3339Nano), Reason: nativeWaiverWarning, Boot: boot, Pending: true}}
					if observation.FencesResources(abandonmentWarnings(r.NativeAbandonments)) {
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
