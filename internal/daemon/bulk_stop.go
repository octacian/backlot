package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"time"

	v1 "github.com/octacian/backlot/api/v1"
	"github.com/octacian/backlot/internal/native"
	bolt "go.etcd.io/bbolt"
)

type bulkSelection struct {
	target              v1.StopAllTarget
	parent              string
	grace               time.Duration
	dependents          map[string][]string
	admissionGeneration uint64
}

func (s *service) selectBulk(ctx context.Context) ([]bulkSelection, error) {
	boot, _ := native.CurrentBoot()
	selected := []bulkSelection{}
	var failure error
	var generation uint64
	s.mu.Lock()
	if s.bulkStopping {
		generation = s.admissionGeneration
	}
	s.mu.Unlock()
	err := s.store.db.View(func(tx *bolt.Tx) error {
		budget := &scanBudget{ctx: ctx, unbounded: true, dependents: map[string][]string{}}
		// The index is private to this transaction. Unknown bindings must retain
		// the ordinary full-scan corruption fences, never imply no dependents.
		if err := tx.Bucket([]byte("instances")).ForEach(func(key, raw []byte) error {
			if err := chargeScan([]*scanBudget{budget}, len(raw)); err != nil {
				return err
			}
			var r record
			if err := decodeRecord(raw, string(key), &r); err != nil {
				failure = err
				return nil
			}
			if r.Instance.Task != nil {
				parent := r.Instance.Task.InstanceID
				budget.dependents[parent] = append(budget.dependents[parent], string(key))
			}
			return nil
		}); err != nil {
			return err
		}
		if failure != nil {
			budget.dependents = nil
		}
		return tx.Bucket([]byte("instances")).ForEach(func(key, raw []byte) error {
			if err := chargeScan([]*scanBudget{budget}, len(raw)); err != nil {
				return err
			}
			chosen := bulkSelection{target: v1.StopAllTarget{ID: string(key), Outcome: "unresolved", Reason: "ownership_unproven", Settlement: v1.ConsumerUnknown}, dependents: budget.dependents, admissionGeneration: generation}
			var r record
			readErr := decodeRecord(raw, string(key), &r)
			if readErr == nil {
				// Erased task tombstones retain lineage but no execution authority.
				var erased bool
				erased, readErr = consumerErased(tx, r, budget)
				if readErr == nil && erased {
					return nil
				}
			}
			if readErr == nil {
				if r.Instance.Task != nil {
					chosen.parent = r.Instance.Task.InstanceID
				}
				var facts consumerFacts
				facts, readErr = consumerRecords(tx, r, budget)
				if readErr == nil {
					chosen.target.Attempt = executionAttempt(r.Instance)
					if chosen.target.Attempt != "" {
						chosen.target.Generation = consumerGeneration(facts)
					}
					chosen.grace = mustGrace(facts.runtime.Options)
					if r.Instance.Execution != nil {
						chosen.target.CollectionFailed = r.Instance.Execution.CollectionFailure != ""
					}
					var settled v1.ConsumerSettlement
					settled, readErr = consumerEligibility(tx, r, boot, budget)
					if readErr == nil {
						if settled.State == v1.ConsumerSettled {
							return nil
						}
						chosen.target.Settlement = settled.State
					}
				}
			}
			if readErr != nil {
				failure = readErr
				chosen.target.Reason = "state_unreadable"
			}
			// Corrupt keys cannot become public target identities or cleanup authority.
			if validID(chosen.target.ID) {
				selected = append(selected, chosen)
			}
			return nil
		})
	})
	return selected, errors.Join(failure, err)
}

// bulkOperation retains compact observations independently of the HTTP observer.
// Its lifetime is the coordinator plus its current observer, without durable replay.
type bulkOperation struct {
	mu      sync.Mutex
	records []v1.StopAllRecord
	changed chan struct{}
}

func (o *bulkOperation) emit(r v1.StopAllRecord) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.records = append(o.records, r)
	close(o.changed)
	o.changed = make(chan struct{})
}

func (o *bulkOperation) next(ctx context.Context, index int) (v1.StopAllRecord, error) {
	for {
		o.mu.Lock()
		if index < len(o.records) {
			r := o.records[index]
			o.mu.Unlock()
			return r, nil
		}
		changed := o.changed
		o.mu.Unlock()
		select {
		case <-changed:
		case <-ctx.Done():
			return v1.StopAllRecord{}, ctx.Err()
		}
	}
}

func bulkFailure() *v1.PlanError {
	return &v1.PlanError{Code: "partial_failure", Message: "bulk stop incomplete; inspect activity and selected targets before explicit recovery"}
}

