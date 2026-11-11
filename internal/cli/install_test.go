//go:build darwin || linux

package cli

import (
	"bytes"
	"context"
	"encoding/json"
	v1 "github.com/octacian/backlot/api/v1"
	"testing"
)

func TestInstallCountsAndFraming(t *testing.T) {
	pages := []v1.ActivityResponse{{PreparingAdmissions: 2, Entries: []v1.ActivityEntry{
		{Owner: "running", Settlement: &v1.ConsumerSettlement{State: v1.ConsumerUnsettled}},
		{Kept: true, Settlement: &v1.ConsumerSettlement{State: v1.ConsumerUnsettled}},
		{Settlement: &v1.ConsumerSettlement{State: v1.ConsumerUnknown}},
		{Settlement: &v1.ConsumerSettlement{State: v1.ConsumerSettled}},
		{Status: "running"},
	}}, {PreparingAdmissions: 1}}
	running, unresolved, preparing := installCounts(pages)
	if running != 2 || unresolved != 3 || preparing != 2 {
		t.Fatal(running, unresolved, preparing)
	}
	var out, diagnostic bytes.Buffer
	err := NewCommand(v1.VersionResponse{}, &out, &diagnostic).Run(context.Background(), []string{"backlot", "install-replace", "--stage", "/stage", "--destination", "/dest", "--version", "v0.4.0", "--json"})
	var response v1.ErrorResponse
	if err == nil || json.Unmarshal(out.Bytes(), &response) != nil || response.Error.Code != "invalid_arguments" {
		t.Fatal(err, out.String())
	}
}
