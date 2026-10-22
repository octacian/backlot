package v1

import (
	"strings"
	"testing"
)

func TestNativeCleanupValidation(t *testing.T) {
	valid := NativeCleanupResult{Component: "api", Attempt: strings.Repeat("a", 64), Confidence: ObservedEmpty, Reason: "session_observed_empty", Detail: "Unseen descendants may remain."}
	if err := valid.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*NativeCleanupResult){
		func(r *NativeCleanupResult) { r.Confidence = "complete" },
		func(r *NativeCleanupResult) { r.Reason = "group_absent" },
		func(r *NativeCleanupResult) { r.Attempt = "old" },
		func(r *NativeCleanupResult) { r.Component = "../private" },
		func(r *NativeCleanupResult) { r.Detail = strings.Repeat("x", 257) },
	} {
		r := valid
		change(&r)
		if r.Validate() == nil {
			t.Fatal("invalid result accepted", r)
		}
	}
}
