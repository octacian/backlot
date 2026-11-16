package v1

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestCleanupConsentAndRecordContracts(t *testing.T) {
	id := strings.Repeat("a", 64)
	q := CleanupRequest{APIVersion: Version, Contract: CleanupContract, CheckoutID: id}
	for _, change := range []func(*CleanupRequest){func(q *CleanupRequest) { q.Contract = "" }, func(q *CleanupRequest) { q.Orphans = true }, func(q *CleanupRequest) { q.CWD = "/tmp" }, func(q *CleanupRequest) { q.ApplyPreview = "yes" }, func(q *CleanupRequest) { q.CheckoutID = "" }} {
		bad := q
		change(&bad)
		if bad.Validate() == nil {
			t.Fatal("invalid consent accepted", bad)
		}
	}
	q.ApplyPreview = id
	if q.Validate() != nil {
		t.Fatal(q.Validate())
	}
	q = CleanupRequest{APIVersion: Version, Contract: CleanupContract, Orphans: true}
	if q.Validate() != nil {
		t.Fatal(q.Validate())
	}
	target := CleanupTarget{ID: id, CheckoutID: id, CheckoutPath: "/missing", Project: "p", Scene: "dev", Status: Stopped, Revision: id, Blockers: []string{}, Resources: []CleanupResource{{Name: "data", Kind: "directory", Outcome: "remove"}}}
	for _, r := range []CleanupRecord{{APIVersion: Version, Type: "preview_target", DaemonGeneration: id, Target: &target}, {APIVersion: Version, Type: "preview", DaemonGeneration: id, Summary: &CleanupSummary{Preview: id, Targets: 1}}, {APIVersion: Version, Type: "accepted", DaemonGeneration: id, OperationID: id}, {APIVersion: Version, Type: "target", DaemonGeneration: id, OperationID: id, InstanceID: id, Outcome: "unattempted", Reason: "cancelled"}, {APIVersion: Version, Type: "summary", DaemonGeneration: id, OperationID: id, Summary: &CleanupSummary{Preview: id, Targets: 1, Unattempted: 1, Cancelled: true}}} {
		if r.Validate() != nil {
			t.Fatal(r, r.Validate())
		}
		raw, _ := json.Marshal(r)
		var decoded CleanupRecord
		if json.Unmarshal(raw, &decoded) != nil || decoded.Validate() != nil {
			t.Fatal("contract roundtrip failed")
		}
	}
	r := CleanupRecord{APIVersion: Version, Type: "summary", DaemonGeneration: id, OperationID: id, Summary: &CleanupSummary{Preview: id, Targets: 1}}
	if r.Validate() == nil {
		t.Fatal("false complete summary accepted")
	}
}
