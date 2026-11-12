package daemon

import (
	"encoding/json"
	"path/filepath"

	v1 "github.com/octacian/backlot/api/v1"
	"github.com/octacian/backlot/internal/plan"
	bolt "go.etcd.io/bbolt"
)

var bindingBuckets = []string{"project_bindings", "checkout_bindings"}

type projectBinding struct {
	Current         sharedProjectIdentity `json:"current"`
	Revision        string                `json:"revision"`
	Acknowledgement string                `json:"acknowledgement,omitempty"`
}
type checkoutBinding struct {
	PendingPath     string              `json:"pending_registration_path,omitempty"`
	Current         v1.CheckoutIdentity `json:"current"`
	Revision        string              `json:"revision"`
	Shared          map[string]string   `json:"shared,omitempty"`
	Acknowledgement string              `json:"acknowledgement,omitempty"`
}

func checkoutParent(i v1.Instance) string {
	if i.CheckoutParent != "" {
		return i.CheckoutParent
	}
	return i.Checkout.ID
}
func readCheckoutBinding(tx *bolt.Tx, id string) (b checkoutBinding, err error) {
	data := tx.Bucket([]byte("checkout_bindings")).Get([]byte(id))
	if data == nil || json.Unmarshal(data, &b) != nil || !validID(b.Current.ID) || !filepath.IsAbs(b.Current.Path) {
		return b, problem("state_corrupt", "invalid checkout binding; preserve state")
	}
	return b, nil
}
func readProjectBinding(tx *bolt.Tx, id string) (b projectBinding, err error) {
	data := tx.Bucket([]byte("project_bindings")).Get([]byte(id))
	if data == nil || json.Unmarshal(data, &b) != nil || !validID(b.Current.ID) || !filepath.IsAbs(b.Current.Anchor) {
		return b, problem("state_corrupt", "invalid project binding; preserve state")
	}
	return b, nil
}
func locateCheckout(tx *bolt.Tx, next v1.CheckoutIdentity) (string, error) {
	found := ""
	var commonConflict error
	err := tx.Bucket([]byte("checkout_bindings")).ForEach(func(key, data []byte) error {
		b, err := readCheckoutBinding(tx, string(key))
		if err != nil {
			return err
		}
		if b.PendingPath != "" {
			if next.Path == b.PendingPath || next.Path == b.Current.Path {
				return &v1.PlanError{Code: "project_registration", Message: "registration publication is staged; inspect and explicitly repair the exact retained checkout", Registration: &v1.RegistrationResult{State: "staged", Identity: next.Registration, Conflicts: []v1.RegistrationConflict{{Role: "checkout", Path: next.Path, ClaimedPath: b.Current.Path, CheckoutID: string(key)}}, NextAction: "project reassociate"}}
			}
			return nil
		}
		old, observed := b.Current.Registration, next.Registration
		if old.CommonID != "" && observed.CommonID == old.CommonID && observed.CommonPath != old.CommonPath {
			commonConflict = registrationConflict("common", next.Path, b.Current.Path, string(key), "", observed)
		}
		if old.CheckoutID != "" {
			if old.CheckoutID == observed.CheckoutID || b.Current.Path == next.Path {
				if old != observed || b.Current.Path != next.Path {
					return registrationConflict("checkout", next.Path, b.Current.Path, string(key), "", observed)
				}
				if found != "" {
					return problem("conflict", "ambiguous checkout registration")
				}
				found = string(key)
				return nil
			}
		}
		if b.Current.Path == next.Path || b.Current.ID == next.ID {
			if !sameCheckout(&b.Current, &next) {
				code := "checkout_moved"
				if b.Current.ID != next.ID {
					code = "checkout_replaced"
				}
				return problem(code, "checkout binding changed; diagnose project and preview explicit reassociation")
			}
			if found != "" {
				return problem("conflict", "ambiguous checkout bindings")
			}
			found = string(key)
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	if commonConflict != nil {
		return "", commonConflict
	}
	if found != "" {
		return found, nil
	}
	if tx.Bucket([]byte("checkouts")).Get([]byte(next.ID)) != nil || tx.Bucket([]byte("paths")).Get([]byte(next.Path)) != nil {
		return "", problem("checkout_moved", "retained checkout origin reserved; explicit reassociation required")
	}
	return next.ID, nil
}
func boundShared(tx *bolt.Tx, current sharedProjectIdentity) (sharedProjectIdentity, error) {
	var owner sharedProjectIdentity
	found := false
	err := tx.Bucket([]byte("project_bindings")).ForEach(func(key, data []byte) error {
		b, err := readProjectBinding(tx, string(key))
		if err != nil {
			return err
		}
		if old, next := b.Current.Registration, current.Registration; old.CommonID != "" {
			if next.CommonID == old.CommonID && next.CommonPath != old.CommonPath {
				return registrationConflict("common", current.Anchor, b.Current.Anchor, "", string(key), next)
			}
			if sharedPath(b.Current) == sharedPath(current) || next.CommonID == old.CommonID && b.Current.Relative == current.Relative && b.Current.Project == current.Project {
				if next.CommonID != old.CommonID || next.CommonPath != old.CommonPath || sharedPath(b.Current) != sharedPath(current) || next.Git != old.Git {
					return registrationConflict("common", current.Anchor, b.Current.Anchor, "", string(key), next)
				}
				if found {
					return problem("conflict", "ambiguous common registration")
				}
				found = true
				if json.Unmarshal(tx.Bucket([]byte("shared_projects")).Get(key), &owner) != nil {
					return problem("state_corrupt", "shared registration parent unavailable")
				}
				return nil
			}
		}
		if b.Current.ID == current.ID || sharedPath(b.Current) == sharedPath(current) {
			if !sameProjectPhysical(b.Current, current) {
				code := "project_moved"
				if b.Current.ID != current.ID {
					code = "project_replaced"
				}
				return problem(code, "shared-project binding changed; diagnose project and preview reassociation")
			}
			if found {
				return problem("conflict", "ambiguous shared-project bindings")
			}
			found = true
			if json.Unmarshal(tx.Bucket([]byte("shared_projects")).Get(key), &owner) != nil {
				return problem("state_corrupt", "shared binding parent unavailable")
			}
		}
		return nil
	})
	if err != nil {
		return owner, err
	}
	if found {
		return owner, nil
	}
	if tx.Bucket([]byte("shared_projects")).Get([]byte(current.ID)) != nil || tx.Bucket([]byte("shared_paths")).Get([]byte(sharedPath(current))) != nil {
		return owner, problem("project_moved", "retained shared-project origin reserved; explicit reassociation required")
	}
	return current, nil
}

func sameProjectPhysical(a, b sharedProjectIdentity) bool {
	a.Registration = v1.RegistrationIdentity{}
	b.Registration = v1.RegistrationIdentity{}
	return a == b
}

func sameSharedBinding(a, b sharedProjectIdentity) bool {
	if a.Registration.CommonID != "" && b.Registration.CommonID != "" {
		return a.Anchor == b.Anchor && a.Relative == b.Relative && a.Project == b.Project && a.Registration.CommonID == b.Registration.CommonID && a.Registration.CommonPath == b.Registration.CommonPath && a.Registration.Git == b.Registration.Git
	}
	return sameProjectPhysical(a, b)
}

func registrationConflict(role, path, claimed, checkout, project string, observed v1.RegistrationIdentity) error {
	state := "conflict"
	if observed.CommonID == "" || observed.CheckoutID == "" {
		state = "missing"
	}
	return &v1.PlanError{Code: "project_registration", Message: "registration differs from its selected-store claim; use project diagnose and explicit repair", Registration: &v1.RegistrationResult{State: state, Identity: observed, Conflicts: []v1.RegistrationConflict{{Role: role, Path: path, ClaimedPath: claimed, CheckoutID: checkout, ProjectID: project}}, NextAction: "project reassociate"}}
}
func sharedPath(p sharedProjectIdentity) string { return p.Anchor + "/" + p.Relative + "/" + p.Project }
func associateInstance(tx *bolt.Tx, i *v1.Instance) error {
	// Exact-ID observation/cleanup does not require valid named-selection metadata.
	if err := i.ValidateSceneLocation(); err != nil {
		return nil
	}
	if tx.Bucket([]byte("checkout_bindings")) == nil {
		return nil
	}
	b, err := readCheckoutBinding(tx, checkoutParent(*i))
	if err != nil {
		return err
	}
	if b.Revision == "" {
		var origin v1.CheckoutIdentity
		// Exact legacy enrollment adds markers to the binding, preserving the origin.
		if json.Unmarshal(tx.Bucket([]byte("checkouts")).Get([]byte(checkoutParent(*i))), &origin) != nil || !sameCheckoutPhysical(origin, b.Current) || !sameCheckout(&b.Current, &i.Checkout) {
			return problem("state_corrupt", "recorded checkout and stable parent disagree")
		}
	}
	relative, err := filepath.Rel(i.Checkout.Path, filepath.Dir(i.Plan.ManifestPath))
	if err != nil || !filepath.IsLocal(relative) {
		return problem("state_corrupt", "invalid recorded project location")
	}
	i.Association = &v1.ProjectAssociation{CheckoutParent: checkoutParent(*i), Checkout: b.Current, ProjectPath: filepath.Join(b.Current.Path, relative), Revision: b.Revision}
	return nil
}
func associatedCheckout(i v1.Instance) v1.CheckoutIdentity {
	if i.Association != nil {
		return i.Association.Checkout
	}
	return i.Checkout
}
func associatedProject(i v1.Instance) string {
	if i.Association != nil {
		return i.Association.ProjectPath
	}
	return filepath.Dir(i.Plan.ManifestPath)
}
func checkSharedBinding(tx *bolt.Tx, checkout v1.CheckoutIdentity, current sharedProjectIdentity) (sharedProjectIdentity, error) {
	owner, err := boundShared(tx, current)
	if err != nil {
		return owner, err
	}
	b, err := readProjectBinding(tx, owner.ID)
	if err != nil {
		return owner, err
	}
	parent, err := locateCheckout(tx, checkout)
	if err != nil {
		return owner, err
	}
	cb, err := readCheckoutBinding(tx, parent)
	if err != nil {
		return owner, err
	}
	if cb.Shared[owner.ID] != b.Revision {
		return owner, problem("project_unverified", "checkout has not verified current shared association; preview explicit reassociation")
	}
	return owner, nil
}
func migrateStateTwelve(tx *bolt.Tx) error {
	if err := validateOriginCheckouts(tx); err != nil {
		return err
	}
	if err := validateStateAuthority(tx); err != nil {
		return err
	}
	for _, name := range bindingBuckets {
		if _, err := tx.CreateBucket([]byte(name)); err != nil {
			return problem("state_corrupt", "binding namespace collision")
		}
	}
	if err := tx.Bucket([]byte("shared_projects")).ForEach(func(k, d []byte) error {
		var p sharedProjectIdentity
		if json.Unmarshal(d, &p) != nil {
			return problem("state_corrupt", "invalid shared origin")
		}
		return put(tx, "project_bindings", string(k), projectBinding{Current: p})
	}); err != nil {
		return err
	}
	return tx.Bucket([]byte("checkouts")).ForEach(func(k, d []byte) error {
		var c v1.CheckoutIdentity
		if json.Unmarshal(d, &c) != nil {
			return problem("state_corrupt", "invalid checkout origin")
		}
		return put(tx, "checkout_bindings", string(k), checkoutBinding{Current: c})
	})
}
func validateBindings(tx *bolt.Tx) error {
	if err := validateOriginCheckouts(tx); err != nil {
		return err
	}
	for _, name := range bindingBuckets {
		if tx.Bucket([]byte(name)) == nil {
			return problem("state_corrupt", "binding namespace missing")
		}
	}
	for _, pair := range [][2]string{{"shared_projects", "project_bindings"}, {"checkouts", "checkout_bindings"}} {
		if err := tx.Bucket([]byte(pair[0])).ForEach(func(k, d []byte) error {
			if tx.Bucket([]byte(pair[1])).Get(k) == nil {
				return problem("state_corrupt", "binding parent coverage incomplete")
			}
			return nil
		}); err != nil {
			return err
		}
	}
	claims := map[string]string{}
	commonClaims := map[string]string{}
	claimCommon := func(r v1.RegistrationIdentity) error {
		if r.CommonID == "" {
			return nil
		}
		if old, ok := commonClaims[r.CommonID]; ok && old != r.CommonPath {
			return problem("state_corrupt", "common registration claims different canonical anchors")
		}
		commonClaims[r.CommonID] = r.CommonPath
		return nil
	}
	if err := tx.Bucket([]byte("project_bindings")).ForEach(func(k, d []byte) error {
		b, err := readProjectBinding(tx, string(k))
		if err != nil {
			return err
		}
		var origin sharedProjectIdentity
		if err := claimCommon(b.Current.Registration); err != nil {
			return err
		}
		if json.Unmarshal(tx.Bucket([]byte("shared_projects")).Get(k), &origin) != nil || b.Current.Project != origin.Project || b.Current.Relative != origin.Relative || !filepath.IsLocal(b.Current.Relative) || b.Revision != "" && !validID(b.Revision) || b.Acknowledgement != "" && !validID(b.Acknowledgement) || b.Revision == "" && !sameProjectPhysical(b.Current, origin) || b.Current.Registration.Validate() != nil {
			return problem("state_corrupt", "invalid shared binding origin/revision")
		}
		if tx.Bucket([]byte("shared_projects")).Get(k) == nil || b.Current.ID != hash(b.Current.Filesystem+"/"+b.Current.Relative+"/"+b.Current.Project) {
			return problem("state_corrupt", "invalid shared binding authority")
		}
		if tx.Bucket([]byte("shared_projects")).Get([]byte(b.Current.ID)) != nil && b.Current.ID != string(k) || foreignOrigin(tx, "shared_paths", sharedPath(b.Current), string(k)) {
			return problem("state_corrupt", "shared binding claims another retained origin")
		}
		for _, claim := range []string{"id/" + b.Current.ID, "path/" + sharedPath(b.Current)} {
			if claims[claim] != "" {
				return problem("state_corrupt", "conflicting shared bindings")
			}
			claims[claim] = string(k)
		}
		return nil
	}); err != nil {
		return err
	}
	claims = map[string]string{}
	if err := tx.Bucket([]byte("checkout_bindings")).ForEach(func(k, d []byte) error {
		b, err := readCheckoutBinding(tx, string(k))
		if err != nil {
			return err
		}
		if tx.Bucket([]byte("checkouts")).Get(k) == nil {
			return problem("state_corrupt", "checkout binding origin missing")
		}
		var origin v1.CheckoutIdentity
		if b.PendingPath != "" {
			if !filepath.IsAbs(b.PendingPath) || foreignOrigin(tx, "paths", b.PendingPath, string(k)) {
				return problem("state_corrupt", "invalid staged registration location")
			}
		} else {
			if err := claimCommon(b.Current.Registration); err != nil {
				return err
			}
			if id := b.Current.Registration.CheckoutID; id != "" {
				claim := "registration/" + id
				if claims[claim] != "" {
					return problem("state_corrupt", "checkout registration has multiple owners")
				}
				claims[claim] = string(k)
			}
		}
		if b.Current.Registration.Validate() != nil || b.Current.Registration.CheckoutID != "" && b.Current.Registration.CheckoutPath != b.Current.Path {
			return problem("state_corrupt", "invalid checkout registration location")
		}
		if json.Unmarshal(tx.Bucket([]byte("checkouts")).Get(k), &origin) != nil || b.Revision != "" && !validID(b.Revision) || b.Acknowledgement != "" && !validID(b.Acknowledgement) || b.Revision == "" && !sameCheckoutPhysical(b.Current, origin) {
			return problem("state_corrupt", "invalid checkout binding revision")
		}
		if tx.Bucket([]byte("checkouts")).Get([]byte(b.Current.ID)) != nil && b.Current.ID != string(k) || foreignOrigin(tx, "paths", b.Current.Path, string(k)) {
			return problem("state_corrupt", "checkout binding claims another retained origin")
		}
		for _, claim := range []string{"id/" + b.Current.ID, "path/" + b.Current.Path} {
			if claims[claim] != "" {
				return problem("state_corrupt", "conflicting checkout bindings")
			}
			claims[claim] = string(k)
		}
		for id := range b.Shared {
			if _, err := readProjectBinding(tx, id); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return err
	}
	if _, err := storedSceneInstances(tx); err != nil {
		return err
	}
	return tx.Bucket([]byte("instances")).ForEach(func(k, d []byte) error {
		r, err := load(tx, string(k))
		if err != nil {
			return err
		}
		owners, err := retainedSharedProjects(tx, r.Instance)
		if err != nil {
			return err
		}
		if len(owners) > 1 {
			return problem("state_corrupt", "checkout and allocations claim independent shared projects")
		}
		return r.Instance.ValidateSceneLocation()
	})
}

func foreignOrigin(tx *bolt.Tx, bucket, path, owner string) bool {
	id := tx.Bucket([]byte(bucket)).Get([]byte(path))
	return id != nil && string(id) != owner
}

func validateOriginCheckouts(tx *bolt.Tx) error {
	checkouts := tx.Bucket([]byte("checkouts"))
	paths := tx.Bucket([]byte("paths"))
	if err := checkouts.ForEach(func(k, d []byte) error {
		var c v1.CheckoutIdentity
		if json.Unmarshal(d, &c) != nil || c.ID != string(k) || !validID(c.ID) || !filepath.IsAbs(c.Path) || string(paths.Get([]byte(c.Path))) != c.ID {
			return problem("state_corrupt", "invalid original checkout index")
		}
		return nil
	}); err != nil {
		return err
	}
	return paths.ForEach(func(k, d []byte) error {
		var c v1.CheckoutIdentity
		if json.Unmarshal(checkouts.Get(d), &c) != nil || c.Path != string(k) {
			return problem("state_corrupt", "original checkout path index disagrees")
		}
		return nil
	})
}

// retainedSharedProjects uses recorded registrations and allocation parents, never current files.
func retainedSharedProjects(tx *bolt.Tx, i v1.Instance) (map[string]bool, error) {
	owners := map[string]bool{}
	cb, err := readCheckoutBinding(tx, checkoutParent(i))
	if err != nil {
		return nil, err
	}
	relative, err := filepath.Rel(i.Checkout.Path, filepath.Dir(i.Plan.ManifestPath))
	if err != nil {
		return nil, err
	}
	allocations, err := decodeSharedAllocations(tx.Bucket([]byte("shared_allocations")).Get([]byte(i.ID)))
	if err != nil {
		return nil, err
	}
	var snapshot plan.Snapshot
	if raw := tx.Bucket([]byte("snapshots")).Get([]byte(i.ID)); raw != nil && json.Unmarshal(raw, &snapshot) != nil {
		return nil, problem("state_corrupt", "retained association provenance unavailable")
	}
	observed := ""
	if p := snapshot.PrivateProvenance; p != nil && p.Identity.Filesystem != "" && p.Identity.Manifest != "" && filepath.Dir(p.Identity.Manifest) == relative {
		observed = hash(p.Identity.Filesystem + "/" + relative + "/" + i.Plan.Project)
	}
	matched := map[string]bool{}
	err = tx.Bucket([]byte("shared_projects")).ForEach(func(k, d []byte) error {
		var p sharedProjectIdentity
		if json.Unmarshal(d, &p) != nil {
			return problem("state_corrupt", "original shared project unavailable")
		}
		if p.Project == i.Plan.Project && p.Relative == relative {
			_, verified := cb.Shared[p.ID]
			binding, err := readProjectBinding(tx, p.ID)
			if err != nil {
				return err
			}
			// Legacy snapshots retain the resolved common-directory identity even without private inputs.
			if verified || observed == p.ID || observed == binding.Current.ID {
				owners[p.ID] = true
			}
		}
		for name, allocation := range allocations {
			if allocation.ServiceID == hash(p.ID+"/"+i.Plan.Resources[name].SharedService) {
				owners[p.ID] = true
				matched[name] = true
			}
		}
		return nil
	})
	if err == nil && len(matched) != len(allocations) {
		err = problem("state_corrupt", "retained allocation owner cannot be established")
	}
	return owners, err
}

func validateRepairOwners(tx *bolt.Tx, project, checkout string) error {
	known := false
	err := tx.Bucket([]byte("instances")).ForEach(func(k, d []byte) error {
		var r record
		if err := decodeRecord(d, string(k), &r); err != nil {
			return err
		}
		if checkoutParent(r.Instance) != checkout {
			return nil
		}
		owners, err := retainedSharedProjects(tx, r.Instance)
		if err != nil {
			return err
		}
		if project == "" && (len(owners) > 0 || len(r.Instance.Plan.SharedServices) > 0) {
			return problem("reassociation_blocked", "checkout has retained shared obligations; select their exact owner")
		}
		for owner := range owners {
			if owner != project {
				return problem("reassociation_blocked", "selected checkout or retained allocation belongs to another shared project; independent owners cannot merge")
			}
			known = true
		}
		return nil
	})
	if err != nil {
		return err
	}
	if project != "" && !known {
		return problem("reassociation_blocked", "selected checkout association with retained shared project is unproven")
	}
	return nil
}
