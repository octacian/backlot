package daemon

import (
	"context"
	"time"
)

// Accepted lifecycle mutations participate in daemon cancellation and storage ownership just
// like executions. The closing fence and registration are atomic under service.mu.
type mutationOwner struct {
	cancel     context.CancelFunc
	done       chan struct{}
	workload   bool
	generation uint64
	id         string
	checkout   string
	admittedAt uint64
}

func (s *service) beginMutation(ctx context.Context) (context.Context, func(), error) {
	return s.beginOwnedMutation(ctx, false)
}

func (s *service) stopMutations() error {
	s.mu.Lock()
	var owners []*mutationOwner
	for owner := range s.mutationOwners {
		owners = append(owners, owner)
		owner.cancel()
	}
	s.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	for _, owner := range owners {
		select {
		case <-owner.done:
		case <-ctx.Done():
			return problem("cleanup_failed", "lifecycle mutation owner remains unjoined; state ownership retained")
		}
	}
	return nil
}

// admissionKey carries revocable authority across input resolution and instance locks.
type admissionKey struct{}

func (s *service) beginWorkload(ctx context.Context) (context.Context, func(), error) {
	return s.beginOwnedMutation(ctx, true)
}

func (s *service) beginOwnedMutation(ctx context.Context, workload bool) (context.Context, func(), error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closing {
		return nil, nil, problem("shutting_down", "daemon is shutting down")
	}
	if workload && s.bulkStopping {
		return nil, nil, problem("admission_closed", "workload admission is closed; inspect activity before starting again")
	}
	ctx, cancel := context.WithCancel(ctx)
	// invariant: discovery cannot renew a ticket admitted before scoped revocation.
	if workload {
		s.admissionSequence++
	}
	owner := &mutationOwner{id: newID(), cancel: cancel, done: make(chan struct{}), workload: workload, generation: s.admissionGeneration, admittedAt: s.admissionSequence}
	if s.mutationOwners == nil {
		s.mutationOwners = map[*mutationOwner]struct{}{}
	}
	s.mutationOwners[owner] = struct{}{}
	if workload {
		ctx = context.WithValue(ctx, admissionKey{}, owner)
	}
	return ctx, func() {
		cancel()
		s.mu.Lock()
		delete(s.mutationOwners, owner)
		close(owner.done)
		s.mu.Unlock()
	}, nil
}

// workloadAllowed requires service.mu; reopening never revives an earlier ticket.
func (s *service) workloadAllowed(ctx context.Context) error {
	owner, _ := ctx.Value(admissionKey{}).(*mutationOwner)
	if s.closing {
		return problem("shutting_down", "daemon is shutting down")
	}
	if s.bulkStopping || owner != nil && owner.generation != s.admissionGeneration {
		return problem("admission_closed", "workload admission was invalidated; inspect activity before starting again")
	}
	if owner != nil && owner.checkout != "" && (s.cleanupFences[owner.checkout] || owner.admittedAt < s.checkoutRevocations[owner.checkout]) {
		return problem("admission_closed", "checkout admission was invalidated by cleanup")
	}
	if ctx.Err() != nil {
		return problem("cancelled", "request cancelled before workload acceptance")
	}
	return nil
}

func (s *service) checkWorkload(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.workloadAllowed(ctx)
}
