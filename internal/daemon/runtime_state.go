package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	v1 "github.com/octacian/backlot/api/v1"
	"github.com/octacian/backlot/internal/gateway"
	"github.com/octacian/backlot/internal/native"
	"github.com/octacian/backlot/internal/plan"
	bolt "go.etcd.io/bbolt"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

type runtimeRecord struct {
	ExportSource  *v1.ExportSource    `json:"export_source,omitempty"`
	StartedAt     string              `json:"started_at,omitempty"`
	Gateway       *gateway.Intent     `json:"gateway,omitempty"`
	LaunchPurpose string              `json:"launch_purpose,omitempty"`
	PendingBoot   string              `json:"pending_boot,omitempty"`
	LaunchPending bool                `json:"launch_pending,omitempty"`
	ConfigPath    string              `json:"config_path,omitempty"`
	Attempt       string              `json:"attempt"`
	Groups        []native.Identity   `json:"groups"`
	Options       v1.ExecutionOptions `json:"options"`
}

func (s *store) snapshot(id string) (plan.Snapshot, error) {
	var snap plan.Snapshot
	err := s.db.View(func(tx *bolt.Tx) error {
		if _, err := load(tx, id); err != nil {
			return err
		}
		data := tx.Bucket([]byte("snapshots")).Get([]byte(id))
		if data == nil {
			return problem("state_corrupt", "private snapshot missing")
		}
		if err := json.Unmarshal(data, &snap); err != nil {
			return err
		}
		return json.Unmarshal(tx.Bucket([]byte("secrets")).Get([]byte(id)), &snap.Secrets)
	})
	return snap, err
}
func (s *store) runtime(id string) (runtimeRecord, error) {
	var r runtimeRecord
	err := s.db.View(func(tx *bolt.Tx) error {
		data := tx.Bucket([]byte("runtime")).Get([]byte(id))
		if data == nil {
			return nil
		}
		var decodeErr error
		r, decodeErr = decodeRuntime(data)
		return decodeErr
	})
	return r, err
}
func decodeRuntime(data []byte) (r runtimeRecord, err error) {
	if !jsonObject(data) || json.Unmarshal(data, &r) != nil {
		return r, problem("state_corrupt", "invalid runtime record shape; preserved")
	}
	if r.Attempt != "" && !validID(r.Attempt) {
		return r, problem("state_corrupt", "invalid runtime attempt; preserved")
	}
	return r, nil
}

func (s *store) saveRuntime(id string, r runtimeRecord) error {
	return s.db.Update(func(tx *bolt.Tx) error { return put(tx, "runtime", id, r) })
}
func (s *store) execution(id string, status v1.InstanceStatus, result *v1.ExecutionResult) (v1.Instance, error) {
	var out v1.Instance
	err := s.db.Update(func(tx *bolt.Tx) error {
		r, err := load(tx, id)
		if err != nil {
			return err
		}
		if result != nil && r.Instance.Execution != nil && r.Instance.Execution.Attempt != result.Attempt {
			return problem("conflict", "execution writer no longer owns current attempt")
		}
		if result != nil && status != v1.Starting && status != v1.RuntimeReady && status != v1.Stopping && status != v1.Destroyed && result.Outcome == "" {
			result.Outcome = status
		}
		if result != nil && r.Instance.Execution != nil && r.Instance.Execution.EvidenceExpired {
			result.EvidenceExpired = true
		}
		r.Instance.Status = status
		r.Instance.Operation.Status = status
		r.Instance.Execution = result
		if status != v1.Starting && status != v1.RuntimeReady && status != v1.Stopping {
			r.LeaseHash = ""
			r.Instance.LeaseExpiresAt = ""
		}
		out = r.Instance
		return put(tx, "instances", id, r)
	})
	return out, err
}
func (s *store) replaceSnapshot(id string, snap plan.Snapshot) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		r, err := load(tx, id)
		if err != nil {
			return err
		}
		r.Instance.Plan = snap.Plan
		if err := put(tx, "snapshots", id, snap); err != nil {
			return err
		}
		if err := put(tx, "secrets", id, snap.Secrets); err != nil {
			return err
		}
		return put(tx, "instances", id, r)
	})
}
func (s *store) recoverRuntime() error {
	ids, err := s.runtimeRecoveryIDs()
	if err != nil {
		return err
	}
	for _, id := range ids {
		if err := s.recoverExecution(id); err != nil {
			return err
		}
	}
	return nil
}

