package daemon

import (
	"context"
	"errors"
	"reflect"
	"time"

	"github.com/containerd/errdefs"
	v1 "github.com/octacian/backlot/api/v1"
	"github.com/octacian/backlot/internal/native"
	bolt "go.etcd.io/bbolt"
)

// consumerRelease binds authenticated cleanup to one exact post-cleanup effect set.
// It is private authority; the public settlement is only its diagnostic projection.
type consumerRelease struct {
	Attempt        string                     `json:"attempt"`
	Generation     string                     `json:"generation"`
	EffectRevision string                     `json:"effect_revision"`
	Cause          v1.ConsumerSettlementCause `json:"cause"`
	ObservedAt     string                     `json:"observed_at"`
}

func (r consumerRelease) valid() bool {
	if !validID(r.Attempt) || !validID(r.Generation) || !validID(r.EffectRevision) || len(r.ObservedAt) > 64 {
		return false
	}
	if _, err := time.Parse(time.RFC3339Nano, r.ObservedAt); err != nil {
		return false
	}
	return r.Cause == v1.SettlementExecutionCleanup || r.Cause == v1.SettlementExplicitStop || r.Cause == v1.SettlementDaemonRecovery
}

func consumerGeneration(f consumerFacts) string {
	if f.record.Instance.Task != nil {
		return f.record.Instance.Task.Generation
	}
	if f.resources.ID != "" {
		return f.resources.ID
	}
	return executionAttempt(f.record.Instance)
}

func consumerReleaseMatches(tx *bolt.Tx, f consumerFacts) bool {
	receipt := f.runtime.Release
	if receipt == nil || !receipt.valid() || receipt.Attempt != executionAttempt(f.record.Instance) || receipt.Generation != consumerGeneration(f) {
		return false
	}
	revision, err := consumerEffectRevision(tx, f.record)
	return err == nil && revision == receipt.EffectRevision
}

// verifyNativeRelease rechecks every exact launch, independently of worst history.
func verifyNativeRelease(j runtimeRecord, result *v1.ExecutionResult) error {
	if result == nil || j.Attempt != result.Attempt || j.LaunchPending {
		return problem("cleanup_failed", "native launch authority remains incomplete; resources preserved")
	}
	var failure error
	for _, group := range j.Groups {
		if group.Protocol != 0 && (group.Purpose != "application" || group.Attempt != j.Attempt || !hasComponent(result.Components, group.Component)) {
			return problem("cleanup_failed", "native launch authority mismatched; resources preserved")
		}
		_, err := native.StopResult(group, 0)
		failure = errors.Join(failure, err)
	}
	return failure
}

// recordConsumerRelease requires exclusive lifecycle ownership and joined collectors.
// The caller independently verified EVERY native launch in j; emptyVerified is only
// for a current joined zero-launch owner or independent pending-launch boot proof.
func (s *store) recordConsumerRelease(ctx context.Context, id string, j runtimeRecord, cause v1.ConsumerSettlementCause, emptyVerified bool) error {
	boot, _ := native.CurrentBoot()
	var f consumerFacts
	var revision string
	err := s.db.View(func(tx *bolt.Tx) error {
		r, err := load(tx, id)
		if err != nil {
			return err
		}
		f, err = consumerRecords(tx, r)
		if err != nil {
			return err
		}
		if j.Attempt != executionAttempt(r.Instance) || j.Attempt != f.runtime.Attempt || j.LaunchPending || f.runtime.LaunchPending || !reflect.DeepEqual(j.Groups, f.runtime.Groups) || j.PendingBoot != f.runtime.PendingBoot || j.LaunchPurpose != f.runtime.LaunchPurpose {
			return problem("cleanup_failed", "consumer launch authority changed before release; preserved")
		}
		revision, err = consumerEffectRevision(tx, r)
		return err
	})
	if err != nil {
		return err
	}
	if f.runtime.Gateway != nil || f.runtime.GatewayOnlyCleanup {
		return problem("cleanup_failed", "publication cleanup remains pending; resources preserved")
	}
	proof := emptyVerified || len(j.Groups) > 0
	var targets []ownedResource
	for _, r := range f.resources.Containers {
		if !r.Removed {
			return problem("cleanup_failed", "container cleanup remains pending; resources preserved")
		}
		if r.Attempt == j.Attempt {
			proof = true
			targets = append(targets, r)
		}
	}
	for _, r := range f.resources.Resources {
		if r.Kind == "network" {
			if !r.Removed {
				return problem("cleanup_failed", "network cleanup remains pending; resources preserved")
			}
			targets = append(targets, r)
		}
	}
	if !proof {
		return problem("cleanup_failed", "consumer release evidence is unavailable; preserve legacy records for diagnosis")
	}
	for _, a := range f.record.NativeAbandonments {
		if !earlierConsumerBoot(a.Boot, boot) || a.Pending && !earlierConsumerBoot(a.PendingBoot, boot) {
			return problem("cleanup_failed", "relinquished native work lacks independent absence proof; preserved")
		}
		for _, g := range a.Groups {
			if !earlierConsumerBoot(g.Boot, boot) {
				return problem("cleanup_failed", "relinquished native launch lacks independent absence proof; preserved")
			}
		}
	}
	if len(targets) > 0 {
		d, err := newDocker(f.resources.Endpoint)
		if err != nil {
			return err
		}
		defer func() { _ = d.Close() }()
		for _, r := range targets {
			observed := verifyDockerResource(ctx, d, r)
			if !errdefs.IsNotFound(observed) {
				return problem("cleanup_failed", "consumer resource absence could not be independently verified; preserved")
			}
		}
	}
	release := &consumerRelease{Attempt: j.Attempt, Generation: consumerGeneration(f), EffectRevision: revision, Cause: cause, ObservedAt: time.Now().UTC().Format(time.RFC3339Nano)}
	if !release.valid() {
		return problem("state_corrupt", "invalid consumer release operation; preserved")
	}
	return s.db.Update(func(tx *bolt.Tx) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		r, err := load(tx, id)
		if err != nil {
			return err
		}
		current, err := consumerEffectRevision(tx, r)
		if err != nil {
			return err
		}
		if executionAttempt(r.Instance) != j.Attempt || current != revision {
			return problem("conflict", "consumer effects changed before release; preserved")
		}
		// A parent receipt never substitutes for the child's current independent proof.
		if err := tx.Bucket([]byte("instances")).ForEach(func(key, data []byte) error {
			var child record
			if err := decodeRecord(data, string(key), &child); err != nil {
				return err
			}
			if child.Instance.Task == nil || child.Instance.Task.InstanceID != id {
				return nil
			}
			erased, err := consumerErased(tx, child)
			if err != nil || erased {
				return err
			}
			settled, err := consumerEligibility(tx, child, boot)
			if err != nil {
				return err
			}
			if settled.State != v1.ConsumerSettled {
				return problem("cleanup_failed", "dependent consumer release remains unverified; parent preserved")
			}
			return nil
		}); err != nil {
			return err
		}
		if r.Instance.Task != nil {
			if err := taskWriterParent(tx, r.Instance); err != nil {
				return err
			}
		}
		journal, err := decodeRuntime(tx.Bucket([]byte("runtime")).Get([]byte(id)))
		if err != nil {
			return err
		}
		journal.Release = release
		return put(tx, "runtime", id, journal)
	})
}
