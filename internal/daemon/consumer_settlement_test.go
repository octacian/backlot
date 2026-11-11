//go:build darwin || linux

package daemon

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	v1 "github.com/octacian/backlot/api/v1"
	"github.com/octacian/backlot/internal/gateway"
	"github.com/octacian/backlot/internal/native"
	bolt "go.etcd.io/bbolt"
)

const settlementBoot = "11111111-1111-1111-1111-111111111111"
const settlementOldBoot = "22222222-2222-2222-2222-222222222222"

// Only preparation is real; journal fixtures never launch or authorize fixture processes.
func settledConsumerFixture(t *testing.T) (*repairFixture, record, runtimeRecord, resourceGeneration) {
	t.Helper()
	f := newRepairFixture(t)
	var r record
	if err := f.s.store.db.View(func(tx *bolt.Tx) error { var err error; r, err = load(tx, f.instance.ID); return err }); err != nil {
		t.Fatal(err)
	}
	attempt := newID()
	r.Instance.Status, r.Instance.Operation.Status = v1.Interrupted, v1.Interrupted
	r.Instance.Execution = &v1.ExecutionResult{Attempt: attempt, Outcome: v1.Interrupted, CollectionFailure: "historical collection gap", Components: []v1.ComponentResult{{Name: "job", Status: "stopped"}}, NativeCleanup: []v1.NativeCleanupResult{{Component: "job", Attempt: attempt, Confidence: v1.ObservedEmpty, Reason: "session_observed_empty", Detail: "Bound observed-empty receipt."}}}
	j := runtimeRecord{Attempt: attempt, Groups: []native.Identity{{Protocol: 1, Purpose: "application", Component: "job", Attempt: attempt, Boot: settlementBoot, PID: 1, Birth: "fixture-only", Control: "/never-contact-consumer-control", Token: newID()}}}
	g := resourceGeneration{ID: newID(), Endpoint: "unix:///never-contact-consumer-engine", Resources: map[string]ownedResource{}, Initialized: map[string]bool{}}
	return f, r, j, g
}

func saveConsumerFixture(t *testing.T, f *repairFixture, r record, j runtimeRecord, g resourceGeneration) {
	t.Helper()
	if err := f.s.store.db.Update(func(tx *bolt.Tx) error {
		if err := put(tx, "runtime", r.Instance.ID, j); err != nil {
			return err
		}
		if err := put(tx, "resources", r.Instance.ID, g); err != nil {
			return err
		}
		return put(tx, "instances", r.Instance.ID, r)
	}); err != nil {
		t.Fatal(err)
	}
}

func projectConsumer(t *testing.T, f *repairFixture, r record, boot string) v1.ConsumerSettlement {
	t.Helper()
	before := stateContents(t, f.s.store)
	var result v1.ConsumerSettlement
	if err := f.s.store.db.View(func(tx *bolt.Tx) error { var err error; result, err = consumerEligibility(tx, r, boot); return err }); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, stateContents(t, f.s.store)) {
		t.Fatal("projection mutated authority/history")
	}
	if result.ObservedAt != "" {
		t.Fatal("read invented observation time")
	}
	return result
}

