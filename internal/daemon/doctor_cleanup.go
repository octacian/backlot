package daemon

import (
	"errors"
	"fmt"
	"strings"

	v1 "github.com/octacian/backlot/api/v1"
)

// These fixed causes cross the diagnostic boundary; underlying filesystem and
// provider errors never do. A journal key correlates an artifact, not authority.
type diagnosticCleanupCause string

func (c diagnosticCleanupCause) Error() string { return string(c) }

const (
	diagnosticUnidentified      diagnosticCleanupCause = "unidentified allocation"
	diagnosticCopyMismatch      diagnosticCleanupCause = "copy mismatch"
	diagnosticParentMismatch    diagnosticCleanupCause = "parent mismatch"
	diagnosticProviderUncertain diagnosticCleanupCause = "provider uncertainty"
	diagnosticRemovalFailed     diagnosticCleanupCause = "removal failure"
	diagnosticRecoveryLimit                            = 8
)

type diagnosticRecoveryFailure struct {
	entries []string
	omitted int
}

func (f *diagnosticRecoveryFailure) Error() string {
	return "diagnostic cleanup unresolved; preserve state; " + strings.Join(f.entries, "; ") +
		fmt.Sprintf("; omitted entries: %d; inspect exact artifacts using the daemon guide", f.omitted)
}
func (f *diagnosticRecoveryFailure) Unwrap() error {
	return problem("cleanup_failed", "diagnostic cleanup unresolved; preserve state")
}

// Return a new bounded value so readers can retain the previous error snapshot.
// Only validated journal identifiers and fixed categories enter the message.
func diagnosticRecoveryFailureFor(previous error, key string, record diagnosticProbeRecord, err error) error {
	f := &diagnosticRecoveryFailure{}
	var prior *diagnosticRecoveryFailure
	if errors.As(previous, &prior) {
		f.entries = append(f.entries, prior.entries...)
		f.omitted = prior.omitted
	}
	if len(f.entries) == diagnosticRecoveryLimit || !validID(key) || !validDiagnosticCACopy(record) {
		f.omitted++
		return f
	}
	cause := diagnosticProviderUncertain
	for _, candidate := range []diagnosticCleanupCause{diagnosticUnidentified, diagnosticCopyMismatch, diagnosticParentMismatch, diagnosticRemovalFailed} {
		if errors.Is(err, candidate) {
			cause = candidate
			break
		}
	}
	artifact := "probe " + key
	if record.CA != nil {
		artifact = diagnosticCACopyName(key)
	}
	f.entries = append(f.entries, artifact+": "+string(cause))
	return f
}

func diagnosticRecoveryMessage(err error) string {
	var failure *diagnosticRecoveryFailure
	if errors.As(err, &failure) {
		return failure.Error()
	}
	return "previous diagnostic cleanup is unresolved; preserve state and restore recorded provider access; inspect using the daemon guide"
}

func diagnosticCleanupResult(r *v1.DoctorResponse, key string, record diagnosticProbeRecord, err error) {
	if err == nil {
		diagnosticAdd(r, "container_probe_cleanup", "daemon/owned Docker", v1.DoctorOK, "owned probe removal verified and ownership journal cleared")
		return
	}
	diagnosticAdd(r, "container_probe_cleanup", "daemon/owned Docker", v1.DoctorFailed, diagnosticRecoveryMessage(diagnosticRecoveryFailureFor(nil, key, record, err)))
}
