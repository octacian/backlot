package daemon

import (
	"context"
	"time"
)

// Accepted lifecycle mutations participate in daemon cancellation and storage ownership just
// like executions. The closing fence and registration are atomic under service.mu.
type mutationOwner struct {
	cancel context.CancelFunc
	done   chan struct{}
}

func (s *service) beginMutation(ctx context.Context) (context.Context, func(), error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closing {
		return nil, nil, problem("shutting_down", "daemon is shutting down")
	}
	ctx, cancel := context.WithCancel(ctx)
	owner := &mutationOwner{cancel: cancel, done: make(chan struct{})}
	if s.mutationOwners == nil {
		s.mutationOwners = map[*mutationOwner]struct{}{}
	}
	s.mutationOwners[owner] = struct{}{}
	return ctx, func() {
		cancel()
		s.mu.Lock()
		delete(s.mutationOwners, owner)
		close(owner.done)
		s.mu.Unlock()
	}, nil
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
