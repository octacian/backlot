package v1

import "path/filepath"

// CleanupContract identifies preview-bound, cancellable destruction peers.
const CleanupContract = "checkout-cleanup/v1"

// CleanupRequest selects one recorded checkout or missing-checkout candidates.
type CleanupRequest struct {
	// APIVersion selects the shared API.
	APIVersion string `json:"api_version"`
	// Contract requires matching cleanup consent and stream semantics.
	Contract string `json:"contract"`
	// Orphans selects eligible missing-checkout records in this state root.
	Orphans bool `json:"orphans,omitempty"`
	// CheckoutID selects an immutable recorded checkout parent even if its path is gone.
	CheckoutID string `json:"checkout_id,omitempty"`
	// CheckoutPath selects an existing exact checkout root.
	CheckoutPath string `json:"checkout_path,omitempty"`
	// CWD discovers the enclosing Git checkout when explicit selection is omitted.
	CWD string `json:"cwd,omitempty"`
	// ApplyPreview authorizes exactly this daemon-produced preview digest.
	ApplyPreview string `json:"apply_preview,omitempty"`
}

// Validate rejects ambiguous scope or unsupported contracts before effects.
func (q CleanupRequest) Validate() error {
	n := 0
	for _, s := range []string{q.CheckoutID, q.CheckoutPath, q.CWD} {
		if s != "" {
			n++
		}
	}
	if q.APIVersion != Version || q.Contract != CleanupContract || q.Orphans && n != 0 || !q.Orphans && n != 1 || q.CheckoutID != "" && !buildImageHex(q.CheckoutID) || q.CheckoutPath != "" && !filepath.IsAbs(q.CheckoutPath) || q.CWD != "" && !filepath.IsAbs(q.CWD) || q.ApplyPreview != "" && !buildImageHex(q.ApplyPreview) {
		return &PlanError{Code: "invalid_request", Message: "cleanup requires a matching contract and exactly one checkout selector, or orphan scope; apply requires the reviewed digest"}
	}
	return nil
}

// CleanupResource describes a logical resource without private provider names.
type CleanupResource struct {
	// Name is the declared resource/component or evidence category.
	Name string `json:"name"`
	// Kind distinguishes owned data, containers, shared allocations and evidence.
	Kind string `json:"kind"`
	// Outcome is remove, removed, failed or unresolved.
	Outcome string `json:"outcome"`
}

// CleanupTarget is one reviewed instance and its destructive impact.
type CleanupTarget struct {
	// ID is the immutable recorded instance.
	ID string `json:"id"`
	// CheckoutID is the recorded ownership parent.
	CheckoutID string `json:"checkout_id"`
	// CheckoutPath is its current recorded association.
	CheckoutPath string `json:"checkout_path"`
	// Project is the recorded project name.
	Project string `json:"project"`
	// Scene is the recorded scene name.
	Scene string `json:"scene"`
	// Status is the recorded lifecycle state.
	Status InstanceStatus `json:"status"`
	// Revision binds private facts without disclosing capabilities.
	Revision string `json:"revision"`
	// MissingPath reports observed absence, including potentially unplugged volumes.
	MissingPath bool `json:"missing_path"`
	// Blockers are controlled reasons excluding destructive application.
	Blockers []string `json:"blockers"`
	// Resources shows the owned data and evidence removed by successful destruction.
	Resources []CleanupResource `json:"resources"`
	// ExportPartial indicates retained incomplete evidence requiring --allow-partial.
	ExportPartial bool `json:"export_partial"`
	// TaskParent orders dependent task destruction before the parent instance.
	TaskParent string `json:"task_parent,omitempty"`
}

// CleanupSummary closes either a preview or an accepted cleanup operation.
type CleanupSummary struct {
	// Preview is the exact digest required to apply this scope.
	Preview string `json:"preview"`
	// Targets counts reviewed instance records, including blocked records.
	Targets int `json:"targets"`
	// Done counts successful complete destruction, including evidence-only retries.
	Done int `json:"done"`
	// Failed counts known cleanup failures.
	Failed int `json:"failed"`
	// Unresolved counts blocked, changed or uncertain targets.
	Unresolved int `json:"unresolved"`
	// Unattempted counts targets not started after cancellation.
	Unattempted int `json:"unattempted"`
	// Cancelled records accepted cancellation independently of cleanup results.
	Cancelled bool `json:"cancelled"`
}

// CleanupRecord is one typed NDJSON record for preview and apply streams.
type CleanupRecord struct {
	// APIVersion selects the shared contract.
	APIVersion string `json:"api_version"`
	// Type is preview_target, preview, accepted, progress, resource, target or summary.
	Type string `json:"type"`
	// DaemonGeneration pins the serving daemon.
	DaemonGeneration string `json:"daemon_generation"`
	// OperationID identifies accepted coordination; absent during preview.
	OperationID string `json:"operation_id,omitempty"`
	// Target carries one preview target only.
	Target *CleanupTarget `json:"target,omitempty"`
	// InstanceID identifies a progress or terminal observation.
	InstanceID string `json:"instance_id,omitempty"`
	// Outcome is a controlled phase or terminal result.
	Outcome string `json:"outcome,omitempty"`
	// Reason is a controlled diagnosis; inspect provides detailed private-safe diagnostics.
	Reason string `json:"reason,omitempty"`
	// Resource reports one committed resource removal.
	Resource *CleanupResource `json:"resource,omitempty"`
	// Summary closes the stream.
	Summary *CleanupSummary `json:"summary,omitempty"`
}

