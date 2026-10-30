package v1

import "time"

// Attempt records public execution facts and bounded launch provenance, never recovery authority.
type Attempt struct {
	Provenance              string                   `json:"provenance"`
	InstanceID              string                   `json:"instance_id"`
	Sequence                int64                    `json:"sequence"`
	Status                  InstanceStatus           `json:"status"`
	CreatedAt               string                   `json:"created_at"`
	StartedAt               string                   `json:"started_at,omitempty"`
	Source                  ExportSource             `json:"source"`
	Result                  ExecutionResult          `json:"result"`
	NativeOwnershipWarnings []NativeOwnershipWarning `json:"native_ownership_warnings,omitempty"`
}

// AttemptSummary bounds history pages independently of result size.
type AttemptSummary struct {
	Provenance      string         `json:"provenance"`
	Attempt         string         `json:"attempt"`
	Sequence        int64          `json:"sequence"`
	Status          InstanceStatus `json:"status"`
	CompletedAt     string         `json:"completed_at,omitempty"`
	EvidenceExpired bool           `json:"evidence_expired"`
	Protected       bool           `json:"protected"`
}

// HistoryRequest selects a live newest-first page in one instance.
type HistoryRequest struct {
	APIVersion string `json:"api_version"`
	InstanceID string `json:"instance_id"`
	After      string `json:"after,omitempty"`
	Limit      int    `json:"limit,omitempty"`
}

// HistoryResponse returns summaries with a continuation bound to this instance.
type HistoryResponse struct {
	APIVersion   string           `json:"api_version"`
	InstanceID   string           `json:"instance_id"`
	Attempts     []AttemptSummary `json:"attempts"`
	Continuation string           `json:"continuation,omitempty"`
}

// AttemptRequest selects exactly one retained attempt or the newest completed attempt.
type AttemptRequest struct {
	APIVersion    string `json:"api_version"`
	InstanceID    string `json:"instance_id"`
	Attempt       string `json:"attempt,omitempty"`
	LastCompleted bool   `json:"last_completed,omitempty"`
}

// AttemptResponse returns a selected public attempt.
type AttemptResponse struct {
	APIVersion string  `json:"api_version"`
	Attempt    Attempt `json:"attempt"`
}

// HistoryPruneRequest selects the union of age and completed-count limits.
// Keep is optional so zero can explicitly select every eligible completed attempt.
type HistoryPruneRequest struct {
	APIVersion string `json:"api_version"`
	InstanceID string `json:"instance_id"`
	OlderThan  string `json:"older_than,omitempty"`
	Keep       *int   `json:"keep,omitempty"`
	DryRun     bool   `json:"dry_run"`
}

// PrunedAttempt describes a selected entry and its independently checked protection.
type PrunedAttempt struct {
	Reasons    []string `json:"reasons"`
	Protection string   `json:"protection,omitempty"`
	Attempt    string   `json:"attempt"`
	Protected  bool     `json:"protected"`
	Bytes      int64    `json:"bytes"`
}

// HistoryPruneResponse reports advisory preview or committed deletion selection.
type HistoryPruneResponse struct {
	APIVersion string          `json:"api_version"`
	InstanceID string          `json:"instance_id"`
	DryRun     bool            `json:"dry_run"`
	Attempts   []PrunedAttempt `json:"attempts"`
}

// Completed reports observed execution completion without inventing legacy times.
func (a Attempt) Completed() bool {
	if a.Result.CompletedAt != "" {
		return true
	}
	if a.Provenance != "legacy_current" {
		return false
	}
	switch a.Status {
	case Stopped, Failed, Succeeded, Cancelled, Interrupted:
		return true
	case Destroyed:
		return a.Result.Outcome != ""
	}
	return false
}

// Validate bounds explicit pruning selectors before admission or filesystem effects.
func (q HistoryPruneRequest) Validate() error {
	if q.APIVersion != Version || !buildImageHex(q.InstanceID) || q.Keep == nil && q.OlderThan == "" || q.Keep != nil && *q.Keep < 0 {
		return &PlanError{Code: "invalid_request", Message: "prune requires recorded instance and age and/or nonnegative keep count"}
	}
	if q.OlderThan != "" {
		d, err := time.ParseDuration(q.OlderThan)
		if err != nil || d <= 0 {
			return &PlanError{Code: "invalid_request", Message: "older_than must be a positive duration"}
		}
	}
	return nil
}
