package v1

import (
	"path/filepath"
)

// ProjectAssociation exposes current selection independently of historical launch facts.
type ProjectAssociation struct {
	CheckoutParent string           `json:"checkout_parent"`
	Checkout       CheckoutIdentity `json:"checkout"`
	ProjectPath    string           `json:"project_path"`
	Revision       string           `json:"revision"`
}

// ProjectIdentity describes an observed shared anchor, never provider authority.
type ProjectIdentity struct {
	ID         string `json:"id"`
	Anchor     string `json:"anchor"`
	Filesystem string `json:"filesystem"`
	Relative   string `json:"relative"`
	Project    string `json:"project"`
}

// ReassociationRequest selects exact retained owners and acknowledges a preview on apply.
type ReassociationRequest struct {
	APIVersion   string `json:"api_version"`
	ProjectPath  string `json:"project_path"`
	ConfigPath   string `json:"config_path,omitempty"`
	RegistryRoot string `json:"registry_root"`
	ProjectID    string `json:"project_id,omitempty"`
	CheckoutID   string `json:"checkout_id,omitempty"`
	Apply        bool   `json:"apply,omitempty"`
	Preview      string `json:"preview,omitempty"`
}

// Validate rejects invalid selectors before filesystem reads or effects.
func (r ReassociationRequest) Validate() error {
	if r.APIVersion != Version || !scenePath(r.ProjectPath) || !scenePath(r.RegistryRoot) || r.ConfigPath != "" && !scenePath(r.ConfigPath) || r.Apply && (r.ProjectID == "" || r.CheckoutID == "" || r.Preview == "") || !r.Apply && r.Preview != "" {
		return &PlanError{Code: "invalid_request", Message: "reassociation requires absolute project/registry/config paths; apply requires exact project and checkout IDs plus preview acknowledgement"}
	}
	for _, id := range []string{r.ProjectID, r.CheckoutID, r.Preview} {
		if id != "" && !buildImageHex(id) {
			return &PlanError{Code: "invalid_request", Message: "invalid reassociation identity or acknowledgement"}
		}
	}
	return nil
}

// ProjectRegistration separates original ownership from the active binding.
type ProjectRegistration struct {
	Origin          ProjectIdentity `json:"origin"`
	Current         ProjectIdentity `json:"current"`
	Revision        string          `json:"revision"`
	Acknowledgement string          `json:"acknowledgement,omitempty"`
}

// ReassociationInstance lists affected retained consumers without secret state.
type ReassociationInstance struct {
	RecordedFilesystemState string             `json:"recorded_checkout_git_filesystem_state"`
	ID                      string             `json:"id"`
	Scene                   string             `json:"scene"`
	Status                  InstanceStatus     `json:"status"`
	Recorded                CheckoutIdentity   `json:"recorded"`
	Association             ProjectAssociation `json:"association"`
	Selected                bool               `json:"selected"`
}

// ReassociationAllocation lists retained allocation links, including released journals.
type ReassociationAllocation struct {
	AllocationID string `json:"allocation_id"`
	InstanceID   string `json:"instance_id"`
	Resource     string `json:"resource"`
	ServiceID    string `json:"service_id"`
	Released     bool   `json:"released"`
}

// ReassociationPrivate describes safe private-selection facts, never input values.
type ReassociationPrivate struct {
	RecordedGenerationPath string `json:"recorded_generation_path,omitempty"`
	OperationRoot          string `json:"operation_root"`
	RuntimeRoot            string `json:"runtime_root"`
	RecordedAssociation    string `json:"recorded_association,omitempty"`
	Association            string `json:"association,omitempty"`
	GenerationPath         string `json:"generation_path,omitempty"`
	State                  string `json:"state"`
	Revision               string `json:"revision"`
}

// FilesystemObservation separates device/inode evidence from checkout hash identity.
type FilesystemObservation struct {
	Path   string `json:"path"`
	Device string `json:"device,omitempty"`
	Inode  string `json:"inode,omitempty"`
}