// Validate checks record shape and controlled values at the wire boundary.
func (r CleanupRecord) Validate() error {
	bad := func() error { return &PlanError{Code: "invalid_response", Message: "invalid cleanup stream"} }
	if r.APIVersion != Version || !buildImageHex(r.DaemonGeneration) || r.OperationID != "" && !buildImageHex(r.OperationID) || r.InstanceID != "" && !buildImageHex(r.InstanceID) {
		return bad()
	}
	switch r.Type {
	case "preview_target":
		t := r.Target
		if t == nil || r.OperationID != "" || r.InstanceID != "" || r.Summary != nil || r.Resource != nil || !buildImageHex(t.ID) || !buildImageHex(t.CheckoutID) || !buildImageHex(t.Revision) || !filepath.IsAbs(t.CheckoutPath) || !t.Status.validActivityStatus() || t.Project == "" || t.Scene == "" || t.Resources == nil || t.Blockers == nil || t.TaskParent != "" && !buildImageHex(t.TaskParent) {
			return bad()
		}
		for _, b := range t.Blockers {
			switch b {
			case "checkout_unavailable", "binding_unverified", "active_work", "cleanup_uncertain", "state_unreadable":
			default:
				return bad()
			}
		}
		for _, v := range t.Resources {
			if !validCleanupResource(v) || v.Outcome != "remove" {
				return bad()
			}
		}
	case "preview", "summary":
		s := r.Summary
		if s == nil || !buildImageHex(s.Preview) || s.Targets < 0 || s.Done < 0 || s.Failed < 0 || s.Unresolved < 0 || s.Unattempted < 0 || r.Target != nil || r.InstanceID != "" || r.Resource != nil {
			return bad()
		}
		if r.Type == "preview" {
			if r.OperationID != "" || s.Done+s.Failed+s.Unresolved+s.Unattempted != 0 || s.Cancelled {
				return bad()
			}
		} else if r.OperationID == "" || s.Done+s.Failed+s.Unresolved+s.Unattempted != s.Targets {
			return bad()
		}
	case "accepted":
		if r.OperationID == "" || r.Target != nil || r.Summary != nil || r.InstanceID != "" || r.Resource != nil {
			return bad()
		}
	case "progress", "resource", "target":
		if r.OperationID == "" || r.InstanceID == "" || r.Target != nil || r.Summary != nil {
			return bad()
		}
		if r.Type == "resource" {
			if r.Resource == nil || !validCleanupResource(*r.Resource) || r.Resource.Outcome == "remove" {
				return bad()
			}
		} else if r.Resource != nil {
			return bad()
		}
		if r.Type == "target" {
			switch r.Outcome {
			case "done", "failed", "unresolved", "unattempted":
			default:
				return bad()
			}
		} else if r.Type == "progress" && r.Outcome != "stopping" {
			return bad()
		}
	default:
		return bad()
	}
	switch r.Reason {
	case "", "blocked", "target_changed", "cleanup_failed", "cancelled", "shutting_down":
	default:
		return bad()
	}
	if r.Type != "target" && (r.Reason != "" || r.Outcome != "" && r.Type != "progress") {
		return bad()
	}
	return nil
}
func validCleanupResource(r CleanupResource) bool {
	if r.Name == "" || len(r.Name) > 255 {
		return false
	}
	switch r.Kind {
	case "directory", "volume", "secret", "network", "container", "allocation", "evidence":
	default:
		return false
	}
	switch r.Outcome {
	case "remove", "removed", "failed", "unresolved":
		return true
	}
	return false
}

// CleanupCancelRequest requests cancellation of one accepted operation.
type CleanupCancelRequest struct {
	// APIVersion selects the shared API.
	APIVersion string `json:"api_version"`
	// Contract requires matching operation cancellation semantics.
	Contract string `json:"contract"`
	// DaemonGeneration prevents cancellation from crossing daemon restarts.
	DaemonGeneration string `json:"daemon_generation"`
	// OperationID selects the accepted operation.
	OperationID string `json:"operation_id"`
}

// Validate rejects invalid cancellation targets.
func (q CleanupCancelRequest) Validate() error {
	if q.APIVersion != Version || q.Contract != CleanupContract || !buildImageHex(q.DaemonGeneration) || !buildImageHex(q.OperationID) {
		return &PlanError{Code: "invalid_request", Message: "cleanup cancellation requires the accepted operation and daemon generation"}
	}
	return nil
}

// CleanupCancelResponse acknowledges cancellation intent, not completed cleanup.
type CleanupCancelResponse struct {
	// APIVersion selects the shared API.
	APIVersion string `json:"api_version"`
	// DaemonGeneration pins the serving daemon.
	DaemonGeneration string `json:"daemon_generation"`
	// OperationID identifies the accepted operation.
	OperationID string `json:"operation_id"`
	// Cancelled acknowledges cancellation, including repeated requests.
	Cancelled bool `json:"cancelled"`
}