// runtimeRecoveryIDs selects durable cleanup obligations, including executions
// recovered by a previous daemon which have no current in-memory owner.
func (s *store) runtimeRecoveryIDs() ([]string, error) {
	var ids []string
	err := s.db.View(func(tx *bolt.Tx) error {
		return tx.Bucket([]byte("instances")).ForEach(func(key, data []byte) error {
			var r record
			if err := decodeRecord(data, string(key), &r); err != nil {
				return err
			}
			if r.Instance.Execution != nil && (active(r.Instance.Status) || r.Instance.Execution.Kept || r.Instance.Execution.CleanupFailure != "") {
				ids = append(ids, string(key))
			}
			return nil
		})
	})
	return ids, err
}
func (s *store) recoverExecution(id string) error {
	r, err := s.runtime(id)
	if err != nil {
		return err
	}
	instance, err := s.inspect(id)
	if err != nil {
		return err
	}
	if instance.Execution == nil || (!active(instance.Status) && !instance.Execution.Kept && instance.Execution.CleanupFailure == "") {
		return nil
	}
	result := *instance.Execution
	result.NativeCleanup = append([]v1.NativeCleanupResult(nil), result.NativeCleanup...)
	var cleanup error
	cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 20*time.Second)
	cleanup = s.removeGateway(cleanupCtx, id, &r)
	cleanupCancel()
	g, err := s.resources(id)
	if err != nil {
		return err
	}
	previousBoot := allNativePreviousBoot(r, result.Attempt)
	pendingAbsent := false
	if r.LaunchPending {
		pendingAbsent, _ = native.PreviousBoot(r.PendingBoot)
		if pendingAbsent {
			r.LaunchPending, r.PendingBoot = false, ""
			if err := s.saveRuntime(id, r); err != nil {
				return err
			}
		}
	}
	proofMissing := r.Attempt != result.Attempt || (!pendingAbsent && instance.Status == v1.RuntimeReady && len(r.Groups) == 0 && len(g.Containers) == 0)
	if proofMissing {
		cleanup = errors.Join(cleanup, problem("cleanup_failed", "native guardian ownership effect is absent or mismatched; preserve uncertain processes and inspect private state"))
	}
	if r.LaunchPending {
		cleanup = errors.Join(cleanup, problem("cleanup_failed", "native guardian launch intent lacks its ownership effect; an inactive helper may remain; preserve and diagnose this instance"))
	}
	// Native observations belong to this attempt, including earlier failed
	// readiness/workload stops. A later bound receipt cannot erase uncertainty.
	var mu sync.Mutex
	var joined sync.WaitGroup
	for _, group := range r.Groups {
		if group.Protocol != 0 && (group.Purpose != "application" || group.Attempt != r.Attempt || !hasComponent(result.Components, group.Component)) {
			mu.Lock()
			cleanup = errors.Join(cleanup, problem("cleanup_failed", "native application authority purpose/attempt/component mismatch; preserve uncertain processes"))
			mu.Unlock()
			continue
		}
		if proofMissing {
			continue
		}
		joined.Go(func() {
			observed, err := native.StopResult(group, 0)
			mu.Lock()
			cleanup = errors.Join(cleanup, err)
			mergeNativeCleanup(&result, observed)
			mu.Unlock()
		})
	}
	joined.Wait()
	cleanup = errors.Join(cleanup, s.recoverContainers(id, &g))
	if previousBoot {
		for i := range result.NativeCleanup {
			if result.NativeCleanup[i].Confidence != v1.ObservedEmpty {
				result.NativeCleanup[i].ResourceRelease = "previous_boot"
			}
		}
	}
	for _, observed := range result.NativeCleanup {
		if observed.FencesResources(instance.NativeOwnershipWarnings) {
			cleanup = errors.Join(cleanup, problem("cleanup_failed", observed.Detail))
		}
	}
	consumersStopped := cleanup == nil
	if consumersStopped {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		cleanup = s.removeNetworks(ctx, id, &g, nil)
		cancel()
	} else {
		// Native uncertainty prevented retrying a separately recorded network failure.
		for _, detail := range result.CleanupDetails {
			if detail.Field == "resources.network" {
				cleanup = errors.Join(cleanup, &detail)
			}
		}
	}

	// Reassess transient non-native failures; retain native fences through their
	// typed observations rather than making an old mixed error string sticky.
	result.CleanupFailure = ""
	result.CleanupDetails = nil
	if cleanup != nil {
		result.CleanupFailure = cleanup.Error()
		result.CleanupDetails = cleanupDetails(cleanup)
	}
	// Retrying resource cleanup does not interrupt an already completed execution.
	completedStatus := instance.Status
	if completedStatus == v1.Stopping && result.CompletedAt != "" && result.Outcome != "" {
		completedStatus = result.Outcome
	}
	if consumersStopped && result.CompletedAt != "" && !active(completedStatus) && !result.Kept {
		_, err := s.execution(id, completedStatus, &result)
		return err
	}
	for i := range result.Components {
		result.Components[i].Status = "stopped"
		if cleanup != nil {
			result.Components[i].Status = "unknown"
		}
	}
	result.Kept = false
	result.CompletedAt = time.Now().UTC().Format(time.RFC3339Nano)
	if result.CollectionFailure == "" {
		result.CollectionFailure = "daemon interruption; output/status collection may contain gaps"
	}
	result.Failure = "daemon recovery interrupted execution; inspect before another explicit start"
	result.FailureDetail = &v1.PlanError{Code: "execution_interrupted", Message: result.Failure}
	if _, err := s.execution(id, v1.Interrupted, &result); err != nil {
		return err
	}
	return nil
}

