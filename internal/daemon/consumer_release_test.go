//go:build darwin || linux

package daemon

import (
	"context"
	"net/http"
	"reflect"
	"testing"

	v1 "github.com/octacian/backlot/api/v1"
	"github.com/octacian/backlot/internal/native"
	bolt "go.etcd.io/bbolt"
)

func TestConsumerReleaseFactFreshness(t *testing.T) {
	for _, change := range []string{"outcome-only", "pending", "native", "container", "network", "generation", "attempt", "dependent", "cleanup-failure"} {
		t.Run(change, func(t *testing.T) {
			f, r, j, g := settledConsumerFixture(t)
			j.Groups = nil
			r.Instance.Execution.NativeCleanup = nil
			saveConsumerFixture(t, f, r, j, g)
			if err := f.s.store.recordConsumerRelease(context.Background(), r.Instance.ID, j, v1.SettlementExplicitStop, false); err == nil {
				t.Fatal("empty legacy journal invented release")
			}
			// Unit fixture owns a known current zero-launch operation; publish through
			// the actual producer, not a public summary or a serialized proof stand-in.
			if err := f.s.store.recordConsumerRelease(context.Background(), r.Instance.ID, j, v1.SettlementExplicitStop, true); err != nil {
				t.Fatal(err)
			}
			project := func() v1.ConsumerSettlement {
				t.Helper()
				before := stateContents(t, f.s.store)
				var result v1.ConsumerSettlement
				if err := f.s.store.db.View(func(tx *bolt.Tx) error {
					var err error
					result, err = consumerEligibility(tx, r, settlementBoot)
					return err
				}); err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(before, stateContents(t, f.s.store)) {
					t.Fatal("read mutated release authority")
				}
				return result
			}
			initial := project()
			if initial.State != v1.ConsumerSettled || initial.Cause != v1.SettlementExplicitStop || initial.ObservedAt == "" {
				t.Fatal("actual producer observation missing")
			}
			if err := f.s.store.db.Update(func(tx *bolt.Tx) error {
				current, err := load(tx, r.Instance.ID)
				if err != nil {
					return err
				}
				switch change {
				case "outcome-only":
					current.Instance.Execution.Outcome = v1.Failed
					current.Instance.Execution.CollectionFailure = "later retained collection detail"
				case "pending":
					j.LaunchPending = true
					j.PendingBoot = settlementBoot
				case "native":
					j.Groups = []native.Identity{{Protocol: 1, Purpose: "application", Component: "job", Attempt: j.Attempt, Boot: settlementBoot}}
				case "container":
					g.Containers = []ownedResource{{Kind: "container", Name: "unit-owned", Token: newID(), Attempt: j.Attempt, Component: "job"}}
				case "network":
					g.Resources["network"] = ownedResource{Kind: "network", Name: "unit-owned", Token: newID()}
				case "generation":
					g.ID = newID()
				case "attempt":
					current.Instance.Execution.Attempt = newID()
					r.Instance.Execution.Attempt = current.Instance.Execution.Attempt
				case "cleanup-failure":
					current.Instance.Execution.CleanupFailure = "independent cleanup remains incomplete"
				case "dependent":
					child := current
					child.Instance.ID = newID()
					child.Instance.Plan.Lifetime = v1.Disposable
					child.Instance.Task = &v1.TaskBinding{InstanceID: r.Instance.ID, Attempt: j.Attempt, Generation: g.ID, Name: "job"}
					child.Instance.Execution = &v1.ExecutionResult{Attempt: newID(), Components: []v1.ComponentResult{}}
					if err := put(tx, "instances", child.Instance.ID, child); err != nil {
						return err
					}
				}
				// Keep the original fact when mutating journals: stale facts must fail by
				// effect comparison, not by conveniently deleting the evidence under test.
				persisted, err := decodeRuntime(tx.Bucket([]byte("runtime")).Get([]byte(r.Instance.ID)))
				if err != nil {
					return err
				}
				j.Release = persisted.Release
				if err := put(tx, "runtime", r.Instance.ID, j); err != nil {
					return err
				}
				if err := put(tx, "resources", r.Instance.ID, g); err != nil {
					return err
				}
				return put(tx, "instances", r.Instance.ID, current)
			}); err != nil {
				t.Fatal(err)
			}
			result := project()
			if (result.State == v1.ConsumerSettled) != (change == "outcome-only") {
				t.Fatal("stale proof bypassed independent obligation", change, result.State)
			}
			if change == "outcome-only" && result.ObservedAt != initial.ObservedAt {
				t.Fatal("read refreshed observation time")
			}
		})
	}
}

