package plan

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	v1 "github.com/octacian/backlot/api/v1"
)

func TestTaskCaptureSelectionAndDrift(t *testing.T) {
	noDefaultConfig(t)
	root := fixture(t)
	m := v1.Manifest{Version: v1.ManifestVersion, Project: "tasks", Tools: map[string]string{"shell": "/bin/sh", "missing": "/not-installed"}, Components: map[string]v1.Component{}, Scenes: map[string]v1.Scene{"dev": {Lifetime: v1.Persistent, Components: []string{"startup"}, Tasks: []string{"admin"}}}}
	job := v1.Component{Kind: v1.Job, Runtime: v1.Native, Policy: v1.EachStart, Command: &v1.Command{Tool: "shell", Args: v1.StringArguments("-c", "exit 0")}}
	m.Components["startup"] = job
	m.Components["admin"] = job
	unused := job
	unused.Command = &v1.Command{Tool: "missing", WorkingDirectory: "absent"}
	m.Components["unused"] = unused
	save := func() {
		t.Helper()
		data, err := json.Marshal(m)
		if err != nil {
			t.Fatal(err)
		}
		write(t, filepath.Join(root, "backlot.json"), data)
	}
	if err := os.Remove(filepath.Join(root, "backlot.yaml")); err != nil {
		t.Fatal(err)
	}
	save()
	source, err := Locate(root)
	if err != nil {
		t.Fatal(err)
	}
	request := v1.PlanRequest{Scene: "dev", ProjectPath: root}
	accepted, err := Prepare(source, request)
	if err != nil {
		t.Fatal(err)
	}
	if len(accepted.Plan.Components) != 1 || len(accepted.Tasks) != 1 || accepted.Plan.Components[0].Name != "startup" {
		t.Fatal("tasks entered startup", accepted.Plan)
	}
	baseline, _ := json.Marshal(accepted.Tasks["admin"])
	// Unrelated enabled task tools need not exist at selected-task comparison.
	scene := m.Scenes["dev"]
	scene.Tasks = append(scene.Tasks, "unused")
	m.Scenes["dev"] = scene
	save()
	if _, err := Prepare(source, request); err == nil {
		t.Fatal("enabled invalid task accepted")
	}
	current, err := PrepareTask(source, request, "admin")
	if err != nil {
		t.Fatal(err)
	}
	got, _ := json.Marshal(current.Tasks["admin"])
	if string(got) != string(baseline) {
		t.Fatal("unrelated task changed selected capture")
	}
	admin := job
	admin.Environment = v1.Environment{Seeds: []string{"private.env"}}
	m.Components["admin"] = admin
	write(t, filepath.Join(root, "private.env"), []byte("TOKEN=first-private-canary\n"))
	save()
	one, err := PrepareTask(source, request, "admin")
	if err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(root, "private.env"), []byte("TOKEN=second-private-canary\n"))
	two, err := PrepareTask(source, request, "admin")
	if err != nil {
		t.Fatal(err)
	}
	a, _ := json.Marshal(one.Tasks["admin"])
	b, _ := json.Marshal(two.Tasks["admin"])
	if string(a) == string(b) {
		t.Fatal("seed drift not captured")
	}
	if _, err := PrepareTask(source, request, "unknown"); err == nil {
		t.Fatal("unselected task accepted")
	}
}