func parseDuration(value string, fallback time.Duration) (time.Duration, error) {
	if value == "" {
		return fallback, nil
	}
	d, err := time.ParseDuration(value)
	if err != nil || d < 0 {
		return 0, problem("invalid_deadline", "execution durations must be nonnegative Go durations; 0 is unlimited")
	}
	return d, nil
}

// outputConflict fences incompatible declarations across old/new snapshots of
// the same checkout. Group renaming must not bypass a live output consumer.
func (s *store) outputConflict(requestedPlan v1.PlanResponse) error {
	return s.db.View(func(tx *bolt.Tx) error {
		return tx.Bucket([]byte("instances")).ForEach(func(key, data []byte) error {
			var r record
			if err := decodeRecord(data, string(key), &r); err != nil {
				return err
			}
			instance := r.Instance
			if instance.Execution == nil || instance.Plan.Checkout != requestedPlan.Checkout {
				return nil
			}
			uncertain := instance.Execution.CleanupFailure != ""
			if !active(instance.Status) && !uncertain && !instance.Execution.Kept {
				return nil
			}
			for _, existing := range instance.Plan.Outputs {
				for _, requested := range requestedPlan.Outputs {
					if existing.ConcurrencyGroup == requested.ConcurrencyGroup {
						if uncertain {
							return problem("cleanup_failed", "uncertain owned consumer blocks checkout output group; resolve cleanup before launching")
						}
						continue
					}
					oldPath, err := plan.OutputPath(filepath.Dir(instance.Plan.ManifestPath), existing.Path)
					if err != nil {
						return err
					}
					newPath, err := plan.OutputPath(filepath.Dir(requestedPlan.ManifestPath), requested.Path)
					if err != nil {
						return err
					}
					separator := string(filepath.Separator)
					if oldPath == newPath || strings.HasPrefix(oldPath, newPath+separator) || strings.HasPrefix(newPath, oldPath+separator) {
						return problem("unsafe_drift", "overlapping checkout output declarations changed groups while another consumer is active or uncertain; stop it before launching")
					}
				}
			}
			return nil
		})
	})
}

