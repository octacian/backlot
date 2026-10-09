package daemon

import (
	"context"
	"errors"
	"strings"
	"time"

	v1 "github.com/octacian/backlot/api/v1"
	"github.com/octacian/backlot/internal/plan"
)

func healthyService(entry *execution, work workload) bool {
	for _, component := range entry.services {
		if component == work {
			return work.Result() == nil && work.Alive()
		}
	}
	return false
}

// stopKept joins collectors and verified ownership before releasing output locks.
// Explicit stop and shutdown retain data; destroy removes it separately.
func (s *service) stopKept(id string, entry *execution) (v1.Instance, error) {
	entry.mu.Lock()
	defer entry.mu.Unlock()
	instance, err := s.store.inspect(id)
	if err != nil {
		return instance, err
	}
	if !entry.kept {
		return instance, nil
	}
	journal, err := s.store.runtime(id)
	if err != nil {
		return instance, err
	}
	var cleanup error
	for _, group := range entry.groups {
		cleanup = errors.Join(cleanup, group.Stop(entry.grace))
		if exit := group.Result(); exit != nil && exit.CollectionFailure != "" && instance.Execution.CollectionFailure == "" {
			instance.Execution.CollectionFailure = "retained native output/status collection could not be verified"
		}
	}
	for _, work := range entry.containers {
		cleanup = errors.Join(cleanup, work.Stop(entry.grace))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cleanup = errors.Join(cleanup, s.store.removeGateway(ctx, id, &journal))
	if cleanup == nil && entry.resources != nil {
		cleanup = s.store.cleanupContainers(ctx, id, entry.resources, entry.docker)
	}
	if cleanup != nil {
		instance.Execution.CleanupFailure = "retained runtime cleanup failed"
		_, saveErr := s.store.execution(id, v1.Failed, instance.Execution)
		return instance, errors.Join(problem("cleanup_failed", instance.Execution.CleanupFailure), saveErr)
	}
	if entry.docker != nil {
		_ = entry.docker.Close()
	}
	entry.kept = false
	instance.Execution.Kept = false
	for i := range instance.Execution.Components {
		if instance.Execution.Components[i].Status == "ready" {
			instance.Execution.Components[i].Status = "stopped"
		}
	}
	release := entry.outputRelease
	entry.outputRelease = nil
	if release != nil {
		release()
	}
	return s.store.execution(id, v1.Stopped, instance.Execution)
}

// sanitizeResult applies only to Backlot diagnostics, never application streams.
func sanitizeResult(result *v1.ExecutionResult, snapshot plan.Snapshot, entry *execution) {
	redact := func(text string) string {
		for _, value := range snapshot.Secrets {
			if value.Literal != nil && *value.Literal != "" {
				text = strings.ReplaceAll(text, *value.Literal, "[redacted]")
			}
		}
		if entry.resources != nil {
			for _, resource := range entry.resources.Resources {
				if resource.Kind == "secret" && resource.Value != "" {
					text = strings.ReplaceAll(text, resource.Value, "[redacted]")
				}
			}
		}
		return text
	}
	result.Failure = redact(result.Failure)
	result.CleanupFailure = redact(result.CleanupFailure)
	result.CollectionFailure = redact(result.CollectionFailure)
	if result.FailureDetail != nil {
		detail := *result.FailureDetail
		detail.Message = redact(detail.Message)
		detail.Field = redact(detail.Field)
		result.FailureDetail = &detail
	}
	for i := range result.CleanupDetails {
		result.CleanupDetails[i].Message = redact(result.CleanupDetails[i].Message)
		result.CleanupDetails[i].Field = redact(result.CleanupDetails[i].Field)
	}
}
