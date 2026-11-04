package plan

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	v1 "github.com/octacian/backlot/api/v1"
)

func compositionFormat(template string, values ...v1.Value) v1.Value {
	return v1.Value{Format: &v1.FormatValue{Template: template, Values: values}}
}
func TestCompositionPartialPlanningAndPrivacy(t *testing.T) {
	m := v1.Manifest{Inputs: map[string]v1.Input{"path": {}, "password": {Secret: true}}, Resources: map[string]v1.Resource{"origin": {Kind: "origin"}, "db": {Kind: "allocation"}}}
	config := resolvedConfig{Inputs: map[string]string{"path": "api a/%25/", "password": "private-canary"}}
	origin := v1.Value{Ref: &v1.Reference{Kind: "resource", Name: "origin", Field: "url"}}
	path := v1.Value{Ref: &v1.Reference{Kind: "input", Name: "path"}}
	value := v1.Value{URLPath: &v1.URLPathValue{Base: origin, Path: path}}
	got, err := resolveValue(m, config, value, "", v1.PlanResponse{})
	if err != nil || got.wire().Composition == nil || got.wire().Composition.URLPath.Base.Ref == nil || got.wire().Composition.URLPath.Path.Literal == nil {
		t.Fatal("partial values lost", got, err)
	}
	for _, secret := range []v1.Value{
		{Ref: &v1.Reference{Kind: "input", Name: "password"}},
		{Ref: &v1.Reference{Kind: "resource", Name: "db", Field: "password"}},
		{Literal: func() *string { s := "private-canary"; return &s }(), Secret: true},
	} {
		v := compositionFormat("%s:%s", origin, compositionFormat("%s", secret))
		resolved, err := resolveValue(m, config, v, "", v1.PlanResponse{})
		if err != nil {
			t.Fatal(err)
		}
		encoded, _ := json.Marshal(resolved.wire())
		if string(encoded) != `{"redacted":true}` || resolved.private().Composition == nil {
			t.Fatalf("privacy/provenance: %s", encoded)
		}
		if _, err := resolveArguments(m, config, []v1.Argument{{Value: v}}, "", v1.PlanResponse{}); err == nil {
			t.Fatal("sensitive argv accepted")
		}
	}
	// Known inputs can exceed a composition cap without changing old literal behavior.
	config.Inputs["path"] = strings.Repeat("x", 1<<20)
	tooLarge := compositionFormat("x%s", path)
	if _, err := resolveValue(m, config, tooLarge, "", v1.PlanResponse{}); err == nil || strings.Contains(err.Error(), config.Inputs["path"]) {
		t.Fatal("known cap not sanitized", err)
	}
	delete(config.Inputs, "path")
	if _, err := resolveValue(m, config, value, "", v1.PlanResponse{}); err == nil || !strings.Contains(err.Error(), "required value is missing") {
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