func (s *store) cleanupFailures() error {
	return s.db.View(func(tx *bolt.Tx) error {
		var failures error
		err := tx.Bucket([]byte("instances")).ForEach(func(key, data []byte) error {
			var r record
			if err := decodeRecord(data, string(key), &r); err != nil {
				return err
			}
			if r.Instance.Execution != nil && r.Instance.Execution.CleanupFailure != "" {
				failures = errors.Join(failures, problem("cleanup_failed", "instance "+r.Instance.ID+": "+r.Instance.Execution.CleanupFailure))
			}
			return nil
		})
		return errors.Join(err, failures)
	})
}

// acceptExecution rechecks eligibility and lease in the same transaction as launch
// acceptance. Cached preparation responses never authorize resurrection.
func (s *store) acceptExecution(id string, journal runtimeRecord, result *v1.ExecutionResult, restart bool) (v1.Instance, error) {
	return s.acceptExecutionSnapshot(id, journal, result, restart, nil, nil)
}

// acceptExecutionSnapshot adopts current configuration for a predecessor attempt
// in the same transaction as execution acceptance. Rejection retains launch facts.
func (s *store) acceptExecutionSnapshot(id string, journal runtimeRecord, result *v1.ExecutionResult, restart bool, snapshot *plan.Snapshot, expected *startObservation) (v1.Instance, error) {
	var instance v1.Instance
	err := s.db.Update(func(tx *bolt.Tx) error {
		r, err := load(tx, id)
		if err != nil {
			return err
		}
		if expected != nil {
			current, err := observeStart(tx, r.Instance)
			if err != nil {
				return err
			}
			switch current.status {
			case v1.Prepared, v1.Stopped, v1.Failed, v1.Cancelled, v1.Interrupted:
			default:
				return startConflict(id, expected.attempt)
			}
			if current.sequence != expected.sequence || current.attempt != expected.attempt {
				return startConflict(id, expected.attempt)
			}
		}
		if !restart && r.Instance.Status != v1.Prepared {
			return problem("restart_required", "instance is no longer prepared; explicit restart required")
		}
		if restart && (r.Instance.Plan.Lifetime != v1.Persistent || (active(r.Instance.Status) && r.Instance.Status != v1.Prepared)) {
			return problem("conflict", "restart requires joined persistent execution")
		}
		if r.Instance.Plan.Lifetime == v1.Disposable {
			expiry, err := time.Parse(time.RFC3339Nano, r.Instance.LeaseExpiresAt)
			if err != nil || r.LeaseHash == "" || !expiry.After(time.Now()) {
				return problem("lease_expired", "disposable lease is not valid for execution acceptance")
			}
		}
		if snapshot != nil {
			r.Instance.Plan = snapshot.Plan
			if err := put(tx, "snapshots", id, snapshot); err != nil {
				return err
			}
			if err := put(tx, "secrets", id, snapshot.Secrets); err != nil {
				return err
			}
		}

		r.Instance.Status = v1.Starting
		r.Instance.Operation.Status = v1.Starting
		r.Instance.Execution = result
		if err := put(tx, "runtime", id, journal); err != nil {
			return err
		}
		instance = r.Instance
		return put(tx, "instances", id, r)
	})
	return instance, err
}

