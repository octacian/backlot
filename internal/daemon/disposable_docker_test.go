//go:build darwin || linux

package daemon

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/moby/moby/client"
	v1 "github.com/octacian/backlot/api/v1"
)

func TestDisposableEvidenceDocker(t *testing.T) {
	endpoint := os.Getenv("BACKLOT_DOCKER_TEST_ENDPOINT")
	if endpoint == "" {
		t.Skip("requires explicit Docker endpoint and pre-pulled alpine:3.21")
	}
	binary := filepath.Join(t.TempDir(), "backlot")
	build := exec.Command("go", "build", "-race", "-o", binary, "../../cmd/backlot")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v %s", err, output)
	}
	for _, mode := range []string{"success", "job-failure", "cancellation", "collection-failure", "cleanup-failure"} {
		t.Run(mode, func(t *testing.T) {
			h := newDockerHarness(t, binary, endpoint)
			h.alpine()
			job := h.manifest.Components["each"]
			script := "printf '%s\\n' \"$@\" >/tmp/report; echo artifact-output; echo artifact-stderr >&2"
			switch mode {
			case "job-failure":
				script += "; exit 17"
			case "cancellation":
				script += "; exec sleep 90"
			case "cleanup-failure":
				script += "; while [ ! -f /private/release ]; do sleep 0.1; done"
			}
			job.Args = []string{"sh", "-c", script, "job"}
			job.Fixtures = map[string]v1.Fixture{"directory": {Value: fixtureRef("resource", "directory", "path")}}
			job.Artifacts = map[string]v1.ArtifactSource{"report": {Path: "/tmp/report"}}
			if mode == "collection-failure" {
				job.Artifacts["missing"] = v1.ArtifactSource{Path: "/tmp/missing"}
			}
			h.manifest.Components["each"] = job
			h.write()
			request := h.request("test")
			request.Plan.TerminalArgs = []string{"argument with spaces", "--selector"}
			request.Options.KeepOnFailure = mode == "job-failure" || mode == "cancellation" || mode == "collection-failure"
			response, err := h.cli.Run(context.Background(), request)
			if err != nil {
				t.Fatal(err)
			}
			id := response.Instance.ID
			h.ids = append(h.ids, id)
			h.tokens[id] = response.LeaseToken
			if mode == "cancellation" || mode == "cleanup-failure" {
				h.waitTerminalRunning(id)
			}
			var marker string
			var proof []byte
			if mode == "cleanup-failure" {
				fixtures, err := h.cli.Fixtures(context.Background(), v1.FixturesRequest{APIVersion: v1.Version, InstanceID: id, Component: "each"})
				if err != nil || len(fixtures.Fixtures) != 1 {
					t.Fatal("fixture directory unavailable", err)
				}
				directory := fixtures.Fixtures[0].Value
				marker = filepath.Join(directory, ".backlot-owner")
				proof, err = os.ReadFile(marker)
				if err != nil {
					t.Fatal(err)
				}
				// Restore only this exact fixture's proof before journal cleanup on every path.
				t.Cleanup(func() {
					if err := os.WriteFile(marker, proof, 0600); err != nil && !os.IsNotExist(err) {
						t.Error(err)
					}
				})
				if err := os.WriteFile(marker, []byte("injected-uncertainty"), 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(directory, "release"), nil, 0600); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "cancellation" {
				waitComponentLog(t, h.nativeHarness, id, "each", "artifact-output")
				if _, err := h.cli.StopExecution(context.Background(), id); err != nil {
					t.Fatal(err)
				}
			}
			want := v1.Succeeded
			if mode == "job-failure" || mode == "collection-failure" || mode == "cleanup-failure" {
				want = v1.Failed
			}
			if mode == "cancellation" {
				want = v1.Cancelled
			}
			final := h.state(id, want).Instance
			result := final.Execution
			if result.TerminalExitCode == nil {
				t.Fatal("original job status absent", result)
			}
			if mode == "job-failure" && (*result.TerminalExitCode != 17 || result.Failure != "") {
				t.Fatalf("job result %+v", result)
			}
			if mode == "collection-failure" && (result.CollectionFailure == "" || *result.TerminalExitCode != 0) {
				t.Fatalf("collection result %+v", result)
			}
			if mode == "cleanup-failure" && (result.CleanupFailure == "" || *result.TerminalExitCode != 0) {
				t.Fatalf("cleanup result %+v", result)
			}
			if mode == "cancellation" && !result.Cancelled {
				t.Fatal("cancellation omitted")
			}
			if result.Kept != request.Options.KeepOnFailure {
				t.Fatalf("kept resources=%v want=%v", result.Kept, request.Options.KeepOnFailure)
			}
			if len(result.Artifacts) != 1 {
				t.Fatalf("collected artifacts %+v", result.Artifacts)
			}
			data, err := os.ReadFile(filepath.Join(result.Artifacts[0].Path, "report"))
			if err != nil || string(data) != "argument with spaces\n--selector\n" {
				t.Fatal(string(data), err)
			}
			if result.Kept {
				server := h.container(id, "server")
				if !h.engineAlive(server) {
					t.Fatal("healthy dependency stopped")
				}
				output, code := h.exec(server, []string{"sh", "-c", "test -f /private/secret && echo retained"})
				if code != 0 || !strings.Contains(output, "retained") {
					t.Fatal("investigation data lost", code)
				}
			} else {
				list, err := h.engine.ContainerList(context.Background(), client.ContainerListOptions{All: true, Filters: client.Filters{}.Add("label", "io.backlot.owner")})
				if err != nil {
					t.Fatal(err)
				}
				for _, item := range list.Items {
					inspected, err := h.engine.ContainerInspect(context.Background(), item.ID, client.ContainerInspectOptions{})
					if err != nil {
						t.Fatal(err)
					}
					if contains(inspected.Container.Config.Env, "BACKLOT_FIXTURE="+h.token) && contains(inspected.Container.Config.Env, "INSTANCE="+id) {
						t.Fatal("owned runtime survived teardown")
					}
				}
			}
			if mode == "cleanup-failure" {
				if err := os.WriteFile(marker, proof, 0600); err != nil {
					t.Fatal(err)
				}
				if _, err := h.cli.Destroy(context.Background(), id); err != nil {
					t.Fatal("cleanup could not recover", err)
				}
			} else if _, err := h.cli.Destroy(context.Background(), id); err != nil {
				t.Fatal(err)
			}
			logs, err := h.cli.Logs(context.Background(), v1.LogsRequest{APIVersion: v1.Version, InstanceID: id, Component: "each"})
			if err != nil || len(logs.Records) < 2 {
				t.Fatal("historical logs missing", logs, err)
			}
			for _, record := range logs.Records {
				if record.Attempt != result.Attempt || record.Time == "" || record.Stream == "" {
					t.Fatal("context missing", record)
				}
			}
			// No cleanup error may be forgotten by the final explicit destroy.
			inspected, err := h.cli.Inspect(context.Background(), id)
			if err != nil || inspected.Instance.Status != v1.Destroyed || inspected.Instance.Execution.CleanupFailure != "" {
				t.Fatal("destroy failed", inspected, err)
			}
		})
	}
}
