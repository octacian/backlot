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

// StopAllContract requires streaming peers to agree before any mutation effects.
const StopAllContract = "stop-all-stream/v1"

// StopAllRequest requires explicit scope-wide intent and a supported result contract.
type StopAllRequest struct {
	// APIVersion selects the shared contract.
	APIVersion string `json:"api_version"`
	// All authorizes the finite scope-wide selection.
	All bool `json:"all"`
	// Contract requires live compact outcomes instead of the older finite response.
	Contract string `json:"contract"`
}

// Validate rejects unsupported peers before accepting a stop.
func (q StopAllRequest) Validate() error {
	if q.APIVersion != Version || !q.All || q.Contract != StopAllContract {
		return &PlanError{Code: "invalid_request", Message: "bulk stop requires explicit all intent and the stop-all-stream/v1 contract; use matching client and daemon binaries"}
	}
	return nil
}

// StopAllTarget is the coordinator's compact terminal observation of one target.
type StopAllTarget struct {
	// ID identifies the recorded workload.
	ID string `json:"id"`
	// Attempt identifies selected execution, absent for unreadable or metadata-only work.
	Attempt string `json:"attempt,omitempty"`
	// Generation binds the selected resources, absent with Attempt.
	Generation string `json:"generation,omitempty"`
	// Outcome is stopped, failed or unresolved; timeout never proves release.
	Outcome string `json:"outcome"`
	// Reason is a controlled diagnosis; details remain available through inspect.
	Reason string `json:"reason,omitempty"`
	// Settlement reports last observed release eligibility separately from execution history.
	Settlement ConsumerSettlementState `json:"settlement"`
	// CollectionFailed preserves diagnostic collection gaps even after runtime release.
	CollectionFailed bool `json:"collection_failed,omitempty"`
}

// Validate rejects malformed target observations without trusting diagnostic strings.
func (t StopAllTarget) Validate() error {
	bad := func() error { return &PlanError{Code: "invalid_response", Message: "invalid bulk target outcome"} }
	if !buildImageHex(t.ID) || (t.Attempt == "") != (t.Generation == "") || t.Attempt != "" && (!buildImageHex(t.Attempt) || !buildImageHex(t.Generation)) {
		return bad()
	}
	if t.Settlement != ConsumerSettled && t.Settlement != ConsumerUnsettled && t.Settlement != ConsumerUnknown {
		return bad()
	}
	switch t.Outcome {
	case "stopped":
		if t.Reason != "" || t.Settlement != ConsumerSettled || t.CollectionFailed {
			return bad()
		}
	case "failed", "unresolved":
		switch t.Reason {
		case "state_unreadable", "stop_timeout", "busy", "target_changed", "dependent_unsettled", "cleanup_failed", "collection_failed", "ownership_unproven", "shutting_down":
		default:
			return bad()
		}
	default:
		return bad()
	}
	return nil
}

// StopAllSummary closes a finite operation; identity lists grow with its targets.
type StopAllSummary struct {
	// Stopped counts targets with verified successful completion.
	Stopped int `json:"stopped"`
	// Failed counts known failures, including collection failures.
	Failed int `json:"failed"`
	// Unresolved counts unfinished or unprovable target outcomes.
	Unresolved int `json:"unresolved"`
	// FailedIDs lists exactly the failed target identities in event order.
	FailedIDs []string `json:"failed_ids"`
	// UnresolvedIDs lists exactly the unresolved identities in event order.
	UnresolvedIDs []string `json:"unresolved_ids"`
	// SelectionComplete means the entire recorded selection was readable.
	SelectionComplete bool `json:"selection_complete"`
	// SelectionFailure explains an incomplete selection independently of target outcomes.
	SelectionFailure string `json:"selection_failure,omitempty"`
	// AdmissionsJoined reports whether every preceding admission owner joined.
	AdmissionsJoined bool `json:"admissions_joined"`
	// Error preserves partial failure alongside useful target outcomes.
	Error *PlanError `json:"error,omitempty"`
}

