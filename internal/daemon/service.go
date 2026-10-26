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
	"github.com/octacian/backlot/internal/native"
	"github.com/octacian/backlot/internal/plan"
	bolt "go.etcd.io/bbolt"
)

type service struct {
	generation            string
	diagnosticRecoveryErr error
	diagnosticOwners      map[string]*diagnosticOwner
	mutationOwners        map[*mutationOwner]struct{}
	gatewayGroup          *native.Group
	evidence              evidencePolicy
	store                 *store
	lease                 time.Duration
	mu                    sync.Mutex
	closing               bool
	directory             string
	executions            map[string]*execution
	outputs               map[string]chan struct{}
	mutations             map[string]*sync.Mutex
	expiryWorkers         map[string]chan struct{}
	expiryErr             error
	// checkpoint is a test-only interruption seam after durable intent/effect.
	checkpoint func(string) error
	// resolve lets tests control the input-read boundary while using the real planner.
	resolve func(plan.Source, v1.PlanRequest) (plan.Snapshot, error)
	// beforeExecution gates tests between prepare and serialized acceptance.
	beforeExecution func(string)
	// beforeSceneMutation is a test seam between recorded resolution and its mutation lock.
	beforeSceneMutation func(string)
}

func validatePlanRequest(request v1.PlanRequest) error {
	if request.Scene == "" || len(request.Scene) > 63 || !filepath.IsAbs(request.ProjectPath) || (request.ConfigPath != "" && !filepath.IsAbs(request.ConfigPath)) {
		return problem("invalid_request", "scene and absolute project/config paths are required")
	}
	return nil
}

func (s *service) prepare(ctx context.Context, request v1.PrepareRequest) (v1.InstanceResponse, error) {
	return s.prepareSelected(ctx, request, false)
}

func (s *service) prepareSelected(ctx context.Context, request v1.PrepareRequest, persistentOnly bool) (v1.InstanceResponse, error) {
	if err := validatePlanRequest(request.Plan); err != nil {
		return v1.InstanceResponse{}, err
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
	return s.prepareCaptured(ctx, snapshot, original, request.Plan.Scene, persistentOnly)
}

// prepareCaptured accepts the exact generation resolved by preparation or launch.
func (s *service) prepareCaptured(ctx context.Context, snapshot plan.Snapshot, original capturedSource, scene string, persistentOnly bool) (v1.InstanceResponse, error) {
	if persistentOnly && snapshot.Plan.Lifetime != v1.Persistent {
		return v1.InstanceResponse{}, problem("invalid_lifetime", "scene start requires a persistent scene; use run for disposable work")
	}
	if persistentOnly && (snapshot.Plan.ManifestPath != original.source.ManifestPath || snapshot.Plan.Checkout != original.checkout.Path || snapshot.Plan.Scene != scene) {
		return v1.InstanceResponse{}, problem("invalid_request", "resolved scene location disagrees with captured source")
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
	if len(snapshot.Plan.SharedServices) > 0 {
		identity, err := identifyShared(original.source, snapshot.Plan.Project)
		if err != nil {
			return v1.InstanceResponse{}, err
		}
		if err := s.store.db.Update(func(tx *bolt.Tx) error { return registerSharedProject(tx, identity) }); err != nil {
			return v1.InstanceResponse{}, err
		}
	}
	response, created, err := s.store.beginSelected(snapshot, original.checkout, s.lease, persistentOnly)
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
	result := errors.Join(s.stopMutations(), s.stopDiagnostics(), s.stopExecutions(ids))
	// Shutdown fences acceptance before joining. Expiry coordinators retain their
	// own channels so storage cannot close while they finish durable results.
	result = errors.Join(result, s.joinExpiryWorkers())
	// Recovered executions have no entry in executions. Retry their durable
	// exact effects before stopping a gateway explicitly resumed for cleanup.
	result = errors.Join(result, s.recoverUnownedExecutions())
	s.mu.Lock()
	expiryErr := s.expiryErr
	s.mu.Unlock()
	s.mu.Lock()
	gatewayRecord, gatewayErr := s.store.managed()
	if gatewayErr == nil {
		gatewayErr = s.store.stopManaged(&gatewayRecord, s.gatewayGroup)
	}
	s.mu.Unlock()
	var sharedErr error
	if s.ownersJoined() {
		sharedErr = s.store.stopSharedServices(context.Background())
	}
	return errors.Join(result, expiryErr, gatewayErr, sharedErr, s.store.reconcile("daemon shutdown interrupted preparation", false), s.store.cleanupFailures())
}

func (s *service) recoverUnownedExecutions() error {
	ids, err := s.store.runtimeRecoveryIDs()
	if err != nil {
		return err
	}
	var result error
	for _, id := range ids {
		s.mu.Lock()
		entry := s.executions[id]
		s.mu.Unlock()
		// Current owners already went through cancellation/join above. In
		// particular, a failed join never authorizes concurrent recovery or
		// waiting on a mutation lock still held by its cancellation coordinator.
		if entry != nil {
			continue
		}
		// Closing fences new owners; serialize with any explicit cleanup request
		// already acting on this recovered instance.
		unlock := s.lockMutation(id)
		result = errors.Join(result, s.store.recoverExecution(id))
		unlock()
	}
	return result
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
	for id, entry := range s.executions {
		instance, err := s.store.inspect(id)
		if err != nil {
			return err
		}
		if !active(instance.Status) || instance.Status == v1.Stopping || (instance.LeaseExpiresAt == "" && entry.attachment == nil) || s.expiryWorkers[id] != nil {
			continue
		}
		expiry, err := time.Parse(time.RFC3339Nano, instance.LeaseExpiresAt)
		if a := entry.attachment; a != nil {
			if a.ended {
				continue
			}
			expiry, err = a.expires, nil
		}
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
			err := s.expireRuntime(id, entry) // recheck lease/eligibility under instance authority
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
	var grace time.Duration
	for id, done := range s.expiryWorkers {
		channels = append(channels, done)
		if entry := s.executions[id]; entry != nil {
			grace = max(grace, entry.grace)
		}
	}
	s.mu.Unlock()
	// Coordinators join their owners concurrently, so the longest owner join bounds all.
	budget := time.NewTimer(OwnerJoin(grace) + 5*time.Second)
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
