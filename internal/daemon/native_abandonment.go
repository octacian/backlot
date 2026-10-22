package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"time"

	v1 "github.com/octacian/backlot/api/v1"
	"github.com/octacian/backlot/internal/native"
	bolt "go.etcd.io/bbolt"
)

const nativeWaiverWarning = "Native ownership was explicitly relinquished with --force; unknown survivors may still run or write. Cleanup and lost collection were not verified."

// nativeAbandonment stays outside replaced attempt journals. Capabilities and
// original results remain private; public summaries contain no process authority.
type nativeAbandonment struct {
	NativeResult *native.Exit        `json:"native_result,omitempty"`
	Failure      string              `json:"cleanup_failure,omitempty"`
	Attempt      string              `json:"attempt,omitempty"`
	RecordedAt   string              `json:"recorded_at"`
	Reason       string              `json:"reason"`
	Boot         string              `json:"acknowledgement_boot,omitempty"`
	Groups       []native.Identity   `json:"groups,omitempty"`
	Pending      bool                `json:"unidentified_intent,omitempty"`
	PendingBoot  string              `json:"intent_boot,omitempty"`
	Result       *v1.ExecutionResult `json:"original_result,omitempty"`
}

func (a nativeAbandonment) warning() v1.NativeOwnershipWarning {
	return v1.NativeOwnershipWarning{Attempt: a.Attempt, RecordedAt: a.RecordedAt, Message: nativeWaiverWarning}
}
func abandonmentWarnings(history []nativeAbandonment) []v1.NativeOwnershipWarning {
	var warnings []v1.NativeOwnershipWarning
	for _, a := range history {
		warnings = append(warnings, a.warning())
	}
	return warnings
}

// guardAbandonedData requires independent resource proof, never waiver itself,
// before deleting data an unknown survivor might still consume. History is kept.
func (s *store) guardAbandonedData(id string) error {
	var history []nativeAbandonment
	if err := s.db.View(func(tx *bolt.Tx) error { r, err := load(tx, id); history = r.NativeAbandonments; return err }); err != nil {
		return err
	}
	for _, a := range history {
		if previous, err := native.PreviousBoot(a.Boot); err == nil && previous {
			continue
		}
		if a.Pending || len(a.Groups) == 0 {
			return problem("native_ownership_relinquished", "unidentified relinquished native work may still consume data; preserve it until independent absence proof after a controlled reboot")
		}
		for _, group := range a.Groups {
			if err := native.Stop(group, 0); err != nil {
				return problem("native_ownership_relinquished", "relinquished native work may still consume data; restore authenticated cleanup or perform a controlled reboot before reset/destroy")
			}
		}
	}
	return nil
}

func (s *service) stopRuntimeRequest(ctx context.Context, request v1.StopRequest) (v1.Instance, error) {
	unlock := s.lockMutation(request.InstanceID)
	defer unlock()
	before, err := s.store.inspect(request.InstanceID)
	if err != nil {
		return before, err
	}
	if request.Force && ((before.Execution != nil && request.ExpectedAttempt != before.Execution.Attempt) || (before.Execution == nil && request.ExpectedAttempt != "")) {
		return before, problem("conflict", "selected execution attempt changed; inspect before issuing a new force stop")
	}
	instance, err := s.stopExecution(request.InstanceID)
	var detail *v1.PlanError
	if !request.Force || !errors.As(err, &detail) || detail.Code != "cleanup_failed" {
		return instance, err
	}
	// Joining the finite owner first prevents it from overwriting the waiver.
	s.mu.Lock()
	entry := s.executions[request.InstanceID]
	s.mu.Unlock()
	if entry != nil {
		select {
		case <-entry.done:
		default:
			return instance, err
		}
	}
	if before.Execution == nil {
		return instance, err
	}
	instance, err = s.store.abandonNative(ctx, request.InstanceID, before.Execution.Attempt)
	if err == nil && entry != nil {
		entry.mu.Lock()
		entry.kept = false
		entry.mu.Unlock()
		if entry.docker != nil {
			_ = entry.docker.Close()
		}
		for _, group := range entry.groups {
			group.Relinquish()
		}
		entry.releaseOutputs()
	}
	return instance, err
}

