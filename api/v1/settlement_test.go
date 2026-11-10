package v1

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestConsumerSettlementValidation(t *testing.T) {
	id, attempt := strings.Repeat("a", 64), strings.Repeat("b", 64)
	valid := ConsumerSettlement{InstanceID: id, Attempt: attempt, Generation: attempt, State: ConsumerSettled, Cause: SettlementVerifiedRelease, Blockers: []ConsumerBlocker{}}
	for _, cause := range []ConsumerSettlementCause{SettlementVerifiedRelease, SettlementExecutionCleanup, SettlementExplicitStop, SettlementDaemonRecovery, SettlementPreviousBoot} {
		s := valid
		s.Cause = cause
		if cause == SettlementExecutionCleanup || cause == SettlementExplicitStop || cause == SettlementDaemonRecovery {
			s.ObservedAt = "2026-11-06T23:02:42Z"
		}
		if err := s.Validate(); err != nil {
			t.Fatal(err)
		}
		b, err := json.Marshal(ExecutionResult{Attempt: attempt, Settlement: &s})
		if err != nil {
			t.Fatal(err)
		}
		var result ExecutionResult
		if err := json.Unmarshal(b, &result); err != nil || result.Settlement.Validate() != nil || !reflect.DeepEqual(result.Settlement, &s) {
			t.Fatal("settlement did not round trip", err)
		}
	}
	metadata := valid
	metadata.Attempt = ""
	metadata.Generation = ""
	metadata.Cause = SettlementNotStarted
	if err := metadata.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*ConsumerSettlement){
		func(s *ConsumerSettlement) { s.InstanceID = "unknown" },
		func(s *ConsumerSettlement) { s.Attempt = "" },
		func(s *ConsumerSettlement) { s.Cause = "rebooted" },
		func(s *ConsumerSettlement) { s.Cause = SettlementNotStarted },
		func(s *ConsumerSettlement) { s.State = "complete" },
		func(s *ConsumerSettlement) { s.State = ConsumerUnknown },
		func(s *ConsumerSettlement) { s.Blockers = nil },
		func(s *ConsumerSettlement) { s.ObservedAt = "now" },
		func(s *ConsumerSettlement) { s.OmittedBlockers = 1 },
		func(s *ConsumerSettlement) { s.OmittedBlockers = -1 },
	} {
		s := valid
		mutate(&s)
		if s.Validate() == nil {
			t.Fatal("invalid settlement accepted", s)
		}
	}
	blocked := valid
	blocked.State = ConsumerUnknown
	blocked.Cause = SettlementUnproven
	blocked.Blockers = []ConsumerBlocker{{Code: "native_unproven", InstanceID: id, Component: "job"}}
	if err := blocked.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*ConsumerSettlement){
		func(s *ConsumerSettlement) { s.Blockers[0].Code = "private-path-or-provider-error" },
		func(s *ConsumerSettlement) { s.Blockers[0].Component = "../private" },
		func(s *ConsumerSettlement) { s.Blockers[0].InstanceID = "unbound" },
		func(s *ConsumerSettlement) { s.Blockers = append(s.Blockers, s.Blockers[0]) },
		func(s *ConsumerSettlement) { s.Blockers = make([]ConsumerBlocker, 101) },
	} {
		s := blocked
		s.Blockers = append([]ConsumerBlocker{}, blocked.Blockers...)
		mutate(&s)
		if s.Validate() == nil {
			t.Fatal("invalid blocker accepted")
		}
	}
	blocked.State = ConsumerUnsettled
	blocked.Cause = SettlementIncomplete
	blocked.ObservedAt = "2026-11-06T23:02:42Z"
	if err := blocked.Validate(); err != nil {
		t.Fatal(err)
	}
}
