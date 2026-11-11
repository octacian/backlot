package cli

import (
	"bytes"
	"context"
	"encoding/json"
	v1 "github.com/octacian/backlot/api/v1"
	"strings"
	"testing"
)

func TestActivityAndBulkArgumentValidation(t *testing.T) {
	for _, args := range [][]string{{"stop", "--all", "id"}, {"stop", "--all", "--force"}, {"stop", "--all", "--force=false"}, {"stop"}, {"activity", "id"}, {"activity", "--limit", "0"}, {"activity", "--limit", "101"}, {"activity", "--after", "secret-value"}} {
		var stdout, stderr bytes.Buffer
		command := NewCommand(v1.VersionResponse{}, &stdout, &stderr)
		err := command.Run(context.Background(), append([]string{"backlot", "--json"}, args...))
		var envelope v1.ErrorResponse
		if err == nil || json.Unmarshal(stdout.Bytes(), &envelope) != nil || envelope.Error.Code == "daemon_unavailable" || strings.Contains(stdout.String(), "secret-value") {
			t.Fatal(args, err, stdout.String())
		}
	}
}

func TestActivityHumanOutputKeepsProvenanceAndEscapes(t *testing.T) {
	var stdout, stderr bytes.Buffer
	c := NewCommand(v1.VersionResponse{}, &stdout, &stderr)
	id := strings.Repeat("a", 64)
	r := v1.ActivityResponse{GatewayStatus: "running", Entries: []v1.ActivityEntry{{Key: "workload/" + id, ID: id, Project: "project\n\x1b", Checkout: "/recorded/project", Scene: "dev", Status: "failed", Owner: "joined", Kept: true, Blockers: []v1.ActivityBlocker{{Kind: "gateway", Stop: true}}}}, Complete: true}
	if err := activityOutput(c.Writer, r); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(stdout.String(), "project\n") || strings.ContainsRune(stdout.String(), '\x1b') || !strings.Contains(stdout.String(), "owner joined; kept true") || !strings.Contains(stdout.String(), "next: backlot stop "+id) {
		t.Fatal("unsafe or incomplete human provenance", stdout.String())
	}
}
