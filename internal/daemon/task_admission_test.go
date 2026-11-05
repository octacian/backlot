//go:build darwin || linux

package daemon

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	v1 "github.com/octacian/backlot/api/v1"
	"github.com/octacian/backlot/internal/plan"
)

// taskAdmissionFixture uses real lifecycle owners with no parent subprocesses.
// The task's owned marker makes accidental launch observable independently of journals.
func taskAdmissionFixture(t *testing.T) (*service, v1.RunRequest, string) {
	t.Helper()
	s, q := attachedCoordinator(t)
	q.Attached = false
	marker := filepath.Join(q.Plan.ProjectPath, "task-effect")
	m := v1.Manifest{Version: v1.ManifestVersion, Project: "fixture", Inputs: map[string]v1.Input{"password": {Secret: true}}, Tools: map[string]string{"touch": "/usr/bin/touch"}, Components: map[string]v1.Component{"check": {Kind: v1.Job, Runtime: v1.Native, Policy: v1.EachStart, Command: &v1.Command{Tool: "touch", Args: v1.StringArguments(marker)}}}, Scenes: map[string]v1.Scene{"dev": {Lifetime: v1.Persistent, Components: []string{"check"}, Tasks: []string{"check"}}}}
	data, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(q.Plan.ProjectPath, "backlot.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	return s, q, marker
}

func taskAdmissionReady(t *testing.T, s *service, id string) v1.Instance {
	t.Helper()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for {
		i, err := s.store.inspect(id)
		if err != nil {
			t.Fatal(err)
		}
		if i.Status == v1.RuntimeReady {
			return i
		}
		select {
		case <-deadline.C:
			t.Fatal("parent did not become ready", i.Status)
		case <-tick.C:
		}
	}
}

func assertTaskNotAdmitted(t *testing.T, s *service, parent, marker string) {
	t.Helper()
	ids, err := s.store.dependentTasks(parent)
	if err != nil || len(ids) != 0 {
		t.Fatal("task record admitted", ids, err)
	}
	s.mu.Lock()
	count := len(s.executions)
	s.mu.Unlock()
	if count != 1 {
		t.Fatal("task owner registered", count)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("task effect", err)
	}
	// Every task journal is keyed by its instance ID; refusal must allocate none.
	state := stateContents(t, s.store)
	for _, bucket := range []string{"runtime", "snapshots", "secrets"} {
		for id := range state[bucket] {
			if id != parent {
				t.Fatal("task journal allocated", bucket, id)
			}
		}
	}
}

func TestTaskAdmissionRejectsParentReplacement(t *testing.T) {
	for _, action := range []string{"restart", "reset", "generation", "unchanged"} {
		t.Run(action, func(t *testing.T) {
			s, q, marker := taskAdmissionFixture(t)
			first, err := s.run(context.Background(), q)
			if err != nil {
				t.Fatal(err)
			}
			parent := taskAdmissionReady(t, s, first.Instance.ID)
			reached, release, joined := make(chan struct{}), make(chan struct{}), make(chan struct{})
			var once sync.Once
			s.beforeTaskMutation = func(string) { close(reached); <-release }
			var result v1.InstanceResponse
			var taskErr error
			go func() {
				defer close(joined)
				result, taskErr = s.runTask(context.Background(), v1.TaskRunRequest{APIVersion: v1.Version, Target: v1.SceneTarget{InstanceID: parent.ID}, Task: "check"})
			}()
			t.Cleanup(func() { once.Do(func() { close(release) }); awaitControlled(t, joined, "task caller not joined") })
			awaitControlled(t, reached, "task did not observe parent")
			if action == "generation" {
				if err := s.store.saveResources(parent.ID, resourceGeneration{ID: newID(), Resources: map[string]ownedResource{}, Initialized: map[string]bool{}}); err != nil {
					t.Fatal(err)
				}
			} else if action != "unchanged" {
				q.InstanceID = parent.ID
				replacement, err := s.runMode(context.Background(), q, action == "reset")
				if err != nil {
					t.Fatal(err)
				}
				next := taskAdmissionReady(t, s, replacement.Instance.ID)
				if next.Execution.Attempt == parent.Execution.Attempt {
					t.Fatal("parent attempt not replaced")
				}
			}
			before := stateContents(t, s.store)
			once.Do(func() { close(release) })
			awaitControlled(t, joined, "task admission did not finish")
			if action == "unchanged" {
				if taskErr != nil || result.Instance.Task == nil || result.Instance.Task.Attempt != parent.Execution.Attempt {
					t.Fatal("unchanged parent rejected", taskErr)
				}
				if _, err := s.stopExecution(result.Instance.ID); err != nil {
					t.Fatal(err)
				}
			} else {
				code(t, taskErr, "conflict")
				if !reflect.DeepEqual(before, stateContents(t, s.store)) {
					t.Fatal("refused task changed durable state")
				}
				assertTaskNotAdmitted(t, s, parent.ID, marker)
			}
		})
	}
}

func TestTaskAdmissionRejectsInitiallyNonreadyParent(t *testing.T) {
	s, q, marker := taskAdmissionFixture(t)
	prepared, err := s.prepare(context.Background(), v1.PrepareRequest{APIVersion: v1.Version, Plan: q.Plan})
	if err != nil {
		t.Fatal(err)
	}
	unlock := s.lockMutation(prepared.Instance.ID)
	result := make(chan error, 1)
	go func() {
		_, err := s.runTask(context.Background(), v1.TaskRunRequest{APIVersion: v1.Version, Target: v1.SceneTarget{InstanceID: prepared.Instance.ID}, Task: "check"})
		result <- err
	}()
	// A nonready selection is refused before waiting for a lifecycle replacement.
	select {
	case err := <-result:
		code(t, err, "parent_unavailable")
	case <-time.After(time.Second):
		unlock()
		t.Fatal("nonready selection queued for a later ready parent")
	}
	unlock()
	started, err := s.run(context.Background(), q)
	if err != nil {
		t.Fatal(err)
	}
	taskAdmissionReady(t, s, started.Instance.ID)
	assertTaskNotAdmitted(t, s, started.Instance.ID, marker)
}

func TestTaskAdmissionRejectsReplacedProjectDirectory(t *testing.T) {
	for _, location := range []string{"checkout", "project-subdirectory"} {
		t.Run(location, func(t *testing.T) {
			s, q, marker := taskAdmissionFixture(t)
			if location == "project-subdirectory" {
				nested := filepath.Join(q.Plan.ProjectPath, "package")
				if err := os.Mkdir(nested, 0700); err != nil {
					t.Fatal(err)
				}
				data, err := os.ReadFile(filepath.Join(q.Plan.ProjectPath, "backlot.json"))
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(nested, "backlot.json"), data, 0600); err != nil {
					t.Fatal(err)
				}
				q.Plan.ProjectPath = nested
			}
			first, err := s.run(context.Background(), q)
			if err != nil {
				t.Fatal(err)
			}
			parent := taskAdmissionReady(t, s, first.Instance.ID)
			// Retain the original inode so replacement cannot accidentally reuse its identity.
			original := q.Plan.ProjectPath + "-original"
			if err := os.Rename(q.Plan.ProjectPath, original); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := os.RemoveAll(original); err != nil {
					t.Error(err)
				}
			})
			if err := os.Mkdir(q.Plan.ProjectPath, 0700); err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(filepath.Join(original, "backlot.json"))
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(q.Plan.ProjectPath, "backlot.json"), data, 0600); err != nil {
				t.Fatal(err)
			}
			// The declaration, absolute tool path and canonical cwd still match.
			source, err := plan.Locate(q.Plan.ProjectPath)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := plan.PrepareTask(source, q.Plan, "check"); err != nil {
				t.Fatal(err)
			}
			before := stateContents(t, s.store)
			_, err = s.runTask(context.Background(), v1.TaskRunRequest{APIVersion: v1.Version, Target: v1.SceneTarget{InstanceID: parent.ID}, Task: "check"})
			code(t, err, "checkout_changed")
			if !reflect.DeepEqual(before, stateContents(t, s.store)) {
				t.Fatal("replacement refusal changed durable state")
			}
			assertTaskNotAdmitted(t, s, parent.ID, marker)

		})
	}
}
