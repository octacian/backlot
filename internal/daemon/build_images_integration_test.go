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

	"github.com/containerd/errdefs"
	moby "github.com/moby/moby/client"
	v1 "github.com/octacian/backlot/api/v1"
)

// Image ownership belongs to this fixture, never to the production handoff.
// Register before harness cleanup so consumers join before their images are removed.
func buildImageFixture(t *testing.T, endpoint string) (string, string) {
	t.Helper()
	d, err := newDocker(endpoint)
	if err != nil {
		t.Fatal(err)
	}
	token := newID()
	t.Cleanup(func() {
		defer func() {
			if err := d.Close(); err != nil {
				t.Error(err)
			}
		}()
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		images, err := d.ImageList(ctx, moby.ImageListOptions{All: true, Filters: moby.Filters{}.Add("label", "io.backlot.fixture="+token)})
		if err != nil {
			t.Error(err)
			return
		}
		pending := map[string]bool{}
		for _, item := range images.Items {
			image, err := d.ImageInspect(ctx, item.ID)
			if err != nil {
				t.Error(err)
				return
			}
			if image.Config == nil || image.Config.Labels["io.backlot.fixture"] != token {
				t.Error("image ownership uncertain")
				return
			}
			for _, tag := range image.RepoTags {
				if _, err = d.ImageRemove(ctx, tag, moby.ImageRemoveOptions{PruneChildren: false}); err != nil && !errdefs.IsNotFound(err) {
					t.Error(err)
					return
				}
			}
			pending[item.ID] = true
		}
		for len(pending) > 0 {
			progress := false
			for id := range pending {
				_, err := d.ImageRemove(ctx, id, moby.ImageRemoveOptions{PruneChildren: false})
				if err == nil || errdefs.IsNotFound(err) {
					delete(pending, id)
					progress = true
				}
			}
			if !progress {
				t.Error("preserved fixture images with unresolved consumers/children", pending)
				return
			}
		}
		remaining, err := d.ImageList(ctx, moby.ImageListOptions{All: true, Filters: moby.Filters{}.Add("label", "io.backlot.fixture="+token)})
		if err != nil || len(remaining.Items) != 0 {
			t.Error("fixture images remain", err)
		}
		if _, err = d.ImageInspect(ctx, "alpine:3.21"); err != nil {
			t.Error("cached base missing", err)
		}
		t.Log("owned build-image aliases/images absent; cached base preserved")
	})
	base, err := d.ImageInspect(context.Background(), "alpine:3.21")
	if err != nil {
		t.Fatal(err)
	}
	return base.ID, token
}

