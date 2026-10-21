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
	if err := validateNativeCleanup([]v1.NativeCleanupResult{valid}, attempt, components, ""); err != nil {
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
		if validateNativeCleanup(results, attempt, components, "") == nil {
			t.Fatal("invalid cleanup binding/consistency accepted", results)
		}
	}
	if err := validateNativeCleanup([]v1.NativeCleanupResult{incomplete}, attempt, components, "cleanup failed"); err != nil {
		t.Fatal(err)
	}
}
