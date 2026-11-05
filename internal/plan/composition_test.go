package plan

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	v1 "github.com/octacian/backlot/api/v1"
	composition "github.com/octacian/backlot/internal/compose"
)

func compositionFormat(template string, values ...v1.Value) v1.Value {
	return v1.Value{Format: &v1.FormatValue{Template: template, Values: values}}
}
func TestCompositionPartialPlanningAndPrivacy(t *testing.T) {
	m := v1.Manifest{Inputs: map[string]v1.Input{"path": {}, "password": {Secret: true}}, Resources: map[string]v1.Resource{"origin": {Kind: "origin"}, "db": {Kind: "allocation"}}}
	config := resolvedConfig{Inputs: map[string]string{"path": "api a/%25/", "password": "private-canary"}}
	origin := v1.Value{Ref: &v1.Reference{Kind: "resource", Name: "origin", Field: "url"}}
	path := v1.Value{Ref: &v1.Reference{Kind: "input", Name: "path"}}
	value := v1.Value{URLPath: &v1.URLPathValue{Base: origin, Parts: []v1.Value{path}}}
	got, err := resolveValue(m, config, value, "", v1.PlanResponse{}, v1.Native)
	if err != nil || got.wire().Composition == nil || got.wire().Composition.URLPath.Base.Ref == nil || got.wire().Composition.URLPath.Parts[0].Literal == nil {
		t.Fatal("partial values lost", got, err)
	}
	for _, secret := range []v1.Value{
		{Ref: &v1.Reference{Kind: "input", Name: "password"}},
		{Ref: &v1.Reference{Kind: "resource", Name: "db", Field: "password"}},
		{Literal: func() *string { s := "private-canary"; return &s }(), Secret: true},
	} {
		v := compositionFormat("%s:%s", origin, compositionFormat("%s", secret))
		resolved, err := resolveValue(m, config, v, "", v1.PlanResponse{}, v1.Native)
		if err != nil {
			t.Fatal(err)
		}
		encoded, _ := json.Marshal(resolved.wire())
		if string(encoded) != `{"redacted":true}` || resolved.private().Composition == nil {
			t.Fatalf("privacy/provenance: %s", encoded)
		}
		if _, err := resolveArguments(m, config, []v1.Argument{{Value: v}}, "", v1.PlanResponse{}, v1.Native); err == nil {
			t.Fatal("sensitive argv accepted")
		}
	}
	// Known inputs can exceed a composition cap without changing old literal behavior.
	config.Inputs["path"] = strings.Repeat("x", 1<<20)
	tooLarge := compositionFormat("x%s", path)
	if _, err := resolveValue(m, config, tooLarge, "", v1.PlanResponse{}, v1.Native); err == nil || strings.Contains(err.Error(), config.Inputs["path"]) {
		t.Fatal("known cap not sanitized", err)
	}
	delete(config.Inputs, "path")
	if _, err := resolveValue(m, config, value, "", v1.PlanResponse{}, v1.Native); err == nil || !strings.Contains(err.Error(), "required value is missing") {
		t.Fatal("lost missing input diagnostic", err)
	}
}

func TestCompositionKnownProbeDestinationsFailOffline(t *testing.T) {
	for _, kind := range []string{"http", "tcp"} {
		t.Run(kind, func(t *testing.T) {
			project, machine := setupFixture(t)
			source := `version: backlot/v1
project: setup-test
tools: {shell: sh}
inputs: {token: {secret: true}}
components:
 web:
  kind: service
  runtime: native
  command: {tool: shell, args: [-c, 'true']}
  readiness:
   kind: KIND
   target: {format: {template: '%s', values: [{ref: {kind: input, name: token}}]}}
   timeout: 1s
scenes:
 dev: {lifetime: persistent, components: [web]}
`
			writeSetup(t, filepath.Join(project, "backlot.yaml"), strings.Replace(source, "KIND", kind, 1))
			setup := openSetup(t, v1.ProjectSetupRequest{ProjectPath: project, ConfigPath: machine, Edit: true})
			saveSetup(t, setup, map[string]string{"token": "private-canary invalid destination"}, nil)
			_, err := Resolve(v1.PlanRequest{Scene: "dev", ProjectPath: project, ConfigPath: machine})
			var detail *v1.PlanError
			if !errors.As(err, &detail) || detail.Code != "invalid_contract" || detail.Field != "components.web.readiness.target" || strings.Contains(err.Error(), "private-canary") {
				t.Fatal("known private destination was not rejected safely offline", err)
			}
		})
	}
}

