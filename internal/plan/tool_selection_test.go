package plan

import (
	"bytes"
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	v1 "github.com/octacian/backlot/api/v1"
)

func TestToolSelectionCapturedLayers(t *testing.T) {
	project, machine := setupFixture(t)
	saveSetup(t, openSetup(t, v1.ProjectSetupRequest{ProjectPath: project, ConfigPath: machine, Edit: true}), map[string]string{"token": "private-canary-51", "unused": "not-selected"}, nil)
	source, err := Locate(project)
	if err != nil {
		t.Fatal(err)
	}
	request := v1.PlanRequest{ProjectPath: project, ConfigPath: machine, Scene: "test"}
	var originalDigest string
	for _, layer := range []string{"manifest", "machine", "private"} {
		if layer == "machine" {
			writeSetup(t, machine, `{"version":"backlot/v1","tools":{"shell":"sh"}}`)
		}
		if layer == "private" {
			saveSetup(t, openSetup(t, v1.ProjectSetupRequest{ProjectPath: project, ConfigPath: machine, Edit: true}), nil, map[string]string{"shell": "sh"})
		}
		snap, err := Prepare(source, request)
		if err != nil {
			t.Fatal(err)
		}
		if len(snap.ToolSelections) != 1 {
			t.Fatal("missing selected tool observation")
		}
		selected := snap.ToolSelections[0]
		expectedPath := source.ManifestPath
		if layer == "machine" {
			expectedPath = machine
		}
		if layer == "private" {
			expectedPath = snap.PrivateProvenance.Path
		}
		if selected.SourceLayer != layer || selected.SourcePath != expectedPath || selected.Candidate != "sh" || selected.Component != "check" || selected.Role != "command" || selected.Executable != snap.Plan.Components[0].Executable {
			t.Fatalf("wrong observation: %+v", selected)
		}
		if originalDigest == "" {
			originalDigest = snap.Plan.EffectiveConfigDigest
		}
		if originalDigest != snap.Plan.EffectiveConfigDigest {
			t.Fatal("equal-valued source override changed effective digest")
		}
		offline, err := Resolve(request)
		if err != nil || !reflect.DeepEqual(offline, snap.Plan) {
			t.Fatal("observations changed public plan", err)
		}
		before, err := json.Marshal(snap)
		if err != nil {
			t.Fatal(err)
		}
		snap.ToolSelections = nil
		after, err := json.Marshal(snap)
		if err != nil || !bytes.Equal(before, after) {
			t.Fatal("observations persisted", err)
		}
	}
	// Replacing a declaration after capture cannot attribute the lookup to new bytes.
	captured, err := loadLayers(source, machine, false, false)
	if err != nil {
		t.Fatal(err)
	}
	writeSetup(t, captured.private.Provenance.Path, `{"version":"backlot/v1","tools":{"shell":"missing-new-generation"}}`)
	var snap Snapshot
	if _, err := tool(captured, "shell", project, "check", "command", &snap); err != nil || snap.ToolSelections[0].Candidate != "sh" {
		t.Fatal("lookup reread captured source", err)
	}
}

func TestToolLookupFailureDetails(t *testing.T) {
	project, machine := setupFixture(t)
	source, err := Locate(project)
	if err != nil {
		t.Fatal(err)
	}
	captured, err := loadLayers(source, machine, false, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, candidate := range []string{"missing-tool-51", "./missing-tool-51", filepath.Join(project, "missing-tool-51"), "missing\ncontrol-51"} {
		for _, role := range []string{"command", "readiness"} {
			for _, layer := range []string{"manifest", "machine", "private"} {
				l := captured
				l.manifest.Tools = map[string]string{"shell": candidate}
				l.machine.Tools = nil
				l.private.Config.Tools = nil
				declaration := source.ManifestPath
				if layer == "machine" {
					l.machine.Tools = map[string]string{"shell": candidate}
					declaration = machine
				}
				if layer == "private" {
					l.private.Config.Tools = map[string]string{"shell": candidate}
					l.private.Provenance.Path = filepath.Join(project, "private\nsource.json")
					declaration = l.private.Provenance.Path
				}
				for _, daemon := range []bool{false, true} {
					var snap *Snapshot
					context := "caller/offline"
					if daemon {
						snap = &Snapshot{}
						context = "daemon/runtime"
					}
					_, err := tool(l, "shell", project, "check", role, snap)
					var detail *v1.PlanError
					attempted := candidate
					if strings.HasPrefix(candidate, "./") {
						attempted = filepath.Join(project, candidate)
					}
					if !errors.As(err, &detail) || detail.Code != "missing_tool" || detail.Field != "tools.shell" || detail.SourceLayer != layer || detail.SourcePath != declaration || !strings.Contains(detail.Message, strconv.Quote(attempted)) || !strings.Contains(detail.Message, context+" check "+role) {
						t.Fatalf("wrong failure: %+v", detail)
					}
					if strings.Contains(detail.Error(), "\n") || strings.Contains(detail.Error(), "$PATH") || strings.Contains(detail.Error(), "exec:") {
						t.Fatal("unquoted or raw diagnostic", detail.Error())
					}
				}
			}
		}
	}
}
