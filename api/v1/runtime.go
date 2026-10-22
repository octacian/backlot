package v1

import (
	"encoding/hex"
	"regexp"
	"strings"
)

// Runtime states distinguish observed execution from metadata preparation.
const (
	Starting     InstanceStatus = "starting"
	RuntimeReady InstanceStatus = "ready"
	Stopping     InstanceStatus = "stopping"
	Stopped      InstanceStatus = "stopped"
	Destroyed    InstanceStatus = "destroyed"
	Succeeded    InstanceStatus = "succeeded"
	Failed       InstanceStatus = "failed"
)

// StopRequest targets one execution; Force acknowledges potentially live native survivors.
type StopRequest struct {
	ExpectedAttempt string `json:"expected_attempt,omitempty"`
	APIVersion      string `json:"api_version"`
	InstanceID      string `json:"instance_id"`
	Force           bool   `json:"force,omitempty"`
}

// NativeOwnershipWarning reports an explicit waiver without exposing private authority.
// It remains visible across replacement attempts and does not prove collection.
type NativeOwnershipWarning struct {
	Attempt    string `json:"attempt,omitempty"`
	RecordedAt string `json:"recorded_at"`
	Message    string `json:"message"`
}

// ExecutionOptions contains Go durations; omitted fields use documented defaults.
// Zero execution deadlines are unlimited; zero stop grace escalates immediately.
type ExecutionOptions struct {
	KeepOnFailure  bool   `json:"keep_on_failure,omitempty"`
	StartupTimeout string `json:"startup_timeout,omitempty"`
	JobTimeout     string `json:"job_timeout,omitempty"`
	StopGrace      string `json:"stop_grace,omitempty"`
}

// RunRequest starts or explicitly restarts a validated scene.
type RunRequest struct {
	APIVersion string           `json:"api_version"`
	Plan       PlanRequest      `json:"plan"`
	Options    ExecutionOptions `json:"options"`
	InstanceID string           `json:"instance_id,omitempty"`
}

// ComponentResult preserves original job status independently of orchestration failures.
type ComponentResult struct {
	Name     string `json:"name"`
	Status   string `json:"status"`
	ExitCode *int   `json:"exit_code,omitempty"`
}

// NativeCleanupResult records bounded application-session observation, not universal absence.
// Strict system helpers retain their stronger pinned-group proof and omit this result.
type NativeCleanupResult struct {
	// ResourceRelease explains current native eligibility without changing historical observation.
	ResourceRelease string                  `json:"resource_release,omitempty"`
	Component       string                  `json:"component"`
	Attempt         string                  `json:"attempt"`
	Confidence      NativeCleanupConfidence `json:"confidence"`
	Reason          string                  `json:"reason"`
	Detail          string                  `json:"detail"`
}

// NativeCleanupConfidence distinguishes successful observation from unresolved cleanup.
type NativeCleanupConfidence string

const (
	// ObservedEmpty permits lifecycle release under the accepted missed-process risk.
	ObservedEmpty NativeCleanupConfidence = "observed_empty"
	// CleanupIncomplete retains fences because cleanup or required joins failed.
	CleanupIncomplete NativeCleanupConfidence = "incomplete"
	// CleanupAuthorityLost retains fences because the original guardian cannot attest cleanup.
	CleanupAuthorityLost NativeCleanupConfidence = "authority_lost"
)

// ExecutionResult is the durable redacted runtime view.
type ExecutionResult struct {
	// Outcome preserves the terminal execution status across later stop/destroy operations.
	Outcome          InstanceStatus  `json:"outcome,omitempty"`
	Fixtures         []FixtureRecord `json:"fixtures,omitempty"`
	TerminalExitCode *int            `json:"terminal_exit_code,omitempty"`
	Kept             bool            `json:"kept,omitempty"`
	CompletedAt      string          `json:"completed_at,omitempty"`
	// EvidenceObservedAt is the persisted inferred retention start for legacy results without a completion time.
	EvidenceObservedAt string                `json:"evidence_observed_at,omitempty"`
	EvidenceExpired    bool                  `json:"evidence_expired,omitempty"`
	Artifacts          []ArtifactRecord      `json:"artifacts,omitempty"`
	Origin             string                `json:"origin,omitempty"`
	Attempt            string                `json:"attempt"`
	Components         []ComponentResult     `json:"components"`
	Ports              map[string]int        `json:"ports,omitempty"`
	Failure            string                `json:"failure,omitempty"`
	FailureDetail      *PlanError            `json:"failure_detail,omitempty"`
	NativeCleanup      []NativeCleanupResult `json:"native_cleanup,omitempty"`
	CleanupFailure     string                `json:"cleanup_failure,omitempty"`
	CleanupDetails     []PlanError           `json:"cleanup_details,omitempty"`
	CollectionFailure  string                `json:"collection_failure,omitempty"`
	Cancelled          bool                  `json:"cancelled,omitempty"`
}

