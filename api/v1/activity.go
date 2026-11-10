package v1

import (
	"path/filepath"
	"strings"
)

// ActivityRequest selects one finite recorded page, without provider probes.
type ActivityRequest struct {
	// APIVersion selects the shared contract.
	APIVersion string `json:"api_version"`
	// After is the previous page's opaque next_after cursor.
	After string `json:"after,omitempty"`
	// Limit bounds results, defaulting to 100.
	Limit int `json:"limit,omitempty"`
}

// Validate rejects invalid pagination before any state read.
func (q ActivityRequest) Validate() error {
	if q.APIVersion != Version || q.Limit < 0 || q.Limit > 100 || q.After != "" && !activityCursor(q.After) {
		return &PlanError{Code: "invalid_request", Message: "activity requires a compatible version, recorded cursor and limit 0 through 100"}
	}
	return nil
}
func activityCursor(c string) bool {
	kind, id, ok := strings.Cut(c, "/")
	return ok && (kind == "workload" || kind == "shared") && buildImageHex(id)
}

// ActivityBlocker identifies the exact infrastructure action fenced by a workload.
type ActivityBlocker struct {
	// Kind is gateway or shared.
	Kind string `json:"kind"`
	// ServiceID selects a shared server; gateway uses the selected daemon scope.
	ServiceID string `json:"service_id,omitempty"`
	// Stop also fences restart; Destroy independently denotes a retained allocation.
	Stop bool `json:"stop"`
	// Destroy fences shared destruction until the allocation is explicitly released.
	Destroy bool `json:"destroy"`
}

// ActivityEntry separates recorded intent from the current in-memory owner.
// It deliberately omits plans, commands, credentials and provider capabilities.
type ActivityEntry struct {
	// Key is the stable pagination identity within this changing observation.
	Key string `json:"key"`
	// ID is the immutable recorded workload or shared server identity.
	ID string `json:"id"`
	// Project identifies the recorded project.
	Project string `json:"project"`
	// Checkout is the recorded effective checkout location, without rediscovery.
	Checkout string `json:"checkout,omitempty"`
	// Scene identifies the recorded workload selection.
	Scene string `json:"scene,omitempty"`
	// Status is historical workload status or recorded server intent, never a probe.
	Status string `json:"status"`
	// Owner is running, joined, or absent in this daemon generation.
	Owner string `json:"owner"`
	// Kept reports investigation retention independently of historical outcome.
	Kept bool `json:"kept,omitempty"`
	// Settlement diagnoses current release independently of historical status.
	Settlement *ConsumerSettlement `json:"settlement,omitempty"`
	// Ports are recorded allocations, not listener observations.
	Ports map[string]int `json:"recorded_ports,omitempty"`
	// Origin is recorded publication intent, not readiness.
	Origin string `json:"recorded_origin,omitempty"`
	// Blockers identifies infrastructure guards and retained allocations.
	Blockers []ActivityBlocker `json:"blockers"`
	// LastTransition preserves the shared server's last actual transition.
	LastTransition *SharedTransition `json:"last_transition,omitempty"`
}

// ActivityResponse is a finite observation; pagination never proves global absence.
type ActivityResponse struct {
	// APIVersion identifies the contract.
	APIVersion string `json:"api_version"`
	// DaemonGeneration identifies current owner observations.
	DaemonGeneration string `json:"daemon_generation"`
	// GatewayStatus is recorded managed-gateway intent, never a readiness probe.
	GatewayStatus string `json:"gateway_status"`
	// PreparingAdmissions counts current registered workload mutations, including unresolved inputs.
	PreparingAdmissions int `json:"preparing_admissions"`
	// AdmissionClosed reports the temporary workload fence or permanent shutdown.
	AdmissionClosed bool `json:"admission_closed"`
	// Entries contains a bounded page.
	Entries []ActivityEntry `json:"entries"`
	// NextAfter continues after the last completely evaluated record.
	NextAfter string `json:"next_after,omitempty"`
	// Complete means this page reached the end of its changing scope.
	Complete bool `json:"complete"`
}

// Validate rejects incoherent, unbounded or unsolicited activity data.
func (r ActivityResponse) Validate() error {
	bad := func() error { return &PlanError{Code: "invalid_response", Message: "invalid activity response"} }
	if r.PreparingAdmissions < 0 || r.APIVersion != Version || !buildImageHex(r.DaemonGeneration) || r.Entries == nil || len(r.Entries) > 100 || r.Complete != (r.NextAfter == "") || r.NextAfter != "" && !activityCursor(r.NextAfter) {
		return bad()
	}
	switch r.GatewayStatus {
	case "unconfigured", "running", "stopped", "interrupted", "uncertain":
	default:
		return bad()
	}
	previous := ""
	for _, e := range r.Entries {
		if !activityCursor(e.Key) || !strings.HasSuffix(e.Key, "/"+e.ID) || e.Key <= previous || e.Project == "" || len(e.Project) > 4096 || len(e.Checkout) > 4096 || len(e.Scene) > 63 || len(e.Origin) > 4096 || e.Blockers == nil || len(e.Blockers) > 100 || len(e.Ports) > 100 {
			return bad()
		}
		previous = e.Key
		if e.Owner != "running" && e.Owner != "joined" && e.Owner != "absent" {
			return bad()
		}
		if strings.HasPrefix(e.Key, "workload/") {
			if !filepath.IsAbs(e.Checkout) || !buildImageIdentifier.MatchString(e.Scene) || !InstanceStatus(e.Status).validActivityStatus() || e.Settlement == nil || e.Settlement.InstanceID != e.ID || e.Settlement.Validate() != nil || e.LastTransition != nil {
				return bad()
			}
		} else {
			switch e.Status {
			case "intent", "running", "stopped", "uncertain", "destroying", "destroyed":
			default:
				return bad()
			}
			if !buildImageHex(e.Project) || e.Settlement != nil || e.Owner != "absent" || e.LastTransition != nil && (e.LastTransition.Validate() != nil || e.LastTransition.To != e.Status) {
				return bad()
			}
		}
		for name, port := range e.Ports {
			if !buildImageIdentifier.MatchString(name) || port < 1 || port > 65535 {
				return bad()
			}
		}
		seen := map[string]bool{}
		for _, b := range e.Blockers {
			k := b.Kind + "/" + b.ServiceID
			if seen[k] || !b.Stop && !b.Destroy || b.Kind == "gateway" && (b.ServiceID != "" || b.Destroy) || b.Kind == "shared" && !buildImageHex(b.ServiceID) || b.Kind != "gateway" && b.Kind != "shared" {
				return bad()
			}
			seen[k] = true
		}
	}
	return nil
}
func (s InstanceStatus) validActivityStatus() bool {
	switch s {
	case Preparing, Prepared, Starting, RuntimeReady, Stopping, Stopped, Succeeded, Failed, Cancelled, Interrupted, Destroyed:
		return true
	}
	return false
}

