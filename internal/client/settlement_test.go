package client

import (
	"strings"
	"testing"

	v1 "github.com/octacian/backlot/api/v1"
)

func TestConsumerSettlementInstanceBinding(t *testing.T) {
	id, attempt := strings.Repeat("a", 64), strings.Repeat("b", 64)
	s := &v1.ConsumerSettlement{InstanceID: id, Attempt: attempt, Generation: attempt, State: v1.ConsumerSettled, Cause: v1.SettlementVerifiedRelease, Blockers: []v1.ConsumerBlocker{}}
	i := v1.Instance{ID: id, Status: v1.Interrupted, Execution: &v1.ExecutionResult{Attempt: attempt, Components: []v1.ComponentResult{}, CollectionFailure: "historical collection gap", Settlement: s}}
	if err := validateExecution(i); err != nil {
		t.Fatal(err)
	}
	s.InstanceID = strings.Repeat("c", 64)
	if validateExecution(i) == nil {
		t.Fatal("accepted another instance's summary")
	}
	s.InstanceID = id
	s.Attempt = strings.Repeat("c", 64)
	if validateExecution(i) == nil {
		t.Fatal("accepted another attempt's summary")
	}
}
