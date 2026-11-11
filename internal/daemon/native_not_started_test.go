//go:build darwin || linux

package daemon

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	v1 "github.com/octacian/backlot/api/v1"
	"github.com/octacian/backlot/internal/native"
	bolt "go.etcd.io/bbolt"
)

func TestPreNativeRecoveryRequiresAcceptedBoundary(t *testing.T) {
	for _, mode := range []string{"accepted-before-intent", "legacy-empty", "lost-effect", "pending-intent"} {
		t.Run(mode, func(t *testing.T) {
			s, id := historyFixture(t)
			result := &v1.ExecutionResult{Attempt: newID(), Components: []v1.ComponentResult{}}
			journal := runtimeRecord{Attempt: result.Attempt, NativeNotStarted: mode == "accepted-before-intent"}
			if mode == "lost-effect" || mode == "pending-intent" {
				journal.LaunchPurpose = "application"
			}
			if mode == "pending-intent" {
				boot, err := native.CurrentBoot()
				if err != nil {
					t.Fatal(err)
				}
				journal.LaunchPending, journal.PendingBoot = true, boot
			}
			if _, err := s.store.acceptExecution(id, journal, result, false); err != nil {
				t.Fatal(err)
			}
			if mode != "accepted-before-intent" {
				invented := journal
				invented.NativeNotStarted = true
				if err := s.store.saveRuntime(id, invented); err == nil {
					t.Fatal("ordinary journal write invented an acceptance boundary")
				}
			}
			if err := s.store.recoverExecution(id); err != nil {
				t.Fatal(err)
			}
			got, err := s.store.inspect(id)
			if err != nil {
				t.Fatal(err)
			}
			settled := got.Execution.Settlement.State == v1.ConsumerSettled
			if settled != (mode == "accepted-before-intent") {
				t.Fatal("native absence inferred without accepted boundary", mode, got.Execution.Settlement)
			}
			if got.Status != v1.Interrupted || got.Execution.Outcome != v1.Interrupted || got.Execution.CompletedAt == "" || got.Execution.CollectionFailure == "" {
				t.Fatal("interruption history lost")
			}
			if settled && got.Execution.CleanupFailure != "" || !settled && got.Execution.CleanupFailure == "" {
				t.Fatal("cleanup diagnostic disagrees with current proof")
			}
		})
	}
}

