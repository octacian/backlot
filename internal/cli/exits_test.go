package cli

import (
	"errors"
	"testing"

	v1 "github.com/octacian/backlot/api/v1"
)

func TestExecutionExitPreservesJobAndIndependentFailures(t *testing.T) {
	code := 23
	for _, test := range []struct {
		name   string
		result v1.ExecutionResult
		status v1.InstanceStatus
		want   int
	}{
		{"job", v1.ExecutionResult{TerminalExitCode: &code}, v1.Failed, 23},
		{"orchestration", v1.ExecutionResult{TerminalExitCode: &code, Failure: "dependency lost"}, v1.Failed, 1},
		{"collection", v1.ExecutionResult{TerminalExitCode: &code, CollectionFailure: "missing artifact"}, v1.Failed, 1},
		{"cleanup", v1.ExecutionResult{TerminalExitCode: &code, CleanupFailure: "uncertain owner"}, v1.Failed, 1},
		{"cancel", v1.ExecutionResult{TerminalExitCode: &code, Cancelled: true}, v1.Cancelled, 130},
		{"success", v1.ExecutionResult{}, v1.Succeeded, 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := executionExit(v1.Instance{Status: test.status, Execution: &test.result})
			got := 0
			if err != nil {
				var exit *ExitError
				if !errors.As(err, &exit) {
					t.Fatal(err)
				}
				got = exit.Code
			}
			if got != test.want {
				t.Fatalf("exit %d want %d", got, test.want)
			}
		})
	}
}

func TestSignalExitPreservesConventionUnlessExecutionHasOtherFailure(t *testing.T) {
	for _, code := range []int{130, 143} {
		for _, failure := range []bool{false, true} {
			result := &v1.ExecutionResult{Cancelled: true}
			want := code
			if failure {
				result.CleanupFailure = "injected cleanup error"
				want = 1
			}
			err := cancelledExit(v1.Instance{Execution: result}, code)
			var exit *ExitError
			if !errors.As(err, &exit) || exit.Code != want {
				t.Fatalf("signal exit %v want %d", err, want)
			}
		}
	}
}
