package manifest

import (
	"encoding/json"
	"strings"
	"testing"

	v1 "github.com/octacian/backlot/api/v1"
)

func composed(v v1.Value) v1.Value {
	return v1.Value{Format: &v1.FormatValue{Template: "%s", Values: []v1.Value{v}}}
}
func TestCompositionRecursiveBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*v1.Manifest)
		valid  bool
	}{
		{"file secret discarded argv", func(m *v1.Manifest) {
			secret := v1.Value{Literal: func() *string { s := "private-canary"; return &s }(), Secret: true}
			m.Components["web"].Command.Args = []v1.Argument{{Value: filesystem(secret)}}
		}, false},
		{"file CA discarded argv", func(m *v1.Manifest) {
			m.Components["web"].Command.Args = []v1.Argument{{Value: filesystem(m.Environments["trust"].Assign["CA"])}}
		}, false},
		{"file unattached discarded operand", func(m *v1.Manifest) {
			m.Resources["hidden"] = v1.Resource{Kind: "directory"}
			m.Environments["trust"].Assign["HIDDEN"] = filesystem(v1.Value{Ref: &v1.Reference{Kind: "resource", Name: "hidden", Field: "path"}})
		}, false},
		{"file cannot establish publication identity", func(m *v1.Manifest) {
			p := m.Scenes["dev"].Publish
			value := filesystem(*p.Probe.Target)
			p.Probe.Target = &value
		}, false},
		{"nested CA environment", func(m *v1.Manifest) { e := m.Environments["trust"]; e.Assign["CA"] = composed(e.Assign["CA"]) }, true},
		{"nested unknown ref", func(m *v1.Manifest) {
			e := m.Environments["trust"]
			e.Assign["CA"] = composed(v1.Value{Ref: &v1.Reference{Kind: "resource", Name: "missing", Field: "port"}})
		}, false},
		{"nested CA argv", func(m *v1.Manifest) {
			m.Components["web"].Command.Args = []v1.Argument{{Value: composed(m.Environments["trust"].Assign["CA"])}}
		}, false},
		{"outer sensitive argv", func(m *v1.Manifest) {
			v := composed(v1.Value{Literal: func() *string { s := "private-canary"; return &s }()})
			v.Secret = true
			m.Components["web"].Command.Args = []v1.Argument{{Value: v}}
		}, false},
		{"nested secret input argv", func(m *v1.Manifest) {
			m.Inputs = map[string]v1.Input{"password": {Secret: true}}
			m.Components["web"].Command.Args = []v1.Argument{{Value: composed(v1.Value{Ref: &v1.Reference{Kind: "input", Name: "password"}})}}
		}, false},
		{"nested CA unmounted container", func(m *v1.Manifest) {
			c := m.Components["web"]
			c.Runtime = v1.Container
			c.Command = nil
			image := "alpine:3.21"
			c.Image = &v1.Value{Literal: &image}
			c.Ports["http"] = v1.ServicePort{Resource: "port", ContainerPort: 8080}
			m.Components["web"] = c
			e := m.Environments["trust"]
			e.Assign["CA"] = composed(e.Assign["CA"])
		}, false},
		{"origin hostname", func(m *v1.Manifest) {
			e := m.Environments["trust"]
			e.Assign["NAME"] = composed(v1.Value{Ref: &v1.Reference{Kind: "resource", Name: "origin", Field: "hostname"}})
		}, true},
		{"unattached hidden ref", func(m *v1.Manifest) {
			m.Resources["hidden"] = v1.Resource{Kind: "port"}
			e := m.Environments["trust"]
			e.Assign["HIDDEN"] = composed(v1.Value{Ref: &v1.Reference{Kind: "resource", Name: "hidden", Field: "port"}})
			c := m.Components["web"]
			c.Environment = v1.Environment{Assign: map[string]v1.Value{"HIDDEN": {Literal: func() *string { s := "override"; return &s }()}}}
			m.Components["web"] = c
		}, false},
		{"origin readiness cycle", func(m *v1.Manifest) {
			m.Components["web"].Readiness.Target = func() *v1.Value {
				v := composed(v1.Value{Ref: &v1.Reference{Kind: "resource", Name: "origin", Field: "url"}})
				return &v
			}()
			m.Components["web"].Readiness.Kind = "http"
		}, false},
		{"origin command readiness cycle", func(m *v1.Manifest) {
			m.Components["web"].Readiness.Kind = "command"
			m.Components["web"].Readiness.Target = nil
			m.Components["web"].Readiness.Command = &v1.Command{Tool: "go", Args: []v1.Argument{{Value: composed(v1.Value{Ref: &v1.Reference{Kind: "resource", Name: "origin", Field: "url"}})}}}
		}, false},
		{"composed publication", func(m *v1.Manifest) {
			s := m.Scenes["dev"]
			base := *s.Publish.Probe.Target
			path := "/api/ready"
			s.Publish.Probe.Target = &v1.Value{URLPath: &v1.URLPathValue{Base: base, Parts: []v1.Value{{Literal: &path}}}}
		}, true},
		{"origin only in publication path", func(m *v1.Manifest) {
			s := m.Scenes["dev"]
			base := "https://evil.invalid"
			s.Publish.Probe.Target = &v1.Value{URLPath: &v1.URLPathValue{Base: v1.Value{Literal: &base}, Parts: []v1.Value{*s.Publish.Probe.Target}}}
		}, false},
		{"image composition", func(m *v1.Manifest) {
			c := m.Components["web"]
			c.Runtime = v1.Container
			c.Command = nil
			image := "alpine:3.21"
			v := composed(v1.Value{Literal: &image})
			c.Image = &v
			c.Ports["http"] = v1.ServicePort{Resource: "port", ContainerPort: 8080}
			m.Components["web"] = c
		}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var m v1.Manifest
			if err := Decode([]byte(caProject), ".yaml", &m); err != nil {
				t.Fatal(err)
			}
			tc.mutate(&m)
			err := Validate(m)
			if (err == nil) != tc.valid || err != nil && strings.Contains(err.Error(), "private-canary") {
				t.Fatalf("valid=%v: %v", tc.valid, err)
			}
		})
	}
}
func TestArgumentStrictUnionDecode(t *testing.T) {
	for _, source := range []string{`{"tool":"go","args":[23]}`, `{"tool":"go","args":[{"format":{"literal":"%s","values":[{"literal":"private-canary"}]}}]}`, `{"tool":"go","args":[{"format":{"template":"%s","values":[{"literal":"private-canary","extra":true}]}}]}`, `{"tool":"go","args":[{"format":{"values":[]}}]}`} {
		var c v1.Command
		if err := Decode([]byte(source), ".json", &c); err == nil || strings.Contains(err.Error(), "private-canary") {
			t.Fatal("strict argv decode", err)
		}
	}
	var c v1.Command
	if err := Decode([]byte(`tool: go
args: [plain, {format: {template: '%s%%', values: [{literal: 'a b'}]}}]`), ".yaml", &c); err != nil {
		t.Fatal(err)
	}
}

