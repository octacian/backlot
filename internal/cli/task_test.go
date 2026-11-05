package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	v1 "github.com/octacian/backlot/api/v1"
)

func TestTaskCLIRejectsSelectors(t *testing.T) {
	for _, args := range [][]string{{"task", "run"}, {"task", "run", "admin"}, {"task", "run", "admin", "--scene", "dev", "--instance", "bad"}, {"task", "run", "admin", "--instance", "bad"}, {"task", "run", "admin", "--instance", "bad", "--project", "."}, {"task", "run", "admin", "--scene", "dev", "--scene", "other"}, {"task", "run", "admin", "--scene", "dev", "--keep-on-failure"}} {
		var out, stderr bytes.Buffer
		c := NewCommand(v1.VersionResponse{APIVersion: v1.Version}, &out, &stderr)
		args = append([]string{"backlot", "--json"}, args...)
		if err := c.Run(context.Background(), args); err == nil {
			t.Fatal("invalid task CLI accepted", args)
		}
		var result v1.ErrorResponse
		if json.Unmarshal(out.Bytes(), &result) != nil || result.Error.Code == "" {
			t.Fatal("missing typed error", out.String())
		}
	}
}