func TestFilePathActualConsumerPlanning(t *testing.T) {
	base, part := "/base", "../outside/"
	raw := v1.Value{FilePath: &v1.FilePathValue{Base: v1.Value{Literal: &base}, Parts: []v1.Value{{Literal: &part}}}}
	private := raw
	private.Secret = true
	c := v1.Component{Runtime: v1.Container, Readiness: &v1.Probe{Kind: "command"}, Environment: v1.Environment{Assign: map[string]v1.Value{
		"PUBLIC": compositionFormat("path=%s", raw), "PRIVATE": compositionFormat("%s", private),
	}}}
	for _, shared := range []bool{true, false} {
		if !shared {
			c.Readiness = nil
		}
		values, err := resolveEnvironmentValues(v1.Manifest{}, resolvedConfig{}, c, "", v1.PlanResponse{})
		if err != nil {
			t.Fatal(err)
		}
		for _, key := range []string{"PUBLIC", "PRIVATE"} {
			value := values[key]
			if shared {
				if value.composition == nil || value.composition.Format == nil || value.composition.Format.Values[0].FilePath == nil || value.literal != nil {
					t.Fatal("shared consumer path prematurely folded", key, value)
				}
			} else if value.literal == nil {
				t.Fatal("known container did not fold", key, value)
			}
			if key == "PRIVATE" {
				wire := value.wire()
				if !wire.Redacted || wire.Composition != nil || wire.Literal != nil {
					t.Fatal("private path leaked", wire)
				}
				data, err := json.Marshal(value.private())
				var retained v1.PlannedValue
				if err != nil || json.Unmarshal(data, &retained) != nil || shared && retained.Composition == nil {
					t.Fatal("private construction lost in snapshot", err)
				}
			}
		}
	}
}

func TestPartialFilePathKnownPrivateInputBound(t *testing.T) {
	m := v1.Manifest{Inputs: map[string]v1.Input{"half": {Secret: true}}}
	half := "private-canary" + strings.Repeat("x", composition.MaxBytes/2-len("private-canary"))
	config := resolvedConfig{Inputs: map[string]string{"half": half}}
	input := v1.Value{Ref: &v1.Reference{Kind: "input", Name: "half"}}
	segment := compositionFormat("%s%s", input, input)
	unknown := v1.Value{Ref: &v1.Reference{Kind: "instance", Field: "id"}}
	root := "/"
	value := v1.Value{FilePath: &v1.FilePathValue{Base: v1.Value{Literal: &root}, Parts: []v1.Value{unknown, segment}}}
	_, err := resolveValue(m, config, value, "", v1.PlanResponse{}, v1.Native)
	if err == nil || strings.Contains(err.Error(), "private-canary") {
		t.Fatal("impossible known output not rejected privately", err)
	}
	value.FilePath.Parts = []v1.Value{segment, unknown}
	partial, err := resolveValue(m, config, value, "", v1.PlanResponse{}, v1.Native)
	if err != nil || !partial.wire().Redacted || partial.private().Composition == nil {
		t.Fatal("removable private prefix rejected or exposed", err)
	}
	parent := "../leaf"
	got, err := composition.Resolve(*partial.private().Composition, v1.Native, func(v v1.Value) (v1.Value, error) { return v1.Value{Literal: &parent}, nil })
	if err != nil || got.Literal == nil || *got.Literal != "/leaf" || !got.Secret {
		t.Fatal("private parent shortening", err)
	}
}

