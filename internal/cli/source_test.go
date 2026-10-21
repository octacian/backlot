package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	v1 "github.com/octacian/backlot/api/v1"
	"github.com/octacian/backlot/internal/plan"
)

func TestSourceIdentitySurvivesHumanAndJSONErrors(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", home)
	project := t.TempDir()
	project, err := filepath.EvalSymlinks(project)
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(project, "backlot.json")
	document := `{"version":"backlot/v1","project":"setup","inputs":{"token":{"secret":true}},"tools":{"shell":"sh"},"components":{"test":{"kind":"job","runtime":"native","policy":"each-start","command":{"tool":"shell"}}},"scenes":{"test":{"lifetime":"disposable","components":["test"],"terminal_job":"test"}}}`
	machine := filepath.Join(t.TempDir(), "machine.json")
	put := func(path, data string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	put(file, document)
	put(machine, `{"version":"backlot/v1"}`)
	s, err := plan.OpenSetup(v1.ProjectSetupRequest{ProjectPath: project, ConfigPath: machine, Edit: true})
	if err != nil {
		t.Fatal(err)
	}
	enrolled, err := s.Save(context.Background(), v1.PrivateConfig{Version: v1.ManifestVersion, Inputs: map[string]string{"token": "private-canary-48"}}, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(enrolled.PrivatePath)
	if err != nil {
		t.Fatal(err)
	}
	registry := filepath.Join(filepath.Dir(filepath.Dir(enrolled.PrivatePath)), "projects.json")
	registryData, err := os.ReadFile(registry)
	if err != nil {
		t.Fatal(err)
	}
	imported := filepath.Join(t.TempDir(), "import.json")
	check := func(layer, path, code, field string, args ...string) {
		t.Helper()
		for _, jsonMode := range []bool{false, true} {
			var out, diag bytes.Buffer
			argv := append([]string{"backlot"}, args...)
			if jsonMode {
				argv = append(argv, "--json")
			}
			err := NewCommand(v1.VersionResponse{}, &out, &diag).Run(context.Background(), argv)
			var detail *v1.PlanError
			if !errors.As(err, &detail) {
				t.Fatalf("expected typed failure: %v", err)
			}
			if detail.SourceLayer != layer || detail.SourcePath != path || code != "" && detail.Code != code || field != "" && detail.Field != field {
				t.Fatalf("wrong source: %+v", detail)
			}
			if !strings.Contains(err.Error(), layer+" source "+path) {
				t.Fatalf("missing human identity: %v", err)
			}
			if jsonMode {
				var envelope v1.ErrorResponse
				if err := json.Unmarshal(out.Bytes(), &envelope); err != nil {
					t.Fatal(err)
				}
				if envelope.Error != *detail {
					t.Fatalf("JSON lost source: %+v", envelope.Error)
				}
			}
			all := out.String() + diag.String() + err.Error()
			for _, canary := range []string{"machine-canary-48", "private-canary-48", "invalid-value-canary-48"} {
				if strings.Contains(all, canary) {
					t.Fatal("secret value in output")
				}
			}
			if (layer == "private" || layer == "registry") && strings.Contains(all, "start daemon") {
				t.Fatal("managed file error has daemon remediation")
			}
		}
	}
	planArgs := []string{"plan", "test", "--project", project, "--config", machine}
	setupArgs := []string{"project", "setup", "--project", project, "--config", machine, "--from", imported}
	// Identical scalar errors must preserve field/code but distinguish source.
	invalid := `{"version":"backlot/v1","inputs":{"token":23},"tools":{"shell":"invalid-value-canary-48"}}`
	put(machine, invalid)
	check("machine", machine, "invalid_document", "$", planArgs...)
	put(machine, `{"version":"backlot/v1"}`)
	put(enrolled.PrivatePath, invalid)
	check("private", enrolled.PrivatePath, "invalid_document", "$.inputs.token", planArgs...)
	put(enrolled.PrivatePath, `{"version":"backlot/v1","inputs":{"token":"invalid-value-canary-48\u0000"}}`)
	check("private", enrolled.PrivatePath, "invalid_contract", "", planArgs...)
	put(enrolled.PrivatePath, string(original))
	if err := os.Chmod(enrolled.PrivatePath, 0644); err != nil {
		t.Fatal(err)
	}
	check("private", enrolled.PrivatePath, "permissions", "", planArgs...)
	if err := os.Chmod(enrolled.PrivatePath, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(enrolled.PrivatePath); err != nil {
		t.Fatal(err)
	}
	check("private", enrolled.PrivatePath, "project_config", "", planArgs...)
	put(enrolled.PrivatePath, string(original))
	if err := os.Chmod(filepath.Dir(enrolled.PrivatePath), 0755); err != nil {
		t.Fatal(err)
	}
	check("private", enrolled.PrivatePath, "permissions", "", planArgs...)
	if err := os.Chmod(filepath.Dir(enrolled.PrivatePath), 0700); err != nil {
		t.Fatal(err)
	}
	put(registry, `{"version":1,"projects":23}`)
	check("registry", registry, "invalid_document", "", planArgs...)
	put(registry, string(registryData))
	if err := os.Chmod(registry, 0644); err != nil {
		t.Fatal(err)
	}
	check("registry", registry, "permissions", "", planArgs...)
	if err := os.Chmod(registry, 0600); err != nil {
		t.Fatal(err)
	}
	put(file, strings.Replace(document, `"project":"setup"`, `"project":23`, 1))
	check("manifest", file, "invalid_document", "$.project", planArgs...)
	put(file, document)
	if os.Getuid() != 0 {
		if err := os.Chmod(file, 0000); err != nil {
			t.Fatal(err)
		}
		check("manifest", file, "missing_file", "", planArgs...)
		if err := os.Chmod(file, 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	check("manifest", file, "discovery", "", "plan", "test", "--project", file, "--config", machine)
	put(file, document)
	if err := os.Remove(machine); err != nil {
		t.Fatal(err)
	}
	check("machine", machine, "missing_file", "", planArgs...)
	put(machine, `{"version":"backlot/v1"}`)
	if os.Getuid() != 0 {
		if err := os.Chmod(machine, 0000); err != nil {
			t.Fatal(err)
		}
		check("machine", machine, "missing_file", "", planArgs...)
		if err := os.Chmod(machine, 0600); err != nil {
			t.Fatal(err)
		}
	}
	put(imported, invalid)
	check("import", imported, "invalid_document", "$.inputs.token", setupArgs...)
	put(imported, `{"version":"backlot/v1","inputs":{"token":"invalid-value-canary-48\u0000"}}`)
	check("import", imported, "invalid_contract", "", setupArgs...)
	if os.Getuid() != 0 {
		if err := os.Chmod(imported, 0000); err != nil {
			t.Fatal(err)
		}
		check("import", imported, "project_config", "", setupArgs...)
		if err := os.Chmod(imported, 0600); err != nil {
			t.Fatal(err)
		}
	}
	put(imported, `{"version":"backlot/v1","inputs":{"unknown":"invalid-value-canary-48"}}`)
	check("import", imported, "invalid_contract", "", setupArgs...)
	if err := os.Remove(imported); err != nil {
		t.Fatal(err)
	}
	check("import", imported, "project_config", "", setupArgs...)
	// Stdin has an explicit source name and the same strict/redacted envelope.
	stdin, err := os.CreateTemp(t.TempDir(), "stdin")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = stdin.Close() }()
	if _, err := stdin.WriteString(invalid); err != nil {
		t.Fatal(err)
	}
	old := os.Stdin
	os.Stdin = stdin
	defer func() { os.Stdin = old }()
	for _, mode := range []bool{false, true} {
		if _, err := stdin.Seek(0, 0); err != nil {
			t.Fatal(err)
		}
		var out bytes.Buffer
		args := []string{"backlot", "project", "setup", "--project", project, "--config", machine, "--from", "-"}
		if mode {
			args = append(args, "--json")
		}
		err := NewCommand(v1.VersionResponse{}, &out, &out).Run(context.Background(), args)
		var detail *v1.PlanError
		if !errors.As(err, &detail) || detail.SourceLayer != "import" || detail.SourcePath != "stdin" || strings.Contains(out.String()+err.Error(), "invalid-value-canary-48") {
			t.Fatalf("stdin source lost: %v", err)
		}
		if mode {
			var envelope v1.ErrorResponse
			if err := json.Unmarshal(out.Bytes(), &envelope); err != nil {
				t.Fatal(err)
			}
			if envelope.Error != *detail {
				t.Fatal("stdin JSON lost source")
			}
		}
	}
}