// abandonNative requires the caller's per-target mutation lock and joined owner.
// Ordinary route/container cleanup must succeed; only native obligations can be
// waived. Commit history, public warning, result and released claims atomically.
func (s *store) abandonNative(ctx context.Context, id, attempt string) (v1.Instance, error) {
	var instance v1.Instance
	journal, err := s.runtime(id)
	if err != nil {
		return instance, err
	}
	instance, err = s.inspect(id)
	if err != nil {
		return instance, err
	}
	if instance.Execution == nil || instance.Execution.Attempt != attempt || journal.Attempt != attempt {
		return instance, problem("conflict", "native waiver target attempt changed or journal mismatched; preserved")
	}
	if err := ctx.Err(); err != nil {
		return instance, err
	}
	cleanupCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	if err := s.removeGateway(cleanupCtx, id, &journal); err != nil {
		return instance, err
	}
	resources, err := s.resources(id)
	if err != nil {
		return instance, err
	}
	if err := s.recoverContainers(id, &resources); err != nil {
		return instance, err
	}
	var unknown []native.Identity
	for _, group := range journal.Groups {
		if err := native.Stop(group, 0); err != nil {
			unknown = append(unknown, group)
		}
	}
	pending := journal.LaunchPending
	if pending {
		if previous, e := native.PreviousBoot(journal.PendingBoot); e == nil && previous {
			pending = false
		}
	}
	// Legacy ready/failed records may contain no identifiable native effect at all.
	if len(journal.Groups) == 0 && len(resources.Containers) == 0 {
		pending = true
	}
	// A sticky observation still requires an exact-attempt acknowledgement even
	// after every authority has recovered a successful receipt. Retain those
	// known authorities so data deletion can independently recheck their proof;
	// an empty unknown set is not an unidentified survivor.
	sticky := false
	for _, observed := range instance.Execution.NativeCleanup {
		sticky = sticky || observed.FencesResources(instance.NativeOwnershipWarnings)
	}
	auditGroups := unknown
	if sticky && len(unknown) == 0 && !pending {
		auditGroups = append([]native.Identity(nil), journal.Groups...)
	}
	boot, _ := native.CurrentBoot()
	audit := nativeAbandonment{Attempt: attempt, RecordedAt: time.Now().UTC().Format(time.RFC3339Nano), Reason: nativeWaiverWarning, Boot: boot, Groups: auditGroups, Pending: pending, PendingBoot: journal.PendingBoot, Result: instance.Execution}
	err = s.db.Update(func(tx *bolt.Tx) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		r, err := load(tx, id)
		if err != nil {
			return err
		}
		if r.Instance.Execution == nil || r.Instance.Execution.Attempt != attempt {
			return problem("conflict", "native waiver target attempt changed; preserved")
		}
		var current runtimeRecord
		if err := json.Unmarshal(tx.Bucket([]byte("runtime")).Get([]byte(id)), &current); err != nil {
			return err
		}
		if !reflect.DeepEqual(current, journal) {
			return problem("conflict", "native claims changed before waiver; preserved")
		}
		audit.Result = r.Instance.Execution
		if len(unknown) > 0 || pending || sticky {
			r.NativeAbandonments = append(r.NativeAbandonments, audit)
		}
		current.Groups = nil
		current.LaunchPending = false
		current.PendingBoot = ""
		result := *r.Instance.Execution
		result.Kept = false
		if result.CollectionFailure == "" {
			result.CollectionFailure = "native output/status collection could not be verified"
		}
		// Non-native fences succeeded above. Prior cleanup failure stays in the audit.
		result.CleanupFailure = ""
		result.CleanupDetails = nil
		if result.CompletedAt == "" {
			result.CompletedAt = audit.RecordedAt
		}
		r.Instance.Execution = &result
		r.Instance.NativeOwnershipWarnings = abandonmentWarnings(r.NativeAbandonments)
		terminal(&r, v1.Stopped, "explicit native ownership relinquishment; collection remains incomplete")
		if err := put(tx, "runtime", id, current); err != nil {
			return err
		}
		if err := put(tx, "instances", id, r); err != nil {
			return err
		}
		if s.waiverCheckpoint != nil {
			if err := s.waiverCheckpoint(); err != nil {
				return err
			}
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		instance = r.Instance
		return nil
	})
	return instance, err
}

func (s *store) abandonManaged(ctx context.Context, r *managedRecord, expected managedRecord, result *native.Exit, failure error) error {
	boot, _ := native.CurrentBoot()
	audit := nativeAbandonment{NativeResult: result, Failure: failure.Error(), RecordedAt: time.Now().UTC().Format(time.RFC3339Nano), Reason: nativeWaiverWarning, Boot: boot, Pending: r.Pending, PendingBoot: r.PendingBoot}
	if r.Identity != nil {
		audit.Groups = []native.Identity{*r.Identity}
	}
	next := *r
	next.NativeAbandonments = append(append([]nativeAbandonment(nil), r.NativeAbandonments...), audit)
	next.Identity = nil
	next.Pending = false
	next.PendingBoot = ""
	next.Status = "stopped"
	next.Failure = nativeWaiverWarning
	if err := s.db.Update(func(tx *bolt.Tx) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		current, err := decodeManagedRecord(tx.Bucket([]byte("meta")).Get([]byte("managed_gateway")))
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(current, expected) {
			return problem("conflict", "managed native claims changed before waiver; preserved")
		}
		if err := put(tx, "meta", "managed_gateway", next); err != nil {
			return err
		}
		if s.waiverCheckpoint != nil {
			if err := s.waiverCheckpoint(); err != nil {
				return err
			}
		}
		return ctx.Err()
	}); err != nil {
		return err
	}
	*r = next
	return nil
}