func TestPreNativeBoundaryConsumedFactIsStale(t *testing.T) {
	s, id := historyFixture(t)
	result := &v1.ExecutionResult{Attempt: newID(), Components: []v1.ComponentResult{}}
	journal := runtimeRecord{Attempt: result.Attempt, NativeNotStarted: true}
	if _, err := s.store.acceptExecution(id, journal, result, false); err != nil {
		t.Fatal(err)
	}
	if err := s.store.recordConsumerRelease(context.Background(), id, journal, v1.SettlementExplicitStop, false); err != nil {
		t.Fatal(err)
	}
	saved, err := s.store.runtime(id)
	if err != nil {
		t.Fatal(err)
	}
	journal.Release = saved.Release
	old := journal
	// Model the persisted intent boundary, not a launch inferred from status.
	journal.NativeNotStarted = false
	journal.LaunchPurpose = "application"
	journal.LaunchPending = true
	journal.PendingBoot, err = native.CurrentBoot()
	if err != nil {
		t.Fatal(err)
	}
	if err := s.store.saveRuntime(id, journal); err != nil {
		t.Fatal(err)
	}
	if err := s.store.saveRuntime(id, old); err == nil {
		t.Fatal("stale writer restored consumed native boundary")
	}
	if err := s.store.recordConsumerRelease(context.Background(), id, old, v1.SettlementExplicitStop, false); err == nil {
		t.Fatal("stale pre-native writer published release after intent")
	}
	if err := s.store.db.View(func(tx *bolt.Tx) error {
		r, err := load(tx, id)
		if err != nil {
			return err
		}
		facts, err := consumerRecords(tx, r)
		if err != nil {
			return err
		}
		if consumerReleaseMatches(tx, facts) {
			t.Fatal("old exact fact survived native intent")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestPreNativeBoundaryRejectsMalformedAuthority(t *testing.T) {
	for _, mode := range []string{"pending", "group", "purpose", "pending-boot", "unbound", "wrong-attempt", "unaccepted", "observation"} {
		t.Run(mode, func(t *testing.T) {
			s, id := historyFixture(t)
			result := &v1.ExecutionResult{Attempt: newID(), Components: []v1.ComponentResult{{Name: "job", Status: "starting"}}}
			j := runtimeRecord{Attempt: result.Attempt, NativeNotStarted: true}
			if _, err := s.store.acceptExecution(id, j, result, false); err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "pending":
				j.LaunchPending = true
			case "group":
				j.Groups = []native.Identity{{Component: "job", Attempt: result.Attempt}}
			case "purpose":
				j.LaunchPurpose = "application"
			case "pending-boot":
				j.PendingBoot = "unknown"
			case "unbound":
				j.Attempt = ""
			case "wrong-attempt":
				j.Attempt = newID()
			case "observation":
				result.NativeCleanup = []v1.NativeCleanupResult{{Component: "job", Attempt: result.Attempt, Confidence: v1.CleanupAuthorityLost, Reason: "guardian_authority_lost", Detail: "Recorded uncertainty."}}
			}
			if err := s.store.db.Update(func(tx *bolt.Tx) error {
				if err := put(tx, "runtime", id, j); err != nil {
					return err
				}
				r, err := load(tx, id)
				if err != nil {
					return err
				}
				r.Instance.Execution = result
				if err := put(tx, "instances", id, r); err != nil {
					return err
				}
				if mode == "unaccepted" {
					return tx.Bucket([]byte("attempt_sequences")).Delete([]byte(id))
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			if err := s.store.db.View(validateStateAuthority); err == nil {
				t.Fatal("contradictory native boundary admitted", mode)
			}
		})
	}
}

func TestPreNativeBoundaryFingerprintOmission(t *testing.T) {
	f, r, j, g := settledConsumerFixture(t)
	j.Groups = nil
	r.Instance.Execution.NativeCleanup = nil
	r.Instance.Execution.CompletedAt = time.Now().UTC().Format(time.RFC3339Nano)
	saveConsumerFixture(t, f, r, j, g)
	revision := func() string {
		t.Helper()
		var h string
		if err := f.s.store.db.View(func(tx *bolt.Tx) error { var err error; h, err = consumerEffectRevision(tx, r); return err }); err != nil {
			t.Fatal(err)
		}
		return h
	}
	before := revision()
	j.NativeNotStarted = true
	// This fixture owns an accepted zero-launch attempt; ordinary journal saves
	// cannot create its acceptance witness retroactively.
	if err := f.s.store.db.Update(func(tx *bolt.Tx) error { return put(tx, "runtime", r.Instance.ID, j) }); err != nil {
		t.Fatal(err)
	}
	if revision() == before {
		t.Fatal("pre-native evidence missing from effect fingerprint")
	}
	j.NativeNotStarted = false
	if err := f.s.store.saveRuntime(r.Instance.ID, j); err != nil {
		t.Fatal(err)
	}
	if revision() != before {
		t.Fatal("absent marker changed legacy fingerprint")
	}
	raw, err := json.Marshal(j)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	if fields["native_not_started"] != nil {
		t.Fatal("absent marker was serialized as evidence")
	}
}

func TestPreNativeBoundaryRejectsPreHistoryNamespace(t *testing.T) {
	s, id := historyFixture(t)
	result := &v1.ExecutionResult{Attempt: newID(), Components: []v1.ComponentResult{}}
	if _, err := s.store.acceptExecution(id, runtimeRecord{Attempt: result.Attempt, NativeNotStarted: true}, result, false); err != nil {
		t.Fatal(err)
	}
	// This negative authority shape also occurs before history namespaces exist in
	// old migration sources; a future witness must fail without a nil-bucket panic.
	if err := s.store.db.Update(func(tx *bolt.Tx) error { return tx.DeleteBucket([]byte("attempt_sequences")) }); err != nil {
		t.Fatal(err)
	}
	if err := s.store.db.View(validateStateNineAuthority); err == nil {
		t.Fatal("unbound pre-history witness accepted")
	}
}