func TestConsumerReleaseRevalidatesAfterProviderObservation(t *testing.T) {
	f, r, j, g := settledConsumerFixture(t)
	j.Groups = nil
	r.Instance.Execution.NativeCleanup = nil
	network := ownedResource{Kind: "network", ID: newID(), Name: "unit-owned", Token: newID(), Removed: true}
	changed := false
	d := forceEngine(t, func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodGet || req.URL.Path != "/networks/"+network.ID {
			t.Error("release verifier attempted effects or widened target")
		}
		if !changed {
			changed = true
			concurrent := j
			concurrent.LaunchPending = true
			concurrent.PendingBoot = settlementBoot
			if err := f.s.store.saveRuntime(r.Instance.ID, concurrent); err != nil {
				t.Error(err)
			}
		}
		w.WriteHeader(404)
		_, _ = w.Write([]byte(`{"message":"absent"}`))
	})
	g.Endpoint = d.DaemonHost()
	g.Resources["network"] = network
	saveConsumerFixture(t, f, r, j, g)
	code(t, f.s.store.recordConsumerRelease(context.Background(), r.Instance.ID, j, v1.SettlementExecutionCleanup, true), "conflict")
	journal, err := f.s.store.runtime(r.Instance.ID)
	if err != nil || journal.Release != nil || !journal.LaunchPending {
		t.Fatal("post-observation new effect was acknowledged", err)
	}
}

