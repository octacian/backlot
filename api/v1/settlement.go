package v1

import "time"

// ConsumerSettlementState separates resource eligibility from execution outcome.
type ConsumerSettlementState string

const (
	// ConsumerSettled means every recorded consumer obligation has release proof.
	ConsumerSettled ConsumerSettlementState = "settled"
	// ConsumerUnsettled means a known active or incomplete consumer retains fences.
	ConsumerUnsettled ConsumerSettlementState = "unsettled"
	// ConsumerUnknown means retained evidence cannot establish release.
	ConsumerUnknown ConsumerSettlementState = "unknown"
)

// ConsumerSettlementCause describes evidence or the actual release operation.
type ConsumerSettlementCause string

const (
	// SettlementNotStarted identifies genuine metadata without accepted work.
	SettlementNotStarted ConsumerSettlementCause = "not_started"
	// SettlementVerifiedRelease identifies legacy positive release evidence.
	SettlementVerifiedRelease ConsumerSettlementCause = "verified_release"
	// SettlementExecutionCleanup identifies verified execution-owner cleanup.
	SettlementExecutionCleanup ConsumerSettlementCause = "execution_cleanup"
	// SettlementExplicitStop identifies verified explicit stop cleanup.
	SettlementExplicitStop ConsumerSettlementCause = "explicit_stop"
	// SettlementDaemonRecovery identifies verified daemon recovery cleanup.
	SettlementDaemonRecovery ConsumerSettlementCause = "daemon_recovery"
	// SettlementPreviousBoot identifies independent earlier-boot native absence.
	SettlementPreviousBoot ConsumerSettlementCause = "previous_boot"
	// SettlementActive identifies an active accepted consumer.
	SettlementActive ConsumerSettlementCause = "active"
	// SettlementIncomplete identifies an unfinished consumer cleanup.
	SettlementIncomplete ConsumerSettlementCause = "incomplete"
	// SettlementUnproven identifies unavailable release evidence.
	SettlementUnproven ConsumerSettlementCause = "unproven"
)

// ConsumerBlocker contains a controlled diagnosis without private authority.
type ConsumerBlocker struct {
	// Code identifies the unmet obligation using the closed settlement vocabulary.
	Code string `json:"code"`
	// InstanceID identifies the consumer retaining the obligation.
	InstanceID string `json:"instance_id"`
	// Component identifies a declared component when the evidence names one.
	Component string `json:"component,omitempty"`
}

// ConsumerSettlement is a diagnostic projection, never authorization from a cache.
type ConsumerSettlement struct {
	// InstanceID identifies the exact consumer being evaluated.
	InstanceID string `json:"instance_id"`
	// Attempt identifies accepted work, or is empty for metadata without execution.
	Attempt string `json:"attempt"`
	// Generation identifies owned or borrowed resources, falling back to Attempt.
	Generation string `json:"generation"`
	// State reports current consumer eligibility independently of historical outcome.
	State ConsumerSettlementState `json:"state"`
	// Cause identifies evidence or the operation that actually verified release.
	Cause ConsumerSettlementCause `json:"cause"`
	// ObservedAt is an actual producer timestamp, absent for untimed legacy evidence.
	ObservedAt string `json:"observed_at,omitempty"`
	// Blockers contains at most 100 sorted unique controlled diagnoses.
	Blockers []ConsumerBlocker `json:"blockers"`
	// OmittedBlockers counts further distinct diagnoses beyond the public bound.
	OmittedBlockers int `json:"omitted_blockers,omitempty"`
}

// Validate rejects malformed, incoherent or unbounded settlement diagnostics.
func (s ConsumerSettlement) Validate() error {
	bad := func() error { return &PlanError{Code: "invalid_response", Message: "invalid consumer settlement"} }
	if !buildImageHex(s.InstanceID) || (s.Attempt == "") != (s.Generation == "") || s.Attempt != "" && (!buildImageHex(s.Attempt) || !buildImageHex(s.Generation)) || s.Blockers == nil || len(s.Blockers) > 100 || s.OmittedBlockers < 0 || s.OmittedBlockers > 0 && len(s.Blockers) != 100 {
		return bad()
	}
	if s.ObservedAt != "" {
		if _, err := time.Parse(time.RFC3339Nano, s.ObservedAt); err != nil || len(s.ObservedAt) > 64 {
			return bad()
		}
	}
	switch s.State {
	case ConsumerSettled:
		if len(s.Blockers) != 0 || s.OmittedBlockers != 0 {
			return bad()
		}
		switch s.Cause {
		case SettlementNotStarted:
			if s.Attempt != "" || s.ObservedAt != "" {
				return bad()
			}
		case SettlementExecutionCleanup, SettlementExplicitStop, SettlementDaemonRecovery:
			if s.Attempt == "" || s.ObservedAt == "" {
				return bad()
			}
		case SettlementVerifiedRelease, SettlementPreviousBoot:
			if s.Attempt == "" {
				return bad()
			}
		default:
			return bad()
		}
	case ConsumerUnsettled:
		if len(s.Blockers) == 0 || s.Cause != SettlementActive && s.Cause != SettlementIncomplete {
			return bad()
		}
	case ConsumerUnknown:
		if len(s.Blockers) == 0 || s.Cause != SettlementUnproven {
			return bad()
		}
	default:
		return bad()
	}
	last := ""
	for _, b := range s.Blockers {
		if !buildImageHex(b.InstanceID) || b.Component != "" && !buildImageIdentifier.MatchString(b.Component) {
			return bad()
		}
		switch b.Code {
		case "active_consumer", "pending_launch", "runtime_missing", "attempt_mismatch", "native_unproven", "native_incomplete", "native_abandoned", "container_not_removed", "container_attempt_mismatch", "gateway_pending", "network_not_removed", "cleanup_incomplete", "dependent_unsettled", "no_release_evidence", "metadata_unproven":
		default:
			return bad()
		}
		key := b.Code + "/" + b.InstanceID + "/" + b.Component
		if key <= last {
			return bad()
		}
		last = key
	}
	return nil
}
