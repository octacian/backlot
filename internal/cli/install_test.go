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
	instances := []v1.Instance{{Status: v1.RuntimeReady}, {Status: v1.Prepared}, {Status: v1.Failed, Execution: &v1.ExecutionResult{Kept: true, CleanupFailure: "pending"}}, {Status: v1.Stopped, Execution: &v1.ExecutionResult{CleanupFailure: "pending"}}}
	running, unresolved := installCounts(instances)
	if running != 2 || unresolved != 2 {
		t.Fatal(running, unresolved)
	}
	var out, diagnostic bytes.Buffer
	err := NewCommand(v1.VersionResponse{}, &out, &diagnostic).Run(context.Background(), []string{"backlot", "install-replace", "--stage", "/stage", "--destination", "/dest", "--version", "v0.4.0", "--json"})
	var response v1.ErrorResponse
	if err == nil || json.Unmarshal(out.Bytes(), &response) != nil || response.Error.Code != "invalid_arguments" {
		t.Fatal(err, out.String())
	}
}
