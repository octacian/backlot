package client

import (
	v1 "github.com/octacian/backlot/api/v1"
	"strings"
	"testing"
)

func TestNativeCleanupBindingAndFailureConsistency(t *testing.T) {
	attempt := strings.Repeat("a", 64)
	valid := v1.NativeCleanupResult{Component: "api", Attempt: attempt, Confidence: v1.ObservedEmpty, Reason: "session_observed_empty", Detail: "Unseen descendants may remain."}
	components := []v1.ComponentResult{{Name: "api", Status: "stopped"}}
	if err := validateNativeCleanup([]v1.NativeCleanupResult{valid}, attempt, components, "", nil, nil); err != nil {
		t.Fatal(err)
	}
	wrongAttempt := valid
	wrongAttempt.Attempt = strings.Repeat("b", 64)
	wrongComponent := valid
	wrongComponent.Component = "caddy"
	incomplete := valid
	incomplete.Confidence = v1.CleanupIncomplete
	incomplete.Reason = "cleanup_incomplete"
	for _, results := range [][]v1.NativeCleanupResult{{wrongAttempt}, {wrongComponent}, {valid, valid}, {incomplete}} {
		if validateNativeCleanup(results, attempt, components, "", nil, nil) == nil {
			t.Fatal("invalid cleanup binding/consistency accepted", results)
		}
	}
	if err := validateNativeCleanup([]v1.NativeCleanupResult{incomplete}, attempt, components, "cleanup failed", nil, nil); err != nil {
		t.Fatal(err)
	}
}

func TestNativeCleanupReleaseAndWaiverBinding(t *testing.T) {
	attempt := strings.Repeat("a", 64)
	r := v1.NativeCleanupResult{Component: "api", Attempt: attempt, Confidence: v1.CleanupAuthorityLost, Reason: "guardian_authority_lost", Detail: "Historical authority lost."}
	components := []v1.ComponentResult{{Name: "api", Status: "stopped"}}
	for _, release := range []string{"previous_boot", "observed_empty", "unknown"} {
		released := r
		released.ResourceRelease = release
		err := validateNativeCleanup([]v1.NativeCleanupResult{released}, attempt, components, "", nil, nil)
		if (err == nil) != (release == "previous_boot") {
			t.Fatal("invalid resource release combination", release, err)
		}
	}
	for _, waiverAttempt := range []string{attempt, strings.Repeat("b", 64)} {
		warnings := []v1.NativeOwnershipWarning{{Attempt: waiverAttempt, RecordedAt: "recorded"}}
		err := validateNativeCleanup([]v1.NativeCleanupResult{r}, attempt, components, "", warnings, nil)
		if (err == nil) != (waiverAttempt == attempt) {
			t.Fatal("waiver attempt fence", err)
		}
	}
	r.Confidence = v1.ObservedEmpty
	r.Reason = "session_observed_empty"
	r.ResourceRelease = "previous_boot"
	if r.Validate() == nil {
		t.Fatal("boot proof misrepresented as observed-empty")
	}
}

func TestNativeHistoryWithCurrentSettlement(t *testing.T) {
	id, attempt := strings.Repeat("a", 64), strings.Repeat("b", 64)
	observation := v1.NativeCleanupResult{Component: "job", Attempt: attempt, Confidence: v1.CleanupAuthorityLost, Reason: "guardian_authority_lost", Detail: "Historical authority loss."}
	result := v1.Instance{ID: id, Status: v1.Interrupted, Execution: &v1.ExecutionResult{Attempt: attempt, Outcome: v1.Interrupted, Components: []v1.ComponentResult{{Name: "job", Status: "stopped"}}, NativeCleanup: []v1.NativeCleanupResult{observation}, CollectionFailure: "historical gap"}}
	if validateExecution(result) == nil {
		t.Fatal("bare historical uncertainty was silently released")
	}
	result.Execution.Settlement = &v1.ConsumerSettlement{InstanceID: id, Attempt: attempt, Generation: attempt, State: v1.ConsumerSettled, Cause: v1.SettlementExplicitStop, ObservedAt: "2026-11-06T23:00:00Z", Blockers: []v1.ConsumerBlocker{}}
	if err := validateExecution(result); err != nil {
		t.Fatal("independent current release rejected historical confidence", err)
	}
	if !observation.FencesResources(nil) {
		t.Fatal("global native receipt policy weakened")
	}
	result.Execution.Settlement.State = v1.ConsumerUnknown
	result.Execution.Settlement.Cause = v1.SettlementUnproven
	result.Execution.Settlement.ObservedAt = ""
	result.Execution.Settlement.Blockers = []v1.ConsumerBlocker{{Code: "native_unproven", InstanceID: id, Component: "job"}}
	if err := validateExecution(result); err != nil {
		t.Fatal("typed current fence rejected truthful history", err)
	}
	result.Execution.Settlement.Attempt = strings.Repeat("c", 64)
	if validateExecution(result) == nil {
		t.Fatal("current diagnostic from another attempt accepted")
	}
}
