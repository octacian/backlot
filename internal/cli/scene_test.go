package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	v1 "github.com/octacian/backlot/api/v1"
)

func TestSceneCLIRejectsConflictingTargetsAndBudgets(t *testing.T) {
	for _, args := range [][]string{
		{"scene", "status"},
		{"scene", "stop", "--instance", "id", "dev"},
		{"scene", "status", "--instance", "id", "--project", "/tmp"},
		{"scene", "status", "--follow", "--duration", "0s", "dev"},
		{"scene", "status", "--follow", "--interval", "99ms", "dev"},
		{"scene", "status", "--duration", "1s", "dev"},
		{"scene", "start", "dev", "extra"},
	} {
		var out, stderr bytes.Buffer
		c := NewCommand(v1.VersionResponse{APIVersion: v1.Version}, &out, &stderr)
		if err := c.Run(context.Background(), append([]string{"backlot"}, args...)); err == nil {
			t.Fatal("invalid CLI accepted", args)
		}
	}
}

func TestSceneFollowUnavailableUsesTypedTerminalFrame(t *testing.T) {
	var out, stderr bytes.Buffer
	c := NewCommand(v1.VersionResponse{APIVersion: v1.Version}, &out, &stderr)
	err := c.Run(context.Background(), []string{"backlot", "scene", "status", "--json", "--follow", "--state-dir", "/nonexistent-backlot-scene-fixture", "dev"})
	var frame v1.SceneFollowRecord
	if err == nil || json.Unmarshal(out.Bytes(), &frame) != nil || frame.Type != "error" || frame.Error == nil {
		t.Fatal(err, out.String())
	}
}
