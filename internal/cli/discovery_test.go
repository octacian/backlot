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

func TestCheckoutAndNamedLifecycleArguments(t *testing.T) {
	for _, args := range [][]string{
		{"checkout", "list", "extra"}, {"checkout", "list", "--checkout"}, {"checkout", "list", "--project", "/project"}, {"checkout", "list", "--here"}, {"checkout", "list", "--limit", "0"},
		{"scene", "restart"}, {"scene", "reset", "dev", "extra"}, {"scene", "destroy", "dev", "extra"},
		{"scene", "restart", "dev", "--attached"}, {"scene", "reset", "dev", "--allow-image-change"}, {"scene", "restart", "dev", "--force"}, {"scene", "destroy", "dev", "--config", "/config"},
	} {
		var out, diagnostic bytes.Buffer
		c := NewCommand(v1.VersionResponse{}, &out, &diagnostic)
		err := c.Run(context.Background(), append([]string{"backlot", "--json"}, args...))
		var r v1.ErrorResponse
		if err == nil || json.Unmarshal(out.Bytes(), &r) != nil || r.Error.Code != "invalid_arguments" {
			t.Fatal(args, err, out.String())
		}
	}
	for _, name := range []string{"restart", "reset", "destroy"} {
		root, named := map[string]bool{}, map[string]bool{}
		for _, c := range runtimeCommands() {
			if c.Name == name {
				for _, f := range c.Flags {
					root[f.Names()[0]] = true
				}
			}
		}
		for _, c := range sceneCommand().Commands {
			if c.Name == name {
				for _, f := range c.Flags {
					named[f.Names()[0]] = true
				}
			}
		}
		if !named["project"] || len(named) != len(root)+1 {
			t.Fatal("flag parity", name, root, named)
		}
		for flag := range root {
			if !named[flag] {
				t.Fatal("missing flag", name, flag)
			}
		}
	}
}
