package daemon

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/json"
	"path/filepath"
	"slices"

	v1 "github.com/octacian/backlot/api/v1"
	"github.com/octacian/backlot/internal/manifest"
	"github.com/octacian/backlot/internal/native"
	"github.com/octacian/backlot/internal/plan"
	"github.com/octacian/backlot/internal/projectconfig"
	"github.com/octacian/backlot/internal/registration"
	bolt "go.etcd.io/bbolt"
)

type registrationRotation struct {
	result           v1.ReassociationResponse
	source           capturedSource
	owners           []string
	selected         string
	common, checkout bool
}

func (s *service) rotationPreview(request v1.ReassociationRequest) (p registrationRotation, err error) {
	r := &p.result
	*r = v1.ReassociationResponse{APIVersion: v1.Version, DaemonGeneration: s.generation, Request: request, Projects: []v1.ProjectRegistration{}, Instances: []v1.ReassociationInstance{}, Services: []v1.SharedStatus{}, Allocations: []v1.ReassociationAllocation{}, Changes: []string{}, Preserved: []string{"retained owners, allocations, credentials, data and recorded cleanup authority"}, Blockers: []string{}}
	r.Filesystems = []v1.FilesystemObservation{}
	r.Request.Apply = false
	r.Request.Preview = ""
	if err = request.Validate(); err != nil {
		return p, err
	}
	requesting, err := plan.Locate(request.ProjectPath)
	if err != nil {
		return p, err
	}
	requested, err := captureSource(requesting)
	if err != nil {
		return p, err
	}
	chosen, err := sceneDirectory(request.FreshIdentityAt)
	if err != nil {
		return p, err
	}
	relative, err := filepath.Rel(requesting.CheckoutPath, requesting.ProjectPath)
	if err != nil || !filepath.IsLocal(relative) {
		return p, problem("invalid_request", "project escapes checkout")
	}
	selected, err := plan.Locate(filepath.Join(chosen, relative))
	if err != nil {
		return p, err
	}
	if selected.CheckoutPath != chosen {
		return p, problem("invalid_request", "fresh identity path must name the exact canonical checkout")
	}
	p.source, err = captureSource(selected)
	if err != nil {
		return p, err
	}
	r.Checkout = p.source.checkout
	data, err := projectconfig.Read(selected.ManifestPath)
	if err != nil {
		return p, err
	}
	var m v1.Manifest
	if err := manifest.Decode(data, filepath.Ext(selected.ManifestPath), &m); err != nil {
		return p, err
	}
	if err := manifest.Validate(m); err != nil {
		return p, err
	}
	r.Current, err = identifyShared(selected, m.Project)
	if err != nil {
		return p, err
	}
	r.Request.ProjectPath = requesting.ProjectPath
	r.Request.FreshIdentityAt = chosen
	r.Registration = &v1.RegistrationResult{State: "enrolled", Identity: r.Checkout.Registration, Conflicts: []v1.RegistrationConflict{}, NextAction: "project setup --reassociate"}
	observed := r.Checkout.Registration
	if requesting.CheckoutPath != chosen && requested.registrationLocation.Admin == p.source.registrationLocation.Admin {
		r.Blockers = append(r.Blockers, "different checkout paths share one administrative directory; repair Git links before rotating either role")
	}
	claims, registryRevision, err := projectconfig.RegistrationClaims(requesting.CheckoutPath, request.RegistryRoot)
	if err != nil {
		return p, err
	}
	privateOriginal := false
	for _, claim := range claims {
		selectedAnchor := r.Current.Anchor
		if claim.CommonID != requested.checkout.Registration.CommonID {
			continue
		}
		if claim.Directory == selectedAnchor && chosen != requesting.CheckoutPath {
			privateOriginal = true
			p.common = p.common || observed.CommonID == requested.checkout.Registration.CommonID && requested.registrationLocation.Common != p.source.registrationLocation.Common
			p.checkout = observed.CheckoutID == requested.checkout.Registration.CheckoutID
		}
		if claim.CommonID == observed.CommonID && claim.Directory != selectedAnchor {
			if claim.CheckoutPath == "" {
				r.Blockers = append(r.Blockers, "legacy private claim has no exact checkout path; explicitly repair original setup first")
				continue
			}
			old, err := plan.Locate(filepath.Join(claim.CheckoutPath, relative))
			if err != nil {
				r.Blockers = append(r.Blockers, "original private registration is unavailable; restore or explicitly resolve its exact owner")
				continue
			}
			original, err := captureSource(old)
			if err != nil {
				return p, err
			}
			p.common = true
			p.checkout = p.checkout || original.checkout.Registration.CheckoutID == observed.CheckoutID
			r.Registration.Conflicts = append(r.Registration.Conflicts, v1.RegistrationConflict{Role: "common", Path: chosen, ClaimedPath: claim.CheckoutPath})
		}
	}
	if observed.Validate() != nil || observed.CommonID == "" {
		r.Blockers = append(r.Blockers, "complete existing markers are required; use exact checkout repair for missing or legacy metadata")
	}
	unjoined := s.repairUnjoinedOwners()
	key := s.repairKeyBytes()
	boot, _ := native.CurrentBoot()
	err = s.store.db.View(func(tx *bolt.Tx) error {
		if err := tx.Bucket([]byte("checkout_bindings")).ForEach(func(k, _ []byte) error {
			b, err := readCheckoutBinding(tx, string(k))
			if err != nil {
				return err
			}
			old := b.Current.Registration
			if b.Current.Path == chosen || b.PendingPath == chosen {
				p.selected = string(k)
				if request.CheckoutID != string(k) {
					r.Blockers = append(r.Blockers, "select the exact retained checkout ID at the chosen original path")
				}
			}
			if old.CheckoutID != "" && old.CheckoutID == observed.CheckoutID && b.Current.Path != chosen {
				if old.AdminPath == observed.AdminPath {
					r.Blockers = append(r.Blockers, "different checkout paths share one administrative directory; repair Git links first")
				}
				p.checkout = true
				r.Registration.Conflicts = append(r.Registration.Conflicts, v1.RegistrationConflict{Role: "checkout", Path: chosen, ClaimedPath: b.Current.Path, CheckoutID: string(k)})
			}
			if old.CommonID != "" && old.CommonID == observed.CommonID && old.CommonPath != observed.CommonPath {
				p.common = true
				r.Registration.Conflicts = append(r.Registration.Conflicts, v1.RegistrationConflict{Role: "common", Path: chosen, ClaimedPath: b.Current.Path, CheckoutID: string(k)})
			}
			return nil
		}); err != nil {
			return err
		}
		if p.selected != "" {
			b, err := readCheckoutBinding(tx, p.selected)
			if err != nil {
				return err
			}
			if b.Current.Registration.CommonID == "" {
				r.Blockers = append(r.Blockers, "legacy original must be explicitly enrolled by exact preparation or checkout repair before marker rotation")
			}
			p.checkout = b.Current.Registration.CheckoutID == observed.CheckoutID
			// A copied anchor rotates the chosen original's common role too; genuine linked metadata stays shared.
			p.common = p.common || b.Current.Registration.CommonID == observed.CommonID && requested.checkout.Registration.CommonID == observed.CommonID && requested.registrationLocation.Common != p.source.registrationLocation.Common
			if p.common || b.Current.Registration.CommonID != observed.CommonID {
				if err := tx.Bucket([]byte("checkout_bindings")).ForEach(func(k, _ []byte) error {
					other, err := readCheckoutBinding(tx, string(k))
					if err == nil && other.Current.Registration.CommonPath == observed.CommonPath {
						p.owners = append(p.owners, string(k))
					}
					return err
				}); err != nil {
					return err
				}
			} else {
				p.owners = []string{p.selected}
			}
		} else if request.CheckoutID != "" {
			r.Blockers = append(r.Blockers, "selected checkout owner is not at the chosen path")
		}
		if chosen != requesting.CheckoutPath && p.selected == "" && !privateOriginal {
			r.Blockers = append(r.Blockers, "chosen original is unavailable or unregistered; resolve exact ownership explicitly")
		}
		if p.selected == "" && !p.checkout && !p.common {
			r.Blockers = append(r.Blockers, "no selected-store duplicate claims this checkout; inspect private setup separately")
		}
		if err := s.store.sharedPoint("registration-consumers-preview"); err != nil {
			return err
		}
		for _, owner := range p.owners {
			if err := tx.Bucket([]byte("instances")).ForEach(func(k, _ []byte) error {
				i, err := load(tx, string(k))
				if err != nil {
					return err
				}
				if checkoutParent(i.Instance) != owner {
					return nil
				}
				if unjoined[i.Instance.ID] {
					r.Blockers = append(r.Blockers, "affected consumer must be joined: "+i.Instance.ID)
				}
				blockers, err := repairConsumerBlockers(tx, i, boot)
				if err != nil {
					return err
				}
				r.Blockers = append(r.Blockers, blockers...)
				r.Instances = append(r.Instances, v1.ReassociationInstance{RecordedFilesystemState: "unrecorded", ID: i.Instance.ID, Scene: i.Instance.Plan.Scene, Status: i.Instance.Status, Recorded: i.Instance.Checkout, Association: *i.Instance.Association, Selected: owner == p.selected})
				return nil
			}); err != nil {
				return err
			}
		}
		serviceOwners := map[string]bool{}
		affected := map[string]bool{}
		for _, i := range r.Instances {
			affected[i.ID] = true
			record, err := load(tx, i.ID)
			if err != nil {
				return err
			}
			owners, err := retainedSharedProjects(tx, record.Instance)
			if err != nil {
				return err
			}
			for owner := range owners {
				serviceOwners[owner] = true
			}
		}
		if err := tx.Bucket([]byte("project_bindings")).ForEach(func(k, _ []byte) error {
			b, err := readProjectBinding(tx, string(k))
			if err != nil {
				return err
			}
			if !serviceOwners[string(k)] {
				return nil
			}
			var origin v1.ProjectIdentity
			if json.Unmarshal(tx.Bucket([]byte("shared_projects")).Get(k), &origin) != nil {
				return problem("state_corrupt", "retained common owner unavailable")
			}
			r.Projects = append(r.Projects, v1.ProjectRegistration{Origin: origin, Current: b.Current, Revision: b.Revision, Acknowledgement: b.Acknowledgement})
			return nil
		}); err != nil {
			return err
		}
		if err := tx.Bucket([]byte("shared_services")).ForEach(func(k, raw []byte) error {
			service, err := decodeSharedService(raw, string(k))
			if err != nil {
				return err
			}
			if serviceOwners[service.Project.ID] {
				r.Services = append(r.Services, v1.SharedStatus{ID: service.ID, ProjectID: service.Project.ID, Name: service.Name, Configuration: service.Configuration, Status: service.Status, LastTransition: service.LastTransition})
			}
			return nil
		}); err != nil {
			return err
		}
		if err := tx.Bucket([]byte("shared_allocations")).ForEach(func(k, raw []byte) error {
			if !affected[string(k)] {
				return nil
			}
			allocations, err := decodeSharedAllocations(raw)
			if err != nil {
				return err
			}
			names := []string{}
			for name := range allocations {
				names = append(names, name)
			}
			slices.Sort(names)
			for _, name := range names {
				a := allocations[name]
				r.Allocations = append(r.Allocations, v1.ReassociationAllocation{AllocationID: a.Request.AllocationID, InstanceID: string(k), Resource: name, ServiceID: a.ServiceID, Released: a.Released})
			}
			return nil
		}); err != nil {
			return err
		}
		r.StateRevision = repairDBRevision(tx, key)
		return nil
	})
	if err != nil {
		return p, err
	}
	opRoot, err := projectconfig.RootAt(selected.CheckoutPath, request.RegistryRoot)
	if err != nil {
		return p, err
	}
	runtimeRoot, err := projectconfig.Root(selected.CheckoutPath)
	if err != nil {
		return p, err
	}
	if opRoot != runtimeRoot {
		r.Blockers = append(r.Blockers, "request and daemon private registry roots differ; align setup selection before rotation")
	}
	r.Private = v1.ReassociationPrivate{OperationRoot: opRoot, RuntimeRoot: runtimeRoot, State: "separate_setup", Revision: hash(opRoot + registryRevision)}
	for _, instance := range r.Instances {
		if instance.Selected && instance.Status != v1.Destroyed {
			freshness := hmac.New(sha256.New, key)
			_, _ = freshness.Write([]byte(registryRevision))
			if err := s.validateRepairSelection(selected, m, r, freshness); err != nil {
				return p, err
			}
			break
		}
	}
	r.Changes = append(r.Changes, "selected path receives fresh checkout registration; copied common metadata receives fresh common registration; private setup remains a separate stage")
	bytes, err := json.Marshal(r)
	if err != nil {
		return p, err
	}
	r.Preview = hash(string(bytes) + string(data))
	return p, nil
}

