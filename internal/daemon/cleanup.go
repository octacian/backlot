package daemon

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	v1 "github.com/octacian/backlot/api/v1"
	"github.com/octacian/backlot/internal/native"
	"github.com/octacian/backlot/internal/plan"
	bolt "go.etcd.io/bbolt"
)

type cleanupSelection struct {
	resources  resourceGeneration
	target     v1.CleanupTarget
	authority  string
	dependents map[string][]string
}
type cleanupPreview struct {
	targets []cleanupSelection
	digest  string
	scope   v1.CleanupRequest
}
type cleanupOperation struct {
	mu        sync.Mutex
	records   []v1.CleanupRecord
	changed   chan struct{}
	cancel    context.CancelFunc
	id        string
	finished  bool
	observing bool
}

func (o *cleanupOperation) emit(r v1.CleanupRecord) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.records = append(o.records, r)
	if r.Type == "summary" {
		o.finished = true
	}
	close(o.changed)
	o.changed = make(chan struct{})
}
func (o *cleanupOperation) next(ctx context.Context, index int) (v1.CleanupRecord, error) {
	for {
		o.mu.Lock()
		if index < len(o.records) {
			r := o.records[index]
			o.mu.Unlock()
			return r, nil
		}
		changed := o.changed
		o.mu.Unlock()
		select {
		case <-ctx.Done():
			return v1.CleanupRecord{}, ctx.Err()
		case <-changed:
		}
	}
}

