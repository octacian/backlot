package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	v1 "github.com/octacian/backlot/api/v1"
)

func TestHistorySelectorsFailBeforeDaemonAccess(t *testing.T) {
	id := strings.Repeat("a", 64)
	cases := [][]string{{"history", "--limit", "1", "prune", id, "--keep", "0"}, {"history", "--after", "cursor", "prune", id, "--keep", "0"}, {"history", "prune", id}, {"history", "prune", id, "--keep", "-1"}, {"history", "prune", id, "--older-than", "0s"}, {"history", "prune", id, "--older-than", "bad"}, {"inspect", id, "--attempt", id, "--last-completed"}, {"logs", id, "--page", "--attempt", ""}, {"export", id, "--attempt", "", "--output", "report"}, {"daemon", "start", "--retention-attempts", "0"}}
	for _, args := range cases {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			var out, diag bytes.Buffer
			err := NewCommand(v1.VersionResponse{}, &out, &diag).Run(context.Background(), append([]string{"backlot", "--json"}, args...))
			var envelope v1.ErrorResponse
			if err == nil || json.Unmarshal(out.Bytes(), &envelope) != nil || envelope.Error.Code != "invalid_arguments" && envelope.Error.Code != "invalid_request" && envelope.Error.Code != "invalid_retention" {
				t.Fatal("invalid selector reached daemon", err, out.String())
			}
		})
	}
}