func (s *service) rotateRegistration(ctx context.Context, request v1.ReassociationRequest) (v1.ReassociationResponse, error) {
	p, err := s.rotationPreview(request)
	if err != nil || !request.Apply {
		return p.result, err
	}
	if p.result.Preview != request.Preview {
		return p.result, problem("stale_preview", "registration preview changed; inspect a fresh preview")
	}
	if len(p.result.Blockers) > 0 {
		return p.result, problem("reassociation_blocked", "registration prerequisites failed; inspect preview")
	}
	unlockMarkers, err := registration.Lock(ctx, p.source.registrationLocation)
	if err != nil {
		return p.result, err
	}
	defer unlockMarkers()
	ctx, finish, err := s.beginMutation(ctx)
	if err != nil {
		return p.result, err
	}
	defer finish()
	ids := []string{}
	for _, i := range p.result.Instances {
		ids = append(ids, i.ID)
	}
	slices.Sort(ids)
	for _, id := range ids {
		lock := s.mutationLock(id)
		if !lock.TryLock() {
			return p.result, problem("conflict", "affected mutation is active")
		}
		defer lock.Unlock()
	}
	serviceIDs := []string{}
	for _, service := range p.result.Services {
		serviceIDs = append(serviceIDs, service.ID)
	}
	unlockServices, err := s.store.lockShared(ctx, serviceIDs...)
	if err != nil {
		return p.result, err
	}
	defer unlockServices()
	unlockRegistry, err := projectconfig.Lock(ctx, p.result.Private.OperationRoot)
	if err != nil {
		return p.result, err
	}
	defer unlockRegistry()
	if !s.store.bindingGate.TryLock() {
		return p.result, problem("conflict", "association admission is busy")
	}
	defer s.store.bindingGate.Unlock()
	if err := s.checkCleanupBindings(p.owners); err != nil {
		return p.result, err
	}

	fresh, err := s.rotationPreview(request)
	if err != nil {
		return p.result, err
	}
	if fresh.result.Preview != request.Preview || len(fresh.result.Blockers) > 0 {
		return fresh.result, problem("stale_preview", "registration claims changed; inspect fresh preview")
	}
	if err := p.source.verify(); err != nil {
		return p.result, err
	}
	if err := s.store.db.Update(func(tx *bolt.Tx) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if p.selected == "" {
			id := p.source.checkout.ID
			if tx.Bucket([]byte("checkouts")).Get([]byte(id)) != nil || tx.Bucket([]byte("paths")).Get([]byte(p.source.checkout.Path)) != nil {
				return problem("conflict", "fresh checkout origin is reserved")
			}
			if err := put(tx, "checkouts", id, p.source.checkout); err != nil {
				return err
			}
			if err := tx.Bucket([]byte("paths")).Put([]byte(p.source.checkout.Path), []byte(id)); err != nil {
				return err
			}
			if err := put(tx, "checkout_bindings", id, checkoutBinding{Current: p.source.checkout, PendingPath: p.source.checkout.Path}); err != nil {
				return err
			}
			p.selected = id
			p.owners = []string{id}
		} else {
			for _, id := range p.owners {
				b, err := readCheckoutBinding(tx, id)
				if err != nil {
					return err
				}
				b.PendingPath = b.Current.Path
				if err := put(tx, "checkout_bindings", id, b); err != nil {
					return err
				}
			}
		}
		return nil
	}); err != nil {
		return p.result, err
	}
	if err := s.store.sharedPoint("registration-staged"); err != nil {
		return p.result, err
	}
	expected := p.source.checkout.Registration
	if p.common {
		if _, err := registration.Rotate(p.source.registrationLocation, "common", expected.CommonID); err != nil {
			return p.result, err
		}
	}
	if p.checkout {
		if _, err := registration.Rotate(p.source.registrationLocation, "checkout", expected.CheckoutID); err != nil {
			return p.result, err
		}
	}
	if err := s.store.sharedPoint("registration-markers-published"); err != nil {
		return p.result, err
	}
	current, err := captureSource(p.source.source)
	if err != nil {
		return p.result, err
	}
	if current.projectIdentity != p.source.projectIdentity || current.checkout.ID != p.source.checkout.ID || current.commonIdentity != p.source.commonIdentity || current.registrationLocation != p.source.registrationLocation {
		return p.result, problem("checkout_changed", "source changed during marker rotation; inspect staged registration")
	}
	newIdentity := current.checkout.Registration
	err = s.store.db.Update(func(tx *bolt.Tx) error {
		if err := publishCommonRegistration(tx, p.selected, newIdentity, request.Preview, p.source.checkout.Registration.CommonPath); err != nil {
			return err
		}
		return validateBindings(tx)
	})
	if err != nil {
		return p.result, err
	}
	p.result.Checkout = current.checkout
	p.result.Registration.Identity = newIdentity
	p.result.Applied = true
	if err := s.store.sharedPoint("registration-committed"); err != nil {
		return p.result, problem("reassociation_committed_uncertain", "registration committed but acknowledgement is uncertain; inspect retained owners before retrying")
	}
	return p.result, nil
}