// ReassociationResponse is the canonical safe actionable preview and applied result.
type ReassociationResponse struct {
	StateRevision    string                    `json:"state_revision"`
	APIVersion       string                    `json:"api_version"`
	DaemonGeneration string                    `json:"daemon_generation"`
	Request          ReassociationRequest      `json:"request"`
	Current          ProjectIdentity           `json:"current"`
	Checkout         CheckoutIdentity          `json:"checkout"`
	Filesystems      []FilesystemObservation   `json:"filesystems"`
	Projects         []ProjectRegistration     `json:"projects"`
	Instances        []ReassociationInstance   `json:"instances"`
	Services         []SharedStatus            `json:"services"`
	Allocations      []ReassociationAllocation `json:"allocations"`
	Private          ReassociationPrivate      `json:"private"`
	Changes          []string                  `json:"changes"`
	Preserved        []string                  `json:"preserved"`
	Blockers         []string                  `json:"blockers"`
	Preview          string                    `json:"preview"`
	Applied          bool                      `json:"applied"`
}

// Validate checks the coherent preview envelope returned by a daemon.
func (r ReassociationResponse) Validate() error {
	if r.APIVersion != Version || !buildImageHex(r.DaemonGeneration) || !buildImageHex(r.Preview) || !buildImageHex(r.Current.ID) || !buildImageHex(r.Checkout.ID) || r.Projects == nil || r.Instances == nil || r.Services == nil || r.Allocations == nil || r.Blockers == nil {
		return &PlanError{Code: "invalid_response", Message: "invalid project reassociation result"}
	}
	if !buildImageHex(r.StateRevision) || !validProjectIdentity(r.Current) || !scenePath(r.Checkout.Path) || r.Checkout.GitDirectory != "" && !scenePath(r.Checkout.GitDirectory) || !buildImageHex(r.Private.Revision) || r.Changes == nil || r.Preserved == nil {
		return &PlanError{Code: "invalid_response", Message: "invalid reassociation observations"}
	}
	for _, p := range r.Projects {
		if !validProjectIdentity(p.Origin) || !validProjectIdentity(p.Current) || p.Revision != "" && !buildImageHex(p.Revision) || p.Acknowledgement != "" && !buildImageHex(p.Acknowledgement) {
			return &PlanError{Code: "invalid_response", Message: "invalid retained project registration"}
		}
	}
	for _, i := range r.Instances {
		if !reassociationStatus(i.Status) || i.RecordedFilesystemState != "unrecorded" || !buildImageHex(i.ID) || !sceneIdentifier.MatchString(i.Scene) || !buildImageHex(i.Recorded.ID) || !scenePath(i.Recorded.Path) {
			return &PlanError{Code: "invalid_response", Message: "invalid affected instance"}
		}
		if err := i.Association.Validate(); err != nil {
			return err
		}
	}
	for _, a := range r.Allocations {
		if !buildImageHex(a.AllocationID) || !buildImageHex(a.InstanceID) || !buildImageHex(a.ServiceID) || !sceneIdentifier.MatchString(a.Resource) {
			return &PlanError{Code: "invalid_response", Message: "invalid allocation reference"}
		}
	}
	for _, s := range r.Services {
		if !buildImageHex(s.ID) || !buildImageHex(s.ProjectID) || !sceneIdentifier.MatchString(s.Name) {
			return &PlanError{Code: "invalid_response", Message: "invalid retained service reference"}
		}
	}
	if err := r.Request.Validate(); err != nil {
		return &PlanError{Code: "invalid_response", Message: "invalid echoed reassociation selectors"}
	}
	return nil
}

// Validate checks current selection independently of historical launch provenance.
func (a ProjectAssociation) Validate() error {
	rel, err := filepath.Rel(a.Checkout.Path, a.ProjectPath)
	if !buildImageHex(a.CheckoutParent) || !buildImageHex(a.Checkout.ID) || !scenePath(a.Checkout.Path) || !scenePath(a.ProjectPath) || a.Checkout.GitDirectory != "" && !scenePath(a.Checkout.GitDirectory) || a.Revision != "" && !buildImageHex(a.Revision) || err != nil || !filepath.IsLocal(rel) {
		return &PlanError{Code: "invalid_response", Message: "invalid current project association"}
	}
	return nil
}
func validProjectIdentity(p ProjectIdentity) bool {
	return buildImageHex(p.ID) && scenePath(p.Anchor) && filepath.IsLocal(p.Relative) && sceneIdentifier.MatchString(p.Project) && p.Filesystem != ""
}

func reassociationStatus(s InstanceStatus) bool {
	switch s {
	case Preparing, Prepared, Cancelled, Interrupted, Starting, RuntimeReady, Stopping, Stopped, Destroyed, Succeeded, Failed:
		return true
	}
	return false
}