func (s *service) startBulk(q v1.StopAllRequest) (*bulkOperation, error) {
	if err := q.Validate(); err != nil {
		return nil, err
	}
	owned, finish, err := s.beginMutation(context.Background())
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	if s.closing || s.bulkStopping {
		s.mu.Unlock()
		finish()
		return nil, problem("admission_closed", "bulk stop or daemon shutdown is already in progress; inspect activity")
	}
	s.bulkStopping = true
	s.admissionGeneration++
	// Retention must win before any cancellation can trigger disposable teardown.
	for _, entry := range s.executions {
		entry.bulkCancelled = true
	}
	preceding := []*mutationOwner{}
	for owner := range s.mutationOwners {
		if owner.workload {
			preceding = append(preceding, owner)
			owner.cancel()
		}
	}
	s.mu.Unlock()
	o := &bulkOperation{changed: make(chan struct{})}
	accepted := v1.StopAllRecord{APIVersion: v1.Version, Type: "accepted", DaemonGeneration: s.generation, OperationID: newID()}
	o.emit(accepted)
	go func() {
		out := v1.StopAllResponse{APIVersion: v1.Version, DaemonGeneration: accepted.DaemonGeneration, OperationID: accepted.OperationID, Targets: []v1.StopAllTarget{}, AdmissionsJoined: true}
		joinCtx, joinCancel := context.WithTimeout(owned, 20*time.Second)
		for _, owner := range preceding {
			select {
			case <-owner.done:
			case <-joinCtx.Done():
				out.AdmissionsJoined = false
			}
		}
		joinCancel()
		selected, selectionErr := s.selectBulk(owned)
		out.SelectionComplete = selectionErr == nil
		if selectionErr != nil {
			out.SelectionFailure = "state_unreadable"
			if owned.Err() != nil {
				out.SelectionFailure = "cancelled"
			}
		}
		blockedParents := map[string]bool{}
		// Tasks borrow scenes, never other tasks. Two passes preserve their ordering
		// without a second selection or a second copy of target diagnostics.
		for _, children := range []bool{true, false} {
			for _, chosen := range selected {
				if (chosen.parent != "") != children {
					continue
				}
				result := chosen.target
				switch {
				case owned.Err() != nil:
					result.Reason = "shutting_down"
				case result.Reason == "state_unreadable":
				case blockedParents[result.ID]:
					result.Reason = "dependent_unsettled"
				default:
					wait, cancel := context.WithTimeout(owned, OwnerJoin(chosen.grace))
					result, _ = s.stopBulkTarget(wait, chosen)
					cancel()
				}
				if chosen.parent != "" && result.Settlement != v1.ConsumerSettled {
					blockedParents[chosen.parent] = true
				}
				out.Targets = append(out.Targets, result)
				event := accepted
				event.Type, event.Target = "target", &result
				o.emit(event)
				if result.Outcome != "stopped" {
					out.Error = bulkFailure()
				}
			}
		}
		if !out.AdmissionsJoined || !out.SelectionComplete {
			out.Error = bulkFailure()
		}
		summary := out.Summary()
		// Reopen before final observation: receiving the summary means coordination
		// settled, while existing execution owners can still retain cleanup authority.
		s.mu.Lock()
		s.bulkStopping = false
		s.mu.Unlock()
		finish()
		event := accepted
		event.Type, event.Summary = "summary", &summary
		o.emit(event)
	}()
	return o, nil
}

func (s *service) stopAll(ctx context.Context, q v1.StopAllRequest) (v1.StopAllResponse, error) {
	o, err := s.startBulk(q)
	if err != nil {
		return v1.StopAllResponse{}, err
	}
	out := v1.StopAllResponse{Targets: []v1.StopAllTarget{}}
	for index := 0; ; index++ {
		r, err := o.next(ctx, index)
		if err != nil {
			return out, problem("result_unknown", "bulk stop accepted; waiting ended, cleanup continues; inspect activity in the same scope before explicit recovery; do not automatically retry")
		}
		out.APIVersion, out.DaemonGeneration, out.OperationID = r.APIVersion, r.DaemonGeneration, r.OperationID
		if r.Target != nil {
			out.Targets = append(out.Targets, *r.Target)
		}
		if r.Summary != nil {
			out.AdmissionsJoined, out.SelectionComplete, out.SelectionFailure, out.Error = r.Summary.AdmissionsJoined, r.Summary.SelectionComplete, r.Summary.SelectionFailure, r.Summary.Error
			return out, nil
		}
	}
}

// waitBulkLock never leaves a lock waiter behind after its individual deadline.
func waitBulkLock(ctx context.Context, lock *sync.Mutex) bool {
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		if ctx.Err() != nil {
			return false
		}
		if lock.TryLock() {
			return true
		}
		select {
		case <-ctx.Done():
			return false
		case <-tick.C:
		}
	}
}