// publishCommonRegistration changes only reviewed same-anchor mappings, never resource parents.
func publishCommonRegistration(tx *bolt.Tx, selected string, next v1.RegistrationIdentity, ack, previousCommon string) error {
	revisions := map[string]string{}
	keys := []string{}
	if err := tx.Bucket([]byte("project_bindings")).ForEach(func(k, _ []byte) error { keys = append(keys, string(k)); return nil }); err != nil {
		return err
	}
	for _, id := range keys {
		b, err := readProjectBinding(tx, id)
		if err != nil {
			return err
		}
		if b.Current.Registration.CommonPath != next.CommonPath && (previousCommon == "" || b.Current.Registration.CommonPath != previousCommon) {
			continue
		}
		if b.Current.Registration.CommonID != next.CommonID || b.Current.Registration.CommonPath != next.CommonPath {
			b.Current.Registration.CommonID = next.CommonID
			b.Current.Registration.CommonPath = next.CommonPath
			b.Revision = newID()
			if b.Current.Registration.CheckoutPath == next.CheckoutPath {
				b.Current.Registration = next
			}
			if err := put(tx, "project_bindings", id, b); err != nil {
				return err
			}
		}
		revisions[id] = b.Revision
	}
	keys = nil
	if err := tx.Bucket([]byte("checkout_bindings")).ForEach(func(k, _ []byte) error { keys = append(keys, string(k)); return nil }); err != nil {
		return err
	}
	for _, id := range keys {
		b, err := readCheckoutBinding(tx, id)
		if err != nil {
			return err
		}
		if id != selected && ((b.Current.Registration.CommonPath != next.CommonPath && (previousCommon == "" || b.Current.Registration.CommonPath != previousCommon)) || b.Current.Registration.CommonID == next.CommonID && b.Current.Registration.CommonPath == next.CommonPath) {
			continue
		}
		if b.PendingPath != "" && b.PendingPath != b.Current.Path && id != selected {
			return problem("reassociation_blocked", "another exact checkout repair is staged; inspect it separately")
		}
		if id == selected {
			b.Current.Registration = next
		} else {
			b.Current.Registration.CommonID = next.CommonID
			b.Current.Registration.CommonPath = next.CommonPath
		}
		b.PendingPath = ""
		b.Revision = newID()
		b.Acknowledgement = ack
		for owner, revision := range revisions {
			if _, ok := b.Shared[owner]; ok {
				b.Shared[owner] = revision
			}
		}
		if err := put(tx, "checkout_bindings", id, b); err != nil {
			return err
		}
	}
	return nil
}