func filesystem(base v1.Value) v1.Value {
	part := ".."
	return v1.Value{FilePath: &v1.FilePathValue{Base: base, Parts: []v1.Value{{Literal: &part}}}}
}

func TestPathPartsStrictDecode(t *testing.T) {
	for _, name := range []string{"url_path", "file_path"} {
		for _, tc := range []struct {
			fields string
			valid  bool
		}{
			{"base: {literal: base}, parts: []", true},
			{"base: {literal: base}, parts: [{literal: a}, {literal: b}]", true},
			{"base: {literal: base}", false},
			{"parts: []", false},
			{"base: {literal: base}, parts: null", false},
			{"base: {literal: base}, parts: [], extra: private-canary", false},
			{"base: {literal: base}, path: {literal: old}", false},
		} {
			source := "tool: go\nargs: [{" + name + ": {" + tc.fields + "}}]"
			var command v1.Command
			err := Decode([]byte(source), ".yaml", &command)
			if (err == nil) != tc.valid || err != nil && strings.Contains(err.Error(), "private-canary") {
				t.Fatalf("%s: %v", source, err)
			}
		}
	}
}

func TestDeferredURLIdentityManifestBoundary(t *testing.T) {
	lit := func(s string) v1.Value { return v1.Value{Literal: &s} }
	base := "http://private-canary.test/" + strings.Repeat("雪", 120000)
	for _, appendRequired := range []bool{false, true} {
		value := v1.Value{URLPath: &v1.URLPathValue{Base: lit(base), Parts: []v1.Value{{Secret: true, FilePath: &v1.FilePathValue{Base: lit(""), Parts: []v1.Value{}}}}}}
		if appendRequired {
			value.URLPath.Parts = append(value.URLPath.Parts, lit("/"))
		}
		m := v1.Manifest{Version: v1.ManifestVersion, Project: "demo", Tools: map[string]string{"sh": "sh"}, Components: map[string]v1.Component{"test": {Kind: v1.Job, Runtime: v1.Native, Policy: v1.EachStart, Command: &v1.Command{Tool: "sh"}, Environment: v1.Environment{Assign: map[string]v1.Value{"URL": value}}}}, Scenes: map[string]v1.Scene{"test": {Lifetime: v1.Disposable, Components: []string{"test"}, TerminalJob: "test"}}}
		data, err := json.Marshal(m)
		if err != nil {
			t.Fatal(err)
		}
		var decoded v1.Manifest
		if err := Decode(data, ".json", &decoded); err != nil {
			t.Fatal("bounded source decode", err)
		}
		err = Validate(decoded)
		if (err != nil) != appendRequired || err != nil && strings.Contains(err.Error(), "private-canary") {
			t.Fatalf("append required=%v: %v", appendRequired, err)
		}
	}
}

func TestFixtureCompositionReferenceBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name   string
		ref    v1.Reference
		mutate func(*v1.Manifest)
		want   string
	}{
		{"CA", v1.Reference{Kind: "resource", Name: "origin", Field: "ca_cert_path"}, nil, "ca_cert_path"},
		{"unattached resource", v1.Reference{Kind: "resource", Name: "hidden", Field: "path"}, func(m *v1.Manifest) {
			m.Resources["hidden"] = v1.Resource{Kind: "directory"}
			s := m.Scenes["dev"]
			s.Resources = append(s.Resources, "hidden")
			m.Scenes["dev"] = s
		}, "attachment"},
		{"unselected resource", v1.Reference{Kind: "resource", Name: "hidden", Field: "path"}, func(m *v1.Manifest) {
			m.Resources["hidden"] = v1.Resource{Kind: "directory"}
			c := m.Components["web"]
			c.Resources = append(c.Resources, "hidden")
			m.Components["web"] = c
		}, "not selected"},
		{"unattached output", v1.Reference{Kind: "output", Name: "hidden", Field: "path"}, func(m *v1.Manifest) {
			m.Outputs = map[string]v1.Output{"hidden": {Path: "dist", ConcurrencyGroup: "build"}}
		}, "attachment"},
		{"unselected service", v1.Reference{Kind: "service", Name: "hidden", Field: "host"}, func(m *v1.Manifest) {
			m.Components["hidden"] = m.Components["web"]
		}, "not selected"},
		{"unpublished origin", v1.Reference{Kind: "resource", Name: "origin", Field: "url"}, func(m *v1.Manifest) {
			s := m.Scenes["dev"]
			s.Publish = nil
			m.Scenes["dev"] = s
		}, "publication"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var m v1.Manifest
			if err := Decode([]byte(caProject), ".yaml", &m); err != nil {
				t.Fatal(err)
			}
			// Remove unrelated CA assignments so the fixture owns the rejection.
			m.Environments = nil
			c := m.Components["web"]
			c.EnvironmentSets = nil
			m.Components["web"] = c
			if tc.mutate != nil {
				tc.mutate(&m)
			}
			c = m.Components["web"]
			c.Fixtures = map[string]v1.Fixture{"bad": {Value: composed(filesystem(v1.Value{Ref: &tc.ref}))}}
			m.Components["web"] = c
			err := Validate(m)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("expected %s: %v", tc.want, err)
			}
		})
	}
}
