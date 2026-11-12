package v1

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestActivityContracts(t *testing.T) {
	id := strings.Repeat("a", 64)
	for _, q := range []ActivityRequest{{APIVersion: Version}, {APIVersion: Version, Limit: 100, After: "workload/" + id}, {APIVersion: Version, After: "shared/" + id}} {
		if err := q.Validate(); err != nil {
			t.Fatal(err)
		}
	}
	for _, q := range []ActivityRequest{{}, {APIVersion: Version, Limit: 101}, {APIVersion: Version, Limit: -1}, {APIVersion: Version, After: "private/token"}} {
		if q.Validate() == nil {
			t.Fatal("accepted invalid cursor/limit")
		}
	}
	for _, q := range []StopAllRequest{{}, {APIVersion: Version}, {APIVersion: "v0", All: true}} {
		if q.Validate() == nil {
			t.Fatal("implicit all accepted")
		}
	}
	if (StopAllRequest{APIVersion: Version, All: true, Contract: StopAllContract}).Validate() != nil {
		t.Fatal("explicit all refused")
	}
	good := ActivityResponse{APIVersion: Version, DaemonGeneration: id, GatewayStatus: "unconfigured", Complete: true, Entries: []ActivityEntry{{Key: "workload/" + id, ID: id, Project: "fixture", Checkout: "/recorded/project", Scene: "dev", Status: "interrupted", Owner: "absent", Settlement: &ConsumerSettlement{InstanceID: id, State: ConsumerUnknown, Cause: SettlementUnproven, Blockers: []ConsumerBlocker{{Code: "metadata_unproven", InstanceID: id}}}, Blockers: []ActivityBlocker{}}}}
	if err := good.Validate(); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name   string
		change func(*ActivityResponse)
	}{
		{"version", func(r *ActivityResponse) { r.APIVersion = "v0" }},
		{"generation", func(r *ActivityResponse) { r.DaemonGeneration = "unknown" }},
		{"unbounded", func(r *ActivityResponse) { r.Entries = make([]ActivityEntry, 101) }},
		{"nil", func(r *ActivityResponse) { r.Entries = nil }},
		{"cursor", func(r *ActivityResponse) { r.Complete = false }},
		{"owner", func(r *ActivityResponse) { r.Entries[0].Owner = "verified-listener" }},
		{"binding", func(r *ActivityResponse) { r.Entries[0].Settlement.InstanceID = strings.Repeat("b", 64) }},
		{"port", func(r *ActivityResponse) { r.Entries[0].Ports = map[string]int{"http": 65536} }},
		{"blocker", func(r *ActivityResponse) { r.Entries[0].Blockers = []ActivityBlocker{{Kind: "gateway", Destroy: true}} }},
		{"transition", func(r *ActivityResponse) { r.Entries[0].LastTransition = &SharedTransition{} }},
		{"duplicate", func(r *ActivityResponse) { r.Entries = append(r.Entries, r.Entries[0]) }},
	}
	data, _ := json.Marshal(good)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var r ActivityResponse
			if err := json.Unmarshal(data, &r); err != nil {
				t.Fatal(err)
			}
			tt.change(&r)
			if r.Validate() == nil {
				t.Fatal("accepted malformed result")
			}
		})
	}
}

func TestStopAllResultBindings(t *testing.T) {
	id, attempt := strings.Repeat("a", 64), strings.Repeat("b", 64)
	good := StopAllResponse{APIVersion: Version, DaemonGeneration: id, OperationID: attempt, AdmissionsJoined: true, SelectionComplete: true, Targets: []StopAllTarget{{ID: id, Outcome: "stopped", Settlement: ConsumerSettled}}}
	if err := good.Validate(); err != nil {
		t.Fatal(err)
	}
	tests := []func(*StopAllResponse){
		func(r *StopAllResponse) { r.Targets[0].Attempt = attempt },
		func(r *StopAllResponse) { r.Targets[0].Outcome = "unresolved" },
		func(r *StopAllResponse) { r.Targets[0].CollectionFailed = true },
		func(r *StopAllResponse) { r.AdmissionsJoined = false },
		func(r *StopAllResponse) { r.SelectionComplete = false },
		func(r *StopAllResponse) { r.Targets = append(r.Targets, r.Targets[0]) },
		func(r *StopAllResponse) { r.Targets[0].Reason = "private-diagnostic" },
	}
	data, _ := json.Marshal(good)
	for _, change := range tests {
		var r StopAllResponse
		_ = json.Unmarshal(data, &r)
		change(&r)
		if r.Validate() == nil {
			t.Fatal("accepted false all-success")
		}
	}
	good.Targets[0].Outcome = "failed"
	good.Targets[0].CollectionFailed = true
	good.Targets[0].Reason = "collection_failed"
	good.Error = &PlanError{Code: "partial_failure", Message: "bulk stop incomplete; inspect activity and selected targets before explicit recovery"}
	if err := good.Validate(); err != nil {
		t.Fatal("settled collection failure rejected", err)
	}
}
