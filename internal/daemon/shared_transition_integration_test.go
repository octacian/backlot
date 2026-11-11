//go:build darwin || linux

package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"testing"

	v1 "github.com/octacian/backlot/api/v1"
)

func TestSharedTransitionsEarly(t *testing.T) {
	if os.Getenv("BACKLOT_DOCKER_TEST_ENDPOINT") == "" {
		t.Skip("requires explicitly granted Docker provider; skip is not runtime evidence")
	}
	endpoint, unavailable := sharedUnavailableProxy(t)
	f := newSharedMariaDBFixture(t, true, endpoint)
	t.Cleanup(func() { unavailable.Store(false) })
	instance := f.run("host", f.h.project).Instance
	credential := f.credential(t, instance.ID, "native")
	witness := independentSharedWitness(t, f.db(t, credential))
	requireTransition := func(from, to, cause string) v1.SharedTransition {
		t.Helper()
		status := f.shared(t)
		transition := status.LastTransition
		if transition == nil || transition.Validate() != nil || transition.From != from || transition.To != to || transition.Cause != cause || status.Status != to {
			t.Fatal("unexpected shared transition", status.Status, transition)
		}
		return *transition
	}
	requireTransition("intent", "running", "consumer_start")
	var consumerStopped v1.InstanceResponse
	stopOutput := f.cli("stop", "--state-dir", f.h.dir, "--json", instance.ID)
	if json.Unmarshal(stopOutput, &consumerStopped) != nil || consumerStopped.Instance.Execution == nil || consumerStopped.Instance.Execution.Settlement == nil || consumerStopped.Instance.Execution.Settlement.State != v1.ConsumerSettled {
		t.Fatal("shared native consumer lacks current release result")
	}
	control := func(action string) v1.SharedStatus {
		t.Helper()
		var result v1.SharedResponse
		out := f.cli("shared", action, "--state-dir", f.h.dir, "--json", f.serviceID)
		if json.Unmarshal(out, &result) != nil || len(result.Services) != 1 {
			t.Fatal("invalid shared CLI response")
		}
		return result.Services[0]
	}
	control("stop")
	stopped := requireTransition("running", "stopped", "explicit_shared_stop")
	control("stop")
	if got := requireTransition("running", "stopped", "explicit_shared_stop"); got != stopped {
		t.Fatal("no-op stop rewrote transition")
	}
	control("start")
	requireTransition("stopped", "running", "explicit_shared_start")
	verifySharedWitness(t, f.db(t, credential), witness)
	unavailable.Store(true)
	out, err := exec.Command(f.binary, "shared", "stop", "--state-dir", f.h.dir, "--json", f.serviceID).CombinedOutput()
	if err == nil {
		t.Fatal("owned provider outage did not fail")
	}
	var failure v1.ErrorResponse
	if json.Unmarshal(bytes.SplitN(out, []byte("\n"), 2)[0], &failure) != nil || failure.Error.Code == "" {
		t.Fatal("outage lacked typed CLI failure")
	}
	failed := requireTransition("running", "uncertain", "explicit_shared_stop")
	// A read is independent of provider availability and must not invent a new transition.
	if got := requireTransition("running", "uncertain", "explicit_shared_stop"); got != failed {
		t.Fatal("read changed transition")
	}
	unavailable.Store(false)
	control("start")
	requireTransition("uncertain", "running", "explicit_shared_start")
	verifySharedWitness(t, f.db(t, credential), witness)
	if got := f.credential(t, instance.ID, "native"); got != credential {
		t.Fatal("control changed retained paired credentials")
	}
	if _, err := f.h.cli.Inspect(context.Background(), instance.ID); err != nil {
		t.Fatal("strict client rejected retained result", err)
	}
	f.destroy(t, instance.ID)
	f.requireAbsent(t, credential)
	destroyed := control("destroy")
	if destroyed.Status != "destroyed" || destroyed.LastTransition == nil || destroyed.LastTransition.From != "destroying" || destroyed.LastTransition.To != "destroyed" || destroyed.LastTransition.Cause != "explicit_shared_destroy" || destroyed.LastTransition.Validate() != nil {
		t.Fatal("destroy response lost final transition")
	}
	list, err := f.h.cli.Shared(context.Background(), "list", v1.SharedRequest{APIVersion: v1.Version})
	if err != nil || len(list.Services) != 0 {
		t.Fatal("destroy retained service/transition", err)
	}
	t.Log("real CLI/daemon/client verified causal start/stop, no-op/read suppression, owned provider outage and recovery; random SQL witness and paired credentials preserved until explicit destroy")
}
