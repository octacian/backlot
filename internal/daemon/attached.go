package daemon

import (
	"crypto/subtle"
	v1 "github.com/octacian/backlot/api/v1"
	"time"
)

type attachment struct {
	owner   v1.AttachedOwner
	expires time.Time
	ended   bool
}

func (s *service) attached(request v1.AttachedRequest, cancel bool) (v1.SceneResponse, error) {
	response := v1.SceneResponse{APIVersion: v1.Version, DaemonGeneration: s.generation, Outcome: v1.SceneFound}
	owner := request.Owner
	if !validID(request.InstanceID) || !validID(owner.Attempt) || !validID(owner.Token) || !validID(owner.DaemonGeneration) {
		return response, problem("invalid_request", "attached operation requires instance, attempt, daemon generation and private capability")
	}
	// Renewal/status must remain readable while an operator holds the mutation
	// lock joining cleanup. service.mu also serializes revocation and replacement.
	if cancel {
		unlock := s.lockMutation(request.InstanceID)
		defer unlock()
	}
	s.mu.Lock()
	entry := s.executions[request.InstanceID]
	if entry == nil || entry.attachment == nil || owner.DaemonGeneration != s.generation || owner.Attempt != entry.attachment.owner.Attempt || subtle.ConstantTimeCompare([]byte(owner.Token), []byte(entry.attachment.owner.Token)) != 1 {
		s.mu.Unlock()
		return response, problem("conflict", "attached execution authority changed or is unavailable")
	}
	instance, err := s.store.inspect(request.InstanceID)
	if err != nil {
		s.mu.Unlock()
		return response, err
	}
	if instance.Execution == nil || instance.Execution.Attempt != owner.Attempt {
		s.mu.Unlock()
		return response, problem("conflict", "attached execution was replaced")
	}
	a := entry.attachment
	running := instance.Status == v1.Starting || instance.Status == v1.RuntimeReady
	if running && !a.ended && !a.expires.After(time.Now()) {
		s.mu.Unlock()
		return response, problem("lease_expired", "attachment ended or expired; cancellation is pending cleanup")
	}
	if active(instance.Status) {
		select {
		case <-entry.done:
			s.mu.Unlock()
			return response, problem("cleanup_failed", "ended attachment has no settled runtime result")
		default:
		}
	}
	if running && !cancel && !a.ended {
		a.expires = time.Now().Add(s.lease)
	}
	s.mu.Unlock()
	if cancel && (running || instance.Status == v1.Stopping) {
		instance, err = s.stopExecution(request.InstanceID)
	}
	response.Result = &v1.InstanceResponse{APIVersion: v1.Version, Instance: instance}
	return response, err
}