func writeBuildImageFixture(t *testing.T, h *dockerHarness, base, token string) {
	t.Helper()
	docker, err := exec.LookPath("docker")
	if err != nil {
		t.Fatal("explicit Docker CLI required", err)
	}
	h.manifest = v1.Manifest{Version: v1.ManifestVersion, Project: "image-handoff", Tools: map[string]string{"sh": "/bin/sh"}, BuildImages: map[string]v1.BuildImage{"app": {Producer: "build"}}, Outputs: map[string]v1.Output{"control": {Path: ".control", ConcurrencyGroup: "build"}}, Resources: map[string]v1.Resource{"data": {Kind: "directory"}}, Components: map[string]v1.Component{
		"build": {Kind: v1.Job, Runtime: v1.Native, Policy: v1.EachStart, Command: &v1.Command{Tool: "sh", Args: []string{"build.sh"}}, Outputs: []string{"control"}, Environment: v1.Environment{Assign: map[string]v1.Value{"APP_IMAGE": fixtureRef("build_image", "app", "tag"), "DOCKER": fixtureLiteral(docker), "DOCKER_HOST": fixtureLiteral(h.endpoint)}}},
		"check": {Kind: v1.Job, Runtime: v1.Container, Policy: v1.EachStart, Image: ptrValue(fixtureRef("build_image", "app", "id")), Args: []string{"sh", "-c", "cat /marker; test -f /data/witness || echo retained > /data/witness; cat /data/witness"}, DependsOn: []v1.Dependency{{Component: "build", Condition: v1.Completed}}, Resources: []string{"data"}, Mounts: []v1.Mount{{Resource: "data", Target: "/data"}}},
	}, Scenes: map[string]v1.Scene{"dev": {Lifetime: v1.Persistent, Components: []string{"build", "check"}, Resources: []string{"data"}}, "test": {Lifetime: v1.Disposable, Components: []string{"build", "check"}, Resources: []string{"data"}, TerminalJob: "check"}}}

	// Only the fresh-only initializer mounts data: later changed-ID checks must
	// include this consumer even when its successful initialization is skipped.
	init := h.manifest.Components["check"]
	init.Policy = v1.FreshOnly
	init.Initializes = []string{"data"}
	h.manifest.Components["init"] = init
	check := h.manifest.Components["check"]
	check.Mounts, check.Resources = nil, nil
	check.Args = []string{"cat", "/marker"}
	check.Environment = v1.Environment{Assign: map[string]v1.Value{"BACKLOT_FIXTURE": fixtureLiteral(h.token), "INSTANCE": fixtureRef("instance", "", "id")}}
	check.DependsOn = append(check.DependsOn, v1.Dependency{Component: "init", Condition: v1.Completed})
	h.manifest.Components["check"] = check
	for name, scene := range h.manifest.Scenes {
		scene.Components = []string{"build", "init", "check"}
		h.manifest.Scenes[name] = scene
	}
	h.write()
	files := map[string]string{
		"Dockerfile": "FROM " + base + "\nLABEL io.backlot.fixture=" + token + "\nCOPY marker /marker\n",
		"marker":     "A\n", "mode": "build\n",
		"build.sh": `set -eu
mkdir -p .control
case "$(cat mode)" in
 fail) exit 23;;
 missing) exit 0;;
 cached) "$DOCKER" image tag "$(cat cached)" "$APP_IMAGE"; exit 0;;
 hold) printf '%s\n' "$APP_IMAGE" >> .control/entered; while test ! -f .control/release; do sleep 0.02; done;;
esac
DOCKER_BUILDKIT=0 "$DOCKER" build --pull=false --network=none --rm=true --force-rm=true -t "$APP_IMAGE" .
`,
	}
	for name, text := range files {
		if err := os.WriteFile(filepath.Join(h.project, name), []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
	}
}

func fixtureFile(t *testing.T, project, name, text string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(project, name), []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
}
func awaitBuildControl(t *testing.T, project string) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(filepath.Join(project, ".control/entered")); err == nil {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("producer barrier not reached")
}

func TestBuildImagesDocker(t *testing.T) {
	endpoint := os.Getenv("BACKLOT_DOCKER_TEST_ENDPOINT")
	if endpoint == "" {
		t.Skip("requires explicit local Docker fixture endpoint and cached alpine:3.21")
	}
	binary := buildFixtureBinary(t, t.TempDir(), "../../cmd/backlot")
	base, token := buildImageFixture(t, endpoint)
	h := newDockerHarness(t, binary, endpoint)
	writeBuildImageFixture(t, h, base, token)
	t.Log("fixture source", h.project, "binary", binary, "base", base)
	first := h.state(h.run("dev").Instance.ID, v1.RuntimeReady)
	id := first.Instance.ID
	pin := first.Instance.Execution.BuildImages[0]
	assertImage := func(response v1.InstanceResponse, want string) {
		t.Helper()
		if len(response.Instance.Execution.BuildImages) != 1 || response.Instance.Execution.BuildImages[0].ID != want {
			t.Fatalf("wrong accepted image: %+v", response.Instance.Execution.BuildImages)
		}
		owned := h.container(response.Instance.ID, "check")
		actual, err := h.engine.ContainerInspect(context.Background(), owned.ID, moby.ContainerInspectOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if actual.Container.Image != want || actual.Container.Config.Image != want {
			t.Fatal("container was not created by immutable accepted ID", actual.Container.Image, actual.Container.Config.Image)
		}
	}
	restart := func(allow bool, want v1.InstanceStatus) v1.InstanceResponse {
		t.Helper()
		request := h.request("dev")
		request.InstanceID = id
		request.AllowImageChange = allow
		r, err := h.cli.Restart(context.Background(), request)
		if err != nil {
			t.Fatal(err)
		}
		return h.state(r.Instance.ID, want)
	}
	fixtureFile(t, h.project, "cached", pin.ID)
	fixtureFile(t, h.project, "mode", "cached")
	assertImage(restart(false, v1.RuntimeReady), pin.ID)
	// Reuse must ignore an unused candidate's identity, even with mounted data.
	if _, err := h.cli.StopExecution(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	fixtureFile(t, h.project, "mode", "build")
	fixtureFile(t, h.project, "marker", "B\n")
	reused := h.state(h.run("dev").Instance.ID, v1.RuntimeReady)
	assertImage(reused, pin.ID)
	denied := restart(false, v1.Failed)
	if denied.Instance.Execution.FailureDetail == nil || denied.Instance.Execution.FailureDetail.Code != "unsafe_drift" || len(denied.Instance.Execution.BuildImages) != 0 {
		t.Fatal("changed mounted ID was accepted", denied.Instance.Execution)
	}
	changed := restart(true, v1.RuntimeReady)
	historicalPin, historyErr := h.cli.Attempt(context.Background(), v1.AttemptRequest{APIVersion: v1.Version, InstanceID: id, Attempt: first.Instance.Execution.Attempt})
	if historyErr != nil || len(historicalPin.Attempt.Result.BuildImages) != 1 || historicalPin.Attempt.Result.BuildImages[0].ID != pin.ID {
		t.Fatal("direct image writer lost historical accepted pin", historyErr)
	}
	next := changed.Instance.Execution.BuildImages[0]
	if next.ID == pin.ID || len(changed.Instance.Execution.ImageChangeWarnings) != 1 {
		t.Fatal("missing replacement/warning")
	}
	// Permission does not bypass existing pre-stop initialization/argument fences.
	originalInit := h.manifest.Components["init"]
	drifted := originalInit
	drifted.Args = append(append([]string{}, drifted.Args...), "changed")
	h.manifest.Components["init"] = drifted
	h.write()
	unsafe := h.request("dev")
	unsafe.InstanceID = id
	unsafe.AllowImageChange = true
	if _, err := h.cli.Restart(context.Background(), unsafe); err == nil {
		t.Fatal("image permission bypassed initializer drift")
	}
	h.manifest.Components["init"] = originalInit
	h.write()
	h.state(id, v1.RuntimeReady)
	// Parent-written witness is independent of application initialization.
	h.close()
	g := h.generation(id)
	directory := g.Resources["data"].Name
	// Rootful containers own the old file; the fixture owns its parent directory.
	if err := os.Remove(filepath.Join(directory, "witness")); err != nil {
		t.Fatal(err)
	}
	fixtureFile(t, directory, "witness", "parent-witness\n")
	h.start()
	assertImage(restart(false, v1.RuntimeReady), next.ID)
	if data, err := os.ReadFile(filepath.Join(directory, "witness")); err != nil || string(data) != "parent-witness\n" {
		t.Fatal("retained witness changed", err)
	}
	// Crash a retained direct child; recovery preserves pins and does not resume.
	h.crash()
	h.start()
	h.state(id, v1.Interrupted)
	assertImage(restart(false, v1.RuntimeReady), next.ID)
	// Missing/failed/cancelled handoffs cannot consume a retained prior result.
	for _, mode := range []string{"missing", "fail"} {
		fixtureFile(t, h.project, "mode", mode)
		r := restart(false, v1.Failed)
		if len(r.Instance.Execution.BuildImages) != 0 {
			t.Fatal("failed handoff reused prior pin", mode)
		}
	}
	fixtureFile(t, h.project, "mode", "hold")
	request := h.request("dev")
	request.InstanceID = id
	pending, err := h.cli.Restart(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	awaitBuildControl(t, h.project)
	stopped, err := h.cli.StopExecution(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if stopped.Instance.Execution.Attempt != pending.Instance.Execution.Attempt || len(stopped.Instance.Execution.BuildImages) != 0 || hasComponent(stopped.Instance.Execution.Components, "check") {
		t.Fatal("cancelled producer authorized consumer")
	}
	fixtureFile(t, h.project, ".control/release", "")
	fixtureFile(t, h.project, "mode", "cached")
	fixtureFile(t, h.project, "cached", next.ID)
	accepted := restart(false, v1.RuntimeReady)
	assertImage(accepted, next.ID)
	currentPin := accepted.Instance.Execution.BuildImages[0]
	// Mutate the current accepted tag to another owned image, then reuse the ID.
	if _, err = h.engine.ImageTag(context.Background(), moby.ImageTagOptions{Source: pin.ID, Target: currentPin.Tag}); err != nil {
		t.Fatal(err)
	}
	mutated, err := h.engine.ImageInspect(context.Background(), currentPin.Tag)
	if err != nil {
		t.Fatal(err)
	}
	if mutated.ID != pin.ID || mutated.ID == currentPin.ID {
		t.Fatal("accepted tag mutation did not change image identity")
	}
	if _, err = h.cli.StopExecution(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	retained := h.state(h.run("dev").Instance.ID, v1.RuntimeReady)
	assertImage(retained, next.ID)
	logs, err := h.cli.Logs(context.Background(), v1.LogsRequest{APIVersion: v1.Version, InstanceID: id, Component: "check"})
	if err != nil {
		t.Fatal(err)
	}
	content := ""
	for _, record := range logs.Records {
		if record.Attempt == retained.Instance.Execution.Attempt {
			content += record.Message
		}
	}
	if !strings.Contains(content, "B\n") {
		t.Fatal("retained image content changed after tag mutation", content)
	}
	// A missing retained image fails before executing the builder, never rebuilds.
	if _, err = h.cli.StopExecution(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	image, err := h.engine.ImageInspect(context.Background(), next.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, tag := range image.RepoTags {
		if _, err = h.engine.ImageRemove(context.Background(), tag, moby.ImageRemoveOptions{PruneChildren: false}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = h.engine.ImageRemove(context.Background(), next.ID, moby.ImageRemoveOptions{PruneChildren: false}); err != nil && !errdefs.IsNotFound(err) {
		t.Fatal(err)
	}
	missing := h.state(h.run("dev").Instance.ID, v1.Failed)
	if len(missing.Instance.Execution.Components) != 0 {
		t.Fatal("missing pin launched a producer")
	}
	fixtureFile(t, h.project, "mode", "build")
	fixtureFile(t, h.project, "marker", "reset\n")
	resetRequest := h.request("dev")
	resetRequest.InstanceID = id
	reset, err := h.cli.Reset(context.Background(), resetRequest)
	if err != nil {
		t.Fatal(err)
	}
	reset = h.state(reset.Instance.ID, v1.RuntimeReady)
	if reset.Instance.Execution.BuildImages[0].ID == next.ID {
		t.Fatal("reset failed to accept new generation image")
	}
	if _, err = os.Stat(directory); !os.IsNotExist(err) {
		t.Fatal("reset retained old generation directory", err)
	}
	t.Log("reuse/restart/opt-in/witness/crash/missing/failure/cancellation/reset checks passed")
	t.Run("concurrent worktrees and disposable attempts", func(t *testing.T) {
		// New fixture generation after the deliberately removed pin; original data is explicitly reset/destroyed.
		if _, err := h.cli.Destroy(context.Background(), id); err != nil {
			t.Fatal(err)
		}
		fixtureFile(t, h.project, "mode", "hold")
		git := func(args ...string) {
			t.Helper()
			cmd := exec.Command("git", args...)
			cmd.Dir = h.project
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("git: %v %s", err, out)
			}
		}
		git("init", "-q")
		git("config", "user.name", "Fixture")
		git("config", "user.email", "fixture@example.invalid")
		fixtureFile(t, h.project, ".gitignore", ".control/\ncached\n")
		fixtureFile(t, h.project, "marker", "worktree-A\n")
		git("add", ".")
		git("commit", "-qm", "fixture A")
		a := filepath.Join(filepath.Dir(h.project), "worktree-a")
		git("worktree", "add", "--detach", a, "HEAD")
		fixtureFile(t, h.project, "marker", "worktree-B\n")
		git("add", "marker")
		git("commit", "-qm", "fixture B")
		b := filepath.Join(filepath.Dir(h.project), "worktree-b")
		git("worktree", "add", "--detach", b, "HEAD")
		for _, pair := range []struct {
			scene      string
			projects   []string
			markers    []string
			serialized bool
		}{
			{"dev", []string{a, b}, []string{"worktree-A", "worktree-B"}, false},
			{"test", []string{a, b}, []string{"worktree-A", "worktree-B"}, false},
			{"test", []string{a, a}, []string{"worktree-A", "worktree-A"}, true},
		} {
			t.Log("actual CLI pair", pair.scene, "same checkout", pair.serialized)
			for _, project := range pair.projects {
				if err := os.RemoveAll(filepath.Join(project, ".control")); err != nil {
					t.Fatal(err)
				}
			}
			beforeSecond := map[string]bool{}
			type runningCLI struct {
				command            *exec.Cmd
				done               <-chan error
				output, diagnostic bytes.Buffer
				joined             bool
			}
			commands := []*runningCLI{}
			for i, project := range pair.projects {
				current := &runningCLI{command: exec.Command(binary, "run", pair.scene, "--project", project, "--config", h.config, "--state-dir", h.dir, "--json")}
				current.command.Stdout = &current.output
				current.command.Stderr = &current.diagnostic
				t.Cleanup(func() {
					if current.done != nil && !current.joined {
						if err := current.command.Process.Kill(); err != nil {
							t.Log("CLI cleanup signal", err)
						}
						select {
						case <-current.done:
							current.joined = true
						case <-time.After(10 * time.Second):
							t.Error("CLI child join incomplete")
							h.cleanupUnverified = true
						}
					}
				})
				if err := current.command.Start(); err != nil {
					t.Fatal(err)
				}
				current.done = waitFixtureChild(current.command)
				commands = append(commands, current)
				if pair.serialized && i == 0 {
					awaitBuildControl(t, a)
					listed, err := h.cli.List(context.Background(), v1.ListRequest{APIVersion: v1.Version})
					if err != nil {
						t.Fatal(err)
					}
					for _, instance := range listed.Instances {
						beforeSecond[instance.ID] = true
					}
				}
			}
			if pair.serialized {
				// Observe admission behind the held output lock, not a second producer barrier.
				admitted := false
				deadline := time.Now().Add(15 * time.Second)
				for time.Now().Before(deadline) && !admitted {
					listed, err := h.cli.List(context.Background(), v1.ListRequest{APIVersion: v1.Version})
					if err != nil {
						t.Fatal(err)
					}
					for _, instance := range listed.Instances {
						if beforeSecond[instance.ID] || instance.Status != v1.Starting {
							continue
						}
						second, err := h.cli.Inspect(context.Background(), instance.ID)
						if err != nil {
							t.Fatal(err)
						}
						if second.Instance.Execution == nil || second.Instance.Execution.Attempt == "" {
							continue
						}
						if len(second.Instance.Execution.Components) != 0 {
							t.Fatal("second producer bypassed held checkout output group")
						}
						admitted = true
					}
					if !admitted {
						time.Sleep(20 * time.Millisecond)
					}
				}
				if !admitted {
					t.Fatal("second disposable CLI was not admitted")
				}
				entered, err := os.ReadFile(filepath.Join(a, ".control/entered"))
				if err != nil {
					t.Fatal(err)
				}
				if len(strings.Fields(string(entered))) != 1 {
					t.Fatal("checkout output group allowed overlapping producers")
				}
			} else {
				awaitBuildControl(t, a)
				awaitBuildControl(t, b)
			}
			fixtureFile(t, a, ".control/release", "")
			if !pair.serialized {
				fixtureFile(t, b, ".control/release", "")
			}
			ids := []string{}
			instances := []string{}
			attempts := []string{}
			tags := []string{}
			for i, current := range commands {
				select {
				case err := <-current.done:
					current.joined = true
					if err != nil {
						t.Fatalf("actual CLI failed: %v %s %s", err, current.output.String(), current.diagnostic.String())
					}
				case <-time.After(90 * time.Second):
					t.Fatal("actual CLI did not settle")
				}
				var settled v1.InstanceResponse
				if err := json.Unmarshal(current.output.Bytes(), &settled); err != nil {
					t.Fatal(err, current.output.String())
				}
				want := v1.RuntimeReady
				if pair.scene == "test" {
					want = v1.Succeeded
				}
				if settled.Instance.Status != want {
					t.Fatal("wrong CLI result", settled.Instance.Status)
				}
				result := settled.Instance.Execution
				if len(result.BuildImages) != 1 {
					t.Fatal("missing disposable image handoff")
				}
				image := result.BuildImages[0]
				if !validImageID(image.ID) || image.Attempt != result.Attempt || image.Tag != buildImageTag(settled.Instance.ID, result.Attempt, "app") {
					t.Fatal("wrong attempt-scoped image provenance")
				}
				ids = append(ids, image.ID)
				instances = append(instances, settled.Instance.ID)
				attempts = append(attempts, result.Attempt)
				tags = append(tags, image.Tag)
				logs, err := h.cli.Logs(context.Background(), v1.LogsRequest{APIVersion: v1.Version, InstanceID: settled.Instance.ID, Component: "check"})
				if err != nil {
					t.Fatal(err)
				}
				text := ""
				for _, r := range logs.Records {
					text += r.Message
				}
				if !strings.Contains(text, pair.markers[i]) {
					t.Fatal("wrong checkout image output", text)
				}
				if _, err = h.cli.Destroy(context.Background(), settled.Instance.ID); err != nil {
					t.Fatal(err)
				}
			}
			if instances[0] == instances[1] || attempts[0] == attempts[1] || tags[0] == tags[1] {
				t.Fatal("concurrent CLI attempts shared handoff identity")
			}
			if !pair.serialized && ids[0] == ids[1] {
				t.Fatal("different checkout sources produced the same image")
			}
			if pair.serialized {
				entered, err := os.ReadFile(filepath.Join(a, ".control/entered"))
				if err != nil {
					t.Fatal(err)
				}
				observed := strings.Fields(string(entered))
				if len(observed) != 2 || observed[0] != tags[0] || observed[1] != tags[1] {
					t.Fatal("serialized producers did not receive their own attempt tags")
				}
			}
		}
		// Worktree metadata is private fixture state; remove only our exact worktrees.
		git("worktree", "remove", "--force", a)
		git("worktree", "remove", "--force", b)
	})
	// Permissions are invocation-only and unsupported routes reject the typed field.
	bad := h.request("dev")
	bad.AllowImageChange = true
	if _, err = h.cli.Run(context.Background(), bad); err == nil {
		t.Fatal("run accepted restart permission")
	}
	t.Log("build-image suite completed; registered cleanup joins all fixtures")
}

func TestBuildImagePinValidation(t *testing.T) {
	id := newID()
	attempt := newID()
	pin := v1.BuildImageResult{Name: "app", Producer: "build", Attempt: attempt, Tag: buildImageTag(id, attempt, "app"), ID: "sha256:" + strings.Repeat("a", 64)}
	g := resourceGeneration{ID: newID(), BuildImages: map[string]v1.BuildImageResult{"app": pin}}
	if err := validateBuildImagePins(id, g); err != nil {
		t.Fatal(err)
	}
	pin.Tag = buildImageTag(newID(), attempt, "app")
	g.BuildImages["app"] = pin
	if err := validateBuildImagePins(id, g); err == nil {
		t.Fatal("accepted another instance's tag")
	}
	if buildImageTag(id, attempt, "a_b") == buildImageTag(id, attempt, "a-b") {
		t.Fatal("tag namespace collision")
	}
	// Exactly 128 tag characters at maximum manifest-name length.
	if tag := buildImageTag(id, attempt, strings.Repeat("a", 63)); len(strings.Split(tag, ":")[1]) != 128 {
		t.Fatal("invalid bounded tag", tag)
	}
}
