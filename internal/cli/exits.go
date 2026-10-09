package cli

import v1 "github.com/octacian/backlot/api/v1"

// ExitError preserves a clean terminal job or signal exit at the executable boundary.
type ExitError struct{ Code int }

// Error implements error without emitting application values.
func (e *ExitError) Error() string { return "execution finished with a nonzero exit status" }

func executionExit(instance v1.Instance) error {
	result := instance.Execution
	if result == nil || result.Failure != "" || result.CollectionFailure != "" || result.CleanupFailure != "" {
		return &ExitError{Code: 1}
	}
	if result.Cancelled {
		return &ExitError{Code: 130}
	}
	if result.TerminalExitCode != nil && *result.TerminalExitCode > 0 {
		return &ExitError{Code: *result.TerminalExitCode}
	}
	if instance.Status != v1.Succeeded && instance.Status != v1.RuntimeReady {
		return &ExitError{Code: 1}
	}
	return nil
}

func cancelledExit(instance v1.Instance, code int) error {
	result := instance.Execution
	if result == nil || result.Failure != "" || result.CollectionFailure != "" || result.CleanupFailure != "" {
		return &ExitError{Code: 1}
	}
	return &ExitError{Code: code}
}
