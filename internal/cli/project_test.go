package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	v1 "github.com/octacian/backlot/api/v1"
	"github.com/octacian/backlot/internal/plan"
)

func TestProjectSetupJSONImportInspectionAndRevealBoundary(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", home)
	project := t.TempDir()
	manifest := `{"version":"backlot/v1","project":"setup","inputs":{"token":{"secret":true,"generate":"random-hex-32"}},"tools":{"shell":"sh"},"components":{"test":{"kind":"job","runtime":"native","policy":"each-start","command":{"tool":"shell"}}},"scenes":{"test":{"lifetime":"disposable","components":["test"],"terminal_job":"test"}}}`
	if err := os.WriteFile(filepath.Join(project, "backlot.json"), []byte(manifest), 0600); err != nil {
		t.Fatal(err)
	}
	imported := filepath.Join(t.TempDir(), "private.yaml")
	if err := os.WriteFile(imported, []byte("version: backlot/v1\ninputs: {token: CLI-sensitive-marker-48}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	run := func(ok bool, args ...string) []byte {
		t.Helper()
		var out, diag bytes.Buffer
		err := NewCommand(v1.VersionResponse{}, &out, &diag).Run(context.Background(), append([]string{"backlot", "project", "setup", "--project", project, "--json"}, args...))
		if (err == nil) != ok {
			t.Fatalf("unexpected setup result: %v", err)
		}
		var document json.RawMessage
		if err := json.Unmarshal(out.Bytes(), &document); err != nil {
			t.Fatal(err)
		}
		return out.Bytes()
	}
	first := run(true, "--from", imported)
	if strings.Contains(string(first), "CLI-sensitive-marker-48") {
		t.Fatal("ordinary setup disclosed value")
	}
	var setup v1.ProjectSetupResponse
	if err := json.Unmarshal(first, &setup); err != nil {
		t.Fatal(err)
	}
	if !setup.Registered || !setup.Changed {
		t.Fatal("import failed")
	}
	inspect := run(true)
	var inspected v1.ProjectSetupResponse
	if err := json.Unmarshal(inspect, &inspected); err != nil {
		t.Fatal(err)
	}
	if inspected.Changed || inspected.PrivatePath != setup.PrivatePath {
		t.Fatal("inspection mutated")
	}
	if !strings.Contains(string(run(true, "--dangerously-reveal-secrets")), "CLI-sensitive-marker-48") {
		t.Fatal("explicit local reveal absent")
	}
	run(false, "--dangerously-reveal-secrets", "--generate", "token")
	run(false, "--token", "raw-cli-value")
	run(false, "--remove-input", "missing")
	if err := os.WriteFile(imported, []byte("inputs: {token: CLI-sensitive-marker-48}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	run(false, "--from", imported)
	generated := run(true, "--generate", "token")
	if strings.Contains(string(generated), "revealed_value") {
		t.Fatal("generation disclosed value")
	}
}

func TestSetupResponseFailureReportsCommittedState(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", home)
	project := t.TempDir()
	document := `{"version":"backlot/v1","project":"setup","inputs":{"token":{"secret":true,"generate":"random-hex-32"}},"tools":{"shell":"sh"},"components":{"test":{"kind":"job","runtime":"native","policy":"each-start","command":{"tool":"shell"}}},"scenes":{"test":{"lifetime":"disposable","components":["test"],"terminal_job":"test"}}}`
	if err := os.WriteFile(filepath.Join(project, "backlot.json"), []byte(document), 0600); err != nil {
		t.Fatal(err)
	}
	err := NewCommand(v1.VersionResponse{}, failingWriter{}, io.Discard).Run(context.Background(), []string{"backlot", "project", "setup", "--project", project, "--generate", "token", "--json"})
	var detail *v1.PlanError
	if !errors.As(err, &detail) || detail.Code != "setup_committed_uncertain" || !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("missing committed response failure: %v", err)
	}
	s, err := plan.OpenSetup(v1.ProjectSetupRequest{ProjectPath: project})
	if err != nil {
		t.Fatal(err)
	}
	if !s.Inspect().Registered {
		t.Fatal("response failure rolled back committed setup")
	}
}
