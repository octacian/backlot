package manifest

import (
	"testing"

	v1 "github.com/octacian/backlot/api/v1"
)

func TestTaskDeclarationRestrictions(t *testing.T) {
	for _, change := range []string{"valid", "unknown", "duplicate", "disposable", "service", "container", "fresh", "initializes", "resource", "dependency", "producer"} {
		t.Run(change, func(t *testing.T) {
			job := v1.Component{Kind: v1.Job, Runtime: v1.Native, Policy: v1.EachStart, Command: &v1.Command{Tool: "shell"}}
			m := v1.Manifest{Version: v1.ManifestVersion, Project: "tasks", Tools: map[string]string{"shell": "sh"}, Resources: map[string]v1.Resource{"data": {Kind: "volume"}}, Components: map[string]v1.Component{"startup": job, "admin": job}, Scenes: map[string]v1.Scene{}}
			scene := v1.Scene{Lifetime: v1.Persistent, Components: []string{"startup"}, Tasks: []string{"admin"}}
			switch change {
			case "unknown":
				scene.Tasks = []string{"absent"}
			case "duplicate":
				scene.Tasks = append(scene.Tasks, "admin")
			case "disposable":
				scene.Lifetime = v1.Disposable
				scene.TerminalJob = "startup"
			case "service":
				job.Kind = v1.Service
			case "container":
				job.Runtime = v1.Container
			case "fresh":
				job.Policy = v1.FreshOnly
			case "initializes":
				job.Initializes = []string{"data"}
			case "resource":
				job.Resources = []string{"data"}
			case "dependency":
				job.DependsOn = []v1.Dependency{{Component: "other", Condition: v1.Completed}}
				m.Components["other"] = m.Components["startup"]
			case "producer":
				m.BuildImages = map[string]v1.BuildImage{"image": {Producer: "admin"}}
			}
			m.Components["admin"] = job
			m.Scenes["dev"] = scene
			err := Validate(m)
			if (err == nil) != (change == "valid") {
				t.Fatalf("%s: %v", change, err)
			}
		})
	}
}