// Multiple readiness/application launches share a component. Preserve the worst
// confidence for the attempt, including persisted observations during recovery.
func mergeNativeCleanup(result *v1.ExecutionResult, observed *v1.NativeCleanupResult) {
	if observed == nil {
		return
	}
	if observed.Confidence != v1.ObservedEmpty && result.CleanupFailure == "" {
		result.CleanupFailure = "native session cleanup remains incomplete; lifecycle fences retained"
	}
	for i, r := range result.NativeCleanup {
		if r.Component == observed.Component && r.Attempt == observed.Attempt {
			if r.Confidence == v1.ObservedEmpty {
				result.NativeCleanup[i] = *observed
			}
			return
		}
	}
	result.NativeCleanup = append(result.NativeCleanup, *observed)
}

func hasComponent(components []v1.ComponentResult, name string) bool {
	for _, c := range components {
		if c.Name == name {
			return true
		}
	}
	return false
}

// allNativePreviousBoot requires every recorded launch obligation, including an
// uncommitted effect, to prove older boot before releasing aggregated observations.
func allNativePreviousBoot(r runtimeRecord, attempt string) bool {
	if r.Attempt != attempt || len(r.Groups) == 0 && !r.LaunchPending {
		return false
	}
	if r.LaunchPending {
		if old, err := native.PreviousBoot(r.PendingBoot); err != nil || !old {
			return false
		}
	}
	for _, id := range r.Groups {
		if old, err := native.PreviousBoot(id.Boot); err != nil || !old {
			return false
		}
	}
	return true
}

// startObservation is request-local selection proof, never part of stored or wire state.
type startObservation struct {
	attempt  string
	status   v1.InstanceStatus
	sequence int64
}

func observeStart(tx *bolt.Tx, instance v1.Instance) (startObservation, error) {
	observed := startObservation{status: instance.Status}
	if instance.Execution != nil {
		observed.attempt = instance.Execution.Attempt
	}
	if raw := tx.Bucket([]byte("attempt_sequences")).Get([]byte(instance.ID)); raw != nil {
		n, err := strconv.ParseInt(string(raw), 10, 64)
		if err != nil || n <= 0 {
			return observed, problem("state_corrupt", "invalid attempt acceptance sequence")
		}
		observed.sequence = n
	}
	if (observed.attempt == "") != (observed.sequence == 0) && instance.Status != v1.Destroyed {
		return observed, problem("state_corrupt", "current execution and acceptance sequence disagree")
	}
	return observed, nil
}

func startConflict(id, attempt string) error {
	return problem("startup_conflict", fmt.Sprintf("Instance %s startup attempt %s is no longer current or available; inspect retained history/logs/export before another explicit start", id, attempt))
}

// boundStart requires the instance mutation lock. Current state plus highwater
// proves a join even after current history pruning; history is diagnostic only.
func (s *store) boundStart(id string, observed startObservation, attached bool) (instance v1.Instance, bound bool, err error) {
	err = s.db.View(func(tx *bolt.Tx) error {
		r, err := load(tx, id)
		if err != nil {
			return err
		}
		instance = r.Instance
		if instance.Status == v1.Destroyed {
			return startConflict(id, observed.attempt)
		}
		current, err := observeStart(tx, instance)
		if err != nil {
			return err
		}
		if current.sequence == observed.sequence && current.attempt == observed.attempt {
			bound = observed.status == v1.Starting || observed.status == v1.RuntimeReady || current.status == v1.Starting || current.status == v1.RuntimeReady
			return nil
		}
		if attached {
			return problem("conflict", "attached start cannot adopt an intervening execution; inspect the recorded instance")
		}
		if observed.status != v1.Starting && observed.status != v1.RuntimeReady && current.sequence == observed.sequence+1 {
			bound = true
			return nil
		}
		attempt := observed.attempt
		if observed.status != v1.Starting && observed.status != v1.RuntimeReady {
			attempt = "unavailable"
			if data := tx.Bucket([]byte("attempts")).Get([]byte(attemptKey(id, observed.sequence+1))); data != nil {
				var successor v1.Attempt
				if err := json.Unmarshal(data, &successor); err != nil {
					return err
				}
				attempt = successor.Result.Attempt
			}
		}
		return startConflict(id, attempt)
	})
	return
}
