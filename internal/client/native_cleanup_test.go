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
	if err := validateNativeCleanup([]v1.NativeCleanupResult{valid}, attempt, components, "", nil); err != nil {
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
		if validateNativeCleanup(results, attempt, components, "", nil) == nil {
			t.Fatal("invalid cleanup binding/consistency accepted", results)
		}
	}
	if err := validateNativeCleanup([]v1.NativeCleanupResult{incomplete}, attempt, components, "cleanup failed", nil); err != nil {
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
		err := validateNativeCleanup([]v1.NativeCleanupResult{released}, attempt, components, "", nil)
		if (err == nil) != (release == "previous_boot") {
			t.Fatal("invalid resource release combination", release, err)
		}
	}
	for _, waiverAttempt := range []string{attempt, strings.Repeat("b", 64)} {
		warnings := []v1.NativeOwnershipWarning{{Attempt: waiverAttempt, RecordedAt: "recorded"}}
		err := validateNativeCleanup([]v1.NativeCleanupResult{r}, attempt, components, "", warnings)
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