func cleanupHash(key []byte, values ...any) string {
	h := hmac.New(sha256.New, key)
	for _, v := range values {
		_ = json.NewEncoder(h).Encode(v)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// cleanupPreviewLocked is a read-only coherent recorded selection; service.mu is held.
func (s *service) cleanupPreviewLocked(ctx context.Context, q v1.CleanupRequest, selected ...cleanupSelection) (out cleanupPreview, err error) {
	out.scope = q
	out.scope.ApplyPreview = ""
	out.targets = []cleanupSelection{}
	if err = q.Validate(); err != nil {
		return out, err
	}
	if q.CheckoutPath != "" || q.CWD != "" {
		path := q.CheckoutPath
		if q.CWD != "" {
			path, err = sceneDirectory(q.CWD)
			if err != nil {
				return out, err
			}
			path = plan.CheckoutRoot(path)
			if _, err = os.Lstat(filepath.Join(path, ".git")); err != nil {
				return out, problem("checkout_identity", "outside a Git checkout; use --checkout PATH or --checkout-id ID")
			}
		}
		current, e := identify(path)
		if e != nil {
			return out, e
		}
		err = s.store.db.View(func(tx *bolt.Tx) error { var e error; out.scope.CheckoutID, e = locateCheckout(tx, current); return e })
		if err != nil {
			return out, err
		}
		out.scope.CheckoutPath = ""
		out.scope.CWD = ""
	}
	if len(s.repairKey) == 0 {
		s.repairKey = randomBytes()
	}
	boot, _ := native.CurrentBoot()
	ownerIDs := map[string][]string{}
	for owner := range s.mutationOwners {
		if owner.workload && owner.checkout != "" {
			ownerIDs[owner.checkout] = append(ownerIDs[owner.checkout], owner.id)
		}
	}
	for _, ids := range ownerIDs {
		sort.Strings(ids)
	}
	err = s.store.db.View(func(tx *bolt.Tx) error {
		if !q.Orphans {
			if tx.Bucket([]byte("checkouts")).Get([]byte(out.scope.CheckoutID)) == nil {
				if q.CheckoutID != "" {
					return problem("not_found", "recorded checkout ID is unavailable; preview an existing checkout path")
				}
			} else if _, e := readCheckoutBinding(tx, out.scope.CheckoutID); e != nil {
				return e
			}
		}
		budget := &scanBudget{ctx: ctx, unbounded: true, dependents: map[string][]string{}}
		eachRecord := tx.Bucket([]byte("instances")).ForEach
		if len(selected) > 0 {
			budget.dependents = selected[0].dependents
			eachRecord = func(visit func([]byte, []byte) error) error {
				id := selected[0].target.ID
				return visit([]byte(id), tx.Bucket([]byte("instances")).Get([]byte(id)))
			}
		}
		if len(selected) == 0 {
			if e := tx.Bucket([]byte("instances")).ForEach(func(k, raw []byte) error {
				var r record
				if e := decodeRecord(raw, string(k), &r); e != nil {
					return e
				}
				if r.Instance.Task != nil {
					p := r.Instance.Task.InstanceID
					budget.dependents[p] = append(budget.dependents[p], string(k))
				}
				return ctx.Err()
			}); e != nil {
				return e
			}
		}
		return eachRecord(func(k, raw []byte) error {
			if e := ctx.Err(); e != nil {
				return e
			}
			var r record
			if e := decodeRecord(raw, string(k), &r); e != nil {
				return e
			}
			parent := checkoutParent(r.Instance)
			if !q.Orphans && parent != out.scope.CheckoutID {
				return nil
			}
			prefix := string(k) + "/"
			pending := tx.Bucket([]byte("evidence_deletions")).Cursor()
			pk, _ := pending.Seek([]byte(prefix))
			hasPending := strings.HasPrefix(string(pk), prefix)
			if r.Instance.Status == v1.Destroyed && !hasPending {
				return nil
			}
			if r.Instance.Status == v1.Destroyed {
				if _, e := consumerErasure(tx, r, true, budget); e != nil {
					return e
				}
			}
			b, e := readCheckoutBinding(tx, parent)
			if e != nil {
				return e
			}
			missing, statErr := cleanupPathAbsent(b.Current.Path)
			if q.Orphans && !missing && statErr == nil {
				return nil
			}
			t := v1.CleanupTarget{ID: string(k), CheckoutID: parent, CheckoutPath: b.Current.Path, Project: r.Instance.Plan.Project, Scene: r.Instance.Plan.Scene, Status: r.Instance.Status, MissingPath: missing, Blockers: []string{}, Resources: []v1.CleanupResource{}}
			block := func(code string) {
				for _, v := range t.Blockers {
					if v == code {
						return
					}
				}
				t.Blockers = append(t.Blockers, code)
			}
			if statErr != nil && !missing {
				block("checkout_unavailable")
			}
			if b.PendingPath != "" {
				block("binding_unverified")
			}
			g, e := decodeCleanupResources(tx, string(k))
			if e != nil {
				return e
			}
			allocations, e := decodeSharedAllocations(tx.Bucket([]byte("shared_allocations")).Get(k))
			if e != nil {
				return e
			}
			if r.Instance.Status != v1.Destroyed {
				if q.Orphans {
					if len(ownerIDs[parent]) > 0 || s.expiryWorkers[string(k)] != nil || cleanupExecutionActive(s.executions[string(k)]) {
						block("active_work")
					}
					settled, e := consumerEligibility(tx, r, boot, budget)
					if e != nil || settled.State != v1.ConsumerSettled {
						block("cleanup_uncertain")
					}
				}
				for _, history := range r.NativeAbandonments {
					if !history.previousBoot() {
						block("cleanup_uncertain")
					}
				}
				if r.Instance.Execution != nil {
					t.ExportPartial = r.Instance.Execution.CollectionFailure != "" || r.Instance.Execution.CleanupFailure != "" || active(r.Instance.Status)
					if q.Orphans && r.Instance.Execution.CleanupFailure != "" {
						block("cleanup_uncertain")
					}
				}
			}
			names := make([]string, 0, len(g.Resources))
			for name := range g.Resources {
				names = append(names, name)
			}
			sort.Strings(names)
			for _, name := range names {
				resource := g.Resources[name]
				if !resource.Removed {
					t.Resources = append(t.Resources, v1.CleanupResource{Name: name, Kind: resource.Kind, Outcome: "remove"})
				}
			}
			for _, c := range g.Containers {
				if !c.Removed {
					name := c.Component
					if name == "" {
						name = "recorded container"
					}
					t.Resources = append(t.Resources, v1.CleanupResource{Name: name, Kind: "container", Outcome: "remove"})
				}
			}
			names = names[:0]
			for name := range allocations {
				names = append(names, name)
			}
			sort.Strings(names)
			for _, name := range names {
				if !allocations[name].Released {
					t.Resources = append(t.Resources, v1.CleanupResource{Name: name, Kind: "allocation", Outcome: "remove"})
				}
			}
			t.Resources = append(t.Resources, v1.CleanupResource{Name: "attempt results, logs and artifacts", Kind: "evidence", Outcome: "remove"})
			if r.Instance.Task != nil {
				t.TaskParent = r.Instance.Task.InstanceID
			}
			// Hash private authority with the daemon key; public digests cannot test guessed secrets.
			revision := []any{raw, tx.Bucket([]byte("checkout_bindings")).Get([]byte(parent)), tx.Bucket([]byte("resources")).Get(k), tx.Bucket([]byte("shared_allocations")).Get(k), ownerIDs[parent], t.Blockers, t.MissingPath}
			attempts := []string{}
			cursor := tx.Bucket([]byte("attempts")).Cursor()
			for ak, av := cursor.Seek([]byte(prefix)); ak != nil && strings.HasPrefix(string(ak), prefix); ak, av = cursor.Next() {
				revision = append(revision, av)
				attempts = append(attempts, string(ak))
			}
			pending = tx.Bucket([]byte("evidence_deletions")).Cursor()
			for dk, dv := pending.Seek([]byte(prefix)); dk != nil && strings.HasPrefix(string(dk), prefix); dk, dv = pending.Next() {
				revision = append(revision, dv)
			}
			t.Revision = cleanupHash(s.repairKey, revision)
			// Stop may settle an existing attempt or remove its network, but never grants
			// authority over another attempt, generation, allocation or evidence identity.
			reviewed := g
			g.Resources = maps.Clone(g.Resources)
			g.Containers = slices.Clone(g.Containers)
			for name, resource := range g.Resources {
				resource.Removed = false
				g.Resources[name] = resource
			}
			for i := range g.Containers {
				g.Containers[i].Removed = false
			}
			for name, a := range allocations {
				a.Released = false
				a.Result = v1.ManagerResult{}
				allocations[name] = a
			}
			authority := cleanupHash(s.repairKey, parent, b, r.Instance.Checkout, r.Instance.Plan, r.Instance.Task, executionAttempt(r.Instance), g, allocations, attempts)
			out.targets = append(out.targets, cleanupSelection{target: t, authority: authority, dependents: budget.dependents, resources: reviewed})
			return nil
		})
	})
	if err != nil {
		return out, err
	}
	sort.SliceStable(out.targets, func(i, j int) bool {
		a, b := out.targets[i].target, out.targets[j].target
		if (a.TaskParent != "") != (b.TaskParent != "") {
			return a.TaskParent != ""
		}
		return a.ID < b.ID
	})
	scopedOwners := map[string][]string{}
	if q.Orphans {
		for _, v := range out.targets {
			scopedOwners[v.target.CheckoutID] = ownerIDs[v.target.CheckoutID]
		}
	} else {
		scopedOwners[out.scope.CheckoutID] = ownerIDs[out.scope.CheckoutID]
	}
	out.digest = cleanupHash(s.repairKey, s.generation, out.scope, out.targetsPublic(), scopedOwners)
	return out, nil
}
func (p cleanupPreview) targetsPublic() []v1.CleanupTarget {
	out := make([]v1.CleanupTarget, 0, len(p.targets))
	for _, v := range p.targets {
		out = append(out, v.target)
	}
	return out
}
func decodeCleanupResources(tx *bolt.Tx, id string) (resourceGeneration, error) {
	raw := tx.Bucket([]byte("resources")).Get([]byte(id))
	if raw == nil {
		return resourceGeneration{}, nil
	}
	return decodeResources(raw)
}
func cleanupExecutionActive(entry *execution) bool {
	if entry == nil {
		return false
	}
	select {
	case <-entry.done:
		// Completion persists Kept before closing done; consumerEligibility checks it
		// independently without waiting on an entry lock inside a read transaction.
		return false
	default:
		return true
	}
}

func (s *service) cleanupPreview(ctx context.Context, q v1.CleanupRequest) (cleanupPreview, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cleanupPreviewLocked(ctx, q)
}

func (s *service) startCleanup(ctx context.Context, q v1.CleanupRequest) (*cleanupOperation, error) {
	if err := q.Validate(); err != nil {
		return nil, err
	}
	if q.ApplyPreview == "" {
		return nil, problem("invalid_request", "apply requires the exact preview digest")
	}
	owned, finish, err := s.beginMutation(context.Background())
	if err != nil {
		return nil, err
	}
	owned, cancel := context.WithCancel(owned)
	accepted := false
	defer func() {
		if !accepted {
			cancel()
			finish()
		}
	}()
	// The binding gate is held only while installing/revalidating scoped admission.
	s.store.bindingGate.Lock()
	s.mu.Lock()
	release := func() { s.mu.Unlock(); s.store.bindingGate.Unlock() }
	if s.closing || s.bulkStopping {
		release()
		return nil, problem("admission_closed", "daemon shutdown or bulk stop in progress")
	}
	preview, err := s.cleanupPreviewLocked(ctx, q)
	if err != nil {
		release()
		return nil, err
	}
	if preview.digest != q.ApplyPreview {
		release()
		return nil, problem("stale_preview", "cleanup preview changed; obtain a fresh preview")
	}
	if err := ctx.Err(); err != nil {
		release()
		return nil, err
	}
	parents := map[string]bool{}
	for _, v := range preview.targets {
		if !q.Orphans || len(v.target.Blockers) == 0 {
			parents[v.target.CheckoutID] = true
		}
	}
	if !q.Orphans {
		parents[preview.scope.CheckoutID] = true
	}
	for parent := range parents {
		if s.cleanupFences[parent] {
			release()
			return nil, problem("conflict", "checkout cleanup already in progress")
		}
	}
	if s.cleanupFences == nil {
		s.cleanupFences = map[string]bool{}
	}
	if s.checkoutRevocations == nil {
		s.checkoutRevocations = map[string]uint64{}
	}
	s.admissionSequence++
	for parent := range parents {
		s.cleanupFences[parent] = true
		s.checkoutRevocations[parent] = s.admissionSequence
	}
	preceding := []*mutationOwner{}
	for owner := range s.mutationOwners {
		if owner.workload && parents[owner.checkout] {
			preceding = append(preceding, owner)
			owner.cancel()
		}
	}
	for _, v := range preview.targets {
		if len(v.target.Blockers) > 0 {
			continue
		}
		if entry := s.executions[v.target.ID]; entry != nil {
			entry.bulkCancelled = true
		}
	}
	if s.cleanupOperations == nil {
		s.cleanupOperations = map[string]*cleanupOperation{}
	}
	id := newID()
	operation := &cleanupOperation{changed: make(chan struct{}), cancel: cancel, id: id, observing: true}
	s.cleanupOperations[id] = operation
	record := v1.CleanupRecord{APIVersion: v1.Version, Type: "accepted", DaemonGeneration: s.generation, OperationID: id}
	operation.emit(record)
	release()
	accepted = true
	go func() {
		defer cancel()
		defer finish()
		summary := v1.CleanupSummary{Preview: preview.digest, Targets: len(preview.targets)}
		unjoined := map[string]bool{}
		for _, owner := range preceding {
			wait, cancel := context.WithTimeout(owned, 20*time.Second)
			select {
			case <-owner.done:
			case <-wait.Done():
				unjoined[owner.checkout] = true
			}
			cancel()
		}
		for _, selected := range preview.targets {
			target := selected.target
			event := record
			event.Type = "target"
			event.InstanceID = target.ID
			switch {
			case owned.Err() != nil:
				event.Outcome = "unattempted"
				event.Reason = "cancelled"
				summary.Unattempted++
			case unjoined[target.CheckoutID]:
				// A timed-out owner retains its resources; never release its parent.
				event.Outcome = "unresolved"
				event.Reason = "blocked"
				summary.Unresolved++
			case len(target.Blockers) > 0:
				event.Outcome = "unresolved"
				event.Reason = "blocked"
				summary.Unresolved++
			default:
				progress := record
				progress.Type = "progress"
				progress.InstanceID = target.ID
				progress.Outcome = "stopping"
				operation.emit(progress)
				observed := map[string]bool{}
				step := &cleanupSteps{reviewed: selected.resources, before: func() error {
					if err := owned.Err(); err != nil {
						return err
					}
					if q.Orphans {
						if absent, err := cleanupPathAbsent(target.CheckoutPath); err != nil || !absent {
							return problem("target_changed", "orphan checkout is no longer absent; preserved")
						}
					}
					return nil
				}, emit: func(name, kind, outcome string) {
					if observed[kind+"/"+name] {
						return
					}
					observed[kind+"/"+name] = true
					e := record
					e.Type = "resource"
					e.InstanceID = target.ID
					e.Resource = &v1.CleanupResource{Name: name, Kind: kind, Outcome: outcome}
					operation.emit(e)
				}}
				err := s.destroyCleanupTarget(context.WithValue(owned, cleanupStepsKey{}, step), preview.scope, selected)
				if err != nil {
					// Prior owners may settle while this target waits for its turn.
					_ = s.store.observeStoppedResources(context.WithValue(owned, cleanupStepsKey{}, step), target.ID, selected.resources)
					for _, resource := range target.Resources {
						if !observed[resource.Kind+"/"+resource.Name] {
							step.emit(resource.Name, resource.Kind, "unresolved")
						}
					}
				}
				switch {
				case err == nil:
					event.Outcome = "done"
					summary.Done++
				case errors.Is(err, context.Canceled):
					event.Outcome = "unresolved"
					event.Reason = "cancelled"
					summary.Unresolved++
				default:
					var typed *v1.PlanError
					event.Outcome = "failed"
					event.Reason = "cleanup_failed"
					if errors.As(err, &typed) && (typed.Code == "target_changed" || typed.Code == "conflict") {
						event.Outcome = "unresolved"
						event.Reason = "target_changed"
						summary.Unresolved++
					} else {
						summary.Failed++
					}
				}
			}
			operation.emit(event)
		}
		summary.Cancelled = owned.Err() != nil
		s.mu.Lock()
		for parent := range parents {
			delete(s.cleanupFences, parent)
		}
		s.mu.Unlock()
		end := record
		end.Type = "summary"
		end.Summary = &summary
		operation.emit(end)
		s.releaseCleanup(id, operation, false)
	}()
	return operation, nil
}

func (s *service) destroyCleanupTarget(ctx context.Context, q v1.CleanupRequest, selected cleanupSelection) error {
	if err := lockCleanup(ctx, &s.evidence.deletion); err != nil {
		return err
	}
	defer s.evidence.deletion.Unlock()
	if err := lockCleanup(ctx, &s.evidence.mu); err != nil {
		return err
	}
	defer s.evidence.mu.Unlock()
	lock := s.mutationLock(selected.target.ID)
	if err := lockCleanup(ctx, lock); err != nil {
		return err
	}
	defer lock.Unlock()
	owners := append([]string{selected.target.ID}, selected.dependents[selected.target.ID]...)
	for _, id := range owners {
		s.mu.Lock()
		entry := s.executions[id]
		s.mu.Unlock()
		if entry != nil {
			// Parent teardown joins direct dependents; a busy child must not bypass this bound.
			if err := lockCleanup(ctx, &entry.mu); err != nil {
				return err
			}
			entry.mu.Unlock()
		}
	}
	s.mu.Lock()
	fresh, err := s.cleanupPreviewLocked(ctx, q, selected)
	s.mu.Unlock()
	if err != nil {
		return err
	}
	var current *cleanupSelection
	for i := range fresh.targets {
		if fresh.targets[i].target.ID == selected.target.ID {
			current = &fresh.targets[i]
			break
		}
	}
	if current == nil || current.authority != selected.authority || len(current.target.Blockers) > 0 || current.target.CheckoutPath != selected.target.CheckoutPath {
		return problem("target_changed", "reviewed cleanup target or eligibility changed; preserved")
	}
	if err := cleanupBefore(ctx); err != nil {
		return err
	}
	_, err = s.destroyLocked(ctx, selected.target.ID, false, true)
	return err
}

type cleanupStepsKey struct{}
type cleanupSteps struct {
	reviewed resourceGeneration
	before   func() error
	emit     func(string, string, string)
}

func cleanupBefore(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if step, _ := ctx.Value(cleanupStepsKey{}).(*cleanupSteps); step != nil {
		return step.before()
	}
	return nil
}
func cleanupRemoved(ctx context.Context, name, kind string) {
	cleanupResourceOutcome(ctx, name, kind, "removed")
}
func cleanupResourceOutcome(ctx context.Context, name, kind, outcome string) {
	if name == "" {
		name = "recorded container"
	}
	if step, _ := ctx.Value(cleanupStepsKey{}).(*cleanupSteps); step != nil {
		step.emit(name, kind, outcome)
	}
}

func (s *service) cancelCleanup(q v1.CleanupCancelRequest) (v1.CleanupCancelResponse, error) {
	if err := q.Validate(); err != nil {
		return v1.CleanupCancelResponse{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	o := s.cleanupOperations[q.OperationID]
	if q.DaemonGeneration != s.generation || o == nil {
		return v1.CleanupCancelResponse{}, problem("not_found", "cleanup operation unavailable; cancellation not acknowledged; inspect known targets")
	}
	o.cancel()
	return v1.CleanupCancelResponse{APIVersion: v1.Version, DaemonGeneration: s.generation, OperationID: q.OperationID, Cancelled: true}, nil
}

// releaseCleanup retains cancellation and observations only for the coordinator and current observer.
func (s *service) releaseCleanup(id string, o *cleanupOperation, observerEnded bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	o.mu.Lock()
	defer o.mu.Unlock()
	if observerEnded {
		o.observing = false
	}
	if o.finished && !o.observing {
		delete(s.cleanupOperations, id)
	}
}

func (s *service) writeCleanup(w http.ResponseWriter, r *http.Request, q v1.CleanupRequest) {
	var operation *cleanupOperation
	if q.ApplyPreview != "" {
		var err error
		operation, err = s.startCleanup(r.Context(), q)
		if err != nil {
			writeError(w, err)
			return
		}
		defer func() { s.releaseCleanup(operation.id, operation, true) }()
	} else {
		preview, err := s.cleanupPreview(r.Context(), q)
		if err != nil {
			writeError(w, err)
			return
		}
		operation = &cleanupOperation{changed: make(chan struct{})}
		for _, t := range preview.targetsPublic() {
			target := t
			operation.emit(v1.CleanupRecord{APIVersion: v1.Version, Type: "preview_target", DaemonGeneration: s.generation, Target: &target})
		}
		operation.emit(v1.CleanupRecord{APIVersion: v1.Version, Type: "preview", DaemonGeneration: s.generation, Summary: &v1.CleanupSummary{Preview: preview.digest, Targets: len(preview.targets)}})
	}
	w.Header().Set("Content-Type", "application/x-ndjson")
	controller := http.NewResponseController(w)
	for index := 0; ; index++ {
		event, err := operation.next(r.Context(), index)
		if err != nil {
			return
		}
		if err = controller.SetWriteDeadline(time.Now().Add(5 * time.Second)); err != nil && !errors.Is(err, http.ErrNotSupported) {
			return
		}
		if err = json.NewEncoder(w).Encode(event); err != nil {
			return
		}
		if err = controller.Flush(); err != nil {
			return
		}
		if err = controller.SetWriteDeadline(time.Time{}); err != nil && !errors.Is(err, http.ErrNotSupported) {
			return
		}
		if event.Type == "summary" || event.Type == "preview" {
			return
		}
	}
}

// bindCheckout attaches scoped revocable admission before preparation can publish effects.
func (s *service) bindCheckout(ctx context.Context, parent string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	owner, _ := ctx.Value(admissionKey{}).(*mutationOwner)
	if s.cleanupFences[parent] {
		return problem("admission_closed", "checkout cleanup is in progress")
	}
	if owner != nil {
		if owner.checkout != "" && owner.checkout != parent {
			return problem("conflict", "workload checkout changed")
		}
		if owner.checkout == "" {
			owner.checkout = parent
		}
	}
	return s.workloadAllowed(ctx)
}
func (s *service) checkCleanupBindings(parents []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, p := range parents {
		if s.cleanupFences[p] {
			return problem("conflict", "checkout cleanup fences association changes")
		}
	}
	return nil
}

// Common registration publication can affect a checkout with no remaining
// instances, so instance-derived repair locks alone do not cover its fence.
func (s *service) checkCleanupRegistration(selected string, next v1.RegistrationIdentity) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.cleanupFences) == 0 {
		return nil
	}
	return s.store.db.View(func(tx *bolt.Tx) error {
		old, err := readCheckoutBinding(tx, selected)
		if err != nil {
			return err
		}
		for parent := range s.cleanupFences {
			b, err := readCheckoutBinding(tx, parent)
			if err != nil {
				return err
			}
			common := b.Current.Registration
			matches := next.CommonPath != "" && common.CommonPath == next.CommonPath || old.Current.Registration.CommonPath != "" && common.CommonPath == old.Current.Registration.CommonPath
			if parent == selected || matches && (next.CommonID == "" || common.CommonID != next.CommonID || common.CommonPath != next.CommonPath) {
				return problem("conflict", "checkout cleanup fences common registration changes")
			}
		}
		return nil
	})
}

// cleanupPathAbsent treats a dangling symlink as a present checkout. All phases
// use the same proof; an inaccessible path never authorizes orphan destruction.
func cleanupPathAbsent(path string) (bool, error) {
	_, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return true, nil
	}
	return false, err
}

// cleanupCallContext bounds one batch lock acquisition or provider effect, never
// a target or operation. Other lifecycle callers retain their existing budgets.
func cleanupCallContext(ctx context.Context, budget time.Duration) (context.Context, context.CancelFunc) {
	if step, _ := ctx.Value(cleanupStepsKey{}).(*cleanupSteps); step != nil {
		return context.WithTimeout(ctx, budget)
	}
	return ctx, func() {}
}

func lockCleanup(ctx context.Context, lock *sync.Mutex) error {
	wait, cancel := cleanupCallContext(ctx, 20*time.Second)
	defer cancel()
	return lockExportEvidence(wait, lock)
}
