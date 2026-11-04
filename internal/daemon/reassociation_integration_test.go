//go:build darwin || linux

package daemon

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	v1 "github.com/octacian/backlot/api/v1"
	"github.com/octacian/backlot/internal/projectconfig"
)

func TestSharedReassociationEarlyFlow(t *testing.T) {
	t.Setenv("BACKLOT_PROJECT_CONFIG_DIR", filepath.Join(shortTemp(t), "registry"))
	f := newSharedMariaDBFixture(t, true)
	started := f.run("host", f.h.project)
	peer := f.run("bridge", f.linked)
	peerCredential := f.credential(t, peer.Instance.ID, "container")
	peerWitness := independentSharedWitness(t, f.db(t, peerCredential))
	credential := f.credential(t, started.Instance.ID, "native")
	witness := independentSharedWitness(t, f.db(t, credential))
	serverID, _, running := f.server(t)
	if !running {
		t.Fatal("shared server not running")
	}
	if _, err := f.h.cli.StopExecution(context.Background(), started.Instance.ID); err != nil {
		t.Fatal(err)
	}
	peerBefore, err := f.h.cli.Inspect(context.Background(), peer.Instance.ID)
	if err != nil {
		t.Fatal(err)
	}
	unavailable := f.linked + "-unavailable"
	if err := os.Rename(f.linked, unavailable); err != nil {
		t.Fatal(err)
	}
	before, err := f.h.cli.Inspect(context.Background(), started.Instance.ID)
	if err != nil {
		t.Fatal(err)
	}
	moved := filepath.Join(filepath.Dir(f.h.project), "moved")
	if err := os.Rename(f.h.project, moved); err != nil {
		t.Fatal(err)
	}
	f.h.project = moved
	root, err := projectconfig.Root(moved)
	if err != nil {
		t.Fatal(err)
	}
	request := v1.ReassociationRequest{APIVersion: v1.Version, ProjectPath: moved, ConfigPath: f.configuration, RegistryRoot: root, ProjectID: f.shared(t).ProjectID, CheckoutID: checkoutParent(before.Instance)}
	livePreview, err := f.h.cli.Reassociate(context.Background(), request)
	if err != nil || len(livePreview.Blockers) == 0 {
		t.Fatal("live shared-anchor peer did not block repair", err)
	}
	blockedRequest := request
	blockedRequest.Apply = true
	blockedRequest.Preview = livePreview.Preview
	_, err = f.h.cli.Reassociate(context.Background(), blockedRequest)
	code(t, err, "reassociation_blocked")
	if _, err := f.h.cli.StopExecution(context.Background(), peer.Instance.ID); err != nil {
		t.Fatal(err)
	}
	preview, err := f.h.cli.Reassociate(context.Background(), request)
	if err != nil || len(preview.Blockers) > 0 || len(preview.Services) != 1 || preview.Services[0].Allocations != 2 {
		t.Fatal("preview failed or omitted retained allocation count", err, preview.Blockers)
	}
	// Exercise the CLI adapter as well as the exact typed API request.
	out := f.cli("project", "reassociate", "--project", moved, "--config", f.configuration, "--project-id", request.ProjectID, "--checkout-id", request.CheckoutID, "--state-dir", f.h.dir, "--json")
	var cliPreview v1.ReassociationResponse
	if err := json.Unmarshal(out, &cliPreview); err != nil || cliPreview.Preview != preview.Preview {
		t.Fatal("CLI/API previews differ", err)
	}
	text := f.cli("project", "reassociate", "--project", moved, "--config", f.configuration, "--project-id", request.ProjectID, "--checkout-id", request.CheckoutID, "--state-dir", f.h.dir)
	for _, allocation := range preview.Allocations {
		if !strings.Contains(string(text), "Allocation "+allocation.AllocationID) {
			t.Fatal("text preview omitted retained allocation ID")
		}
	}
	for _, secret := range []string{credential.password, credential.user, peerCredential.password, peerCredential.user} {
		if strings.Contains(string(text), secret) || strings.Contains(string(out), secret) {
			t.Fatal("text/JSON preview exposed SQL credentials")
		}
	}
	request.Apply = true
	request.Preview = preview.Preview
	applied, err := f.h.cli.Reassociate(context.Background(), request)
	if err != nil || !applied.Applied {
		t.Fatal("apply failed", err)
	}
	if id, _, live := f.server(t); id != serverID || !live {
		t.Fatal("repair replaced or stopped shared server")
	}
	verifySharedWitness(t, f.db(t, credential), witness)
	if f.credential(t, started.Instance.ID, "native") != credential {
		t.Fatal("repair changed paired credentials")
	}
	_, err = f.h.cli.Reassociate(context.Background(), request)
	code(t, err, "stale_preview")
	after := f.run("host", moved)
	if after.Instance.ID != started.Instance.ID {
		t.Fatal("repair lost retained scene identity")
	}
	verifySharedWitness(t, f.db(t, credential), witness)
	if f.credential(t, started.Instance.ID, "native") != credential {
		t.Fatal("new accepted attempt changed paired credentials")
	}
	if after.Instance.Checkout.Path == before.Instance.Checkout.Path {
		t.Fatal("new launch did not record current checkout")
	}
	peerAfter, err := f.h.cli.Inspect(context.Background(), peer.Instance.ID)
	if err != nil || !sameCheckoutValue(peerBefore.Instance.Association.Checkout, peerAfter.Instance.Association.Checkout) {
		t.Fatal("unavailable peer redirected", err)
	}
	verifySharedWitness(t, f.db(t, peerCredential), peerWitness)
	if _, err := f.h.cli.StopExecution(context.Background(), started.Instance.ID); err != nil {
		t.Fatal(err)
	}
	third := moved + "-again"
	if err := os.Rename(moved, third); err != nil {
		t.Fatal(err)
	}
	f.h.project = third
	request.Apply = false
	request.Preview = ""
	request.ProjectPath = third
	secondPreview, err := f.h.cli.Reassociate(context.Background(), request)
	if err != nil || len(secondPreview.Blockers) > 0 {
		t.Fatal("second repair did not compare latest B launch", err, secondPreview.Blockers)
	}
	request.Apply = true
	request.Preview = secondPreview.Preview
	if _, err := f.h.cli.Reassociate(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	// Exact-ID CLI restart must select current association rather than the old B path.
	f.cli("restart", started.Instance.ID, "--config", f.configuration, "--state-dir", f.h.dir, "--json")
	verifySharedWitness(t, f.db(t, credential), witness)
	if f.credential(t, started.Instance.ID, "native") != credential {
		t.Fatal("repeated repair changed credentials")
	}
	if err := os.Rename(unavailable, f.linked); err != nil {
		t.Fatal(err)
	}
	git(t, third, "worktree", "repair", f.linked)
	_, err = f.h.cli.Prepare(context.Background(), v1.PrepareRequest{APIVersion: v1.Version, Plan: v1.PlanRequest{ProjectPath: f.linked, Scene: "bridge", ConfigPath: f.configuration}})
	if err == nil {
		t.Fatal("unresolved peer started after shared anchor repair")
	}
	peerRequest := v1.ReassociationRequest{APIVersion: v1.Version, ProjectPath: f.linked, ConfigPath: f.configuration, RegistryRoot: root, ProjectID: request.ProjectID, CheckoutID: checkoutParent(peerBefore.Instance)}
	// The unchanged shared anchor does not make unrelated live consumers affected.
	peerPreview, err := f.h.cli.Reassociate(context.Background(), peerRequest)
	if err != nil || len(peerPreview.Blockers) != 0 {
		t.Fatal("checkout-only verification blocked on an unaffected consumer", err, peerPreview.Blockers)
	}
	peerRequest.Apply = true
	peerRequest.Preview = peerPreview.Preview
	if _, err := f.h.cli.Reassociate(context.Background(), peerRequest); err != nil {
		t.Fatal(err)
	}
	resumed := f.run("bridge", f.linked)
	if resumed.Instance.ID != peer.Instance.ID {
		t.Fatal("peer lost instance")
	}
	verifySharedWitness(t, f.db(t, peerCredential), peerWitness)
	if _, err := f.h.cli.StopExecution(context.Background(), peer.Instance.ID); err != nil {
		t.Fatal(err)
	}
}
