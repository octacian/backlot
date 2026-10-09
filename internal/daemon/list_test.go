//go:build !windows

package daemon

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	v1 "github.com/octacian/backlot/api/v1"
)

func TestPublicDiscoveryAndPrivateSnapshotAuthority(t *testing.T) {
	root, dir := shortTemp(t), shortTemp(t)
	config := writeFixture(t, root)
	cli, stop := startFixture(t, dir, time.Minute)
	empty, err := cli.List(context.Background(), v1.ListRequest{APIVersion: v1.Version})
	if err != nil || empty.Instances == nil || len(empty.Instances) != 0 {
		t.Fatal("empty list contract", err)
	}
	ids := map[string]bool{}
	for range 3 {
		prepared := prepareFixture(t, cli, request(root, config, "test"))
		if prepared.Instance.Operation.Allocations[0].OwnershipToken != "" {
			t.Fatal("snapshot authority exposed")
		}
		ids[prepared.Instance.ID] = true
	}
	after := ""
	for range 3 {
		page, err := cli.List(context.Background(), v1.ListRequest{APIVersion: v1.Version, AfterID: after, Limit: 1})
		if err != nil || len(page.Instances) != 1 {
			t.Fatal("page unavailable", err)
		}
		item := page.Instances[0]
		if !ids[item.ID] || item.Project != "fixture" || item.Scene != "test" || item.Status != v1.Prepared {
			t.Fatal("discovery lost instance")
		}
		delete(ids, item.ID)
		inspected, err := cli.Inspect(context.Background(), item.ID)
		if err != nil || inspected.Instance.Operation.Allocations[0].OwnershipToken != "" {
			t.Fatal("inspect exposed authority", err)
		}
		after = page.NextAfterID
	}
	if len(ids) != 0 || after != "" {
		t.Fatal("pagination did not terminate")
	}
	_, err = cli.List(context.Background(), v1.ListRequest{APIVersion: v1.Version, Limit: 101})
	code(t, err, "invalid_request")
	stop()
	state, err := openStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = state.db.Close() }()
	page, err := state.list(v1.ListRequest{Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	private, err := state.inspect(page.Instances[0].ID)
	if err != nil || !validID(private.Operation.Allocations[0].OwnershipToken) {
		t.Fatal("redaction erased private recovery authority")
	}
	token := private.Operation.Allocations[0].OwnershipToken
	recorder := httptest.NewRecorder()
	writeJSON(recorder, v1.InstanceResponse{APIVersion: v1.Version, Instance: private})
	if strings.Contains(recorder.Body.String(), token) || strings.Contains(recorder.Body.String(), "ownership_token") {
		t.Fatal("wire response leaked authority")
	}
	var public v1.InstanceResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &public); err != nil {
		t.Fatal(err)
	}
	if private.Operation.Allocations[0].OwnershipToken != token {
		t.Fatal("redaction mutated shared slice")
	}
}
