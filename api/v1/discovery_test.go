package v1

import (
	"strings"
	"testing"
)

func TestDiscoveryTrustBoundaries(t *testing.T) {
	id := strings.Repeat("a", 64)
	valid := FilteredListRequest{APIVersion: Version, ProjectPath: "/project", Lifetime: Persistent, State: Stopped, Limit: 1}
	if err := valid.Validate(); err != nil {
		t.Fatal(err)
	}
	bad := valid
	bad.CheckoutPath = "/checkout"
	if bad.Validate() == nil {
		t.Fatal("mixed locations accepted")
	}
	bad = valid
	bad.State = "ready-ish"
	if bad.Validate() == nil {
		t.Fatal("unknown status accepted")
	}
	q := LogPageRequest{APIVersion: Version, InstanceID: id, Attempt: id, Stream: "stdout", Limit: 1}
	if err := q.Validate(); err != nil {
		t.Fatal(err)
	}
	r := LogPageResponse{APIVersion: Version, InstanceID: id, Records: []LogRecord{}, Gaps: []EvidenceGap{}, HistoricalCollection: "unknown"}
	if err := r.Validate(q); err != nil {
		t.Fatal(err)
	}
	r.More = true
	if r.Validate(q) == nil {
		t.Fatal("unresumable more accepted")
	}
	q.After = strings.Repeat("x", 2049)
	if q.Validate() == nil {
		t.Fatal("unbounded cursor accepted")
	}
	r.More = false
	r.Records = []LogRecord{{InstanceID: id, Attempt: id, Component: "test", Time: "2026-10-27T00:00:00Z", Stream: "stderr", Message: "output"}}
	if r.Validate(LogPageRequest{InstanceID: id, Stream: "stdout", Limit: 1}) == nil {
		t.Fatal("wrong stream accepted")
	}
	p := DaemonConfigurationResponse{APIVersion: Version, StateVersion: StateVersion - 1}
	if p.Validate() == nil {
		t.Fatal("state fence bypassed")
	}
}
