//go:build darwin || linux

package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	v1 "github.com/octacian/backlot/api/v1"
	"github.com/octacian/backlot/internal/plan"
)

func TestEnsureStartNativeCLI(t *testing.T) {
	binary := buildFixtureBinary(t, t.TempDir(), "../../cmd/backlot")
	for _, named := range []bool{true, false} {
		name := "run"
		if named {
			name = "scene-start"
		}
		t.Run(name, func(t *testing.T) {
			home := shortTemp(t)
			t.Setenv("HOME", home)
			t.Setenv("XDG_CONFIG_HOME", home)
			t.Setenv("BACKLOT_PROJECT_CONFIG_DIR", filepath.Join(home, "registry"))
			config := filepath.Join(home, "machine.json")
			if err := os.WriteFile(config, []byte(`{"version":"backlot/v1"}`), 0600); err != nil {
				t.Fatal(err)
			}
			// Omitted-path probes use this process's owned HOME, including the daemon.
			defaultConfig, err := plan.DefaultConfigPath()
			if err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(filepath.Dir(defaultConfig), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(defaultConfig, []byte(`{"version":"backlot/v1"}`), 0600); err != nil {
				t.Fatal(err)
			}
			h := newNativeHarness(t, binary)
			delete(h.manifest.Scenes, "test")
			delete(h.manifest.Components, "job")
			h.manifest.Tools["sh"] = "sh"
			h.manifest.Resources["data"] = v1.Resource{Kind: "directory"}
			h.manifest.Resources["password"] = v1.Resource{Kind: "secret"}
			env := v1.Environment{Assign: map[string]v1.Value{"DATA": {Ref: &v1.Reference{Kind: "resource", Name: "data", Field: "path"}}, "SECRET": {Ref: &v1.Reference{Kind: "resource", Name: "password", Field: "value"}}}}
			h.manifest.Components["prep"] = v1.Component{Kind: v1.Job, Runtime: v1.Native, Policy: v1.FreshOnly, Initializes: []string{"data"}, Resources: []string{"data", "password"}, Environment: env, Command: &v1.Command{Tool: "sh", Args: []string{"-c", `printf '%s\n' "$SECRET" >> "$DATA/init"`}}}
			each := v1.Component{Kind: v1.Job, Runtime: v1.Native, Policy: v1.EachStart, Resources: []string{"data", "password"}, Environment: env, Command: &v1.Command{Tool: "sh", Args: []string{"-c", `echo failed-A; echo start >> "$DATA/starts"; exit 23`}}, DependsOn: []v1.Dependency{{Component: "prep", Condition: v1.Completed}}}
			h.manifest.Components["each"] = each
			server := h.manifest.Components["server"]
			server.DependsOn = []v1.Dependency{{Component: "each", Condition: v1.Completed}}
			h.manifest.Components["server"] = server
			h.manifest.Scenes["dev"] = v1.Scene{Lifetime: v1.Persistent, Components: []string{"prep", "each", "server"}, Resources: []string{"port", "data", "password"}}
			h.write()
			invoke := func(omit bool, wantExit int) v1.InstanceResponse {
				t.Helper()
				args := []string{"run"}
				if named {
					args = []string{"scene", "start"}
				}
				args = append(args, "--project", h.project, "--state-dir", h.dir, "--startup-timeout", "3s", "--job-timeout", "3s", "--stop-grace", "50ms", "--json")
				if !omit {
					args = append(args, "--config", config)
				}
				args = append(args, "dev")
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()
				t.Logf("command: %s %s", binary, strings.Join(args, " "))
				command := exec.CommandContext(ctx, binary, args...)
				var out, diagnostic bytes.Buffer
				command.Stdout, command.Stderr = &out, &diagnostic
				err := command.Run()
				exit := 0
				if err != nil {
					if e, ok := err.(*exec.ExitError); ok {
						exit = e.ExitCode()
					} else {
						t.Fatal(err)
					}
				}
				if exit != wantExit {
					t.Fatalf("CLI exit %d want %d: %s %s", exit, wantExit, out.String(), diagnostic.String())
				}
				var result v1.InstanceResponse
				if named {
					var scene v1.SceneResponse
					if err := json.Unmarshal(out.Bytes(), &scene); err != nil || scene.Result == nil {
						t.Fatal(out.String(), err)
					}
					result = *scene.Result
				} else if err := json.Unmarshal(out.Bytes(), &result); err != nil {
					t.Fatal(out.String(), err)
				}
				t.Logf("CLI %s config-omitted=%t exit=%d instance=%s attempt=%s status=%s manifest-drift=%t config-drift=%t", name, omit, exit, result.Instance.ID, result.Instance.Execution.Attempt, result.Instance.Status, result.ManifestDrift, result.ConfigDrift)
				return result
			}
			a := invoke(false, 1)
			id := a.Instance.ID
			if a.Instance.Status != v1.Failed || a.Instance.Execution.CleanupFailure != "" || a.Instance.Execution.CompletedAt == "" {
				t.Fatal("A not joined/failed", a.Instance.Status)
			}
			foundExit := false
			for _, c := range a.Instance.Execution.Components {
				if c.Name == "each" && c.ExitCode != nil && *c.ExitCode == 23 {
					foundExit = true
				}
			}
			if !foundExit {
				t.Fatal("original nonzero each-start exit lost")
			}
			paths, err := filepath.Glob(filepath.Join(h.dir, "data", "*", "init"))
			if err != nil || len(paths) != 1 {
				t.Fatal("retained initialization missing", err)
			}
			witness, err := os.ReadFile(paths[0])
			if err != nil {
				t.Fatal(err)
			}
			each.Command.Args[1] = `echo ready-B; echo start >> "$DATA/starts"; test "$(cat "$DATA/init")" = "$SECRET"`
			h.manifest.Components["each"] = each
			server.Readiness.Timeout = "3s"
			h.manifest.Components["server"] = server
			h.write()
			if err := os.Remove(config); err != nil {
				t.Fatal(err)
			}
			b := invoke(true, 0)
			if err := os.WriteFile(config, []byte(`{"version":"backlot/v1"}`), 0600); err != nil {
				t.Fatal(err)
			}
			if b.Instance.ID != id || b.Instance.Execution.Attempt == a.Instance.Execution.Attempt || b.Instance.Status != v1.RuntimeReady || b.ManifestDrift || b.ConfigDrift || b.Instance.Plan.ManifestDigest == a.Instance.Plan.ManifestDigest {
				t.Fatal("B did not adopt current compatible inputs")
			}
			retained, err := os.ReadFile(paths[0])
			if err != nil || !bytes.Equal(witness, retained) || bytes.Count(retained, []byte("\n")) != 1 {
				t.Fatal("paired credential/fresh-only witness changed", err)
			}
			// Changing an each-start command while ready must only disclose drift.
			each.Command.Args[1] = `echo failed-next; echo start >> "$DATA/starts"; exit 23`
			h.manifest.Components["each"] = each
			h.write()
			ready := invoke(false, 0)
			if ready.Instance.Execution.Attempt != b.Instance.Execution.Attempt || !ready.ManifestDrift {
				t.Fatal("ready start replaced B or hid drift")
			}
			history, err := h.cli.History(context.Background(), v1.HistoryRequest{APIVersion: v1.Version, InstanceID: id})
			if err != nil || len(history.Attempts) != 2 {
				t.Fatal("unexpected acceptance count", history, err)
			}
			old, err := h.cli.Attempt(context.Background(), v1.AttemptRequest{APIVersion: v1.Version, InstanceID: id, Attempt: a.Instance.Execution.Attempt})
			if err != nil || old.Attempt.Source.ManifestDigest != a.Instance.Plan.ManifestDigest || old.Attempt.Status != v1.Failed {
				t.Fatal("A provenance lost", err)
			}
			logs, err := h.cli.LogPage(context.Background(), v1.LogPageRequest{APIVersion: v1.Version, InstanceID: id, Attempt: a.Instance.Execution.Attempt})
			if err != nil || len(logs.Records) == 0 {
				t.Fatal("A logs lost", err)
			}
			exported, err := h.cli.Export(context.Background(), v1.ExportRequest{APIVersion: v1.Version, InstanceID: id, Attempt: a.Instance.Execution.Attempt, Output: filepath.Join(home, "A-report"), Wait: true})
			if err != nil || exported.Result.Attempt != a.Instance.Execution.Attempt {
				t.Fatal("A export failed", err)
			}
			if _, err := h.cli.StopExecution(context.Background(), id); err != nil {
				t.Fatal(err)
			}
			c := invoke(false, 1)
			if c.Instance.Execution.Attempt == b.Instance.Execution.Attempt {
				t.Fatal("explicit failure reused B")
			}
			time.Sleep(100 * time.Millisecond)
			history, err = h.cli.History(context.Background(), v1.HistoryRequest{APIVersion: v1.Version, InstanceID: id})
			if err != nil || len(history.Attempts) != 3 {
				t.Fatal("failure retried automatically", history, err)
			}
			starts, err := os.ReadFile(filepath.Join(filepath.Dir(paths[0]), "starts"))
			if err != nil || strings.Count(string(starts), "start\n") != 3 {
				t.Fatal("each-start count wrong", err)
			}
			if _, err := h.cli.Destroy(context.Background(), id); err != nil {
				t.Fatal(err)
			}
			h.absent()
			t.Log("A history/log/export retained; one explicit next failure; data/paired credential/fresh-only witness preserved; destroy and native absence checked")
		})
	}
}
