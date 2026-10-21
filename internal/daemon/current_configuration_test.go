//go:build !windows

package daemon

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	v1 "github.com/octacian/backlot/api/v1"
	"github.com/octacian/backlot/internal/plan"
)

func TestCurrentCompositionRetainsPartialSourceProvenance(t *testing.T) {
	for _, failure := range []string{"missing", "invalid", "replaced", "moved", "tool", "machine", "manifest", "unchanged", "new-generation"} {
		t.Run(failure, func(t *testing.T) {
			home := shortTemp(t)
			t.Setenv("HOME", home)
			t.Setenv("XDG_CONFIG_HOME", home)
			project := shortTemp(t)
			config := writeFixture(t, project)
			put := func(path, data string) {
				t.Helper()
				if err := os.WriteFile(path, []byte(data), 0600); err != nil {
					t.Fatal(err)
				}
			}
			// A movable external Git directory leaves the launch manifest path intact.
			gitDir := filepath.Join(shortTemp(t), "metadata")
			if err := os.Mkdir(gitDir, 0700); err != nil {
				t.Fatal(err)
			}
			put(filepath.Join(project, ".git"), "gitdir: "+gitDir+"\n")
			save := func(value string) v1.ProjectSetupResponse {
				t.Helper()
				setup, err := plan.OpenSetup(v1.ProjectSetupRequest{ProjectPath: project, ConfigPath: config, Edit: true})
				if err != nil {
					t.Fatal(err)
				}
				result, err := setup.Save(context.Background(), v1.PrivateConfig{Version: v1.ManifestVersion, Inputs: map[string]string{"password": value}}, nil, nil, nil)
				if err != nil {
					t.Fatal(err)
				}
				return result
			}
			setup := save("launch-private-canary-48")
			source, err := plan.Locate(project)
			if err != nil {
				t.Fatal(err)
			}
			launch, err := plan.Prepare(source, v1.PlanRequest{Scene: "dev", ConfigPath: config})
			if err != nil {
				t.Fatal(err)
			}
			bytesBefore, err := json.Marshal(launch)
			if err != nil {
				t.Fatal(err)
			}
			secretBefore, err := json.Marshal(launch.Secrets)
			if err != nil {
				t.Fatal(err)
			}
			expectedPath, expectedState := setup.PrivatePath, "present"
			switch failure {
			case "missing":
				if err := os.Remove(setup.PrivatePath); err != nil {
					t.Fatal(err)
				}
				expectedState = "missing"
			case "invalid":
				put(setup.PrivatePath, `{"version":"backlot/v1","inputs":{"password":23},"tools":{"go":"current-value-canary-48"}}`)
				expectedState = "invalid"
			case "replaced":
				if err := os.Rename(gitDir, gitDir+"-old"); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(gitDir, 0700); err != nil {
					t.Fatal(err)
				}
				expectedState = "rebound"
			case "moved":
				if err := os.Rename(gitDir, gitDir+"-moved"); err != nil {
					t.Fatal(err)
				}
				put(filepath.Join(project, ".git"), "gitdir: "+gitDir+"-moved\n")
				expectedState = "rebound"
			case "tool":
				put(config, `{"version":"backlot/v1","inputs":{"password":"current-value-canary-48"},"tools":{"go":"/unavailable/current-tool"}}`)
			case "machine":
				put(config, `{"version":"backlot/v1","inputs":{"password":23}}`)
				expectedState = "unavailable"
			case "manifest":
				put(source.ManifestPath, `{"version":"backlot/v1","project":23}`)
				expectedState = "unavailable"
			case "new-generation":
				expectedPath = save("current-value-canary-48").PrivatePath
				expectedState = "changed"
			}
			response := v1.DoctorResponse{APIVersion: v1.Version, Scope: "instance", InstanceID: strings.Repeat("a", 64), Healthy: true, Paths: []v1.DoctorPath{}, Checks: []v1.DoctorCheck{}}
			addLaunchPaths(&response, launch)
			checkCurrentComposition(&response, launch)
			if err := response.Validate(); err != nil {
				t.Fatal(err)
			}
			paths := map[string]v1.DoctorPath{}
			for _, p := range response.Paths {
				paths[p.Name] = p
			}
			current, ok := paths["current_private_config"]
			if !ok || current.Path != expectedPath || current.State != expectedState || current.Association != setup.Association {
				t.Fatalf("wrong partial source: %+v expected %s %s", current, expectedPath, expectedState)
			}
			effective, ok := paths["current_effective_configuration"]
			if !ok {
				t.Fatal("missing effective state")
			}
			if failure == "unchanged" || failure == "new-generation" {
				if effective.Digest == "" || effective.State != expectedState {
					t.Fatalf("wrong computable effective state: %+v", effective)
				}
				if failure == "new-generation" && (effective.Digest == launch.Plan.EffectiveConfigDigest || current.Digest == launch.PrivateProvenance.Digest) {
					t.Fatal("mixed current and launch generation")
				}
			} else if effective.State != "uncomputable" || effective.Digest != "" {
				t.Fatalf("current facts fabricated from launch: %+v", effective)
			}
			if failure == "tool" && current.Digest != launch.PrivateProvenance.Digest {
				t.Fatal("unrelated tool failure hid available private digest")
			}
			after, err := json.Marshal(launch)
			if err != nil {
				t.Fatal(err)
			}
			secretsAfter, err := json.Marshal(launch.Secrets)
			if err != nil {
				t.Fatal(err)
			}
			if string(bytesBefore) != string(after) || string(secretBefore) != string(secretsAfter) {
				t.Fatal("doctor mutated launch snapshot")
			}
			if paths["launch_private_config"].Path != setup.PrivatePath || paths["launch_private_config"].Digest != launch.PrivateProvenance.Digest {
				t.Fatal("launch provenance overwritten")
			}
			encoded, err := json.Marshal(response)
			if err != nil {
				t.Fatal(err)
			}
			for _, canary := range []string{"launch-private-canary-48", "current-value-canary-48", "resolved-fixture-secret"} {
				if strings.Contains(string(encoded), canary) {
					t.Fatal("doctor leaked configuration value")
				}
			}
		})
	}
}