// stopBulkTarget reuses individually bounded lifecycle cleanup synchronously.
// Execution owners retain their authority if their existing join deadline expires.
func (s *service) stopBulkTarget(ctx context.Context, chosen bulkSelection) (v1.StopAllTarget, bool) {
	selected := chosen.target
	lock := s.mutationLock(selected.ID)
	if !waitBulkLock(ctx, lock) {
		selected.Reason = "busy"
		return selected, true
	}
	defer lock.Unlock()
	_, finish, err := s.beginMutation(context.Background())
	if err != nil {
		selected.Reason = "shutting_down"
		return selected, false
	}
	defer finish()
	result := selected
	boot, _ := native.CurrentBoot()
	err = s.store.db.View(func(tx *bolt.Tx) error {
		r, err := load(tx, selected.ID)
		if err != nil {
			return err
		}
		facts, err := consumerRecords(tx, r)
		if err != nil {
			return err
		}
		if executionAttempt(r.Instance) != selected.Attempt || selected.Attempt != "" && consumerGeneration(facts) != selected.Generation {
			return problem("conflict", "bulk target changed")
		}
		return nil
	})
	if err != nil {
		result.Reason = "target_changed"
		return result, false
	}
	s.mu.Lock()
	entry := s.executions[selected.ID]
	if entry != nil && entry.attempt == selected.Attempt {
		entry.bulkCancelled = true
	}
	s.mu.Unlock()
	if entry != nil {
		if !waitBulkLock(ctx, &entry.mu) {
			result.Reason = "busy"
			return result, true
		}
		entry.mu.Unlock()
	}
	if selected.Attempt == "" {
		_, err = s.store.cancel(selected.ID)
	} else {
		_, err = s.stopExecution(selected.ID)
	}
	if entry != nil {
		select {
		case <-entry.done:
			entry.mu.Lock()
			kept := entry.kept
			entry.mu.Unlock()
			if kept {
				_, err = s.stopKept(selected.ID, entry)
			}
		default:
		}
	}
	ownerRunning := false
	if entry != nil {
		select {
		case <-entry.done:
		default:
			ownerRunning = true
		}
	}
	// invariant: no admission can add a child while this same fence is held.
	// Only metadata leaves reuse that private absence proof; known children and
	// reopened generations retain the full fresh dependency guards.
	s.mu.Lock()
	metadataLeaf := selected.Attempt == "" && chosen.dependents != nil && len(chosen.dependents[selected.ID]) == 0 && chosen.admissionGeneration != 0 && s.bulkStopping && s.admissionGeneration == chosen.admissionGeneration
	if !metadataLeaf {
		s.mu.Unlock()
	}
	readErr := s.store.db.View(func(tx *bolt.Tx) error {
		r, e := load(tx, selected.ID)
		if e != nil {
			return e
		}
		var budgets []*scanBudget
		if metadataLeaf {
			if r.Instance.Execution != nil || r.Instance.Task != nil {
				return problem("conflict", "bulk metadata target changed")
			}
			budgets = []*scanBudget{{ctx: ctx, unbounded: true, dependents: chosen.dependents}}
		}
		settled, e := consumerEligibility(tx, r, boot, budgets...)
		if e != nil {
			return e
		}
		if settled.Attempt != selected.Attempt || settled.Generation != selected.Generation {
			return problem("conflict", "bulk target changed")
		}
		result.Settlement = settled.State
		if r.Instance.Execution != nil {
			result.CollectionFailed = r.Instance.Execution.CollectionFailure != ""
		}
		return nil
	})
	if metadataLeaf {
		s.mu.Unlock()
	}
	switch {
	case ownerRunning:
		result.Reason = "stop_timeout"
	case readErr != nil:
		result.Reason = "state_unreadable"
	case result.CollectionFailed:
		result.Outcome, result.Reason = "failed", "collection_failed"
	case result.Settlement == v1.ConsumerSettled && err == nil:
		result.Outcome, result.Reason = "stopped", ""
	case result.Settlement != v1.ConsumerUnknown && err != nil:
		result.Outcome, result.Reason = "failed", "cleanup_failed"
	default:
		result.Reason = "ownership_unproven"
	}
	return result, false
}

// writeBulk observes compact outcomes without lending HTTP backpressure to cleanup.
func (s *service) writeBulk(w http.ResponseWriter, r *http.Request, operation *bulkOperation) {
	w.Header().Set("Content-Type", "application/x-ndjson")
	controller := http.NewResponseController(w)
	for index := 0; ; index++ {
		event, err := operation.next(r.Context(), index)
		if err != nil {
			return
		}
		// A blocked observer releases its connection; mutation ownership is independent.
		if err := controller.SetWriteDeadline(time.Now().Add(5 * time.Second)); err != nil && !errors.Is(err, http.ErrNotSupported) {
			return
		}
		if err := json.NewEncoder(w).Encode(event); err != nil {
			return
		}
		if err := controller.Flush(); err != nil {
			return
		}
		// No write deadline may run while waiting for workload cleanup.
		if err := controller.SetWriteDeadline(time.Time{}); err != nil && !errors.Is(err, http.ErrNotSupported) {
			return
		}
		if event.Type == "summary" {
			return
		}
	}
}
