package plan

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	v1 "github.com/octacian/backlot/api/v1"
)

const setupManifest = `version: backlot/v1
project: setup-test
tools: {shell: sh, unused: /unavailable/unselected}
inputs:
 token: {secret: true, description: Local credential, generate: random-hex-32}
 unused: {}
components:
 check:
  kind: job
  runtime: native
  policy: each-start
  command: {tool: shell, args: [-c, 'true']}
  environment: {assign: {TOKEN: {ref: {kind: input, name: token}}}}
scenes:
 test: {lifetime: disposable, components: [check], terminal_job: check}
`

func setupFixture(t *testing.T) (string, string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", home)
	project := t.TempDir()
	project, err := filepath.EvalSymlinks(project)
	if err != nil {
		t.Fatal(err)
	}
	writeSetup(t, filepath.Join(project, "backlot.yaml"), setupManifest)
	machine := filepath.Join(t.TempDir(), "machine.json")
	writeSetup(t, machine, `{"version":"backlot/v1","inputs":{"token":"machine"}}`)
	return project, machine
}
func writeSetup(t *testing.T, path, data string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
}
func openSetup(t *testing.T, request v1.ProjectSetupRequest) *Setup {
	t.Helper()
	s, err := OpenSetup(request)
	if err != nil {
		t.Fatal(err)
	}
	return s
}
func saveSetup(t *testing.T, s *Setup, inputs, tools map[string]string) v1.ProjectSetupResponse {
	t.Helper()
	r, err := s.Save(context.Background(), v1.PrivateConfig{Version: v1.ManifestVersion, Inputs: inputs, Tools: tools}, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestPrivateCompositionSelectionAndStrictLayers(t *testing.T) {
	project, machine := setupFixture(t)
	request := v1.PlanRequest{Scene: "test", ProjectPath: project, ConfigPath: machine}
	// Unused missing input/tool does not block selected work before enrollment.
	if _, err := Resolve(request); err != nil {
		t.Fatal(err)
	}
	setup := openSetup(t, v1.ProjectSetupRequest{ProjectPath: project, ConfigPath: machine, Edit: true})
	result := saveSetup(t, setup, map[string]string{"token": "test-sensitive-marker-48", "unused": "kept"}, nil)
	if !result.Registered || !result.Changed {
		t.Fatal("not enrolled")
	}
	source, err := Locate(project)
	if err != nil {
		t.Fatal(err)
	}
	snap, err := Prepare(source, request)
	if err != nil {
		t.Fatal(err)
	}
	if snap.Secrets["check/environment/TOKEN"].Literal == nil || *snap.Secrets["check/environment/TOKEN"].Literal != "test-sensitive-marker-48" {
		t.Fatal("private precedence")
	}
	data, _ := json.Marshal(snap.Plan)
	if strings.Contains(string(data), "test-sensitive-marker-48") {
		t.Fatal("secret escaped")
	}
	before, _ := os.ReadFile(result.PrivatePath)
	inspection := openSetup(t, v1.ProjectSetupRequest{ProjectPath: project, ConfigPath: machine}).Inspect()
	after, _ := os.ReadFile(result.PrivatePath)
	if inspection.Changed || string(before) != string(after) {
		t.Fatal("inspection mutated")
	}
	reveal := openSetup(t, v1.ProjectSetupRequest{ProjectPath: project, ConfigPath: machine, RevealSecrets: true}).Inspect()
	if reveal.Inputs[0].RevealedValue == nil || *reveal.Inputs[0].RevealedValue != "test-sensitive-marker-48" {
		t.Fatal("explicit secret inspection")
	}
	for _, bad := range []string{`{"version":"backlot/v1","inputs":{"token":"bad\u0000"}}`, `{"version":"backlot/v1","inputs":{"typo":"bad"}}`, `{"version":"backlot/v1","tools":{"shell":""}}`, `{"version":"backlot/v1","docker":{"endpoint":"tcp://unsafe"}}`} {
		writeSetup(t, machine, bad)
		if _, err := Resolve(request); err == nil {
			t.Fatal("invalid overridden/unused machine layer accepted")
		}
	}
}

func TestPrivateDriftAndProjectRelativeTools(t *testing.T) {
	project, machine := setupFixture(t)
	tool := filepath.Join(project, "custom")
	writeSetup(t, tool, "#!/bin/sh\nexit 0\n")
	if err := os.Chmod(tool, 0700); err != nil {
		t.Fatal(err)
	}
	saveSetup(t, openSetup(t, v1.ProjectSetupRequest{ProjectPath: project, ConfigPath: machine, Edit: true}), map[string]string{"token": "first", "unused": "ok"}, map[string]string{"shell": "./custom"})
	req := v1.PlanRequest{Scene: "test", ProjectPath: project, ConfigPath: machine}
	first, err := Resolve(req)
	if err != nil {
		t.Fatal(err)
	}
	if first.Components[0].Executable != tool {
		t.Fatal("private relative executable resolved beside private file")
	}
	writeSetup(t, machine, "{\n\"version\":\"backlot/v1\",\"inputs\":{\"token\":\"masked-change\"}}")
	next, err := Resolve(req)
	if err != nil {
		t.Fatal(err)
	}
	if first.ConfigDigest == next.ConfigDigest || ConfigurationDrift(first, next) {
		t.Fatal("masked source drift conflated with effective drift")
	}
	saveSetup(t, openSetup(t, v1.ProjectSetupRequest{ProjectPath: project, ConfigPath: machine, Edit: true}), map[string]string{"token": "second"}, nil)
	current, err := Resolve(req)
	if err != nil {
		t.Fatal(err)
	}
	if !ConfigurationDrift(first, current) {
		t.Fatal("private effective change hidden")
	}
}

func TestRetiredPrivateKeysRequireExplicitAtomicRepair(t *testing.T) {
	for _, kind := range []string{"input", "tool"} {
		t.Run(kind, func(t *testing.T) {
			project, machine := setupFixture(t)
			s := openSetup(t, v1.ProjectSetupRequest{ProjectPath: project, ConfigPath: machine, Edit: true})
			initial := saveSetup(t, s, map[string]string{"token": "old", "unused": "retained"}, map[string]string{"unused": "custom"})
			path := filepath.Join(project, "backlot.yaml")
			changed := setupManifest
			if kind == "input" {
				changed = strings.ReplaceAll(changed, "token", "renamed")
				writeSetup(t, machine, `{"version":"backlot/v1"}`)
			} else {
				changed = strings.Replace(changed, "unused: /unavailable/unselected", "renamed: /unavailable/unselected", 1)
			}
			writeSetup(t, path, changed)
			if _, err := Resolve(v1.PlanRequest{Scene: "test", ProjectPath: project, ConfigPath: machine}); err == nil {
				t.Fatal("ordinary stale read succeeded")
			}
			if _, err := OpenSetup(v1.ProjectSetupRequest{ProjectPath: project, ConfigPath: machine, RevealSecrets: true}); err == nil {
				t.Fatal("reveal bypassed strict membership")
			}
			edit := openSetup(t, v1.ProjectSetupRequest{ProjectPath: project, ConfigPath: machine, Edit: true})
			update := v1.PrivateConfig{Version: v1.ManifestVersion}
			var inputs, tools []string
			if kind == "input" {
				inputs = []string{"token"}
				update.Inputs = map[string]string{"renamed": "new"}
			} else {
				tools = []string{"unused"}
				update.Tools = map[string]string{"renamed": "custom"}
			}
			if _, err := edit.Save(context.Background(), v1.PrivateConfig{Version: v1.ManifestVersion}, nil, nil, nil); err == nil {
				t.Fatal("unremoved stale key carried forward")
			}
			if _, err := edit.Save(context.Background(), update, nil, append(inputs, inputs...), append(tools, tools...)); err == nil {
				t.Fatal("duplicate removal accepted")
			}
			if current := openSetup(t, v1.ProjectSetupRequest{ProjectPath: project, ConfigPath: machine, Edit: true}).Inspect(); current.PrivatePath != initial.PrivatePath {
				t.Fatal("failed edit changed active generation")
			}
			repaired, err := edit.Save(context.Background(), update, nil, inputs, tools)
			if err != nil {
				t.Fatal(err)
			}
			if repaired.PrivatePath == initial.PrivatePath {
				t.Fatal("immutable generation overwritten")
			}
			if _, err := Resolve(v1.PlanRequest{Scene: "test", ProjectPath: project, ConfigPath: machine}); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestSetupRemovalGenerationConflictsAndCompleteness(t *testing.T) {
	project, machine := setupFixture(t)
	s := openSetup(t, v1.ProjectSetupRequest{ProjectPath: project, ConfigPath: machine, Edit: true})
	initial := saveSetup(t, s, map[string]string{"token": "old", "unused": "keep"}, nil)
	update := v1.PrivateConfig{Version: v1.ManifestVersion}
	for _, test := range []struct {
		generate, removeInput, removeTool []string
		inputs                            map[string]string
	}{
		{removeInput: []string{"missing"}}, {removeTool: []string{"shell"}}, {removeInput: []string{"token"}, inputs: map[string]string{"token": "replacement"}}, {generate: []string{"unused"}}, {generate: []string{"token", "token"}}, {generate: []string{"token"}, inputs: map[string]string{"token": "literal"}}, {removeInput: []string{"token"}, generate: []string{"token"}},
	} {
		edit := openSetup(t, v1.ProjectSetupRequest{ProjectPath: project, ConfigPath: machine, Edit: true})
		update.Inputs = test.inputs
		if _, err := edit.Save(context.Background(), update, test.generate, test.removeInput, test.removeTool); err == nil {
			t.Fatal("invalid mutation accepted")
		}
		if edit.Inspect().PrivatePath != initial.PrivatePath {
			t.Fatal("invalid mutation published")
		}
	}
	edit := openSetup(t, v1.ProjectSetupRequest{ProjectPath: project, ConfigPath: machine, Edit: true})
	exposed, err := edit.Save(context.Background(), v1.PrivateConfig{Version: v1.ManifestVersion}, nil, []string{"token"}, nil)
	if err != nil || !exposed.Inputs[0].Configured {
		t.Fatal("removal did not expose lower machine value", err)
	}
	edit = openSetup(t, v1.ProjectSetupRequest{ProjectPath: project, ConfigPath: machine, Edit: true})
	generated, err := edit.Save(context.Background(), v1.PrivateConfig{Version: v1.ManifestVersion}, []string{"token"}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(generated.PrivatePath)
	if err != nil {
		t.Fatal(err)
	}
	var private v1.PrivateConfig
	if err := json.Unmarshal(data, &private); err != nil {
		t.Fatal(err)
	}
	b, err := hex.DecodeString(private.Inputs["token"])
	if err != nil || len(b) != 32 || private.Inputs["unused"] != "keep" {
		t.Fatal("generation shape or unspecified preservation")
	}
	// Source drift after collecting answers must not overwrite active setup.
	stale := openSetup(t, v1.ProjectSetupRequest{ProjectPath: project, ConfigPath: machine, Edit: true})
	writeSetup(t, machine, `{"version":"backlot/v1"}`)
	if _, err := stale.Save(context.Background(), v1.PrivateConfig{Version: v1.ManifestVersion}, nil, nil, nil); err == nil {
		t.Fatal("source conflict ignored")
	}
	// An incomplete removal without a lower value must fail atomically.
	fresh := openSetup(t, v1.ProjectSetupRequest{ProjectPath: project, ConfigPath: machine, Edit: true})
	if _, err := fresh.Save(context.Background(), v1.PrivateConfig{Version: v1.ManifestVersion}, nil, []string{"token"}, nil); err == nil {
		t.Fatal("incomplete removal accepted")
	}
	if fresh.Inspect().PrivatePath != generated.PrivatePath {
		t.Fatal("failed removal changed generation")
	}
}

func TestComposedDigestCapturesSelectedPATHResolution(t *testing.T) {
	project, machine := setupFixture(t)
	saveSetup(t, openSetup(t, v1.ProjectSetupRequest{ProjectPath: project, ConfigPath: machine, Edit: true}), map[string]string{"token": "private-path-marker", "unused": "configured"}, nil)
	req := v1.PlanRequest{Scene: "test", ProjectPath: project, ConfigPath: machine}
	var plans []v1.PlanResponse
	for i := 0; i < 2; i++ {
		dir := t.TempDir()
		path := filepath.Join(dir, "sh")
		writeSetup(t, path, "#!/bin/sh\nexit 0\n")
		if err := os.Chmod(path, 0700); err != nil {
			t.Fatal(err)
		}
		t.Setenv("PATH", dir)
		result, err := Resolve(req)
		if err != nil {
			t.Fatal(err)
		}
		plans = append(plans, result)
	}
	if plans[0].Components[0].Executable == plans[1].Components[0].Executable || !ConfigurationDrift(plans[0], plans[1]) {
		t.Fatal("selected PATH tool replacement was absent from effective drift")
	}
}

func TestDeclarationRemediationIdentifiesLayer(t *testing.T) {
	for _, kind := range []string{"input", "tool"} {
		t.Run(kind, func(t *testing.T) {
			project, machine := setupFixture(t)
			planRequest := v1.PlanRequest{ProjectPath: project, ConfigPath: machine, Scene: "test"}
			// Retire a previously valid declaration in an enrolled private generation.
			setup := openSetup(t, v1.ProjectSetupRequest{ProjectPath: project, ConfigPath: machine, Edit: true})
			inputs, tools := map[string]string{"unused": "private"}, map[string]string{}
			if kind == "tool" {
				tools["unused"] = "sh"
			}
			saved := saveSetup(t, setup, inputs, tools)
			updated := strings.Replace(setupManifest, " unused: {}\n", "", 1)
			if kind == "tool" {
				updated = strings.Replace(setupManifest, ", unused: /unavailable/unselected", "", 1)
			}
			writeSetup(t, filepath.Join(project, "backlot.yaml"), updated)
			assertPrivate := func(err error) {
				t.Helper()
				if err == nil || !strings.Contains(err.Error(), "--remove-"+kind) || strings.Contains(err.Error(), "edit selected machine file") {
					t.Fatalf("private guidance: %v", err)
				}
			}
			_, err := Resolve(planRequest)
			assertPrivate(err)
			_, err = OpenSetup(v1.ProjectSetupRequest{ProjectPath: project, ConfigPath: machine})
			assertPrivate(err)
			privateBefore, err := os.ReadFile(saved.PrivatePath)
			if err != nil {
				t.Fatal(err)
			}
			// The same undeclared name now occurs only in the machine file. A private
			// removal cannot repair it, and setup must not mutate either source.
			writeSetup(t, saved.PrivatePath, `{"version":"backlot/v1"}`)
			machineData := `{"version":"backlot/v1","inputs":{"token":"machine","unused":"machine"}}`
			if kind == "tool" {
				machineData = `{"version":"backlot/v1","inputs":{"token":"machine"},"tools":{"unused":"sh"}}`
			}
			writeSetup(t, machine, machineData)
			assertMachine := func(err error) {
				t.Helper()
				if err == nil || !strings.Contains(err.Error(), "edit selected machine file "+machine) || !strings.Contains(err.Error(), "undeclared "+kind) || strings.Contains(err.Error(), "--remove-") {
					t.Fatalf("machine guidance: %v", err)
				}
			}
			_, err = Resolve(planRequest)
			assertMachine(err)
			removal := v1.ProjectSetupRequest{ProjectPath: project, ConfigPath: machine, Edit: true}
			if kind == "input" {
				removal.RemoveInputs = []string{"unused"}
			} else {
				removal.RemoveTools = []string{"unused"}
			}
			_, err = OpenSetup(removal)
			assertMachine(err)
			actual, err := os.ReadFile(machine)
			if err != nil || string(actual) != machineData {
				t.Fatal("setup changed machine config", err)
			}
			actual, err = os.ReadFile(saved.PrivatePath)
			if err != nil || string(actual) != `{"version":"backlot/v1"}` {
				t.Fatal("setup changed private config", err)
			}
			// Restore the deliberately edited immutable fixture for consistent teardown.
			if err := os.WriteFile(saved.PrivatePath, privateBefore, 0600); err != nil {
				t.Fatal(err)
			}
		})
	}
}