func TestConsumerReleaseNeverBypassesChangedChild(t *testing.T) {
	f, r, j, g := settledConsumerFixture(t)
	j.Groups = nil
	r.Instance.Execution.NativeCleanup = nil
	saveConsumerFixture(t, f, r, j, g)
	child := r
	child.Instance.ID = newID()
	child.Instance.Plan.Lifetime = v1.Disposable
	child.Instance.Task = &v1.TaskBinding{InstanceID: r.Instance.ID, Attempt: j.Attempt, Generation: g.ID, Name: "job"}
	child.Instance.Execution = &v1.ExecutionResult{Attempt: newID(), Components: []v1.ComponentResult{{Name: "job", Status: "stopped"}}}
	cj := runtimeRecord{Attempt: child.Instance.Execution.Attempt}
	if err := f.s.store.db.Update(func(tx *bolt.Tx) error {
		if err := put(tx, "runtime", child.Instance.ID, cj); err != nil {
			return err
		}
		return put(tx, "instances", child.Instance.ID, child)
	}); err != nil {
		t.Fatal(err)
	}
	if err := f.s.store.recordConsumerRelease(context.Background(), child.Instance.ID, cj, v1.SettlementExecutionCleanup, true); err != nil {
		t.Fatal(err)
	}
	if err := f.s.store.recordConsumerRelease(context.Background(), r.Instance.ID, j, v1.SettlementExecutionCleanup, true); err != nil {
		t.Fatal(err)
	}
	var before, after string
	if err := f.s.store.db.View(func(tx *bolt.Tx) error { var err error; before, err = consumerEffectRevision(tx, r); return err }); err != nil {
		t.Fatal(err)
	}
	cj, err := f.s.store.runtime(child.Instance.ID)
	if err != nil {
		t.Fatal(err)
	}
	cj.LaunchPending = true
	cj.PendingBoot = settlementBoot
	if err := f.s.store.saveRuntime(child.Instance.ID, cj); err != nil {
		t.Fatal(err)
	}
	var result v1.ConsumerSettlement
	if err := f.s.store.db.View(func(tx *bolt.Tx) error {
		var err error
		after, err = consumerEffectRevision(tx, r)
		if err != nil {
			return err
		}
		result, err = consumerEligibility(tx, r, settlementBoot)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if before != after {
		t.Fatal("fixture failed to isolate unchanged parent receipt with changed child effects")
	}
	if result.State == v1.ConsumerSettled {
		t.Fatal("parent aggregate receipt bypassed child's new pending launch")
	}
	found := false
	for _, b := range result.Blockers {
		found = found || b.Code == "dependent_unsettled" && b.InstanceID == child.Instance.ID
	}
	if !found {
		t.Fatal("independent child fence omitted")
	}
}

func TestConsumerReleaseRejectsChangedExactNativeEffect(t *testing.T) {
	for _, mode := range []string{"added-sibling", "changed-identity"} {
		t.Run(mode, func(t *testing.T) {
			f, r, j, g := settledConsumerFixture(t)
			saveConsumerFixture(t, f, r, j, g)
			if err := f.s.store.recordConsumerRelease(context.Background(), r.Instance.ID, j, v1.SettlementExplicitStop, false); err != nil {
				t.Fatal(err)
			}
			if _, err := f.s.store.execution(r.Instance.ID, r.Instance.Status, r.Instance.Execution); err != nil {
				t.Fatal(err)
			}
			stored, err := f.s.store.runtime(r.Instance.ID)
			if err != nil {
				t.Fatal(err)
			}
			oldFact := *stored.Release
			sibling := stored.Groups[0]
			sibling.Token, sibling.Control = newID(), "/never-contact-new-native-effect"
			if mode == "added-sibling" {
				stored.Groups = append(stored.Groups, sibling)
			} else {
				stored.Groups[0] = sibling
			}
			if err := f.s.store.saveRuntime(r.Instance.ID, stored); err != nil {
				t.Fatal(err)
			}
			before := stateContents(t, f.s.store)
			var result v1.ConsumerSettlement
			if err := f.s.store.db.View(func(tx *bolt.Tx) error {
				facts, err := consumerRecords(tx, r)
				if err != nil {
					return err
				}
				if consumerReleaseMatches(tx, facts) || !reflect.DeepEqual(oldFact, *facts.runtime.Release) || !reflect.DeepEqual(r.Instance.Execution.NativeCleanup, facts.record.Instance.Execution.NativeCleanup) {
					t.Fatal("fixture did not retain stale exact fact and historical summary")
				}
				result, err = consumerEligibility(tx, r, settlementBoot)
				return err
			}); err != nil {
				t.Fatal(err)
			}
			if result.State != v1.ConsumerUnknown || result.ObservedAt != "" {
				t.Fatal("changed exact launch reused old component summary", result)
			}
			if !reflect.DeepEqual(before, stateContents(t, f.s.store)) {
				t.Fatal("projection mutated authority")
			}
		})
	}
}

func TestSceneSettlementSelectorEquivalence(t *testing.T) {
	for _, mode := range []string{"pending-effect", "dependent"} {
		t.Run(mode, func(t *testing.T) {
			f, r, j, g := settledConsumerFixture(t)
			j.Groups, r.Instance.Execution.NativeCleanup = nil, nil
			saveConsumerFixture(t, f, r, j, g)
			if err := f.s.store.recordConsumerRelease(context.Background(), r.Instance.ID, j, v1.SettlementExplicitStop, true); err != nil {
				t.Fatal(err)
			}
			if _, err := f.s.store.execution(r.Instance.ID, r.Instance.Status, r.Instance.Execution); err != nil {
				t.Fatal(err)
			}
			if mode == "pending-effect" {
				stored, err := f.s.store.runtime(r.Instance.ID)
				if err != nil {
					t.Fatal(err)
				}
				stored.LaunchPending, stored.PendingBoot = true, ""
				if err := f.s.store.saveRuntime(r.Instance.ID, stored); err != nil {
					t.Fatal(err)
				}
			} else {
				child := r
				child.Instance.ID, child.Instance.Plan.Lifetime = newID(), v1.Disposable
				child.Instance.Task = &v1.TaskBinding{InstanceID: r.Instance.ID, Attempt: j.Attempt, Generation: g.ID, Name: "job"}
				child.Instance.Execution = &v1.ExecutionResult{Attempt: newID(), Outcome: v1.Interrupted}
				saveConsumerFixture(t, f, child, runtimeRecord{Attempt: child.Instance.Execution.Attempt, LaunchPending: true}, resourceGeneration{})
			}
			before := stateContents(t, f.s.store)
			exact, err := f.s.resolveScene(v1.SceneTarget{InstanceID: r.Instance.ID}, false)
			if err != nil {
				t.Fatal(err)
			}
			named, err := f.s.resolveScene(v1.SceneTarget{CWD: f.project, Scene: "dev"}, false)
			if err != nil {
				t.Fatal(err)
			}
			if exact.Result == nil || named.Result == nil {
				t.Fatal("recorded scene missing")
			}
			a, b := exact.Result.Instance.Execution.Settlement, named.Result.Instance.Execution.Settlement
			if a.State != v1.ConsumerUnknown || !reflect.DeepEqual(a, b) || a.ObservedAt != "" {
				t.Fatal("selectors disagree or served stale success", a, b)
			}
			if !reflect.DeepEqual(before, stateContents(t, f.s.store)) {
				t.Fatal("selector read mutated state")
			}
		})
	}
}
