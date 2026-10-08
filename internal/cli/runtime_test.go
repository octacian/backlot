package cli

import (
	"bytes"
	"context"
	v1 "github.com/octacian/backlot/api/v1"
	"strings"
	"testing"
)

func TestRuntimeRequiresTerminalSeparator(t *testing.T) {
	for _, args := range [][]string{{"backlot", "run", "test", "unseparated"}, {"backlot", "run", "dev", "--job-timeout"}, {"backlot", "run", "dev", "--unknown"}, {"backlot", "restart", "id", "--", "extra"}} {
		var stdout, stderr bytes.Buffer
		command := NewCommand(v1.VersionResponse{APIVersion: v1.Version}, &stdout, &stderr)
		if err := command.Run(context.Background(), args); err == nil {
			t.Fatal("invalid runtime args accepted", args)
		}
	}
	var stdout, stderr bytes.Buffer
	command := NewCommand(v1.VersionResponse{APIVersion: v1.Version}, &stdout, &stderr)
	err := command.Run(context.Background(), []string{"backlot", "run", "dev", "--json", "--state-dir", "/nonexistent-backlot-native-fixture", "--", "--json", "focus"})
	if err == nil || !strings.Contains(stdout.String(), `"api_version":"v1"`) {
		t.Fatal("runtime parser lost JSON flag or interpreted forwarded flag", stdout.String(), err)
	}
}
