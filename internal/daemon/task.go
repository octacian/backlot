package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"path/filepath"
	"slices"
	"strings"
	"time"

	v1 "github.com/octacian/backlot/api/v1"
	"github.com/octacian/backlot/internal/plan"
	bolt "go.etcd.io/bbolt"
)

func (s *service) runTask(ctx context.Context, q v1.TaskRunRequest) (v1.InstanceResponse, error) {
	out := v1.InstanceResponse{APIVersion: v1.Version}
	if err := q.Validate(); err != nil {
		return out, err
	}
	if _, err := executionDeadlines(q.Options); err != nil {
		return out, err
	}
	_, finish, err := s.beginMutation(ctx)
	if err != nil {
		return out, err
	}
	defer finish()
	selected, err := s.resolveScene(q.Target, true)
	if err != nil {
		return out, err
	}
	if selected.Result == nil {
		return out, problem("parent_unavailable", "recorded parent must already be ready")
	}
	parent := selected.Result.Instance
	unlock := s.lockMutation(parent.ID)
	defer unlock()
	accepted, err := s.store.snapshot(parent.ID)
	if err != nil {
		return out, err
	}
	capture, ok := accepted.Tasks[q.Task]
	if !ok {
		return out, problem("restart_required", "task has no accepted capture; explicitly restart the parent to accept enabled tasks")
	}
	journal, err := s.store.runtime(parent.ID)
	if err != nil {
		return out, err
	}
	source, err := plan.Locate(associatedProject(parent))
	if err != nil {
		return out, taskDrift()
	}
	identity, err := captureSource(source)
	if err != nil {
		return out, err
	}
	current, err := plan.PrepareTask(source, v1.PlanRequest{Scene: parent.Plan.Scene, ConfigPath: journal.ConfigPath}, q.Task)
	if err != nil {
		return out, taskDrift()
	}
	a, err := json.Marshal(capture)
	if err != nil {
		return out, err
	}
	b, err := json.Marshal(current.Tasks[q.Task])
	if err != nil {
		return out, err
	}
	if string(a) != string(b) || source.ManifestPath != accepted.Plan.ManifestPath {
		return out, taskDrift()
	}
	if err := identity.verify(); err != nil {
		return out, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closing {
		return out, problem("shutting_down", "daemon is shutting down")
	}
	parent, err = s.store.inspect(parent.ID)
	if err != nil {
		return out, err
	}
	owner := s.executions[parent.ID]
	if parent.Plan.Lifetime != v1.Persistent || parent.Status != v1.RuntimeReady || parent.Execution == nil || owner == nil || owner.taskAdmissionClosed || owner.attempt != parent.Execution.Attempt {
		return out, problem("parent_unavailable", "parent must already be ready in this daemon; explicitly restart an interrupted parent")
	}
	select {
	case <-owner.done:
		return out, problem("parent_unavailable", "parent owner has ended")
	default:
	}
	if err := serviceFailure(owner.services); err != nil {
		return out, problem("parent_unavailable", "a required parent service has ended")
	}
	for _, dependency := range capture.Component.DependsOn {
		satisfied := false
		for _, component := range parent.Execution.Components {
			if component.Name == dependency.Component {
				satisfied = dependency.Condition == v1.Ready && component.Status == "ready" || dependency.Condition == v1.Completed && (component.Status == "skipped" || component.ExitCode != nil && *component.ExitCode == 0)
			}
		}
		if !satisfied {
			return out, problem("dependency_unavailable", "task dependency is not satisfied by the accepted parent")
		}
	}
	generation := parent.Execution.Attempt
	if owner.resources != nil && owner.resources.ID != "" {
		generation = owner.resources.ID
	}
	binding := &v1.TaskBinding{InstanceID: parent.ID, Attempt: parent.Execution.Attempt, Generation: generation, Name: q.Task}
	id, attempt := newID(), newID()
	component := capture.Component
	command := *component.Command
	command.Args = append(slices.Clone(command.Args), v1.StringArguments(q.Args...)...)
	component.Command = &command
	snapshot := plan.Snapshot{Plan: accepted.Plan, Manifest: accepted.Manifest, Docker: accepted.Docker, Caddy: accepted.Caddy, Secrets: maps.Clone(capture.Secrets)}
	snapshot.Plan.Components = []v1.PlannedComponent{component}
	snapshot.Plan.Tasks = nil
	snapshot.Plan.Lifetime = v1.Disposable
	snapshot.Plan.TerminalJob = q.Task
	snapshot.Plan.Outputs = capture.Outputs
	snapshot.Plan.Publish = nil
	snapshot.Plan.StartupDetails = nil
	snapshot.Plan.BuildImages = nil
	result := &v1.ExecutionResult{Attempt: attempt, Components: []v1.ComponentResult{}, Ports: maps.Clone(parent.Execution.Ports), Origin: parent.Execution.Origin}
	now := time.Now().UTC()
	runtime := runtimeRecord{Attempt: attempt, Options: q.Options, StartedAt: now.Format(time.RFC3339Nano), OutputPaths: capture.Paths}
	out.LeaseToken = newID()
	instance := v1.Instance{ID: id, Task: binding, CheckoutParent: checkoutParent(parent), Checkout: parent.Checkout, Plan: snapshot.Plan, Status: v1.Starting, Execution: result, CreatedAt: now.Format(time.RFC3339Nano), LeaseExpiresAt: now.Add(s.lease).Format(time.RFC3339Nano), Operation: v1.Operation{ID: newID(), Status: v1.Starting, Allocations: []v1.AllocationRecord{{ID: newID(), Kind: "private_snapshot", OwnershipToken: newID(), Phase: "effect"}}}}
	err = s.store.db.Update(func(tx *bolt.Tx) error {
		current, err := load(tx, parent.ID)
		if err != nil {
			return err
		}
		if current.Instance.Status != v1.RuntimeReady || current.Instance.Execution == nil || current.Instance.Execution.Attempt != binding.Attempt {
			return problem("conflict", "parent changed during task admission; no task was started")
		}
		if err := outputConflictTx(tx, snapshot.Plan, capture.Paths, "", true); err != nil {
			return err
		}
		if err := put(tx, "runtime", id, runtime); err != nil {
			return err
		}
		if err := put(tx, "snapshots", id, snapshot); err != nil {
			return err
		}
		if err := put(tx, "secrets", id, snapshot.Secrets); err != nil {
			return err
		}
		return put(tx, "instances", id, record{Instance: instance, LeaseHash: hash(out.LeaseToken)})
	})
	if err != nil {
		return v1.InstanceResponse{}, err
	}
	runCtx, cancel := context.WithCancel(context.Background())
	entry := &execution{borrowed: true, referenceID: parent.ID, referencePlan: &accepted.Plan, shared: owner.shared, resources: owner.resources, caCertPath: owner.caCertPath, attempt: attempt, cancel: cancel, done: make(chan struct{}), source: identity, grace: mustGrace(q.Options), jobs: map[string]workload{}, services: map[string]workload{}, components: map[string]workload{}}
	if s.executions == nil {
		s.executions = map[string]*execution{}
	}
	s.executions[id] = entry
	out.Instance, err = s.store.inspect(id)
	if err != nil {
		cancel()
		delete(s.executions, id)
		return out, err
	}
	go s.execute(runCtx, id, snapshot, runtime, entry, result, identity)
	return out, nil
}

func taskDrift() error {
	return problem("restart_required", "selected task configuration or parent contract changed or is unavailable; explicitly restart the parent to accept current declarations")
}

// dependentTasks selects durable lineage, including owners no longer in memory.
func (s *store) dependentTasks(id string) ([]string, error) {
	var ids []string
	err := s.db.View(func(tx *bolt.Tx) error {
		return tx.Bucket([]byte("instances")).ForEach(func(key, data []byte) error {
			var r record
			if err := decodeRecord(data, string(key), &r); err != nil {
				return err
			}
			if r.Instance.Task != nil && r.Instance.Task.InstanceID == id {
				ids = append(ids, string(key))
			}
			return nil
		})
	})
	return ids, err
}

// stopDependents runs after durable parent admission fencing, before any parent effect.
// Collection gaps do not imply that a consumer remains alive.
func (s *service) stopDependents(id string) error {
	ids, err := s.store.dependentTasks(id)
	if err != nil {
		return err
	}
	var failure error
	for _, child := range ids {
		_, err := s.stopExecution(child)
		var detail *v1.PlanError
		if errors.As(err, &detail) && detail.Code == "collection_failed" {
			err = nil
		}
		failure = errors.Join(failure, err)
	}
	return errors.Join(failure, s.store.guardDependentTasks(id))
}

func outputPaths(p v1.PlanResponse) (map[string]string, error) {
	out := map[string]string{}
	for name, output := range p.Outputs {
		path, err := plan.OutputPath(filepath.Dir(p.ManifestPath), output.Path)
		if err != nil {
			return nil, err
		}
		out[name] = path
	}
	return out, nil
}

func overlaps(a, b string) bool {
	sep := string(filepath.Separator)
	return a == b || strings.HasPrefix(a, b+sep) || strings.HasPrefix(b, a+sep)
}

// outputConflictTx checks both accepted reservations and current canonical aliases.
// Task admission is immediate; ordinary parent group queuing remains unchanged.
func outputConflictTx(tx *bolt.Tx, p v1.PlanResponse, paths map[string]string, self string, immediate bool) error {
	return tx.Bucket([]byte("instances")).ForEach(func(key, data []byte) error {
		if string(key) == self {
			return nil
		}
		var r record
		if err := decodeRecord(data, string(key), &r); err != nil {
			return err
		}
		i := r.Instance
		if self != "" && i.Task == nil {
			return nil
		}
		if i.Execution == nil || associatedCheckout(i).Path != p.Checkout || !active(i.Status) && i.Execution.CleanupFailure == "" && !i.Execution.Kept {
			return nil
		}
		var journal runtimeRecord
		if data := tx.Bucket([]byte("runtime")).Get(key); data != nil {
			var err error
			journal, err = decodeRuntime(data)
			if err != nil {
				return err
			}
		}
		for oldName, old := range i.Plan.Outputs {
			for name, next := range p.Outputs {
				if old.ConcurrencyGroup == next.ConcurrencyGroup {
					if !immediate && i.Task == nil && i.Execution.CleanupFailure != "" {
						return problem("cleanup_failed", "uncertain owned consumer blocks checkout output group; resolve cleanup before launching")
					}
					if immediate || i.Task != nil || i.Execution.CleanupFailure != "" {
						return problem("output_conflict", "checkout output group is held by an active or uncertain execution; stop it before invoking the task")
					}
					continue
				}
				oldCurrent, err := plan.OutputPath(associatedProject(i), old.Path)
				if err != nil {
					return problem("output_conflict", "held output path is unavailable; stop its owner before launching")
				}
				for _, a := range []string{journal.OutputPaths[oldName], oldCurrent} {
					for _, b := range []string{paths[name]} {
						if a != "" && b != "" && overlaps(a, b) {
							if !immediate && i.Task == nil {
								return problem("unsafe_drift", "overlapping checkout output declarations changed groups while another consumer is active or uncertain; stop it before launching")
							}
							return problem("output_conflict", "checkout output paths overlap an active or uncertain owner; stop it before launching")
						}
					}
				}
			}
		}
		return nil
	})
}

// fenceTaskAdmission shares the durable ready predicate with atomic admission.
func (s *store) fenceTaskAdmission(id string) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		r, err := load(tx, id)
		if err != nil {
			return err
		}
		if r.Instance.Task == nil && r.Instance.Status == v1.RuntimeReady {
			r.Instance.Status = v1.Stopping
			r.Instance.Operation.Status = v1.Stopping
			return put(tx, "instances", id, r)
		}
		return nil
	})
}

