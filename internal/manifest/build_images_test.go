package manifest

import (
	"encoding/json"
	v1 "github.com/octacian/backlot/api/v1"
	"os"
	"testing"
)

func TestBuildImageValidation(t *testing.T) {
	data, err := os.ReadFile("../../examples/build-images/backlot.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var original v1.Manifest
	if err = Decode(data, ".yaml", &original); err != nil {
		t.Fatal(err)
	}
	tests := map[string]func(*v1.Manifest){
		"valid":               func(*v1.Manifest) {},
		"missing producer":    func(m *v1.Manifest) { m.BuildImages["app"] = v1.BuildImage{Producer: "missing"} },
		"fresh producer":      func(m *v1.Manifest) { c := m.Components["build"]; c.Policy = v1.FreshOnly; m.Components["build"] = c },
		"missing dependency":  func(m *v1.Manifest) { c := m.Components["test"]; c.DependsOn = nil; m.Components["test"] = c },
		"unselected producer": func(m *v1.Manifest) { s := m.Scenes["test"]; s.Components = []string{"test"}; m.Scenes["test"] = s },
		"wrong env owner": func(m *v1.Manifest) {
			c := m.Components["test"]
			c.Environment = m.Components["build"].Environment
			m.Components["test"] = c
		},
		"wrong env field": func(m *v1.Manifest) {
			c := m.Components["build"]
			c.Environment.Assign["APP_IMAGE"] = v1.Value{Ref: &v1.Reference{Kind: "build_image", Name: "app", Field: "id"}}
			m.Components["build"] = c
		},
		"missing effective tag": func(m *v1.Manifest) {
			c := m.Components["build"]
			c.Environment.Assign = nil
			m.Components["build"] = c
		},
		"fixture context": func(m *v1.Manifest) {
			c := m.Components["test"]
			c.Fixtures = map[string]v1.Fixture{"bad": {Value: *c.Image}}
			m.Components["test"] = c
		},
		"cycle": func(m *v1.Manifest) {
			c := m.Components["build"]
			c.DependsOn = []v1.Dependency{{Component: "test", Condition: v1.Completed}}
			m.Components["build"] = c
		},
		"extra field": func(m *v1.Manifest) { c := m.Components["test"]; c.Image.Ref.Port = "bad"; m.Components["test"] = c },
	}
	for name, change := range tests {
		t.Run(name, func(t *testing.T) {
			raw, _ := json.Marshal(original)
			var m v1.Manifest
			if err := json.Unmarshal(raw, &m); err != nil {
				t.Fatal(err)
			}
			change(&m)
			err := Validate(m)
			if (err == nil) != (name == "valid") {
				t.Fatalf("validation: %v", err)
			}
		})
	}
}
