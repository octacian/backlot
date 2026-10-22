package daemon

import (
	v1 "github.com/octacian/backlot/api/v1"
	"strings"
	"testing"
)

func TestNativeCleanupAggregationPreservesEarlierFailure(t *testing.T) {
	attempt := strings.Repeat("a", 64)
	result := &v1.ExecutionResult{Attempt: attempt}
	failed := &v1.NativeCleanupResult{Component: "api", Attempt: attempt, Confidence: v1.CleanupAuthorityLost, Reason: "guardian_authority_lost", Detail: "Original authority unavailable."}
	empty := *failed
	empty.Confidence = v1.ObservedEmpty
	empty.Reason = "session_observed_empty"
	mergeNativeCleanup(result, failed)
	mergeNativeCleanup(result, &empty)
	if len(result.NativeCleanup) != 1 || result.NativeCleanup[0].Confidence != v1.CleanupAuthorityLost || result.CleanupFailure == "" {
		t.Fatal("later readiness/workload success erased earlier failure", result)
	}
	other := empty
	other.Component = "web"
	mergeNativeCleanup(result, &other)
	if len(result.NativeCleanup) != 2 || result.CleanupFailure == "" {
		t.Fatal(result)
	}
}