func TestConsumerEligibilityExistingFacts(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*record, *runtimeRecord, *resourceGeneration)
		state  v1.ConsumerSettlementState
		code   string
	}{
		{"component summary is not exact proof", func(_ *record, j *runtimeRecord, _ *resourceGeneration) { j.Groups[0].Boot = settlementBoot }, v1.ConsumerUnknown, "native_unproven"},
		{"active", func(r *record, _ *runtimeRecord, _ *resourceGeneration) {
			r.Instance.Status = v1.RuntimeReady
			r.Instance.Operation.Status = v1.RuntimeReady
		}, v1.ConsumerUnsettled, "active_consumer"},
		{"kept", func(r *record, _ *runtimeRecord, _ *resourceGeneration) { r.Instance.Execution.Kept = true }, v1.ConsumerUnsettled, "active_consumer"},
		{"missing receipt", func(r *record, j *runtimeRecord, _ *resourceGeneration) {
			r.Instance.Execution.NativeCleanup = nil
			j.Groups[0].Boot = settlementBoot
		}, v1.ConsumerUnknown, "native_unproven"},
		{"empty legacy journal", func(_ *record, j *runtimeRecord, _ *resourceGeneration) { j.Groups = nil }, v1.ConsumerUnknown, "no_release_evidence"},
		{"mismatched journal", func(_ *record, j *runtimeRecord, _ *resourceGeneration) { j.Attempt = newID() }, v1.ConsumerUnknown, "attempt_mismatch"},
		{"pending current boot", func(_ *record, j *runtimeRecord, _ *resourceGeneration) {
			j.LaunchPending = true
			j.PendingBoot = settlementBoot
		}, v1.ConsumerUnknown, "pending_launch"},
		{"mixed pending boot", func(_ *record, j *runtimeRecord, _ *resourceGeneration) {
			j.Groups[0].Boot = settlementBoot
			j.LaunchPending = true
			j.PendingBoot = settlementOldBoot
		}, v1.ConsumerUnknown, "pending_launch"},
		{"all earlier boot", func(r *record, j *runtimeRecord, _ *resourceGeneration) {
			r.Instance.Execution.NativeCleanup = nil
			j.Groups[0].Boot = settlementOldBoot
			j.LaunchPending = true
			j.PendingBoot = settlementOldBoot
		}, v1.ConsumerSettled, ""},
		{"invalid earlier boot", func(r *record, j *runtimeRecord, _ *resourceGeneration) {
			r.Instance.Execution.NativeCleanup = nil
			j.Groups[0].Boot = "unknown"
		}, v1.ConsumerUnknown, "native_unproven"},
		{"waiver alone", func(r *record, _ *runtimeRecord, _ *resourceGeneration) {
			r.Instance.Execution.NativeCleanup = nil
			r.NativeAbandonments = []nativeAbandonment{{Attempt: r.Instance.Execution.Attempt, Boot: settlementBoot}}
		}, v1.ConsumerUnknown, "native_abandoned"},
		{"incomplete cleanup", func(r *record, _ *runtimeRecord, _ *resourceGeneration) {
			r.Instance.Execution.CleanupFailure = "unfinished cleanup"
		}, v1.ConsumerUnsettled, "cleanup_incomplete"},
		{"unremoved container", func(r *record, _ *runtimeRecord, g *resourceGeneration) {
			g.Containers = []ownedResource{{Kind: "container", Name: "fixture", Token: newID(), Attempt: r.Instance.Execution.Attempt, Component: "job"}}
		}, v1.ConsumerUnknown, "container_not_removed"},
		{"removed container", func(r *record, j *runtimeRecord, g *resourceGeneration) {
			j.Groups = nil
			r.Instance.Execution.NativeCleanup = nil
			g.Containers = []ownedResource{{Kind: "container", Name: "fixture", Token: newID(), Attempt: r.Instance.Execution.Attempt, Removed: true}}
		}, v1.ConsumerSettled, ""},
		{"removed historical container", func(_ *record, _ *runtimeRecord, g *resourceGeneration) {
			g.Containers = []ownedResource{{Kind: "container", Name: "fixture", Token: newID(), Attempt: newID(), Removed: true}}
		}, v1.ConsumerSettled, ""},
		{"independent network", func(_ *record, _ *runtimeRecord, g *resourceGeneration) {
			g.Resources["network"] = ownedResource{Kind: "network", Name: "fixture", Token: newID()}
		}, v1.ConsumerUnknown, "network_not_removed"},
		{"retained data", func(_ *record, _ *runtimeRecord, g *resourceGeneration) {
			g.Resources["data"] = ownedResource{Kind: "volume", Name: "fixture", Token: newID()}
		}, v1.ConsumerSettled, ""},
		{"gateway only cleanup", func(_ *record, j *runtimeRecord, _ *resourceGeneration) { j.GatewayOnlyCleanup = true }, v1.ConsumerUnknown, "gateway_pending"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, r, j, g := settledConsumerFixture(t)
			j.Groups[0].Boot = settlementOldBoot
			tc.change(&r, &j, &g)
			saveConsumerFixture(t, f, r, j, g)
			s := projectConsumer(t, f, r, settlementBoot)
			if s.State != tc.state || s.Generation != g.ID {
				t.Fatal("unexpected eligibility", s)
			}
			found := tc.code == ""
			for _, b := range s.Blockers {
				found = found || b.Code == tc.code
			}
			if !found {
				t.Fatal("missing independent blocker", s)
			}
			if s.State == v1.ConsumerSettled && s.Cause != v1.SettlementVerifiedRelease && s.Cause != v1.SettlementPreviousBoot {
				t.Fatal("invented legacy cause", s)
			}
		})
	}
}

