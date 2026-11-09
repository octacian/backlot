package daemon

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	hashpkg "hash"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	v1 "github.com/octacian/backlot/api/v1"
	"github.com/octacian/backlot/internal/localipc"
	"github.com/octacian/backlot/internal/manifest"
	"github.com/octacian/backlot/internal/plan"
	"github.com/octacian/backlot/internal/projectconfig"
	"github.com/octacian/backlot/internal/registration"
	bolt "go.etcd.io/bbolt"
)

// repairKey is private to a daemon generation; public freshness tokens cannot test guessed secrets.
func (s *service) repairKeyBytes() []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.repairKey) == 0 {
		s.repairKey = randomBytes()
	}
	return slices.Clone(s.repairKey)
}
func observation(path string) v1.FilesystemObservation {
	out := v1.FilesystemObservation{Path: path}
	if info, err := os.Stat(path); err == nil {
		if id, e := fileIdentity(info); e == nil {
			out.Device, out.Inode, _ = strings.Cut(id, ":")
		}
	}
	return out
}
func (s *service) projectPreview(request v1.ReassociationRequest) (result v1.ReassociationResponse, previewErr error) {
	key := s.repairKeyBytes()
	unjoined := s.repairUnjoinedOwners()
	result = v1.ReassociationResponse{APIVersion: v1.Version, DaemonGeneration: s.generation, Request: request, Projects: []v1.ProjectRegistration{}, Instances: []v1.ReassociationInstance{}, Services: []v1.SharedStatus{}, Allocations: []v1.ReassociationAllocation{}, Changes: []string{}, Preserved: []string{"service and instance IDs", "allocations and paired credentials", "data and resource capabilities", "recorded launch and cleanup authority"}, Blockers: []string{}}
	result.Request.Apply = false
	result.Request.Preview = ""
	if err := request.Validate(); err != nil {
		return result, err
	}
	source, err := plan.Locate(request.ProjectPath)
	if err != nil {
		return result, err
	}
	result.Request.ProjectPath = source.ProjectPath
	if request.ConfigPath != "" {
		canonical, e := filepath.EvalSymlinks(request.ConfigPath)
		if e == nil {
			request.ConfigPath = canonical
			result.Request.ConfigPath = canonical
		}
	}
	data, err := projectconfig.Read(source.ManifestPath)
	if err != nil {
		return result, err
	}
	var m v1.Manifest
	if err := manifest.Decode(data, filepath.Ext(source.ManifestPath), &m); err != nil {
		return result, err
	}
	if err := manifest.Validate(m); err != nil {
		return result, err
	}
	result.Current, err = identifyShared(source, m.Project)
	if err != nil {
		return result, err
	}
	result.Checkout, err = identify(source.CheckoutPath)
	if err != nil {
		return result, err
	}
	state := "legacy"
	if result.Checkout.Registration.CommonID != "" {
		state = "enrolled"
	}
	result.Registration = &v1.RegistrationResult{State: state, Identity: result.Checkout.Registration, Conflicts: []v1.RegistrationConflict{}}
	result.Filesystems = []v1.FilesystemObservation{observation(result.Checkout.Path), observation(result.Current.Anchor)}
	if result.Checkout.GitDirectory != "" {
		result.Filesystems = append(result.Filesystems, observation(result.Checkout.GitDirectory))
	}
	freshness := hmac.New(sha256.New, key)
	_, _ = freshness.Write(data)
	block := func(message string) { result.Blockers = append(result.Blockers, message) }
	var selected projectBinding
	var selectedOrigin sharedProjectIdentity
	commonAffected := map[string]bool{}
	location, err := registration.Locate(source.ProjectPath, source.CheckoutPath)
	if err != nil {
		return result, err
	}
	err = s.store.db.View(func(tx *bolt.Tx) error {
		if _, e := locateCheckout(tx, result.Checkout); e != nil {
			var detail *v1.PlanError
			if errors.As(e, &detail) && detail.Registration != nil {
				result.Registration = detail.Registration
			}
		}
		if tx.Bucket([]byte("shared_projects")).Get([]byte(result.Current.ID)) != nil && result.Current.ID != request.ProjectID || foreignOrigin(tx, "shared_paths", sharedPath(result.Current), request.ProjectID) {
			block("destination is another retained shared-project origin; ownership remains reserved")
		}
		if tx.Bucket([]byte("checkouts")).Get([]byte(result.Checkout.ID)) != nil && result.Checkout.ID != request.CheckoutID || foreignOrigin(tx, "paths", result.Checkout.Path, request.CheckoutID) {
			block("destination is another retained checkout origin; ownership remains reserved")
		}
		if err := tx.Bucket([]byte("project_bindings")).ForEach(func(k, d []byte) error {
			b, e := readProjectBinding(tx, string(k))
			if e != nil {
				return e
			}
			var origin sharedProjectIdentity
			if json.Unmarshal(tx.Bucket([]byte("shared_projects")).Get(k), &origin) != nil {
				return problem("state_corrupt", "shared origin unavailable")
			}
			result.Projects = append(result.Projects, v1.ProjectRegistration{Origin: origin, Current: b.Current, Revision: b.Revision, Acknowledgement: b.Acknowledgement})
			if string(k) == request.ProjectID {
				selected = b
				selectedOrigin = origin
			}
			if string(k) != request.ProjectID && (b.Current.ID == result.Current.ID || sharedPath(b.Current) == sharedPath(result.Current)) {
				block("destination belongs to another retained shared project")
			}
			return nil
		}); err != nil {
			return err
		}
		if request.ProjectID != "" && selectedOrigin.ID == "" {
			block("selected retained project not found")
		} else if request.ProjectID != "" && (selectedOrigin.Project != result.Current.Project || selectedOrigin.Relative != result.Current.Relative) {
			block("declared project or relative location differs; repair cannot rename projects")
		}
		if request.CheckoutID == "" {
			block("select exact retained checkout registration for repair")
		}
		if request.CheckoutID != "" {
			if b, e := readCheckoutBinding(tx, request.CheckoutID); e == nil && b.Current.Registration.CommonID != "" && (b.Current.Registration.CommonPath != location.Common || b.Current.Registration.CommonID != result.Checkout.Registration.CommonID) {
				if err := tx.Bucket([]byte("checkout_bindings")).ForEach(func(k, _ []byte) error {
					other, e := readCheckoutBinding(tx, string(k))
					if e == nil && other.Current.Registration.CommonPath == b.Current.Registration.CommonPath {
						commonAffected[string(k)] = true
					}
					return e
				}); err != nil {
					return err
				}
			}
		}
		if err := tx.Bucket([]byte("checkout_bindings")).ForEach(func(k, d []byte) error {
			cb, e := readCheckoutBinding(tx, string(k))
			if e != nil {
				return e
			}
			if string(k) != request.CheckoutID && (cb.Current.ID == result.Checkout.ID || cb.Current.Path == result.Checkout.Path) {
				block("destination belongs to another retained checkout")
			}
			return nil
		}); err != nil {
			return err
		}
		affected := map[string]bool{}
		serviceOwners := map[string]bool{request.ProjectID: true}
		sharedChange := selectedOrigin.ID != "" && !sameSharedBinding(selected.Current, result.Current)
		if request.CheckoutID != "" {
			if err := validateRepairOwners(tx, request.ProjectID, request.CheckoutID); err != nil {
				block("selected project/checkout/allocation associations are incompatible or unproven; independent retained owners cannot merge")
			}
		}
		if err := tx.Bucket([]byte("instances")).ForEach(func(k, d []byte) error {
			var r record
			if err := decodeRecord(d, string(k), &r); err != nil {
				return err
			}
			owners, err := retainedSharedProjects(tx, r.Instance)
			if err != nil {
				return err
			}
			chosen := checkoutParent(r.Instance) == request.CheckoutID
			if sharedChange && len(owners) == 0 && len(r.Instance.Plan.SharedServices) > 0 && r.Instance.Plan.Project == selectedOrigin.Project {
				block("retained consumer shared association is unproven; cannot safely scope repair: " + string(k))
				affected[string(k)] = true
			}
			if chosen || commonAffected[checkoutParent(r.Instance)] || sharedChange && owners[request.ProjectID] || request.ProjectID == "" && request.CheckoutID == "" {
				affected[string(k)] = true
				for owner := range owners {
					serviceOwners[owner] = true
				}
			}
			return nil
		}); err != nil {
			return err
		}
		if err := tx.Bucket([]byte("shared_services")).ForEach(func(k, d []byte) error {
			r, e := decodeSharedService(d, string(k))
			if e != nil {
				return e
			}
			if serviceOwners[r.Project.ID] || request.ProjectID == "" && request.CheckoutID == "" {
				result.Services = append(result.Services, v1.SharedStatus{ID: r.ID, ProjectID: r.Project.ID, Name: r.Name, Configuration: r.Configuration, Status: r.Status})
				_, _ = freshness.Write(d)
			}
			return nil
		}); err != nil {
			return err
		}
		if err := tx.Bucket([]byte("shared_allocations")).ForEach(func(k, d []byte) error {
			allocations, e := decodeSharedAllocations(d)
			if e != nil {
				return e
			}
			names := make([]string, 0, len(allocations))
			for name := range allocations {
				names = append(names, name)
			}
			slices.Sort(names)
			for _, name := range names {
				a := allocations[name]
				for index := range result.Services {
					if result.Services[index].ID == a.ServiceID && !a.Released {
						result.Services[index].Allocations++
						if a.Request.Operation == "release" {
							result.Services[index].PendingReleases++
						}
					}
				}
				if affected[string(k)] {
					result.Allocations = append(result.Allocations, v1.ReassociationAllocation{AllocationID: a.Request.AllocationID, InstanceID: string(k), Resource: name, ServiceID: a.ServiceID, Released: a.Released})
				}
			}
			if affected[string(k)] {
				_, _ = freshness.Write(d)
			}
			return nil
		}); err != nil {
			return err
		}
		if err := tx.Bucket([]byte("instances")).ForEach(func(k, d []byte) error {
			r, e := load(tx, string(k))
			if e != nil {
				return e
			}
			i := r.Instance
			chosen := checkoutParent(i) == request.CheckoutID
			if !chosen && !affected[i.ID] && (request.ProjectID != "" || request.CheckoutID != "") {
				return nil
			}
			if i.Association == nil {
				return problem("state_corrupt", "instance association missing")
			}

			result.Instances = append(result.Instances, v1.ReassociationInstance{RecordedFilesystemState: "unrecorded", ID: i.ID, Scene: i.Plan.Scene, Status: i.Status, Recorded: i.Checkout, Association: *i.Association, Selected: chosen})
			_, _ = freshness.Write(d)
			if unjoined[i.ID] {
				block("affected consumer " + i.ID + " must be stopped/joined with verified cleanup")
			}
			blockers, e := repairConsumerBlockers(tx, r)
			if e != nil {
				return e
			}
			for _, message := range blockers {
				block(message)
			}
			if raw := tx.Bucket([]byte("runtime")).Get(k); raw != nil {
				_, _ = freshness.Write(raw)
			}
			return nil
		}); err != nil {
			return err
		}
		if request.CheckoutID != "" {
			cb, e := readCheckoutBinding(tx, request.CheckoutID)
			if e != nil {
				block("selected retained checkout not found")
			} else {
				result.Changes = append(result.Changes, fmt.Sprintf("checkout %s: %s -> %s; per-worktree Git directory %s -> %s", request.CheckoutID, cb.Current.Path, result.Checkout.Path, cb.Current.GitDirectory, result.Checkout.GitDirectory))
			}
		}
		if selectedOrigin.ID != "" {
			result.Changes = append(result.Changes, fmt.Sprintf("shared project %s: anchor %s (%s) -> %s (%s); ownership ID preserved", selectedOrigin.ID, selected.Current.Anchor, selected.Current.Filesystem, result.Current.Anchor, result.Current.Filesystem))
		}
		result.StateRevision = repairDBRevision(tx, key)
		return nil
	})
	if err != nil {
		return result, err
	}
	if err := s.validateRepairSelection(source, m, &result, freshness); err != nil {
		return result, err
	}
	s.mu.Lock()
	for _, i := range result.Instances {
		if entry := s.executions[i.ID]; entry != nil {
			select {
			case <-entry.done:
			default:
				block("affected execution owner remains unjoined: " + i.ID)
			}
		}
		if s.expiryWorkers[i.ID] != nil {
			block("affected expiry owner remains unjoined: " + i.ID)
		}
	}
	s.mu.Unlock()
	result.Private.Revision = hex.EncodeToString(freshness.Sum(nil))
	raw, _ := json.Marshal(result)
	result.Preview = hash(string(raw))
	return result, nil
}

