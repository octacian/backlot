// Package daemon owns private preparation snapshots, native lifecycle execution,
// leases and durable recovery. It does not contact Docker or Caddy.
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
	store      *store
	lease      time.Duration
	mu         sync.Mutex
	closing    bool
	directory  string
	executions map[string]*execution
	outputs    map[string]chan struct{}
	mutations  map[string]*sync.Mutex
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

	return errors.Join(result, s.store.reconcile("daemon shutdown interrupted preparation", false), s.store.cleanupFailures())
}
func (s *service) expire() error {
	s.mu.Lock()
	var ids []string
	for id := range s.executions {
		instance, err := s.store.inspect(id)
		if err != nil {
			s.mu.Unlock()
			return err
		}
		if instance.LeaseExpiresAt != "" && instance.Status != v1.Stopping {
			expiry, err := time.Parse(time.RFC3339Nano, instance.LeaseExpiresAt)
			if err != nil {
				s.mu.Unlock()
				return err
			}
			if !expiry.After(time.Now()) {
				ids = append(ids, id)
			}
		}
	}
	s.mu.Unlock()
	var expiryErr error
	for _, id := range ids {
		expiryErr = errors.Join(expiryErr, s.expireRuntime(id))
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return errors.Join(expiryErr, s.store.reconcile("", true))
}

func (s *service) stopExecutions(ids []string) error {
	var result error
	var mu sync.Mutex
	var joined sync.WaitGroup
	for _, id := range ids {
		joined.Go(func() { _, err := s.stopRuntime(id); mu.Lock(); result = errors.Join(result, err); mu.Unlock() })
	}
	joined.Wait()
	return result
}
