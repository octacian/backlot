// Package daemon owns private preparation snapshots, native/container execution,
// retained resource generations, leases and durable recovery. Publication is deferred.
package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"time"

	v1 "github.com/octacian/backlot/api/v1"
	"github.com/octacian/backlot/internal/manifest"
	"github.com/octacian/backlot/internal/plan"
)

type service struct {
	evidence      evidencePolicy
	store         *store
	lease         time.Duration
	mu            sync.Mutex
	closing       bool
	directory     string
	executions    map[string]*execution
	outputs       map[string]chan struct{}
	mutations     map[string]*sync.Mutex
	expiryWorkers map[string]chan struct{}
	expiryErr     error
	// checkpoint is a test-only interruption seam after durable intent/effect.
	checkpoint func(string) error
	// resolve lets tests control the input-read boundary while using the real planner.
	resolve func(plan.Source, v1.PlanRequest) (plan.Snapshot, error)
	// beforeExecution gates tests between prepare and serialized acceptance.
	beforeExecution func(string)
}

func (s *service) prepare(ctx context.Context, request v1.PrepareRequest) (v1.InstanceResponse, error) {
	if request.Plan.Scene == "" || len(request.Plan.Scene) > 63 || !filepath.IsAbs(request.Plan.ProjectPath) || (request.Plan.ConfigPath != "" && !filepath.IsAbs(request.Plan.ConfigPath)) {
		return v1.InstanceResponse{}, problem("invalid_request", "scene and absolute project/config paths are required")
	}
	source, err := plan.Locate(request.Plan.ProjectPath)
	if err != nil {
		return v1.InstanceResponse{}, err
	}
	original, err := captureSource(source)
	if err != nil {
		return v1.InstanceResponse{}, err
	}
	resolve := plan.Prepare
	if s.resolve != nil {
		resolve = s.resolve
	}
	snapshot, err := resolve(source, request.Plan)
	if err != nil {
		return v1.InstanceResponse{}, err
	}
	public, err := json.Marshal(snapshot.Plan)
	if err != nil || len(public) > manifest.MaxBytes-8192 {
		return v1.InstanceResponse{}, problem("plan_too_large", "resolved public plan exceeds bounded API response size")
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closing {
		return v1.InstanceResponse{}, problem("shutting_down", "daemon is shutting down")
	}
	if err := ctx.Err(); err != nil {
		return v1.InstanceResponse{}, problem("cancelled", "request cancelled before acceptance")
	}
	if err := original.verify(); err != nil {
		return v1.InstanceResponse{}, err
	}
	response, created, err := s.store.begin(snapshot, original.checkout, s.lease)
	if err != nil || !created {
		return response, err
	}
	// Once accepted, persistent preparation is daemon-owned, independent of HTTP disconnect.
	if s.checkpoint != nil {
		if err := s.checkpoint("intent"); err != nil {
			return response, err
		}
	}
	if err := s.store.effect(response.Instance.ID, snapshot); err != nil {
		return response, err
	}
	if s.checkpoint != nil {
		if err := s.checkpoint("effect"); err != nil {
			return response, err
		}
	}
	response.Instance, err = s.store.complete(response.Instance.ID)
	return response, err
}

func validID(id string) bool {
	if len(id) != 64 {
		return false
	}
	for _, r := range id {
		if !strings.ContainsRune("0123456789abcdef", r) {
			return false
		}
	}
	return true
}
func (s *service) stop() error {
	s.mu.Lock()
	s.closing = true
	var ids []string
	for id := range s.executions {
		ids = append(ids, id)
	}
	s.mu.Unlock()
	result := s.stopExecutions(ids)
	// Shutdown fences acceptance before joining. Expiry coordinators retain their
	// own channels so storage cannot close while they finish durable results.
	result = errors.Join(result, s.joinExpiryWorkers())
	s.mu.Lock()
	expiryErr := s.expiryErr
	s.mu.Unlock()
	return errors.Join(result, expiryErr, s.store.reconcile("daemon shutdown interrupted preparation", false), s.store.cleanupFailures())
}

// expire dispatches each eligible owner independently. It never joins native
// cleanup in the daemon event loop; later sweeps and shutdown remain responsive.
func (s *service) expire() error {
	if err := s.expireOwners(); err != nil {
		return err
	}
	if err := s.pruneEvidence(); err != nil {
		return problem("evidence_retention_failed", "evidence retention failed; preserve private state for diagnosis")
	}
	return nil
}
func (s *service) expireOwners() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.expiryErr != nil {
		return s.expiryErr
	}
	if s.closing {
		return nil
	}
	for id := range s.executions {
		instance, err := s.store.inspect(id)
		if err != nil {
			return err
		}
		if !active(instance.Status) || instance.Status == v1.Stopping || instance.LeaseExpiresAt == "" || s.expiryWorkers[id] != nil {
			continue
		}
		expiry, err := time.Parse(time.RFC3339Nano, instance.LeaseExpiresAt)
		if err != nil {
			return err
		}
		if expiry.After(time.Now()) {
			continue
		}
		if s.expiryWorkers == nil {
			s.expiryWorkers = map[string]chan struct{}{}
		}
		done := make(chan struct{})
		s.expiryWorkers[id] = done
		go func() {
			err := s.expireRuntime(id) // recheck lease/eligibility under instance authority
			s.mu.Lock()
			s.expiryErr = errors.Join(s.expiryErr, err)
			close(done)
			delete(s.expiryWorkers, id)
			s.mu.Unlock()
		}()
	}
	return s.store.reconcile("", true)
}

func (s *service) joinExpiryWorkers() error {
	s.mu.Lock()
	var channels []<-chan struct{}
	for _, done := range s.expiryWorkers {
		channels = append(channels, done)
	}
	s.mu.Unlock()
	budget := time.NewTimer(20 * time.Second)
	defer budget.Stop()
	for _, done := range channels {
		select {
		case <-done:
		case <-budget.C:
			return problem("cleanup_failed", "lease cancellation coordinator did not join; state ownership retained")
		}
	}
	return nil
}

func (s *service) stopExecutions(ids []string) error {
	var result error
	var mu sync.Mutex
	var joined sync.WaitGroup
	for _, id := range ids {
		joined.Go(func() { // closing fences all new owner registrations. Shutdown can cancel/join
			// independently of an expiry coordinator already holding this instance lock.
			_, err := s.stopExecution(id)
			// Shutdown can race the executor's keep decision. After its finite owner
			// joins, stop any retained services before releasing daemon authority.
			s.mu.Lock()
			entry := s.executions[id]
			s.mu.Unlock()
			if entry != nil {
				select {
				case <-entry.done:
					entry.mu.Lock()
					kept := entry.kept
					entry.mu.Unlock()
					if kept {
						_, stopErr := s.stopKept(id, entry)
						err = errors.Join(err, stopErr)
					}
				default:
				}
			}
			mu.Lock()
			result = errors.Join(result, err)
			mu.Unlock()
		})
	}
	joined.Wait()
	return result
}
