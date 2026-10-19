package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"

	v1 "github.com/octacian/backlot/api/v1"
)

func TestExportWaitArgumentValidation(t *testing.T) {
	for _, wait := range []string{"0", "-1", "0s", "bad", "false"} {
		t.Run(wait, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			command := NewCommand(v1.VersionResponse{APIVersion: v1.Version}, &stdout, &stderr)
			err := command.Run(context.Background(), []string{"backlot", "export", strings.Repeat("a", 64), "--output", t.TempDir() + "/new", "--wait=" + wait, "--json"})
			if err == nil || !strings.Contains(stdout.String(), "invalid_arguments") {
				t.Fatal("invalid optional wait accepted", stdout.String(), err)
			}
		})
	}
}