func (s *service) reassociate(ctx context.Context, request v1.ReassociationRequest) (v1.ReassociationResponse, error) {
	if request.FreshIdentityAt != "" {
		return s.rotateRegistration(ctx, request)
	}
	preview, err := s.projectPreview(request)
	if err != nil || !request.Apply {
		return preview, err
	}
	if err := ctx.Err(); err != nil {
		return preview, err
	}
	if preview.Preview != request.Preview {
		return preview, problem("stale_preview", "preview changed; take a fresh preview before applying")
	}
	if len(preview.Blockers) > 0 {
		return preview, problem("reassociation_blocked", "reassociation prerequisites failed; inspect a fresh preview")
	}
	sourceForLock, err := plan.Locate(request.ProjectPath)
	if err != nil {
		return preview, err
	}
	markerLocation, err := registration.Locate(sourceForLock.ProjectPath, sourceForLock.CheckoutPath)
	if err != nil {
		return preview, err
	}
	unlockMarkers, err := registration.Lock(ctx, markerLocation)
	if err != nil {
		return preview, err
	}
	defer unlockMarkers()
	ctx, finish, err := s.beginMutation(ctx)
	if err != nil {
		return preview, err
	}
	defer finish()
	ids := make([]string, 0, len(preview.Instances))
	for _, i := range preview.Instances {
		ids = append(ids, i.ID)
	}
	slices.Sort(ids)
	var locks []*sync.Mutex
	defer func() {
		for n := len(locks) - 1; n >= 0; n-- {
			locks[n].Unlock()
		}
	}()
	for _, id := range ids {
		lock := s.mutationLock(id)
		if !lock.TryLock() {
			return preview, problem("conflict", "affected instance mutation in progress; retry with fresh preview")
		}
		locks = append(locks, lock)
	}
	serviceIDs := make([]string, 0, len(preview.Services))
	for _, r := range preview.Services {
		serviceIDs = append(serviceIDs, r.ID)
	}
	unlock, err := s.store.lockShared(ctx, serviceIDs...)
	if err != nil {
		return preview, err
	}
	defer unlock()
	unlockRegistry, err := projectconfig.Lock(ctx, preview.Private.OperationRoot)
	if err != nil {
		return preview, err
	}
	defer unlockRegistry()
	source, err := plan.Locate(request.ProjectPath)
	if err != nil {
		return preview, err
	}
	captured, err := captureSource(source)
	if err != nil {
		return preview, err
	}
	current, err := s.projectPreview(request)
	if err != nil {
		return preview, err
	}
	if current.Preview != request.Preview || len(current.Blockers) > 0 {
		return current, problem("stale_preview", "preview or prerequisites changed; take a fresh preview")
	}
	if err := s.store.sharedPoint("reassociation-source-validation"); err != nil {
		return current, err
	}
	if err := captured.verify(); err != nil {
		return current, err
	}
	anchor, err := identifyShared(source, current.Current.Project)
	if err != nil {
		return current, err
	}
	if anchor != current.Current {
		return current, problem("stale_preview", "sharing anchor changed during validation; take a fresh preview")
	}
	if !s.store.bindingGate.TryLock() {
		return preview, problem("conflict", "association admission busy; take a fresh preview")
	}
	defer s.store.bindingGate.Unlock()
	// Only database comparisons occur under the gate/transaction; source reads are complete.
	key := s.repairKeyBytes()
	expectedRevision := preview.StateRevision
	if preview.Checkout.Registration.CheckoutID == "" || preview.Checkout.Registration.CommonID == "" {
		if err := s.store.db.Update(func(tx *bolt.Tx) error {
			if repairDBRevision(tx, key) != expectedRevision {
				return problem("stale_preview", "registration changed before enrollment")
			}
			b, err := readCheckoutBinding(tx, request.CheckoutID)
			if err != nil {
				return err
			}
			b.PendingPath = source.CheckoutPath
			if err := put(tx, "checkout_bindings", request.CheckoutID, b); err != nil {
				return err
			}
			expectedRevision = repairDBRevision(tx, key)
			return nil
		}); err != nil {
			return preview, err
		}
		if _, err := registration.Enroll(markerLocation); err != nil {
			return preview, err
		}
		observed, err := captureSource(source)
		if err != nil {
			return preview, err
		}
		if observed.projectIdentity != captured.projectIdentity || observed.checkout.ID != captured.checkout.ID || observed.commonIdentity != captured.commonIdentity {
			return preview, problem("checkout_changed", "source changed during explicit enrollment; inspect staged repair")
		}
		preview.Checkout.Registration = observed.checkout.Registration
		preview.Current.Registration = observed.checkout.Registration
	}
	err = s.store.db.Update(func(tx *bolt.Tx) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		for _, r := range preview.Projects {
			b, e := readProjectBinding(tx, r.Origin.ID)
			if e != nil {
				return e
			}
			if b.Revision != r.Revision || b.Current != r.Current {
				return problem("stale_preview", "project registration changed")
			}
		}
		if repairDBRevision(tx, key) != expectedRevision {
			return problem("stale_preview", "registrations or consumers changed; take a fresh preview")
		}
		var selected projectBinding
		if request.ProjectID != "" {
			var err error
			selected, err = readProjectBinding(tx, request.ProjectID)
			if err != nil {
				return err
			}
		}
		cb, err := readCheckoutBinding(tx, request.CheckoutID)
		if err != nil {
			return err
		}
		if err := validateRepairOwners(tx, request.ProjectID, request.CheckoutID); err != nil {
			return err
		}
		for _, i := range preview.Instances {
			r, e := load(tx, i.ID)
			if e != nil {
				return e
			}
			if r.Instance.Status != i.Status || r.Instance.Association.Revision != i.Association.Revision {
				return problem("stale_preview", "affected instance changed")
			}
		}
		revision := newID()
		if request.ProjectID != "" && !sameSharedBinding(selected.Current, preview.Current) {
			selected.Current = preview.Current
			selected.Revision = revision
		}
		selected.Acknowledgement = request.Preview
		previousCommon := cb.Current.Registration.CommonPath
		cb.Current = preview.Checkout
		cb.PendingPath = ""
		cb.Revision = revision
		cb.Acknowledgement = request.Preview
		if cb.Shared == nil {
			cb.Shared = map[string]string{}
		}
		if request.ProjectID != "" {
			cb.Shared[request.ProjectID] = selected.Revision
			if err := put(tx, "project_bindings", request.ProjectID, selected); err != nil {
				return err
			}
		}
		if err := put(tx, "checkout_bindings", request.CheckoutID, cb); err != nil {
			return err
		}
		if preview.Checkout.Registration.CommonID != "" {
			if err := publishCommonRegistration(tx, request.CheckoutID, preview.Checkout.Registration, request.Preview, previousCommon); err != nil {
				return err
			}
		}
		if err := validateBindings(tx); err != nil {
			return err
		}
		if err := s.store.sharedPoint("reassociation-before-commit"); err != nil {
			return err
		}
		return ctx.Err()
	})
	if err != nil {
		return preview, err
	}
	if err := s.store.sharedPoint("reassociation-committed"); err != nil {
		return preview, problem("reassociation_committed_uncertain", "association committed but acknowledgement is uncertain; inspect accepted digest before a fresh preview")
	}
	preview.Applied = true
	return preview, nil
}
func repairDBRevision(tx *bolt.Tx, key []byte) string {
	h := hmac.New(sha256.New, key)
	for _, name := range []string{"project_bindings", "checkout_bindings", "persistent", "instances", "shared_services", "shared_allocations", "runtime", "resources", "attempt_sequences"} {
		_, _ = h.Write([]byte(name))
		_ = tx.Bucket([]byte(name)).ForEach(func(k, d []byte) error { raw, _ := json.Marshal([][]byte{k, d}); _, _ = h.Write(raw); return nil })
	}
	return hex.EncodeToString(h.Sum(nil))
}

