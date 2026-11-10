package daemon

import (
	"context"
	"errors"
	"time"

	v1 "github.com/octacian/backlot/api/v1"
	"github.com/octacian/backlot/internal/native"
	bolt "go.etcd.io/bbolt"
)

const bulkStopBudget = 2 * time.Minute

type bulkSelection struct {
	target v1.StopAllTarget
	parent string
}

func (s *service) selectBulk(ctx context.Context) ([]bulkSelection, error) {
	boot, _ := native.CurrentBoot()
	budget := &scanBudget{ctx: ctx}
	selected := []bulkSelection{}
	err := s.store.db.View(func(tx *bolt.Tx) error {
		if err := boundActivityState(tx, budget); err != nil {
			return err
		}
		return tx.Bucket([]byte("instances")).ForEach(func(key, raw []byte) error {
			if err := chargeScan([]*scanBudget{budget}, len(raw)); err != nil {
				return err
			}
			var r record
			if err := decodeRecord(raw, string(key), &r); err != nil {
				return err
			}
			erased, err := consumerErased(tx, r, budget)
			if err != nil || erased {
				return err
			}
			settled, err := consumerEligibility(tx, r, boot, budget)
			if err != nil {
				return err
			}
			if settled.State == v1.ConsumerSettled {
				return nil
			}
			if len(selected) == v1.BulkStopTargetLimit {
				return problem("scope_too_large", "bulk stop exceeds 25 targets; use explicit recorded stops")
			}
			target := v1.StopAllTarget{ID: string(key), Attempt: settled.Attempt, Generation: settled.Generation, Outcome: "unresolved", Settlement: settled, SettlementObservation: "selected"}
			parent := ""
			if r.Instance.Task != nil {
				parent = r.Instance.Task.InstanceID
			}
			if r.Instance.Execution != nil {
				target.CollectionFailed = r.Instance.Execution.CollectionFailure != ""
			}
			selected = append(selected, bulkSelection{target: target, parent: parent})
			return nil
		})
	})
	return selected, err
}

func (s *service) stopAll(ctx context.Context, q v1.StopAllRequest) (v1.StopAllResponse, error) {
	if err := q.Validate(); err != nil {
		return v1.StopAllResponse{}, err
	}
	owned, finish, err := s.beginMutation(context.Background())
	if err != nil {
		return v1.StopAllResponse{}, err
	}
	s.mu.Lock()
	if s.closing || s.bulkStopping {
		s.mu.Unlock()
		finish()
		return v1.StopAllResponse{}, problem("admission_closed", "bulk stop or daemon shutdown is already in progress; inspect activity")
	}
	if len(s.mutationOwners) > 10000 {
		s.mu.Unlock()
		finish()
		return v1.StopAllResponse{}, problem("scope_too_large", "bulk stop exceeds 10000 lifecycle owners; inspect activity and use explicit targets")
	}
	s.bulkStopping = true
	s.admissionGeneration++
	preceding := []*mutationOwner{}
	for owner := range s.mutationOwners {
		if owner.workload {
			preceding = append(preceding, owner)
			owner.cancel()
		}
	}
	s.mu.Unlock()
	done := make(chan v1.StopAllResponse, 1)
	settled := make(chan struct{})
	go func() {
		defer close(settled)
		defer finish()
		defer func() { s.mu.Lock(); s.bulkStopping = false; s.mu.Unlock() }()
		bounded, cancel := context.WithTimeout(owned, bulkStopBudget)
		defer cancel()
		out := v1.StopAllResponse{APIVersion: v1.Version, DaemonGeneration: s.generation, OperationID: newID(), Targets: []v1.StopAllTarget{}, AdmissionsJoined: true}
		joinCtx, joinCancel := context.WithTimeout(bounded, 20*time.Second)
		for _, owner := range preceding {
			select {
			case <-owner.done:
			case <-joinCtx.Done():
				out.AdmissionsJoined = false
			}
		}
		joinCancel()
		selected, selectionErr := s.selectBulk(bounded)
		if selectionErr != nil {
			// No target stop has been issued; admission invalidation may already have effects.
			out.Error = &v1.PlanError{Code: "partial_failure", Message: "bulk stop incomplete; inspect activity and selected targets before explicit recovery"}
			out.SelectionFailure = "state_unreadable"
			var detail *v1.PlanError
			if errors.As(selectionErr, &detail) && detail.Code == "scope_too_large" {
				out.SelectionFailure = "scope_too_large"
			}
			if bounded.Err() != nil {
				out.SelectionFailure = "budget_exhausted"
			}
			done <- out
			return
		}
		out.SelectionComplete = true
		// Children first. A parent never starts cleanup after an unresolved selected child.
		ordered := make([]bulkSelection, 0, len(selected))
		for _, t := range selected {
			if t.parent != "" {
				ordered = append(ordered, t)
			}
		}
		for _, t := range selected {
			if t.parent == "" {
				ordered = append(ordered, t)
			}
		}
		blockedParents := map[string]bool{}
		exhausted := false
		for _, chosen := range ordered {
			result := chosen.target
			if !exhausted && !blockedParents[result.ID] && bounded.Err() == nil {
				result, exhausted = s.stopBulkTarget(bounded, result)
			}
			if chosen.parent != "" && result.Settlement.State != v1.ConsumerSettled {
				blockedParents[chosen.parent] = true
			}
			out.Targets = append(out.Targets, result)
			if result.Outcome != "completed" {
				out.Error = &v1.PlanError{Code: "partial_failure", Message: "bulk stop incomplete; inspect activity and selected targets before explicit recovery"}
			}
		}
		if !out.AdmissionsJoined {
			out.Error = &v1.PlanError{Code: "partial_failure", Message: "bulk stop incomplete; inspect activity and selected targets before explicit recovery"}
		}
		done <- out
	}()
	select {
	case result := <-done:
		<-settled
		return result, nil
	case <-ctx.Done():
		return v1.StopAllResponse{}, problem("result_unknown", "bulk stop accepted; waiting ended, cleanup continues; inspect activity in the same scope before explicit recovery; do not automatically retry")
	}
}