func TestConsumerEligibilityMetadataAndMissingProof(t *testing.T) {
	f := newRepairFixture(t)
	r := record{Instance: f.instance}
	s := projectConsumer(t, f, r, settlementBoot)
	if s.State != v1.ConsumerSettled || s.Cause != v1.SettlementNotStarted || s.Attempt != "" || s.Generation != "" {
		t.Fatal("genuine preparation not recognized", s)
	}
	if err := f.s.store.db.Update(func(tx *bolt.Tx) error { return put(tx, "runtime", r.Instance.ID, runtimeRecord{}) }); err != nil {
		t.Fatal(err)
	}
	if s := projectConsumer(t, f, r, settlementBoot); s.State != v1.ConsumerUnknown {
		t.Fatal("empty journal fabricated not-started proof", s)
	}
	f, r, j, g := settledConsumerFixture(t)
	j.Groups[0].Boot = settlementOldBoot
	r.Instance.Execution.NativeCleanup = nil
	saveConsumerFixture(t, f, r, j, g)
	if s := projectConsumer(t, f, r, ""); s.State != v1.ConsumerUnknown {
		t.Fatal("missing current boot released native work", s)
	}
}

func TestConsumerEligibilityDoesNotContactProvidersOrTrustSummary(t *testing.T) {
	f, r, j, g := settledConsumerFixture(t)
	var contacts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { contacts.Add(1) }))
	defer server.Close()
	g.Endpoint = server.URL
	j.Gateway = &gateway.Intent{Endpoint: server.URL, Scope: "fixture", ID: "fixture", Host: "fixture.invalid", Route: json.RawMessage(`{}`)}
	r.Instance.Execution.Settlement = &v1.ConsumerSettlement{State: v1.ConsumerSettled}
	saveConsumerFixture(t, f, r, j, g)
	if s := projectConsumer(t, f, r, settlementBoot); s.State != v1.ConsumerUnknown {
		t.Fatal("cached summary bypassed gateway", s)
	}
	if contacts.Load() != 0 {
		t.Fatal("read-only eligibility contacted provider")
	}
}

func TestConsumerEffectRevision(t *testing.T) {
	f, r, j, g := settledConsumerFixture(t)
	saveConsumerFixture(t, f, r, j, g)
	revision := func() string {
		t.Helper()
		var out string
		if err := f.s.store.db.View(func(tx *bolt.Tx) error { var err error; out, err = consumerEffectRevision(tx, r); return err }); err != nil {
			t.Fatal(err)
		}
		return out
	}
	baseline := revision()
	r.Instance.Execution.CollectionFailure = "different historical collection gap"
	r.Instance.Execution.Failure = "historical failure"
	r.Instance.Execution.Settlement = &v1.ConsumerSettlement{State: v1.ConsumerUnknown}
	saveConsumerFixture(t, f, r, j, g)
	if revision() != baseline {
		t.Fatal("outcome/diagnostic changed effect fingerprint")
	}
	j.Groups[0].Token = newID()
	saveConsumerFixture(t, f, r, j, g)
	if revision() == baseline {
		t.Fatal("new same-attempt native effect retained release freshness")
	}
	baseline = revision()
	g.Resources["network"] = ownedResource{Kind: "network", Name: "fixture", Token: newID()}
	saveConsumerFixture(t, f, r, j, g)
	if revision() == baseline {
		t.Fatal("new network effect retained release freshness")
	}
	baseline = revision()
	g.ID = newID()
	saveConsumerFixture(t, f, r, j, g)
	if revision() == baseline {
		t.Fatal("new generation retained release freshness")
	}
	baseline = revision()
	r.Instance.Execution.Attempt = newID()
	r.Instance.Execution.NativeCleanup = nil
	j.Attempt = r.Instance.Execution.Attempt
	j.Groups = nil
	saveConsumerFixture(t, f, r, j, g)
	if revision() == baseline {
		t.Fatal("new accepted attempt retained release freshness")
	}
	if !validID(revision()) || strings.Contains(revision(), g.Endpoint) {
		t.Fatal("invalid/private fingerprint format")
	}
}

