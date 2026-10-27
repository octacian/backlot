package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	v1 "github.com/octacian/backlot/api/v1"
)

func TestDiscoveryApplicabilityBeforeAccess(t *testing.T) {
	for _, args := range [][]string{
		{"logs", "id", "--attempt", "attempt"}, {"logs", "id", "--stream", "stdout"}, {"logs", "id", "--after", "cursor"}, {"logs", "id", "--limit", "1"}, {"logs", "id", "--page", "--follow"}, {"logs", "id", "--page", "--limit", "0"},
		{"list", "--project", "/p", "--checkout", "/c"}, {"list", "--limit", "101"}, {"list", "--limit", "0"}, {"list", "--state", "invented"},
		{"schema", "other"}, {"contracts", "--state-dir", "/absent"}, {"config", "show", "extra"}, {"daemon", "config", "extra"}, {"scene", "list", "extra"}, {"scene", "describe"},
	} {
		var out, diagnostic bytes.Buffer
		c := NewCommand(v1.VersionResponse{}, &out, &diagnostic)
		err := c.Run(context.Background(), append([]string{"backlot", "--json"}, args...))
		var r v1.ErrorResponse
		if err == nil || json.Unmarshal(out.Bytes(), &r) != nil || r.Error.Code != "invalid_arguments" && r.Error.Code != "invalid_request" {
			t.Fatal(args, err, out.String())
		}
	}
}
