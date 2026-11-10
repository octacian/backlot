package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"maps"
	"slices"
	"strings"

	v1 "github.com/octacian/backlot/api/v1"
	"github.com/octacian/backlot/internal/native"
	bolt "go.etcd.io/bbolt"
)

// scanBudget includes dependent settlement/release-proof scans, even skipped history.
// It is local to a read transaction, never shared between owners.
type scanBudget struct {
	ctx          context.Context
	scans, bytes int
	err          error
}

func chargeScan(budgets []*scanBudget, size int) error {
	if len(budgets) == 0 {
		return nil
	}
	b := budgets[0]
	if b.err != nil {
		return b.err
	}
	b.scans++
	b.bytes += size
	if b.scans > 10000 || b.bytes > 64<<20 {
		b.err = problem("scope_too_large", "recorded selection exceeds 10000 scans or 64 MiB; use explicit recorded targets")
	} else if b.ctx.Err() != nil {
		b.err = problem("cancelled", "recorded observation cancelled")
	}
	return b.err
}

// boundActivityState includes direct journal reads in the aggregate byte budget.
// Subsequent dependent scans are charged again for the actual work they perform.
func boundActivityState(tx *bolt.Tx, b *scanBudget) error {
	for _, name := range []string{"instances", "runtime", "resources", "shared_allocations", "shared_services"} {
		if err := tx.Bucket([]byte(name)).ForEach(func(key, raw []byte) error {
			if err := chargeScan([]*scanBudget{b}, len(raw)); err != nil {
				return err
			}
			var err error
			switch name {
			case "instances":
				var r record
				err = decodeRecord(raw, string(key), &r)
			case "runtime":
				_, err = decodeRuntime(raw)
			case "resources":
				_, err = decodeResources(raw)
			case "shared_allocations":
				_, err = decodeSharedAllocations(raw)
			case "shared_services":
				_, err = decodeSharedService(raw, string(key))
			}
			return err
		}); err != nil {
			return err
		}
	}
	return nil
}

// gatewayConsumerBlocked is shared with the actual gateway lifecycle guard.
// Route-only recovery waives only the exact attempt's route obligation.
func gatewayConsumerBlocked(tx *bolt.Tx, r record, boot string, budget ...*scanBudget) (bool, error) {
	i := r.Instance
	if i.Plan.Publish == nil || i.Execution == nil || !active(i.Status) && !i.Execution.Kept && i.Execution.CleanupFailure == "" {
		return false, nil
	}
	if !active(i.Status) && !i.Execution.Kept {
		if raw := tx.Bucket([]byte("runtime")).Get([]byte(i.ID)); raw != nil {
			j, err := decodeRuntime(raw)
			if err != nil {
				return false, err
			}
			if j.Attempt == i.Execution.Attempt && j.Gateway != nil && j.GatewayOnlyCleanup {
				// Parent eligibility includes a route blocker; only child obligations apply here.
				var blocked bool
				err := tx.Bucket([]byte("instances")).ForEach(func(key, raw []byte) error {
					if err := chargeScan(budget, len(raw)); err != nil {
						return err
					}
					var child record
					if err := decodeRecord(raw, string(key), &child); err != nil {
						return err
					}
					if child.Instance.Task == nil || child.Instance.Task.InstanceID != i.ID {
						return nil
					}
					erased, err := consumerErased(tx, child, budget...)
					if err != nil || erased {
						return err
					}
					settled, err := consumerEligibility(tx, child, boot, budget...)
					if err != nil {
						return err
					}
					blocked = blocked || settled.State != v1.ConsumerSettled
					return nil
				})
				return blocked, err
			}
		}
	}
	return true, nil
}
func sharedConsumerBlocked(i v1.Instance, settlement v1.ConsumerSettlement) bool {
	return i.Status == v1.Prepared || settlement.State != v1.ConsumerSettled
}

func activityWorkload(tx *bolt.Tx, r record, boot string, b *scanBudget) (v1.ActivityEntry, error) {
	i := r.Instance
	settlement, err := consumerEligibility(tx, r, boot, b)
	if err != nil {
		return v1.ActivityEntry{}, err
	}
	if err := associateInstance(tx, &i); err != nil {
		return v1.ActivityEntry{}, err
	}
	e := v1.ActivityEntry{Key: "workload/" + i.ID, ID: i.ID, Project: i.Plan.Project, Checkout: associatedCheckout(i).Path, Scene: i.Plan.Scene, Status: string(i.Status), Owner: "absent", Settlement: &settlement, Blockers: []v1.ActivityBlocker{}}
	if i.Execution != nil {
		e.Kept = i.Execution.Kept
		e.Ports = maps.Clone(i.Execution.Ports)
		if i.Task == nil {
			e.Origin = i.Execution.Origin
		}
	}
	blocked, err := gatewayConsumerBlocked(tx, r, boot, b)
	if err != nil {
		return e, err
	}
	if blocked {
		e.Blockers = append(e.Blockers, v1.ActivityBlocker{Kind: "gateway", Stop: true})
	}
	allocationRaw := tx.Bucket([]byte("shared_allocations")).Get([]byte(i.ID))
	if err := chargeScan([]*scanBudget{b}, len(allocationRaw)); err != nil {
		return e, err
	}
	allocations, err := decodeSharedAllocations(allocationRaw)
	if err != nil {
		return e, err
	}
	seen := map[string]bool{}
	for _, a := range allocations {
		if a.Released || seen[a.ServiceID] {
			continue
		}
		seen[a.ServiceID] = true
		e.Blockers = append(e.Blockers, v1.ActivityBlocker{Kind: "shared", ServiceID: a.ServiceID, Stop: sharedConsumerBlocked(i, settlement), Destroy: true})
	}
	slices.SortFunc(e.Blockers, func(a, b v1.ActivityBlocker) int {
		return strings.Compare(a.Kind+"/"+a.ServiceID, b.Kind+"/"+b.ServiceID)
	})
	if len(e.Blockers) > 100 || len(e.Ports) > 100 {
		return e, problem("scope_too_large", "workload activity summary exceeds 100 blockers or ports; inspect the recorded target")
	}
	return e, nil
}