func TestConsumerEligibilityDependentTasks(t *testing.T) {
	f, r, j, g := settledConsumerFixture(t)
	j.Groups[0].Boot = settlementOldBoot
	saveConsumerFixture(t, f, r, j, g)
	var child record
	data, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &child); err != nil {
		t.Fatal(err)
	}
	child.Instance.ID = newID()
	child.Instance.Operation.ID = newID()
	child.Instance.Operation.Allocations[0].ID = newID()
	child.Instance.Operation.Allocations[0].OwnershipToken = newID()
	child.Instance.Plan.Lifetime = v1.Disposable
	child.Instance.Task = &v1.TaskBinding{InstanceID: r.Instance.ID, Attempt: r.Instance.Execution.Attempt, Generation: g.ID, Name: "job"}
	child.Instance.Execution.Attempt = newID()
	child.Instance.Execution.NativeCleanup[0].Attempt = child.Instance.Execution.Attempt
	childJournal := j
	childJournal.Attempt = child.Instance.Execution.Attempt
	childJournal.Groups = append([]native.Identity{}, j.Groups...)
	childJournal.Groups[0].Attempt = childJournal.Attempt
	baseline := ""
	if err := f.s.store.db.View(func(tx *bolt.Tx) error { var err error; baseline, err = consumerEffectRevision(tx, r); return err }); err != nil {
		t.Fatal(err)
	}
	saveConsumerFixture(t, f, child, childJournal, resourceGeneration{})
	if s := projectConsumer(t, f, child, settlementBoot); s.State != v1.ConsumerSettled || s.Generation != g.ID {
		t.Fatal("task did not report exact borrowed generation", s)
	}
	if s := projectConsumer(t, f, r, settlementBoot); s.State != v1.ConsumerSettled {
		t.Fatal("settled child blocked parent", s)
	}
	if err := f.s.store.db.View(func(tx *bolt.Tx) error {
		now, err := consumerEffectRevision(tx, r)
		if now == baseline {
			t.Fatal("new dependent retained parent effect freshness")
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	child.Instance.Execution.NativeCleanup = nil
	childJournal.Groups[0].Boot = settlementBoot
	saveConsumerFixture(t, f, child, childJournal, resourceGeneration{})
	s := projectConsumer(t, f, r, settlementBoot)
	if s.State != v1.ConsumerUnknown {
		t.Fatal("parent release bypassed independent child", s)
	}
	found := false
	for _, b := range s.Blockers {
		found = found || b.Code == "dependent_unsettled" && b.InstanceID == child.Instance.ID
	}
	if !found {
		t.Fatal("missing child fence", s)
	}
	// Invalid task nesting must terminate with an error, never recurse or authorize release.
	r.Instance.Task = &v1.TaskBinding{InstanceID: child.Instance.ID, Attempt: childJournal.Attempt, Generation: childJournal.Attempt, Name: "job"}
	r.Instance.Plan.Lifetime = v1.Disposable
	saveConsumerFixture(t, f, r, j, g)
	if err := f.s.store.db.View(func(tx *bolt.Tx) error { _, err := consumerEligibility(tx, r, settlementBoot); return err }); err == nil {
		t.Fatal("cyclic task authority accepted")
	}
}

func TestConsumerEligibilityBlockerBound(t *testing.T) {
	f, r, j, g := settledConsumerFixture(t)
	j.Groups[0].Boot = settlementOldBoot
	for n := 0; n < 150; n++ {
		g.Containers = append(g.Containers, ownedResource{Kind: "container", Name: fmt.Sprintf("fixture-%d", n), Component: fmt.Sprintf("component%d", n), Token: newID(), Attempt: r.Instance.Execution.Attempt})
	}
	saveConsumerFixture(t, f, r, j, g)
	s := projectConsumer(t, f, r, settlementBoot)
	if len(s.Blockers) != 100 || s.OmittedBlockers != 50 {
		t.Fatal("blockers not bounded after complete evaluation", len(s.Blockers), s.OmittedBlockers)
	}
	g.Containers[149].Attempt = newID()
	saveConsumerFixture(t, f, r, j, g)
	s = projectConsumer(t, f, r, settlementBoot)
	if s.OmittedBlockers != 51 || s.Blockers[0].Code != "container_attempt_mismatch" {
		t.Fatal("later effect not evaluated or blockers not deterministic", s)
	}
}

func TestConsumerEligibilityRejectsStaleAndMalformedAuthority(t *testing.T) {
	f, r, j, g := settledConsumerFixture(t)
	saveConsumerFixture(t, f, r, j, g)
	stale := r
	stale.Instance.Execution = &v1.ExecutionResult{Attempt: newID()}
	if err := f.s.store.db.View(func(tx *bolt.Tx) error { _, err := consumerEligibility(tx, stale, settlementBoot); return err }); err == nil {
		t.Fatal("substituted an attempt for stale supplied selection")
	}
	r.Instance.Execution.NativeCleanup[0].Attempt = newID()
	saveConsumerFixture(t, f, r, j, g)
	if err := f.s.store.db.View(func(tx *bolt.Tx) error { _, err := consumerEligibility(tx, r, settlementBoot); return err }); err == nil {
		t.Fatal("accepted malformed receipt binding")
	}
	if earlierConsumerBoot("ABCDEFAB-ABCD-ABCD-ABCD-ABCDEFABCDEF", "abcdefab-abcd-abcd-abcd-abcdefabcdef") {
		t.Fatal("case-only spelling established earlier boot")
	}
}

func TestConsumerSettlementRepairRequiresOwnerJoin(t *testing.T) {
	f, r, j, g := settledConsumerFixture(t)
	j.Groups[0].Boot = settlementOldBoot
	saveConsumerFixture(t, f, r, j, g)
	done := make(chan struct{})
	f.s.executions = map[string]*execution{r.Instance.ID: {done: done}}
	blocked := f.preview(t)
	if !strings.Contains(strings.Join(blocked.Blockers, "\n"), "must be stopped/joined") {
		t.Fatal("database proof substituted for in-memory owner join", blocked.Blockers)
	}
	close(done)
	settled := f.preview(t)
	if len(settled.Blockers) > 0 {
		t.Fatal("joined owner with positive current receipts blocked", settled.Blockers)
	}
}

func TestConsumerErasedTaskTombstone(t *testing.T) {
	for _, mode := range []string{"complete", "binding", "runtime", "snapshot", "secret", "resources", "abandoned", "pending", "attempt", "nested", "live"} {
		t.Run(mode, func(t *testing.T) {
			f, r, j, g := settledConsumerFixture(t)
			j.Groups[0].Boot = settlementOldBoot
			saveConsumerFixture(t, f, r, j, g)
			var child record
			data, err := json.Marshal(r)
			if err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(data, &child); err != nil {
				t.Fatal(err)
			}
			child.Instance.ID = newID()
			child.Instance.Plan.Lifetime = v1.Disposable
			child.Instance.Task = &v1.TaskBinding{InstanceID: r.Instance.ID, Attempt: r.Instance.Execution.Attempt, Generation: g.ID, Name: "job"}
			child.Instance.Execution.Attempt = newID()
			child.Instance.Execution.NativeCleanup[0].Attempt = child.Instance.Execution.Attempt
			cj := runtimeRecord{Attempt: child.Instance.Execution.Attempt, Groups: append([]native.Identity{}, j.Groups...)}
			cj.Groups[0].Attempt = cj.Attempt
			revision := func() (out string, err error) {
				err = f.s.store.db.View(func(tx *bolt.Tx) error { out, err = consumerEffectRevision(tx, r); return err })
				return
			}
			absent, err := revision()
			if err != nil {
				t.Fatal(err)
			}
			saveConsumerFixture(t, f, child, cj, resourceGeneration{})
			retained, err := revision()
			if err != nil || retained == absent {
				t.Fatal("retained child absent from fingerprint", err)
			}
			// Exercise the actual erasure writer, not a serialized approximation.
			if _, err := f.s.eraseDestroyed(child.Instance.ID); err != nil {
				t.Fatal(err)
			}
			after, err := revision()
			if err != nil || after != absent {
				t.Fatal("completed erasure retained consumer effect", err)
			}
			if _, err := f.s.store.inspect(r.Instance.ID); err != nil {
				t.Fatal("erased child broke parent inspection", err)
			}
			if result := projectConsumer(t, f, r, settlementBoot); result.State != v1.ConsumerSettled {
				t.Fatal(result)
			}
			if mode == "complete" {
				return
			}
			if err := f.s.store.db.Update(func(tx *bolt.Tx) error {
				tombstone, err := load(tx, child.Instance.ID)
				if err != nil {
					return err
				}
				switch mode {
				case "binding":
					tombstone.Instance.Task.Generation = "invalid"
				case "runtime":
					return put(tx, "runtime", child.Instance.ID, runtimeRecord{Attempt: cj.Attempt})
				case "snapshot":
					return put(tx, "snapshots", child.Instance.ID, json.RawMessage(`{}`))
				case "secret":
					return put(tx, "secrets", child.Instance.ID, map[string]string{"fixture": "retained"})
				case "resources":
					return put(tx, "resources", child.Instance.ID, resourceGeneration{ID: newID()})
				case "abandoned":
					tombstone.NativeAbandonments = []nativeAbandonment{{Attempt: cj.Attempt, Boot: settlementBoot}}
				case "pending":
					return put(tx, "evidence_deletions", child.Instance.ID+"/", evidenceDeletion{InstanceID: child.Instance.ID})
				case "attempt":
					return put(tx, "attempt_sequences", child.Instance.ID, int64(1))
				case "nested":
					nested := child
					nested.Instance.ID = newID()
					binding := *child.Instance.Task
					binding.InstanceID = child.Instance.ID
					nested.Instance.Task = &binding
					return put(tx, "instances", nested.Instance.ID, nested)
				case "live":
					child.Instance.Status, child.Instance.Operation.Status = v1.Starting, v1.Starting
					if err := put(tx, "runtime", child.Instance.ID, cj); err != nil {
						return err
					}
					return put(tx, "instances", child.Instance.ID, child)
				}
				return put(tx, "instances", child.Instance.ID, tombstone)
			}); err != nil {
				t.Fatal(err)
			}
			var result v1.ConsumerSettlement
			projectionErr := f.s.store.db.View(func(tx *bolt.Tx) error { result, err = consumerEligibility(tx, r, settlementBoot); return err })
			changed, fingerprintErr := revision()
			if mode == "live" {
				if projectionErr != nil || result.State != v1.ConsumerUnsettled || fingerprintErr != nil || changed == absent {
					t.Fatal("live child bypassed parent fence", result, projectionErr, fingerprintErr)
				}
			} else if projectionErr == nil || fingerprintErr == nil {
				t.Fatal("incomplete erasure bypassed authority validation", mode, result)
			}
		})
	}
}

func TestConsumerPendingErasureIsScopedToParent(t *testing.T) {
	f, r, j, g := settledConsumerFixture(t)
	j.Groups[0].Boot = settlementOldBoot
	saveConsumerFixture(t, f, r, j, g)
	var owner record
	raw, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &owner); err != nil {
		t.Fatal(err)
	}
	owner.Instance.ID = newID()
	saveConsumerFixture(t, f, owner, j, g)
	child := owner
	child.Instance.ID, child.Instance.Execution = newID(), nil
	child.Instance.Status, child.Instance.Operation.Status = v1.Destroyed, v1.Destroyed
	child.Instance.Plan.Lifetime = v1.Disposable
	child.Instance.Task = &v1.TaskBinding{InstanceID: owner.Instance.ID, Attempt: j.Attempt, Generation: g.ID, Name: "job"}
	if err := f.s.store.db.Update(func(tx *bolt.Tx) error {
		if err := put(tx, "instances", child.Instance.ID, child); err != nil {
			return err
		}
		return put(tx, "evidence_deletions", child.Instance.ID+"/", evidenceDeletion{InstanceID: child.Instance.ID})
	}); err != nil {
		t.Fatal(err)
	}
	before := stateContents(t, f.s.store)
	if _, err := f.s.store.inspect(owner.Instance.ID); err == nil {
		t.Fatal("owning parent lost pending erasure fence")
	}
	if _, err := f.s.store.inspect(r.Instance.ID); err != nil {
		t.Fatal("unrelated parent inspection blocked", err)
	}
	if !reflect.DeepEqual(before, stateContents(t, f.s.store)) {
		t.Fatal("inspection mutated state")
	}
	if _, err := f.s.stopExecution(r.Instance.ID); err != nil {
		t.Fatal("unrelated parent stop blocked", err)
	}
	if storedBytes(t, f.s.store, "evidence_deletions", child.Instance.ID+"/") == nil {
		t.Fatal("unrelated stop erased foreign task obligation")
	}
}

func TestConsumerCompletedErasureRepairSelection(t *testing.T) {
	for _, kind := range []string{"scene", "task"} {
		for _, remnant := range []string{"complete", "pending", "runtime", "resources"} {
			t.Run(kind+"/"+remnant, func(t *testing.T) {
				f := newRepairFixture(t)
				erasedID := f.instance.ID
				if kind == "task" {
					var parent record
					if err := f.s.store.db.View(func(tx *bolt.Tx) error { var err error; parent, err = load(tx, f.instance.ID); return err }); err != nil {
						t.Fatal(err)
					}
					child := parent
					child.Instance.ID, child.Instance.Plan.Lifetime = newID(), v1.Disposable
					child.Instance.Task = &v1.TaskBinding{InstanceID: parent.Instance.ID, Attempt: newID(), Generation: newID(), Name: "job"}
					child.Instance.Execution = &v1.ExecutionResult{Attempt: newID()}
					if err := f.s.store.db.Update(func(tx *bolt.Tx) error { return put(tx, "instances", child.Instance.ID, child) }); err != nil {
						t.Fatal(err)
					}
					erasedID = child.Instance.ID
				}
				if _, err := f.s.eraseDestroyed(erasedID); err != nil {
					t.Fatal(err)
				}
				if remnant != "complete" {
					if err := f.s.store.db.Update(func(tx *bolt.Tx) error {
						switch remnant {
						case "pending":
							return put(tx, "evidence_deletions", erasedID+"/", evidenceDeletion{InstanceID: erasedID})
						case "runtime":
							return put(tx, "runtime", erasedID, runtimeRecord{Attempt: newID(), LaunchPending: true})
						default:
							return put(tx, "resources", erasedID, resourceGeneration{ID: newID()})
						}
					}); err != nil {
						t.Fatal(err)
					}
				}
				before := stateContents(t, f.s.store)
				preview, err := f.s.projectPreview(f.request)
				if remnant == "complete" {
					if err != nil {
						t.Fatal("completed erasure broke repair", err)
					}
					found := false
					for _, i := range preview.Instances {
						found = found || i.ID == erasedID
					}
					if !found {
						t.Fatal("repair lost erased record/path reservation")
					}
					for _, b := range preview.Blockers {
						if strings.Contains(b, erasedID) && strings.Contains(b, "requires verified cleanup") {
							t.Fatal("erased work treated as unproven/no-launch", b)
						}
					}
				} else if err == nil && len(preview.Blockers) == 0 {
					t.Fatal("repair bypassed incomplete erasure", remnant)
				}
				if !reflect.DeepEqual(before, stateContents(t, f.s.store)) {
					t.Fatal("repair preview mutated state")
				}
			})
		}
	}
}