func TestSharedConsumerKnownBoundsBeforePreparation(t *testing.T) {
	for _, valid := range []bool{false, true} {
		t.Run(map[bool]string{false: "overflow", true: "exact-cap"}[valid], func(t *testing.T) {
			project, machine := setupFixture(t)
			size, template := composition.MaxBytes/2, "%s%s%s"
			if valid {
				size--
				template += "x"
			}
			half := "deferred-private-canary" + strings.Repeat("x", size-len("deferred-private-canary"))
			lit := func(s string) v1.Value { return v1.Value{Literal: &s} }
			input := v1.Value{Ref: &v1.Reference{Kind: "input", Name: "half"}}
			tail := v1.Value{FilePath: &v1.FilePathValue{Base: lit("/base"), Parts: []v1.Value{lit("..")}}}
			value := compositionFormat("%s", compositionFormat(template, input, input, compositionFormat("%s", tail)))
			port := v1.Value{Ref: &v1.Reference{Kind: "resource", Name: "port", Field: "port"}}
			unknown := v1.Value{FilePath: &v1.FilePathValue{Base: lit("/"), Parts: []v1.Value{input, port}}}
			identityURL := v1.Value{URLPath: &v1.URLPathValue{Base: lit("http://a/" + strings.Repeat("雪", 120000)), Parts: []v1.Value{{FilePath: &v1.FilePathValue{Base: lit(""), Parts: []v1.Value{}}}}}}
			image := lit("alpine:3.21")
			c := v1.Component{Kind: v1.Service, Runtime: v1.Container, Image: &image, Resources: []string{"port"}, Readiness: &v1.Probe{Kind: "command", Timeout: "1s", Command: &v1.Command{Tool: "shell", Args: v1.StringArguments("-c", "true")}}, Environment: v1.Environment{Assign: map[string]v1.Value{"PRIVATE": value, "UNKNOWN": unknown, "URL": identityURL}}}
			m := v1.Manifest{Version: v1.ManifestVersion, Project: "setup-test", Tools: map[string]string{"shell": "sh"}, Inputs: map[string]v1.Input{"half": {Secret: true}}, Resources: map[string]v1.Resource{"port": {Kind: "port"}}, Components: map[string]v1.Component{"web": c}, Scenes: map[string]v1.Scene{"dev": {Lifetime: v1.Persistent, Resources: []string{"port"}, Components: []string{"web"}}}}
			data, err := json.Marshal(m)
			if err != nil || len(data) > 1<<20 {
				t.Fatal("fixture manifest bounds", err)
			}
			writeSetup(t, filepath.Join(project, "backlot.yaml"), string(data))
			config, err := json.Marshal(v1.MachineConfig{Version: v1.ManifestVersion, Docker: &v1.DockerConfig{Endpoint: "unix://" + filepath.Join(t.TempDir(), "absent.sock")}})
			if err != nil {
				t.Fatal(err)
			}
			writeSetup(t, machine, string(config))
			setup := openSetup(t, v1.ProjectSetupRequest{ProjectPath: project, ConfigPath: machine, Edit: true})
			saveSetup(t, setup, map[string]string{"half": half}, nil)
			request := v1.PlanRequest{Scene: "dev", ProjectPath: project, ConfigPath: machine}
			result, planErr := Resolve(request)
			source, err := Locate(project)
			if err != nil {
				t.Fatal(err)
			}
			snapshot, prepareErr := Prepare(source, request)
			if !valid {
				for _, err := range []error{planErr, prepareErr} {
					var detail *v1.PlanError
					if !errors.As(err, &detail) || detail.Code != "invalid_composition" || strings.Contains(err.Error(), "deferred-private-canary") {
						t.Fatal("known overflow not rejected privately", err)
					}
				}
				return
			}
			if planErr != nil || prepareErr != nil {
				t.Fatal("valid contextual plan rejected", planErr, prepareErr)
			}
			public, err := json.Marshal(result)
			if err != nil || strings.Contains(string(public), "deferred-private-canary") || !result.Components[0].Environment["PRIVATE"].Redacted {
				t.Fatal("private plan exposed", err)
			}
			captured := snapshot.Secrets["web/environment/PRIVATE"].Composition
			if captured == nil || captured.Format.Values[0].Format.Values[2].Format.Values[0].FilePath == nil {
				t.Fatal("shared context folded during preparation")
			}
			pending := snapshot.Secrets["web/environment/UNKNOWN"].Composition
			if pending == nil || pending.FilePath.Parts[1].Ref == nil || pending.FilePath.Parts[1].Ref.Name != "port" {
				t.Fatal("allocation provenance lost")
			}
			if snapshot.PrivateProvenance == nil || !snapshot.PrivateProvenance.Present {
				t.Fatal("private provenance lost")
			}
			for _, runtime := range []v1.Runtime{v1.Native, v1.Container} {
				actual, err := composition.Resolve(*captured, runtime, func(v v1.Value) (v1.Value, error) { return v, nil })
				if err != nil || actual.Literal == nil || len(*actual.Literal) != composition.MaxBytes || !actual.Secret {
					t.Fatalf("actual consumer %s: %v", runtime, err)
				}
			}
		})
	}
}