// Validate checks counts, identity lists and aggregate success truthfulness.
func (s StopAllSummary) Validate() error {
	bad := func() error { return &PlanError{Code: "invalid_response", Message: "invalid bulk stop summary"} }
	if s.Stopped < 0 || s.Failed < 0 || s.Unresolved < 0 || s.FailedIDs == nil || s.UnresolvedIDs == nil || s.Failed != len(s.FailedIDs) || s.Unresolved != len(s.UnresolvedIDs) {
		return bad()
	}
	seen := map[string]bool{}
	for _, ids := range [][]string{s.FailedIDs, s.UnresolvedIDs} {
		for _, id := range ids {
			if !buildImageHex(id) || seen[id] {
				return bad()
			}
			seen[id] = true
		}
	}
	if s.SelectionComplete && s.SelectionFailure != "" || !s.SelectionComplete && s.SelectionFailure != "state_unreadable" && s.SelectionFailure != "cancelled" {
		return bad()
	}
	failed := !s.SelectionComplete || !s.AdmissionsJoined || s.Failed > 0 || s.Unresolved > 0
	if failed != (s.Error != nil) || s.Error != nil && (s.Error.Code != "partial_failure" || s.Error.Message != "bulk stop incomplete; inspect activity and selected targets before explicit recovery") {
		return bad()
	}
	return nil
}

// StopAllRecord is one typed NDJSON event, ordered acceptance, targets, summary.
type StopAllRecord struct {
	// APIVersion identifies the shared API.
	APIVersion string `json:"api_version"`
	// Type is accepted, target or summary.
	Type string `json:"type"`
	// DaemonGeneration binds every event to the accepting daemon.
	DaemonGeneration string `json:"daemon_generation"`
	// OperationID identifies this transient operation without authorizing replay.
	OperationID string `json:"operation_id"`
	// Target is present only for a target observation.
	Target *StopAllTarget `json:"target,omitempty"`
	// Summary is present only for the final event.
	Summary *StopAllSummary `json:"summary,omitempty"`
}

// Validate checks individual events; clients also validate ordering and totals.
func (r StopAllRecord) Validate() error {
	bad := func() error { return &PlanError{Code: "invalid_response", Message: "invalid bulk stop event"} }
	if r.APIVersion != Version || !buildImageHex(r.DaemonGeneration) || !buildImageHex(r.OperationID) {
		return bad()
	}
	switch r.Type {
	case "accepted":
		if r.Target != nil || r.Summary != nil {
			return bad()
		}
	case "target":
		if r.Target == nil || r.Summary != nil || r.Target.Validate() != nil {
			return bad()
		}
	case "summary":
		if r.Target != nil || r.Summary == nil || r.Summary.Validate() != nil {
			return bad()
		}
	default:
		return bad()
	}
	return nil
}

// StopAllResponse collects compact stream observations for programmatic callers.
// It is not the HTTP response framing; StopAllRecord defines that stream.
type StopAllResponse struct {
	// APIVersion identifies the shared API.
	APIVersion string
	// DaemonGeneration binds the operation to a daemon lifetime.
	DaemonGeneration string
	// OperationID identifies the transient operation.
	OperationID string
	// Targets contains validated compact outcomes in event order.
	Targets []StopAllTarget
	// SelectionComplete reports complete selection.
	SelectionComplete bool
	// SelectionFailure explains incomplete selection.
	SelectionFailure string
	// AdmissionsJoined reports preceding admission settlement.
	AdmissionsJoined bool
	// Error reports partial failure with retained outcomes.
	Error *PlanError
}

// Summary derives totals without retaining detailed diagnostics.
func (r StopAllResponse) Summary() StopAllSummary {
	s := StopAllSummary{FailedIDs: []string{}, UnresolvedIDs: []string{}, SelectionComplete: r.SelectionComplete, SelectionFailure: r.SelectionFailure, AdmissionsJoined: r.AdmissionsJoined, Error: r.Error}
	for _, t := range r.Targets {
		switch t.Outcome {
		case "stopped":
			s.Stopped++
		case "failed":
			s.Failed++
			s.FailedIDs = append(s.FailedIDs, t.ID)
		case "unresolved":
			s.Unresolved++
			s.UnresolvedIDs = append(s.UnresolvedIDs, t.ID)
		}
	}
	return s
}

// Validate checks a collected stream without imposing an aggregate scope ceiling.
func (r StopAllResponse) Validate() error {
	if r.APIVersion != Version || !buildImageHex(r.DaemonGeneration) || !buildImageHex(r.OperationID) || r.Targets == nil {
		return &PlanError{Code: "invalid_response", Message: "invalid bulk stop response"}
	}
	seen := map[string]bool{}
	for _, t := range r.Targets {
		if seen[t.ID] || t.Validate() != nil {
			return &PlanError{Code: "invalid_response", Message: "invalid bulk stop response"}
		}
		seen[t.ID] = true
	}
	return r.Summary().Validate()
}
