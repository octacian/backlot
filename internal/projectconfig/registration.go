package projectconfig

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"

	v1 "github.com/octacian/backlot/api/v1"
	"github.com/octacian/backlot/internal/registration"
)

func registrationError(err error) error {
	return &v1.PlanError{Code: "project_registration", Message: err.Error(), Registration: &v1.RegistrationResult{State: "unavailable", Conflicts: []v1.RegistrationConflict{}, NextAction: "project setup --reassociate"}}
}

// Registration describes captured enrollment without reading or publishing files.
func (s Selection) Registration() v1.RegistrationResult {
	r := v1.RegistrationResult{State: "legacy", Conflicts: []v1.RegistrationConflict{}}
	if s.markers.CommonID != "" || s.markers.CheckoutID != "" {
		r.State = "enrolled"
		if s.markers.CommonID == "" || s.markers.CheckoutID == "" {
			r.State = "missing"
		}
		r.Identity = v1.RegistrationIdentity{CommonID: s.markers.CommonID, CheckoutID: s.markers.CheckoutID, CheckoutPath: s.location.Checkout, AdminPath: s.location.Admin, CommonPath: s.location.Common, Git: s.location.Git}
	}
	return r
}

// EnrollSource publishes markers only after checking this registry's existing claims.
// Publication releases marker locks before resolution; admission fences rotation again.
// It never writes the private registry; only explicit setup may enroll that store.
func EnrollSource(ctx context.Context, project, checkout, file string) error {
	l, err := registration.Locate(project, checkout)
	if err != nil {
		return err
	}
	markers, err := registration.Read(l)
	if err != nil {
		return registrationError(err)
	}
	if markers.CommonID != "" && markers.CheckoutID != "" {
		_, err := Select(project, checkout, file, false)
		return err
	}
	unlock, err := registration.Lock(ctx, l)
	if err != nil {
		return registrationError(err)
	}
	defer unlock()
	if _, err := Select(project, checkout, file, false); err != nil {
		return err
	}
	if _, err := registration.Enroll(l); err != nil {
		return registrationError(err)
	}
	return nil
}

// Enroll binds a captured setup to complete markers before taking a registry lock.
// The returned selection keeps historical provenance and current observations separate.
func (s Selection) Enroll(ctx context.Context) (Selection, func(), error) {
	unlock, err := registration.Lock(ctx, s.location)
	if err != nil {
		return s, nil, registrationError(err)
	}
	fail := func(err error) (Selection, func(), error) { unlock(); return s, nil, err }
	next, err := SelectAt(s.project, s.checkout, s.manifestPath, s.root, s.reassociate)
	if err != nil {
		return fail(err)
	}
	// Read may have populated private provenance/digest; selection alone must not reread values.
	if s.registryDigest != next.registryDigest || s.observed != next.observed || s.location != next.location || !next.markers.Extends(s.markers) {
		return fail(registrationError(fmt.Errorf("sources changed before registration; retry setup or preparation")))
	}
	s.markers, err = registration.Enroll(s.location)
	if err != nil {
		return fail(registrationError(err))
	}
	return s, unlock, nil
}

// EnrollPrivate explicitly updates only the registry's registration metadata.
// The immutable private generation and historical source identity are preserved.
func (s *Selection) EnrollPrivate(ctx context.Context) (Provenance, error) {
	if !s.Provenance.Present {
		return s.Provenance, problem("metadata enrollment requires an exact existing private association")
	}
	enrolled, unlock, err := s.Enroll(ctx)
	if err != nil {
		return s.Provenance, err
	}
	defer unlock()
	*s = enrolled
	unlockStore, err := Lock(ctx, s.root)
	if err != nil {
		return s.Provenance, err
	}
	defer unlockStore()
	r, revision, err := readRegistry(s.root)
	if err != nil {
		return s.Provenance, err
	}
	if revision != s.registryDigest {
		return s.Provenance, problem("registry changed before enrollment; inspect before retrying")
	}
	current, err := Identify(s.project, s.checkout, s.manifestPath)
	if err != nil || current != s.observed {
		return s.Provenance, problem("source changed before enrollment")
	}
	private, err := Read(s.Provenance.Path)
	if err != nil || digest(private) != s.Provenance.Digest {
		return s.Provenance, problem("private generation changed before enrollment")
	}
	e := r.Projects[s.Provenance.Association]
	if e.CheckoutPath == "" {
		e.CheckoutPath = s.location.Checkout
	}
	if e.AdminPath == "" {
		e.AdminPath = s.location.Admin
	}
	e.CommonID = s.markers.CommonID
	r.Projects[s.Provenance.Association] = e
	r.Version = 2
	r.Revision, err = randomID()
	if err != nil {
		return s.Provenance, err
	}
	b, err := json.Marshal(r)
	if err != nil {
		return s.Provenance, err
	}
	temp := filepath.Join(s.root, ".projects-"+r.Revision+".tmp")
	defer func() { _ = os.Remove(temp) }()
	if err := writeExclusive(temp, b); err != nil {
		return s.Provenance, err
	}
	if err := os.Rename(temp, filepath.Join(s.root, "projects.json")); err != nil {
		return s.Provenance, err
	}
	s.Provenance.CommonID = s.markers.CommonID
	if err := syncDir(s.root); err != nil {
		return s.Provenance, &v1.PlanError{Code: "setup_committed_uncertain", Message: "registration committed but directory sync failed; inspect before retrying"}
	}
	return s.Provenance, nil
}

// RegistrationClaim exposes only the location claims needed for explicit marker repair.
type RegistrationClaim struct {
	// CommonID is local continuity, not a private capability.
	CommonID string
	// Directory is the registered canonical common anchor or non-Git project.
	Directory string
	// CheckoutPath is the enrolled checkout location, absent in legacy entries.
	CheckoutPath string
}

// RegistrationClaims reads registry claims without opening any private generation.
func RegistrationClaims(checkout, root string) ([]RegistrationClaim, string, error) {
	if _, err := rootPathAt(checkout, root); err != nil {
		return nil, "", err
	}
	r, revision, err := readRegistry(root)
	if err != nil {
		return nil, "", err
	}
	claims := []RegistrationClaim{}
	keys := make([]string, 0, len(r.Projects))
	for key := range r.Projects {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	for _, key := range keys {
		e := r.Projects[key]
		if e.CommonID != "" {
			claims = append(claims, RegistrationClaim{e.CommonID, e.Identity.Directory, e.CheckoutPath})
		}
	}
	return claims, revision, nil
}
