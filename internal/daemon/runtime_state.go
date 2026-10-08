package daemon

import (
	"context"
	"encoding/json"
	"errors"
	v1 "github.com/octacian/backlot/api/v1"
	"github.com/octacian/backlot/internal/native"
	"github.com/octacian/backlot/internal/plan"
	bolt "go.etcd.io/bbolt"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type runtimeRecord struct {
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
		return json.Unmarshal(data, &r)
	})
	return r, err
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
	var ids []string
	if err := s.db.View(func(tx *bolt.Tx) error {
		return tx.Bucket([]byte("instances")).ForEach(func(key, data []byte) error {
			var r record
			if err := decodeRecord(data, string(key), &r); err != nil {
				return err
			}
			if r.Instance.Execution != nil && (active(r.Instance.Status) || r.Instance.Execution.CleanupFailure != "") {
				ids = append(ids, string(key))
			}
			return nil
		})
	}); err != nil {
		return err
	}

	for _, id := range ids {
		if err := s.recoverExecution(id); err != nil {
			return err
		}
	}
	return nil
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
	if instance.Execution == nil || (!active(instance.Status) && instance.Execution.CleanupFailure == "") {
		return nil
	}
	result := *instance.Execution
	var cleanup error
	g, err := s.resources(id)
	if err != nil {
		return err
	}
	proofMissing := r.Attempt != result.Attempt || (instance.Status == v1.RuntimeReady && len(r.Groups) == 0 && len(g.Containers) == 0)
	if proofMissing {
		cleanup = problem("cleanup_failed", "native guardian ownership effect is absent or mismatched; preserve uncertain processes and inspect private state")
	}
	if r.LaunchPending {
		cleanup = errors.Join(cleanup, problem("cleanup_failed", "native guardian launch intent lacks its ownership effect; an inactive helper may remain; preserve and diagnose this instance"))
	}
	var mu sync.Mutex
	var joined sync.WaitGroup
	for _, group := range r.Groups {
		if proofMissing {
			continue
		}
		joined.Go(func() { err := native.Stop(group, 0); mu.Lock(); cleanup = errors.Join(cleanup, err); mu.Unlock() })
	}
	joined.Wait()
	cleanup = errors.Join(cleanup, s.recoverContainers(id, &g))
	if cleanup == nil && instance.Plan.Lifetime == v1.Disposable {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		cleanup = s.removeResources(cleanupCtx, id)
		cancel()
	}
	result.CleanupFailure = ""
	if cleanup != nil {
		result.CleanupFailure = cleanup.Error()
	}
	for i := range result.Components {
		result.Components[i].Status = "stopped"
		if cleanup != nil {
			result.Components[i].Status = "unknown"
		}
	}
	result.CollectionFailure = "daemon interruption; output/status collection may contain gaps"
	result.Failure = "daemon recovery interrupted execution; explicit restart required"
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
			if !active(instance.Status) && !uncertain {
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
	var instance v1.Instance
	err := s.db.Update(func(tx *bolt.Tx) error {
		r, err := load(tx, id)
		if err != nil {
			return err
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
