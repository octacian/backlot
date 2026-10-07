package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	v1 "github.com/octacian/backlot/api/v1"
)

func TestPlanCLIForwardingAndJSONErrors(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", root)
	t.Setenv("XDG_CONFIG_HOME", root)
	for _, file := range []string{"backlot.yaml", "defaults.env"} {
		data, err := os.ReadFile("../../examples/adoption/" + file)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, file), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	var stdout, stderr bytes.Buffer
	err := NewCommand(v1.VersionResponse{}, &stdout, &stderr).Run(context.Background(), []string{"backlot", "plan", "test", "--project", root, "--json", "--", "-run", "TestThing", "--config", "runner-value"})
	if err != nil {
		t.Fatal(err)
	}
	var p v1.PlanResponse
	decoder := json.NewDecoder(&stdout)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&p); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(p.Components[0].Command.Args, []string{"test", "./...", "-run", "TestThing", "--config", "runner-value"}) {
		t.Fatalf("forwarded args=%v", p.Components[0].Command.Args)
	}
	if err := decoder.Decode(&p); !errors.Is(err, io.EOF) {
		t.Fatal("expected one JSON result")
	}
	if stderr.Len() != 0 {
		t.Fatal("success wrote diagnostics")
	}
	stdout.Reset()
	err = NewCommand(v1.VersionResponse{}, &stdout, &stderr).Run(context.Background(), []string{"backlot", "plan", "missing", "--project", root, "--json"})
	if err == nil {
		t.Fatal("unknown scene succeeded")
	}
	var envelope v1.ErrorResponse
	if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.APIVersion != v1.Version || envelope.Error.Code != "unknown_scene" || envelope.Error.Field != "scene" {
		t.Fatalf("error envelope=%+v", envelope)
	}
	stdout.Reset()
	if err := NewCommand(v1.VersionResponse{}, &stdout, &stderr).Run(context.Background(), []string{"backlot", "plan", "test", "--project", root}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "No work executed") {
		t.Fatal("human plan omits offline limitation")
	}
}

func TestPlanArguments(t *testing.T) {
	for _, args := range [][]string{nil, {"--json"}, {"test", "extra"}, {"test", "--project"}, {"test", "--config", "--json"}, {"test", "--json", "--json"}, {"test", "--unsupported"}, {"test", "--project", "a", "--project", "b"}} {
		if _, _, err := parsePlanArgs(args); err == nil {
			t.Fatalf("invalid args accepted: %v", args)
		}
	}
	request, jsonMode, err := parsePlanArgs([]string{"test", "--json", "--project", "a b", "--config", "machine.yaml", "--", "--project", "forwarded", ""})
	if err != nil {
		t.Fatal(err)
	}
	if !jsonMode || request.ProjectPath != "a b" || request.ConfigPath != "machine.yaml" || !reflect.DeepEqual(request.TerminalArgs, []string{"--project", "forwarded", ""}) {
		t.Fatalf("request=%+v", request)
	}
}