// repairConsumerBlockers retains established repair policy; callers separately join owners.
func repairConsumerBlockers(tx *bolt.Tx, r record) ([]string, error) {
	i := r.Instance
	var blockers []string
	if i.Status == v1.Preparing || i.Status == v1.Starting || i.Status == v1.RuntimeReady || i.Status == v1.Stopping || i.Execution != nil && (i.Execution.Kept || i.Execution.CleanupFailure != "") {
		blockers = append(blockers, "affected consumer "+i.ID+" must be stopped/joined with verified cleanup")
	}
	var previousBootWarnings []v1.NativeOwnershipWarning
	for _, abandoned := range r.NativeAbandonments {
		if !abandoned.previousBoot() {
			blockers = append(blockers, "affected relinquished native consumer lacks independent earlier-boot absence proof: "+i.ID)
		} else {
			previousBootWarnings = append(previousBootWarnings, abandoned.warning())
		}
	}
	if i.Execution != nil {
		for _, cleanup := range i.Execution.NativeCleanup {
			// Only independently proven earlier-boot warnings may discharge an exact-attempt fence.
			if cleanup.Validate() != nil || cleanup.FencesResources(previousBootWarnings) {
				blockers = append(blockers, "affected native consumer cleanup remains uncertain: "+i.ID)
			}
		}
	}
	if raw := tx.Bucket([]byte("runtime")).Get([]byte(i.ID)); raw != nil {
		journal, err := decodeRuntime(raw)
		if err != nil {
			return nil, err
		}
		if journal.LaunchPending {
			blockers = append(blockers, "affected consumer has pending launch: "+i.ID)
		}
	}
	return blockers, nil
}