// stopBulkTarget never waits for a busy lock. Started cleanup owns both registration
// and serialization until completion, even when coordination stops waiting.
func (s *service) stopBulkTarget(ctx context.Context, selected v1.StopAllTarget) (v1.StopAllTarget, bool) {
	lock := s.mutationLock(selected.ID)
	if !lock.TryLock() {
		return selected, false
	}
	_, finish, err := s.beginMutation(context.Background())
	if err != nil {
		lock.Unlock()
		return selected, false
	}
	done := make(chan v1.StopAllTarget, 1)
	go func() {
		defer finish()
		defer lock.Unlock()
		result := selected
		boot, _ := native.CurrentBoot()
		err := s.store.db.View(func(tx *bolt.Tx) error {
			r, err := load(tx, selected.ID)
			if err != nil {
				return err
			}
			facts, err := consumerRecords(tx, r)
			if err != nil {
				return err
			}
			if executionAttempt(r.Instance) != selected.Attempt || selected.Attempt != "" && consumerGeneration(facts) != selected.Generation {
				return problem("conflict", "bulk target attempt or generation changed; no cleanup issued")
			}
			return nil
		})
		if err != nil {
			done <- result
			return
		}
		s.mu.Lock()
		entry := s.executions[selected.ID]
		if entry != nil && entry.attempt == selected.Attempt {
			entry.bulkCancelled = true
		}
		s.mu.Unlock()
		if selected.Attempt == "" {
			_, err = s.store.cancel(selected.ID)
		} else {
			_, err = s.stopExecution(selected.ID)
		}
		// An owner may have made its keep decision immediately before bulk cancellation.
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
		readErr := s.store.db.View(func(tx *bolt.Tx) error {
			r, e := load(tx, selected.ID)
			if e != nil {
				return e
			}
			settled, e := consumerEligibility(tx, r, boot)
			if e != nil {
				return e
			}
			if settled.Attempt != selected.Attempt || settled.Generation != selected.Generation {
				return problem("conflict", "bulk target changed")
			}
			result.Settlement = settled
			result.SettlementObservation = "after_stop"
			if r.Instance.Execution != nil {
				result.CollectionFailed = r.Instance.Execution.CollectionFailure != ""
			}
			return nil
		})
		if readErr == nil {
			if result.Settlement.State == v1.ConsumerSettled && err == nil && !result.CollectionFailed {
				result.Outcome = "completed"
			} else if result.Settlement.State != v1.ConsumerUnknown && err != nil || result.CollectionFailed {
				result.Outcome = "failed"
			}
		}
		done <- result
	}()
	select {
	case result := <-done:
		return result, false
	case <-ctx.Done():
		return selected, true
	}
}
