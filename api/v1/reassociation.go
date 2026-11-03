package v1

import (
	"path/filepath"
)

// ProjectAssociation exposes current selection independently of historical launch facts.
type ProjectAssociation struct {
	// CheckoutParent is the immutable checkout registration used for scene slots.
	CheckoutParent string `json:"checkout_parent"`
	// Checkout is the currently selected checkout, independent of launch history.
	Checkout CheckoutIdentity `json:"checkout"`
	// ProjectPath is the current project directory inside Checkout.
	ProjectPath string `json:"project_path"`
	// Revision is the opaque current checkout-binding revision.
	Revision string `json:"revision"`
}

// ProjectIdentity describes an observed shared anchor, never provider authority.
type ProjectIdentity struct {
	// ID hashes the filesystem identity, relative location and declared project.
	ID string `json:"id"`
	// Anchor is the resolved Git common directory or non-Git project directory.
	Anchor string `json:"anchor"`
	// Filesystem records the filesystem object identity observed at Anchor.
	Filesystem string `json:"filesystem"`
	// Relative is the project directory location within its checkout.
	Relative string `json:"relative"`
	// Project is the declared project name.
	Project string `json:"project"`
}

// ReassociationRequest selects exact retained owners and acknowledges a preview on apply.
type ReassociationRequest struct {
	// APIVersion selects the typed local API contract.
	APIVersion string `json:"api_version"`
	// ProjectPath selects the absolute destination project or manifest.
	ProjectPath string `json:"project_path"`
	// ConfigPath selects explicit destination machine configuration when supplied.
	ConfigPath string `json:"config_path,omitempty"`
	// RegistryRoot selects the requesting operation's absolute private registry.
	RegistryRoot string `json:"registry_root"`
	// ProjectID selects the exact retained shared-project owner.
	ProjectID string `json:"project_id,omitempty"`
	// CheckoutID selects the original retained checkout registration.
	CheckoutID string `json:"checkout_id,omitempty"`
	// Apply requests explicit publication after preview revalidation.
	Apply bool `json:"apply,omitempty"`
	// Preview acknowledges the exact safe preview digest on apply.
	Preview string `json:"preview,omitempty"`
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
	// Origin retains immutable shared ownership facts.
	Origin ProjectIdentity `json:"origin"`
	// Current records the active observed association.
	Current ProjectIdentity `json:"current"`
	// Revision is the opaque shared-binding revision.
	Revision string `json:"revision"`
	// Acknowledgement records the last accepted preview digest.
	Acknowledgement string `json:"acknowledgement,omitempty"`
}

// ReassociationInstance lists affected retained consumers without secret state.
type ReassociationInstance struct {
	// RecordedFilesystemState labels unavailable historical checkout/Git filesystem observations.
	RecordedFilesystemState string `json:"recorded_checkout_git_filesystem_state"`
	// ID is the stable retained instance identity.
	ID string `json:"id"`
	// Scene is the recorded scene name.
	Scene string `json:"scene"`
	// Status is the durable consumer lifecycle status.
	Status InstanceStatus `json:"status"`
	// Recorded retains accepted launch checkout provenance.
	Recorded CheckoutIdentity `json:"recorded"`
	// Association describes current selection separately from Recorded.
	Association ProjectAssociation `json:"association"`
	// Selected marks consumers whose destination scenes will be composed.
	Selected bool `json:"selected"`
}

// ReassociationAllocation lists retained allocation links, including released journals.
type ReassociationAllocation struct {
	// AllocationID identifies the retained allocation without its ownership capability.
	AllocationID string `json:"allocation_id"`
	// InstanceID identifies the retained allocation consumer.
	InstanceID string `json:"instance_id"`
	// Resource is the recorded resource name within that consumer.
	Resource string `json:"resource"`
	// ServiceID identifies the immutable shared service parent.
	ServiceID string `json:"service_id"`
	// Released marks a retained release journal rather than a live allocation.
	Released bool `json:"released"`
}

// ReassociationPrivate describes safe private-selection facts, never input values.
type ReassociationPrivate struct {
	// RecordedGenerationPath is the immutable private source recorded at launch, when known.
	RecordedGenerationPath string `json:"recorded_generation_path,omitempty"`
	// OperationRoot is the reviewed requesting registry selection.
	OperationRoot string `json:"operation_root"`
	// RuntimeRoot is the daemon's ordinary registry selection.
	RuntimeRoot string `json:"runtime_root"`
	// RecordedAssociation identifies the private association recorded at launch, when known.
	RecordedAssociation string `json:"recorded_association,omitempty"`
	// Association identifies the usable current private selection, when available.
	Association string `json:"association,omitempty"`
	// GenerationPath is the selected immutable private source path, when available.
	GenerationPath string `json:"generation_path,omitempty"`
	// State reports safe selection/provenance status without input values.
	State string `json:"state"`
	// Revision is keyed opaque source freshness, scoped to this daemon generation.
	Revision string `json:"revision"`
}

// FilesystemObservation separates device/inode evidence from checkout hash identity.
type FilesystemObservation struct {
	// Path identifies the observed checkout, Git directory or sharing anchor.
	Path string `json:"path"`
	// Device records the device value when available.
	Device string `json:"device,omitempty"`
	// Inode records the inode value when available.
	Inode string `json:"inode,omitempty"`
}

// ReassociationResponse is the canonical safe actionable preview and applied result.
type ReassociationResponse struct {
	// StateRevision is opaque freshness for retained registrations and consumers.
	StateRevision string `json:"state_revision"`
	// APIVersion identifies the typed local API contract.
	APIVersion string `json:"api_version"`
	// DaemonGeneration invalidates acknowledgements across daemon restarts.
	DaemonGeneration string `json:"daemon_generation"`
	// Request contains normalized selectors for this preview.
	Request ReassociationRequest `json:"request"`
	// Current describes the destination sharing anchor observation.
	Current ProjectIdentity `json:"current"`
	// Checkout describes the requesting checkout observation.
	Checkout CheckoutIdentity `json:"checkout"`
	// Filesystems separates available device/inode evidence from derived IDs.
	Filesystems []FilesystemObservation `json:"filesystems"`
	// Projects lists retained origin/current shared registrations.
	Projects []ProjectRegistration `json:"projects"`
	// Instances lists selected and affected retained consumers.
	Instances []ReassociationInstance `json:"instances"`
	// Services lists relevant retained shared services without credentials.
	Services []SharedStatus `json:"services"`
	// Allocations lists exact retained allocation links without capabilities.
	Allocations []ReassociationAllocation `json:"allocations"`
	// Private reports safe private selection and root-agreement facts.
	Private ReassociationPrivate `json:"private"`
	// Changes describes the proposed association changes.
	Changes []string `json:"changes"`
	// Preserved describes authority and data kept by the repair.
	Preserved []string `json:"preserved"`
	// Blockers lists prerequisites that prevent application.
	Blockers []string `json:"blockers"`
	// Preview is the canonical safe acknowledgement digest.
	Preview string `json:"preview"`
	// Applied confirms completed association publication.
	Applied bool `json:"applied"`
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