func (s *store) retireTaskInputs(id string) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		r, err := load(tx, id)
		if err != nil {
			return err
		}
		if active(r.Instance.Status) || r.Instance.Execution == nil || r.Instance.Execution.CleanupFailure != "" {
			return nil
		}
		if r.Instance.Task == nil {
			data := tx.Bucket([]byte("snapshots")).Get([]byte(id))
			if data == nil {
				return nil
			}
			var snapshot plan.Snapshot
			if err := json.Unmarshal(data, &snapshot); err != nil {
				return err
			}
			if len(snapshot.Tasks) == 0 {
				return nil
			}
			snapshot.Tasks = nil
			return put(tx, "snapshots", id, snapshot)
		}
		for _, name := range []string{"snapshots", "secrets"} {
			if err := tx.Bucket([]byte(name)).Delete([]byte(id)); err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *store) guardDependentTasks(id string) error {
	ids, err := s.dependentTasks(id)
	if err != nil {
		return err
	}
	for _, child := range ids {
		i, err := s.inspect(child)
		if err != nil {
			return err
		}
		if active(i.Status) || i.Execution != nil && i.Execution.CleanupFailure != "" {
			return problem("cleanup_failed", "dependent task still holds parent resources")
		}
	}
	return nil
}

func taskWriterParent(tx *bolt.Tx, i v1.Instance) error {
	parent, err := load(tx, i.Task.InstanceID)
	if err != nil {
		return err
	}
	if parent.Instance.Execution == nil || parent.Instance.Execution.Attempt != i.Task.Attempt {
		return problem("conflict", "task writer no longer owns parent attempt")
	}
	generation := i.Task.Attempt
	if data := tx.Bucket([]byte("resources")).Get([]byte(i.Task.InstanceID)); data != nil {
		g, err := decodeResources(data)
		if err != nil {
			return err
		}
		if g.ID != "" {
			generation = g.ID
		}
	}
	if generation != i.Task.Generation {
		return problem("conflict", "task writer no longer owns parent resource generation")
	}
	return nil
}