// LogsRequest selects retained records by component and absolute record offset.
type LogsRequest struct {
	APIVersion string `json:"api_version"`
	InstanceID string `json:"instance_id"`
	Component  string `json:"component,omitempty"`
	Offset     int    `json:"offset"`
}

// LogRecord is one typed NDJSON output record. Data is base64 for non-UTF8 chunks;
// otherwise Message contains the original UTF8 text.
type LogRecord struct {
	InstanceID string `json:"instance_id"`
	Component  string `json:"component"`
	Time       string `json:"time"`
	Stream     string `json:"stream"`
	Attempt    string `json:"attempt"`
	Message    string `json:"message"`
	Data       string `json:"data,omitempty"`
}

// LogsResponse returns a bounded page and the next unfiltered record offset.
type LogsResponse struct {
	NativeOwnershipWarnings []NativeOwnershipWarning `json:"native_ownership_warnings,omitempty"`
	APIVersion              string                   `json:"api_version"`
	Records                 []LogRecord              `json:"records"`
	NextOffset              int                      `json:"next_offset"`
	Active                  bool                     `json:"active"`
	Gap                     string                   `json:"gap,omitempty"`
}

// ArtifactRecord locates collected evidence outside runtime and data resources.
type ArtifactRecord struct {
	Component string `json:"component"`
	Name      string `json:"name"`
	Attempt   string `json:"attempt"`
	Path      string `json:"path"`
}

// FixturesRequest selects component metadata; SecretName enables explicit access.
type FixturesRequest struct {
	APIVersion string `json:"api_version"`
	InstanceID string `json:"instance_id"`
	Component  string `json:"component,omitempty"`
	SecretName string `json:"secret_name,omitempty"`
}

// FixtureRecord is a resolved fixture identity, redacted unless explicitly requested.
type FixtureRecord struct {
	Component   string `json:"component"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Value       string `json:"value,omitempty"`
	Sensitive   bool   `json:"sensitive,omitempty"`
}

// FixturesResponse returns named fixture identities and explicitly requested secrets.
type FixturesResponse struct {
	APIVersion string          `json:"api_version"`
	Fixtures   []FixtureRecord `json:"fixtures"`
}

// Validate checks cleanup enums and bounded public diagnostics without private authority data.
func (r NativeCleanupResult) Validate() error {
	bad := func() error { return &PlanError{Code: "invalid_response", Message: "invalid native cleanup result"} }
	decoded, err := hex.DecodeString(r.Attempt)
	if err != nil || len(decoded) != 32 || strings.ToLower(r.Attempt) != r.Attempt || !regexp.MustCompile(schemaIdentifier).MatchString(r.Component) || len(r.Detail) == 0 || len(r.Detail) > 256 || strings.ContainsAny(r.Detail, "\x00\r\n") {
		return bad()
	}
	reason := ""
	switch r.Confidence {
	case ObservedEmpty:
		reason = "session_observed_empty"
	case CleanupIncomplete:
		reason = "cleanup_incomplete"
	case CleanupAuthorityLost:
		reason = "guardian_authority_lost"
	default:
		return bad()
	}
	if r.ResourceRelease != "" && (r.ResourceRelease != "previous_boot" || r.Confidence == ObservedEmpty) {
		return bad()
	}
	if r.Reason != reason {
		return bad()
	}
	return nil
}

// FencesResources reports the active native obligation, separately from historical
// confidence and collection. Only an exact-attempt committed waiver or persisted
// previous-boot release can explain an incomplete observation without that fence.
func (r NativeCleanupResult) FencesResources(warnings []NativeOwnershipWarning) bool {
	if r.Confidence == ObservedEmpty || r.ResourceRelease == "previous_boot" {
		return false
	}
	for _, w := range warnings {
		if w.Attempt == r.Attempt && w.RecordedAt != "" {
			return false
		}
	}
	return true
}
