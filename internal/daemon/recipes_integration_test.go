//go:build darwin || linux

package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"

	v1 "github.com/octacian/backlot/api/v1"
	"github.com/octacian/backlot/internal/manifest"
)

func recipeBinary(t *testing.T, setting, source string) string {
	t.Helper()
	if binary := os.Getenv(setting); binary != "" {
		return binary
	}
	return buildFixtureBinary(t, t.TempDir(), source)
}

func copyRecipe(t *testing.T, h *nativeHarness, name string, files ...string) {
	t.Helper()
	source := filepath.Join("../../examples", name)
	data, err := os.ReadFile(filepath.Join(source, "backlot.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if err := manifest.Decode(data, ".yaml", &h.manifest); err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		data, err := os.ReadFile(filepath.Join(source, file))
		if err != nil {
			t.Fatal(err)
		}
		destination := filepath.Join(h.project, file)
		if err := os.MkdirAll(filepath.Dir(destination), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(destination, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	h.write()
	// Keep raw, private diagnostics after the normal authenticated fixture teardown.
	h.preserveFailureEvidence = true
}

func recipeCLI(t *testing.T, h *nativeHarness, config, scene string, args []string, want int) (v1.Instance, string) {
	t.Helper()
	commandArgs := []string{"run", scene, "--project", h.project, "--state-dir", h.dir,
		"--startup-timeout", "90s", "--job-timeout", "90s", "--stop-grace", "2s", "--json"}
	if config != "" {
		commandArgs = append(commandArgs, "--config", config)
	}
	commandArgs = append(commandArgs, "--")
	commandArgs = append(commandArgs, args...)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, h.binary, commandArgs...)
	var stdout, stderr bytes.Buffer
	command.Stdout, command.Stderr = &stdout, &stderr
	started := time.Now()
	err := command.Run()
	code := 0
	if err != nil {
		var exit *exec.ExitError
		if !errors.As(err, &exit) {
			t.Fatal(err)
		}
		code = exit.ExitCode()
	}
	var response v1.InstanceResponse
	if err := json.Unmarshal(stdout.Bytes(), &response); err != nil {
		t.Fatalf("recipe response: %v; diagnostics %s", err, stderr.String())
	}
	instance := response.Instance
	result := instance.Execution
	if code != want || result == nil || result.TerminalExitCode == nil || *result.TerminalExitCode != want ||
		result.Failure != "" || result.CollectionFailure != "" || result.CleanupFailure != "" {
		t.Fatalf("recipe CLI=%d want=%d execution=%+v; diagnostics %s", code, want, result, stderr.String())
	}
	for _, component := range instance.Plan.Components {
		if component.Name == instance.Plan.TerminalJob {
			base := h.manifest.Components[component.Name].Command.Args
			if !reflect.DeepEqual(component.Command.Args, append(append([]string{}, base...), args...)) {
				t.Fatal("terminal argument vector changed")
			}
		}
	}
	bundle := exportRecipe(t, h, instance, args, want)
	if _, err := h.cli.Destroy(context.Background(), instance.ID); err != nil {
		t.Fatal("authenticated recipe destroy", err)
	}
	t.Logf("recipe=%s args=%q original_exit=%d elapsed=%s export=%s", scene, args, code, time.Since(started), bundle)
	return instance, bundle
}

func exportRecipe(t *testing.T, h *nativeHarness, instance v1.Instance, args []string, want int) string {
	t.Helper()
	if len(instance.Execution.Artifacts) != 1 {
		t.Fatal("missing retained application report")
	}
	artifact := instance.Execution.Artifacts[0]
	if artifact.Component != "tests" || artifact.Name != "reports" || artifact.Attempt != instance.Execution.Attempt {
		t.Fatal("application artifact belongs to another component or attempt")
	}
	parent, err := os.MkdirTemp(os.Getenv("BACKLOT_RECIPE_EXPORT_ROOT"), "bl-recipe-export-")
	if err != nil {
		t.Fatal(err)
	}
	exported, err := h.cli.Export(context.Background(), v1.ExportRequest{APIVersion: v1.Version,
		InstanceID: instance.ID, Output: filepath.Join(parent, "bundle")})
	if err != nil || !exported.Published {
		t.Fatal("recipe export", err)
	}
	verifyRecipeExport(t, instance, exported.Output, args, want)
	return exported.Output
}

func verifyRecipeExport(t *testing.T, instance v1.Instance, bundle string, args []string, want int) {
	t.Helper()
	baseURL, selected := "", strings.Trim(args[1], "^$")
	if instance.Plan.Scene == "browser" {
		baseURL = fmt.Sprintf("http://127.0.0.1:%d", instance.Execution.Ports["http"])
	}
	expected, err := json.Marshal(map[string]any{"instance": instance.ID, "attempt": instance.Execution.Attempt,
		"base_url": baseURL, "args": args, "test": selected, "exit": want,
		"terminal_exit": instance.Execution.TerminalExitCode, "cancelled": instance.Execution.Cancelled})
	if err != nil {
		t.Fatal(err)
	}
	if output, err := exec.Command("python3", "testdata/recipes/verify.py", bundle, string(expected)).CombinedOutput(); err != nil {
		t.Fatalf("application report verification: %v %s", err, output)
	}
}

func TestNativePlaywrightRecipe(t *testing.T) {
	project := os.Getenv("BACKLOT_RECIPE_PLAYWRIGHT_PROJECT")
	if project == "" {
		t.Skip("requires explicitly installed native-playwright project and Chromium")
	}
	binary := recipeBinary(t, "BACKLOT_INSTALLED_TEST_BINARY", "../../cmd/backlot")
	h := newNativeHarness(t, binary, "30s")
	copyRecipe(t, h, "native-playwright", "app.mjs", "prepare.mjs", "playwright.config.mjs", "package.json", "tests/application.spec.mjs")
	if err := os.Symlink(filepath.Join(project, "node_modules"), filepath.Join(h.project, "node_modules")); err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(h.project, "browser-home")
	if err := os.Mkdir(home, 0700); err != nil {
		t.Fatal(err)
	}
	job := h.manifest.Components["tests"]
	job.Environment.Assign["HOME"], job.Environment.Assign["TMPDIR"] = fixtureLiteral(home), fixtureLiteral(home)
	h.manifest.Components["tests"] = job
	h.write()
	if err := os.WriteFile(filepath.Join(h.project, "tests/failure.spec.mjs"), []byte(
		"import { test, expect } from '@playwright/test'; test('intentional failure', async ({ page }) => { await page.goto('/'); await expect(page.getByRole('heading')).toHaveText('intentional mismatch'); });\n"), 0600); err != nil {
		t.Fatal(err)
	}
	var first v1.Instance
	var firstExport string
	for _, tc := range []struct {
		selector string
		exit     int
	}{{"counter interaction", 0}, {"intentional failure", 1}, {"counter interaction", 0}} {
		instance, exported := recipeCLI(t, h, "", "browser", []string{"--grep", tc.selector}, tc.exit)
		if firstExport == "" {
			first, firstExport = instance, exported
		}
	}
	data, err := os.ReadFile("testdata/recipes/hold.spec.mjs")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(h.project, "tests/hold.spec.mjs"), data, 0600); err != nil {
		t.Fatal(err)
	}
	app, err := os.OpenFile(filepath.Join(h.project, "app.mjs"), os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	_, writeErr := app.WriteString("\nimport {writeFileSync} from 'node:fs'; writeFileSync('reports/server.json', JSON.stringify({pid:process.pid}));\n")
	closeErr := app.Close()
	if writeErr != nil || closeErr != nil {
		t.Fatal(writeErr, closeErr)
	}
	server := h.manifest.Components["app"]
	server.Outputs = []string{"reports"}
	h.manifest.Components["app"] = server
	recipeCancellation(t, h, nil, "browser", []string{"--grep", "held browser"}, os.Interrupt)
	verifyRecipeExport(t, first, firstExport, []string{"--grep", "counter interaction"}, 0)
}

func goRecipe(t *testing.T, binary, endpoint, probe string) *dockerHarness {
	t.Helper()
	h := newDockerHarness(t, binary, endpoint)
	copyRecipe(t, h.nativeHarness, "native-go-mariadb", "database_test.go")
	h.manifest.Tools["ready"] = probe
	for _, file := range []string{"go.mod", "go.sum"} {
		data, err := os.ReadFile(filepath.Join("../..", file))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(h.project, file), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	database := h.manifest.Components["database"]
	database.Environment.Assign["BACKLOT_FIXTURE"] = v1.Value{Literal: &h.token, Secret: true}
	database.Environment.Assign["INSTANCE"] = fixtureRef("instance", "", "id")
	h.manifest.Components["database"] = database
	h.write()
	return h
}

func TestNativeGoMariaDBRecipe(t *testing.T) {
	endpoint := os.Getenv("BACKLOT_DOCKER_TEST_ENDPOINT")
	if endpoint == "" {
		t.Skip("requires explicit Docker endpoint and pre-pulled MariaDB recipe image")
	}
	binary := recipeBinary(t, "BACKLOT_INSTALLED_TEST_BINARY", "../../cmd/backlot")
	probe := recipeBinary(t, "BACKLOT_RECIPE_READY_BINARY", "../../examples/native-go-mariadb/cmd/ready")
	h := goRecipe(t, binary, endpoint, probe)
	if err := os.WriteFile(filepath.Join(h.project, "failure_test.go"), []byte(
		"package mariadb; import \"testing\"; func TestIntentionalFailure(t *testing.T) { t.Fatal(\"intentional failure\") }\n"), 0600); err != nil {
		t.Fatal(err)
	}
	var first v1.Instance
	var firstExport string
	for _, tc := range []struct {
		selector string
		exit     int
	}{{"^TestDatabase$", 0}, {"^TestIntentionalFailure$", 1}, {"^TestDatabase$", 0}} {
		instance, exported := recipeCLI(t, h.nativeHarness, h.config, "test", []string{"-run", tc.selector, "-v"}, tc.exit)
		if firstExport == "" {
			first, firstExport = instance, exported
		}
	}
	data, err := os.ReadFile("testdata/recipes/held_test.go")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(h.project, "held_test.go"), data, 0600); err != nil {
		t.Fatal(err)
	}
	recipeCancellation(t, h.nativeHarness, h, "test", []string{"-run", "^TestHeld$", "-v"}, syscall.SIGTERM)
	verifyRecipeExport(t, first, firstExport, []string{"-run", "^TestDatabase$", "-v"}, 0)
}

func TestMariaDBRecipeCompatibility(t *testing.T) {
	endpoint := os.Getenv("BACKLOT_DOCKER_TEST_ENDPOINT")
	if endpoint == "" {
		t.Skip("requires explicit Docker endpoint and pre-pulled recipe images")
	}
	binary := recipeBinary(t, "BACKLOT_INSTALLED_TEST_BINARY", "../../cmd/backlot")
	probe := recipeBinary(t, "BACKLOT_RECIPE_READY_BINARY", "../../examples/native-go-mariadb/cmd/ready")
	images := []string{"mariadb:11.4.13"}
	if selected := os.Getenv("BACKLOT_RECIPE_MARIADB_IMAGES"); selected != "" {
		images = strings.Split(selected, ",")
	}
	for _, image := range images {
		t.Run(image, func(t *testing.T) {
			started := time.Now()
			t.Cleanup(func() { t.Logf("image=%s startup/check/cleanup=%s", image, time.Since(started)) })
			h := goRecipe(t, binary, endpoint, probe)
			database := h.manifest.Components["database"]
			database.Image = ptrValue(fixtureLiteral(image))
			h.manifest.Components["database"] = database
			// The real probe rejects credentials against an already usable database.
			wrong := fixtureLiteral("invalid-password")
			wrong.Secret = true
			h.manifest.Components["reject"] = v1.Component{Kind: v1.Job, Runtime: v1.Native, Policy: v1.EachStart,
				Command: &v1.Command{Tool: "ready"}, DependsOn: []v1.Dependency{{Component: "tests", Condition: v1.Completed}},
				Resources: []string{"sql", "app-password"}, EnvironmentSets: []string{"application"},
				Environment: v1.Environment{Assign: map[string]v1.Value{"DB_PASSWORD": wrong}}}
			scene := h.manifest.Scenes["test"]
			scene.Components, scene.TerminalJob = append(scene.Components, "reject"), "reject"
			h.manifest.Scenes["test"] = scene
			h.write()
			request := h.request("test")
			request.Options.KeepOnFailure = true
			response, err := h.cli.Run(context.Background(), request)
			if err != nil {
				t.Fatal(err)
			}
			id := response.Instance.ID
			h.ids, h.tokens[id] = append(h.ids, id), response.LeaseToken
			instance := h.state(id, v1.Failed).Instance
			result := instance.Execution
			if result == nil || result.TerminalExitCode == nil || *result.TerminalExitCode != 1 || !result.Kept ||
				result.Failure != "" || result.CollectionFailure != "" || result.CleanupFailure != "" {
				t.Fatal("application readiness rejection result", result)
			}
			absent := h.observeDisposableDockerObjects(id, "")
			if _, err := h.cli.Destroy(context.Background(), id); err != nil {
				t.Fatal(err)
			}
			if err := absent(); err != nil {
				h.cleanupUnverified = true
				t.Fatal(err)
			}
			exportRecipe(t, h.nativeHarness, instance, []string{"-run", "^TestDatabase$"}, 0)
		})
	}
}