// BulkStopTargetLimit keeps worst-case settlement results within the 1 MiB wire budget.
const BulkStopTargetLimit = 25

// StopAllRequest requires explicit scope-wide intent; force and IDs are unsupported.
type StopAllRequest struct {
	// APIVersion selects the shared contract.
	APIVersion string `json:"api_version"`
	// All must be true to authorize the finite selection.
	All bool `json:"all"`
}

// Validate requires explicit all intent.
func (q StopAllRequest) Validate() error {
	if q.APIVersion != Version || !q.All {
		return &PlanError{Code: "invalid_request", Message: "bulk stop requires explicit all intent and a compatible version"}
	}
	return nil
}

// StopAllTarget retains the selected authority and final current observation.
type StopAllTarget struct {
	// ID is the selected immutable workload identity.
	ID string `json:"id"`
	// Attempt is the selected execution, absent for metadata preparation.
	Attempt string `json:"attempt,omitempty"`
	// Generation is the selected resource authority, absent for metadata preparation.
	Generation string `json:"generation,omitempty"`
	// Outcome is completed, failed or unresolved.
	Outcome string `json:"outcome"`
	// Settlement is the latest observed diagnostic, never cleanup authority.
	Settlement ConsumerSettlement `json:"settlement"`
	// SettlementObservation distinguishes selection from a completed cleanup observation.
	SettlementObservation string `json:"settlement_observation"`
	// CollectionFailed preserves collection gaps separately from runtime settlement.
	CollectionFailed bool `json:"collection_failed,omitempty"`
}

// StopAllResponse reports a finite operation, without a durable replay contract.
type StopAllResponse struct {
	// APIVersion identifies the shared contract.
	APIVersion string `json:"api_version"`
	// DaemonGeneration scopes this transient operation identity.
	DaemonGeneration string `json:"daemon_generation"`
	// OperationID identifies accepted coordination, without a cancellation endpoint.
	OperationID string `json:"operation_id"`
	// Targets is the complete bounded selection, including unresolved targets.
	Targets []StopAllTarget `json:"targets"`
	// SelectionComplete distinguishes a complete selection from refusal before target stops.
	SelectionComplete bool `json:"selection_complete"`
	// SelectionFailure is scope_too_large, state_unreadable or budget_exhausted on refusal.
	SelectionFailure string `json:"selection_failure,omitempty"`
	// AdmissionsJoined distinguishes bounded invalidation from successful joining.
	AdmissionsJoined bool `json:"admissions_joined"`
	// Error reports partial failure alongside usable target results.
	Error *PlanError `json:"error,omitempty"`
}

// Validate rejects inconsistent bulk results before presentation.
func (r StopAllResponse) Validate() error {
	bad := func() error { return &PlanError{Code: "invalid_response", Message: "invalid bulk stop response"} }
	if r.APIVersion != Version || !buildImageHex(r.DaemonGeneration) || !buildImageHex(r.OperationID) || r.Targets == nil || len(r.Targets) > BulkStopTargetLimit {
		return bad()
	}
	if !r.SelectionComplete && (len(r.Targets) != 0 || r.SelectionFailure != "scope_too_large" && r.SelectionFailure != "state_unreadable" && r.SelectionFailure != "budget_exhausted") || r.SelectionComplete && r.SelectionFailure != "" {
		return bad()
	}
	failed := !r.AdmissionsJoined || !r.SelectionComplete
	seen := map[string]bool{}
	for _, t := range r.Targets {
		if t.SettlementObservation != "selected" && t.SettlementObservation != "after_stop" || seen[t.ID] || !buildImageHex(t.ID) || t.Settlement.InstanceID != t.ID || t.Settlement.Attempt != t.Attempt || t.Settlement.Generation != t.Generation || t.Settlement.Validate() != nil {
			return bad()
		}
		seen[t.ID] = true
		switch t.Outcome {
		case "completed":
			if t.SettlementObservation != "after_stop" || t.Settlement.State != ConsumerSettled || t.CollectionFailed {
				return bad()
			}
		case "failed", "unresolved":
			failed = true
		default:
			return bad()
		}
	}
	if failed != (r.Error != nil) || r.Error != nil && (r.Error.Code != "partial_failure" || r.Error.Message != "bulk stop incomplete; inspect activity and selected targets before explicit recovery") {
		return bad()
	}
	return nil
}
