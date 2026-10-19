package manifest

import (
	v1 "github.com/octacian/backlot/api/v1"
	"testing"
)

func TestSharedAllocationInitialization(t *testing.T) {
	m := v1.Manifest{Version: v1.ManifestVersion, Project: "allocation-init", Tools: map[string]string{"init": "/fixture"}, SharedServices: map[string]v1.SharedService{"sql": {Manager: "mariadb", Image: "mariadb:11.4", Storage: "volume", Admin: "generated", Port: 3306, ReadinessTimeout: "30s"}}, Resources: map[string]v1.Resource{"db": {Kind: "allocation", SharedService: "sql"}}, Components: map[string]v1.Component{"init": {Kind: v1.Job, Runtime: v1.Native, Policy: v1.FreshOnly, Initializes: []string{"db"}, Resources: []string{"db"}, Command: &v1.Command{Tool: "init"}}}, Scenes: map[string]v1.Scene{"dev": {Lifetime: v1.Persistent, Components: []string{"init"}, Resources: []string{"db"}}}}
	if err := Validate(m); err != nil {
		t.Fatal("allocation cannot use ordinary fresh initialization", err)
	}
	c := m.Components["init"]
	c.Resources = nil
	m.Components["init"] = c
	if Validate(m) == nil {
		t.Fatal("unattached allocation initialization accepted")
	}
}