func (s *service) activity(ctx context.Context, q v1.ActivityRequest) (v1.ActivityResponse, error) {
	out := v1.ActivityResponse{APIVersion: v1.Version, DaemonGeneration: s.generation, Entries: []v1.ActivityEntry{}, Complete: true}
	if err := q.Validate(); err != nil {
		return out, err
	}
	if ctx.Err() != nil {
		return out, problem("cancelled", "recorded observation cancelled")
	}
	limit := q.Limit
	if limit == 0 {
		limit = 100
	}
	boot, _ := native.CurrentBoot()
	budget := &scanBudget{ctx: ctx}
	last := q.After
	responseBytes := 0
	err := s.store.db.View(func(tx *bolt.Tx) error {
		g, err := decodeManagedRecord(tx.Bucket([]byte("meta")).Get([]byte("managed_gateway")))
		if err != nil {
			return err
		}
		out.GatewayStatus = g.Status
		afterKind, afterID, _ := strings.Cut(q.After, "/")
		for _, kind := range []string{"shared", "workload"} {
			if ctx.Err() != nil {
				return problem("cancelled", "recorded observation cancelled")
			}
			if kind < afterKind {
				continue
			}
			bucket := "shared_services"
			if kind == "workload" {
				bucket = "instances"
			}
			cursor := tx.Bucket([]byte(bucket)).Cursor()
			var key, raw []byte
			if kind == afterKind {
				key, raw = cursor.Seek([]byte(afterID))
				if bytes.Equal(key, []byte(afterID)) {
					key, raw = cursor.Next()
				}
			} else {
				key, raw = cursor.First()
			}
			for ; key != nil; key, raw = cursor.Next() {
				if ctx.Err() != nil {
					return problem("cancelled", "recorded observation cancelled")
				}
				pageKey := kind + "/" + string(key)
				if len(out.Entries) == limit {
					out.Complete = false
					out.NextAfter = last
					return nil
				}
				if err := chargeScan([]*scanBudget{budget}, len(raw)); err != nil {
					return err
				}
				var e v1.ActivityEntry
				if kind == "shared" {
					r, err := decodeSharedService(raw, string(key))
					if err != nil {
						return err
					}
					e = v1.ActivityEntry{Key: pageKey, ID: r.ID, Project: r.Project.ID, Status: r.Status, Owner: "absent", Blockers: []v1.ActivityBlocker{}, LastTransition: r.LastTransition}
				} else {
					var r record
					if err := decodeRecord(raw, string(key), &r); err != nil {
						return err
					}
					erased, err := consumerErased(tx, r, budget)
					if err != nil {
						return err
					}
					if erased {
						last = pageKey
						continue
					}
					e, err = activityWorkload(tx, r, boot, budget)
					if err != nil {
						return err
					}
					if e.Settlement.State == v1.ConsumerSettled && len(e.Blockers) == 0 {
						last = pageKey
						continue
					}
				}
				encoded, err := json.Marshal(e)
				if err != nil {
					return err
				}
				if len(encoded) > 512<<10 {
					return problem("scope_too_large", "one activity entry exceeds the response budget; inspect its recorded ID")
				}
				if responseBytes+len(encoded) > 512<<10 {
					out.Complete = false
					out.NextAfter = last
					return nil
				}
				out.Entries = append(out.Entries, e)
				responseBytes += len(encoded)
				last = pageKey
			}
		}
		return nil
	})
	if err != nil {
		var detail *v1.PlanError
		if errors.As(err, &detail) && detail.Code == "scope_too_large" && last != q.After {
			out.Complete = false
			out.NextAfter = last
		} else {
			return out, err
		}
	}
	if ctx.Err() != nil {
		return out, problem("cancelled", "recorded observation cancelled")
	}
	// Observe owners separately; no claim that recorded ports correspond to listeners.
	s.mu.Lock()
	if len(s.mutationOwners) > 10000 {
		s.mu.Unlock()
		return out, problem("scope_too_large", "activity exceeds 10000 lifecycle owners; use explicit recorded targets")
	}
	out.AdmissionClosed = s.closing || s.bulkStopping
	for owner := range s.mutationOwners {
		if owner.workload {
			out.PreparingAdmissions++
		}
	}
	for n := range out.Entries {
		e := &out.Entries[n]
		if !strings.HasPrefix(e.Key, "workload/") {
			continue
		}
		if owner := s.executions[e.ID]; owner != nil && owner.attempt == e.Settlement.Attempt {
			select {
			case <-owner.done:
				e.Owner = "joined"
			default:
				e.Owner = "running"
			}
		}
	}
	s.mu.Unlock()
	return out, out.Validate()
}
