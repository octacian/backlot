package v1

import "path/filepath"

// RegistrationIdentity is local administrative continuity, never resource authority.
type RegistrationIdentity struct {
	// CommonID identifies the shared administrative anchor.
	CommonID string `json:"common_id"`
	// CheckoutID identifies this checkout's administrative registration.
	CheckoutID string `json:"checkout_id"`
	// CheckoutPath is the registered canonical checkout location.
	CheckoutPath string `json:"checkout_path"`
	// AdminPath is its canonical administrative directory.
	AdminPath string `json:"admin_path"`
	// CommonPath is the canonical common marker directory.
	CommonPath string `json:"common_path"`
	// Git distinguishes Git administrative metadata from local non-Git metadata.
	Git bool `json:"git"`
}

// Validate checks a complete enrolled identity; zero remains legacy/unrecorded.
func (r RegistrationIdentity) Validate() error {
	if r == (RegistrationIdentity{}) {
		return nil
	}
	if !buildImageHex(r.CommonID) || !buildImageHex(r.CheckoutID) || r.CommonID == r.CheckoutID || !filepath.IsAbs(r.CheckoutPath) || !filepath.IsAbs(r.AdminPath) || !filepath.IsAbs(r.CommonPath) {
		return &PlanError{Code: "invalid_response", Message: "invalid project registration"}
	}
	return nil
}

// RegistrationConflict describes one selected-store claim without capabilities.
type RegistrationConflict struct {
	// Role identifies the conflicting common or checkout marker.
	Role string `json:"role"`
	// Path identifies the requesting registered location.
	Path string `json:"path"`
	// ClaimedPath identifies the existing claim's canonical location.
	ClaimedPath string `json:"claimed_path"`
	// CheckoutID identifies an actual retained checkout owner, when present.
	CheckoutID string `json:"checkout_id,omitempty"`
	// ProjectID identifies an actual retained shared owner, when present.
	ProjectID string `json:"project_id,omitempty"`
}

// RegistrationResult explains enrollment or repair without private inputs.
type RegistrationResult struct {
	// State is legacy, enrolled, missing, conflict, staged or unavailable.
	State string `json:"state"`
	// Identity is the observed local registration.
	Identity RegistrationIdentity `json:"identity"`
	// Conflicts lists claims in this selected store only.
	Conflicts []RegistrationConflict `json:"conflicts"`
	// NextAction names the explicit setup or preview command needed next.
	NextAction string `json:"next_action,omitempty"`
}

// Validate checks safe bounded diagnostics, including incomplete enrollment observations.
func (r RegistrationResult) Validate() error {
	bad := func() error { return &PlanError{Code: "invalid_response", Message: "invalid registration diagnostic"} }
	switch r.State {
	case "legacy", "enrolled", "missing", "conflict", "staged", "unavailable":
	default:
		return bad()
	}
	for _, id := range []string{r.Identity.CommonID, r.Identity.CheckoutID} {
		if id != "" && !buildImageHex(id) {
			return bad()
		}
	}
	for _, path := range []string{r.Identity.CheckoutPath, r.Identity.AdminPath, r.Identity.CommonPath} {
		if path != "" && !scenePath(path) {
			return bad()
		}
	}
	if len(r.Conflicts) > 100 {
		return bad()
	}
	for _, c := range r.Conflicts {
		if c.Role != "common" && c.Role != "checkout" || !scenePath(c.Path) || !scenePath(c.ClaimedPath) || c.CheckoutID != "" && !buildImageHex(c.CheckoutID) || c.ProjectID != "" && !buildImageHex(c.ProjectID) {
			return bad()
		}
	}
	return nil
}
