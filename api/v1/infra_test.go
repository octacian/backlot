package v1

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestInfraContracts(t *testing.T) {
	id := strings.Repeat("a", 64)
	good := InfraRequest{APIVersion: Version, Contract: InfraStopContract, DaemonGeneration: id}
	if err := good.Validate("stop"); err != nil {
		t.Fatal(err)
	}
	for _, q := range []InfraRequest{{APIVersion: Version}, {APIVersion: Version, Contract: InfraStopContract}, {APIVersion: Version, Contract: InfraStopContract, DaemonGeneration: id, ConfigPath: "/private/config"}} {
		if q.Validate("stop") == nil {
			t.Fatal("accepted unsupported shutdown intent", q)
		}
	}
	for _, r := range []InfraStopRecord{
		{APIVersion: Version, Type: "accepted", DaemonGeneration: id, OperationID: id},
		{APIVersion: Version, Type: "phase", DaemonGeneration: id, OperationID: id, Phase: "gateway", State: "running"},
		{APIVersion: Version, Type: "target", DaemonGeneration: id, OperationID: id, Target: &InfraTarget{ID: id, Outcome: "stopped"}},
		{APIVersion: Version, Type: "cleanup", DaemonGeneration: id, OperationID: id},
	} {
		raw, err := json.Marshal(r)
		if err != nil {
			t.Fatal(err)
		}
		var decoded InfraStopRecord
		if json.Unmarshal(raw, &decoded) != nil || decoded.Validate() != nil {
			t.Fatal(string(raw))
		}
		decoded.Type = "completion"
		if decoded.Validate() == nil {
			t.Fatal("daemon forged foreground completion")
		}
	}
	bad := InfraStopRecord{APIVersion: Version, DaemonGeneration: id, OperationID: id, Type: "phase", Phase: "gateway", State: "failed"}
	if bad.Validate() == nil {
		t.Fatal("missing phase failure accepted")
	}
	bad = InfraStopRecord{APIVersion: Version, DaemonGeneration: id, OperationID: id, Type: "target", Target: &InfraTarget{ID: "pid-123", Outcome: "stopped"}}
	if bad.Validate() == nil {
		t.Fatal("unvalidated target identity accepted")
	}
}