// validateRepairSelection shares selected retained-scene compatibility outside database transactions.
func (s *service) validateRepairSelection(source plan.Source, m v1.Manifest, result *v1.ReassociationResponse, freshness hashpkg.Hash) error {
	request := result.Request
	block := func(message string) { result.Blockers = append(result.Blockers, message) }
	opRoot, err := projectconfig.RootAt(source.CheckoutPath, request.RegistryRoot)
	if err != nil {
		block("operation private registry is unsafe or unavailable")
	}
	runtimeRoot, rootErr := projectconfig.Root(source.CheckoutPath)
	if rootErr != nil {
		block("runtime private registry is unsafe or unavailable")
	}
	if opRoot != "" {
		result.Request.RegistryRoot = opRoot
	}
	if _, e := os.Lstat(opRoot); e == nil {
		if e := localipc.Directory(opRoot, false); e != nil {
			block("private registry directory must be owned/private for publication locking")
		}
	}
	result.Private.OperationRoot = opRoot
	result.Private.RuntimeRoot = runtimeRoot
	if opRoot != runtimeRoot {
		block("operation and daemon runtime registry roots differ; align daemon selection and take a fresh preview")
	}
	selection, selectErr := projectconfig.SelectAt(source.ProjectPath, source.CheckoutPath, source.ManifestPath, request.RegistryRoot, false)
	if selectErr == nil {
		selection, selectErr = selection.Read(m, false)
	}
	result.Private.State = selection.State
	result.Private.Association = selection.Provenance.Association
	result.Private.GenerationPath = selection.Provenance.Path
	if selection.State == "rebound" || selection.State == "conflict" {
		result.Private.RecordedAssociation = selection.Provenance.Association
		result.Private.RecordedGenerationPath = selection.Provenance.Path
		result.Private.Association = ""
		result.Private.GenerationPath = ""
	}
	if selectErr != nil {
		block("private association unavailable; use project setup --project " + source.ProjectPath + " --reassociate --from FILE with explicit values, then take a fresh preview")
	}
	if raw, e := selection.Freshness(); e == nil {
		_, _ = freshness.Write(raw)
	} else {
		block("private source unavailable")
	}
	if selectErr == nil && opRoot == runtimeRoot {
		for _, i := range result.Instances {
			recorded, e := s.store.inspect(i.ID)
			if e != nil {
				return e
			}
			if i.Selected && (recorded.Plan.Project != m.Project || filepath.Clean(associatedProject(recorded)) != filepath.Join(recorded.Association.Checkout.Path, result.Current.Relative) || filepath.Base(recorded.Plan.ManifestPath) != filepath.Base(source.ManifestPath)) {
				block("selected checkout project/manifest association differs; unchanged relative locations required")
			}
			if !i.Selected || i.Status == v1.Destroyed || recorded.Task != nil {
				continue
			}
			snapshot, e := plan.Prepare(source, v1.PlanRequest{Scene: i.Scene, ProjectPath: source.ProjectPath, ConfigPath: request.ConfigPath})
			if e != nil {
				block("destination scene cannot compose safely: " + i.Scene)
				continue
			}
			old, e := s.store.snapshot(i.ID)
			if e != nil {
				return e
			}
			if old.PrivateProvenance == nil {
				block("recorded private provenance unavailable for " + i.Scene)
			} else {
				result.Private.RecordedAssociation = old.PrivateProvenance.Association
				result.Private.RecordedGenerationPath = old.PrivateProvenance.Path
			}
			if e = safeRestart(old, snapshot); e != nil {
				block("protected resource/input/initializer compatibility changed for " + i.Scene + "; resolved fresh-only executable or nondefault working-directory relocation is unsupported")
			}
			for _, service := range result.Services {
				record, e := s.store.sharedService(service.ID)
				if e != nil {
					return e
				}
				declaration, ok := snapshot.Plan.SharedServices[record.Name]
				if !ok {
					continue
				} // Unselected peer declarations belong to their recorded branch.
				if snapshot.Docker == nil || sharedDigest(declaration, snapshot.Docker.Endpoint) != record.Configuration {
					block("shared declaration/provider differs from retained authority: " + record.Name)
				}
			}
			raw, _ := json.Marshal(snapshot)
			_, _ = freshness.Write(raw)
			raw, _ = json.Marshal(snapshot.Secrets)
			_, _ = freshness.Write(raw)
		}
	}
	if request.ConfigPath != "" {
		raw, e := projectconfig.Read(request.ConfigPath)
		if e != nil {
			block("explicit machine source unavailable")
		} else {
			_, _ = freshness.Write(raw)
		}
	}

	result.Private.Revision = hex.EncodeToString(freshness.Sum(nil))
	return nil
}

// repairUnjoinedOwners snapshots service state before any database reader is held.
func (s *service) repairUnjoinedOwners() map[string]bool {
	unjoined := map[string]bool{}
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, owner := range s.executions {
		select {
		case <-owner.done:
		default:
			unjoined[id] = true
		}
	}
	for id, owner := range s.expiryWorkers {
		if owner != nil {
			unjoined[id] = true
		}
	}
	return unjoined
}
