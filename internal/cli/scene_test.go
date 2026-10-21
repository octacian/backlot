package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	v1 "github.com/octacian/backlot/api/v1"
)

func TestSceneCLIRejectsInvalidArgumentsAndBudgets(t *testing.T) {
	for _, args := range [][]string{
		{"scene", "status"},
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

func TestStatusIDFollowArgumentsAndUnavailableFrame(t *testing.T) {
	id := strings.Repeat("a", 64)
	for _, args := range [][]string{
		{"--duration", "1s"}, {"--interval", "1s"},
		{"--follow", "--duration", "0s"}, {"--follow", "--interval", "0s"},
		{"--follow", "--interval", "99ms"}, {"--follow", "--interval", "61s"},
		{"--follow", "--duration", "999ms"}, {"--follow", "--duration", "25h"},
		{"--follow", "--duration", "invalid"}, {"--follow", "--project", "/tmp"},
	} {
		var out, stderr bytes.Buffer
		c := NewCommand(v1.VersionResponse{APIVersion: v1.Version}, &out, &stderr)
		argv := append([]string{"backlot", "status"}, args...)
		if err := c.Run(context.Background(), append(argv, id)); err == nil {
			t.Fatal("invalid status follow accepted", args)
		}
	}
	var out, stderr bytes.Buffer
	c := NewCommand(v1.VersionResponse{APIVersion: v1.Version}, &out, &stderr)
	err := c.Run(context.Background(), []string{"backlot", "status", "--follow", "--json", "--state-dir", "/nonexistent-backlot-id-fixture", id})
	var frame v1.SceneFollowRecord
	if err == nil || json.Unmarshal(out.Bytes(), &frame) != nil || frame.Type != "error" || frame.Error == nil {
		t.Fatal(err, out.String())
	}
}

func TestSceneCommandsRejectInstanceFlag(t *testing.T) {
	for _, action := range []string{"start", "status", "stop"} {
		var out, stderr bytes.Buffer
		c := NewCommand(v1.VersionResponse{APIVersion: v1.Version}, &out, &stderr)
		err := c.Run(context.Background(), []string{"backlot", "scene", action, "--instance", strings.Repeat("a", 64)})
		if err == nil || !strings.Contains(err.Error(), "flag provided but not defined: -instance") {
			t.Fatal(action, err, out.String(), stderr.String())
		}
	}
}
