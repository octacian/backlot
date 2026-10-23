//go:build darwin || linux

package daemon

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/containerd/errdefs"
	"github.com/moby/moby/client"
	v1 "github.com/octacian/backlot/api/v1"
	bolt "go.etcd.io/bbolt"
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
			var proveObjectsAbsent func() error
			if mode == "cleanup-failure" {
				proveObjectsAbsent = h.observeDisposableDockerObjects(id)
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
			if mode == "cleanup-failure" && (result.CompletedAt == "" || result.CollectionFailure != "") {
				t.Fatal("cleanup-only failure changed completed collection facts")
			}
			if mode == "cancellation" && !result.Cancelled {
				t.Fatal("cancellation omitted")
			}
			if mode == "collection-failure" {
				proveObjectsAbsent = h.observeDisposableDockerObjects(id)
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
			if mode == "cleanup-failure" {
				expected := *result
				expected.CleanupFailure, expected.CleanupDetails = "", nil
				if !reflect.DeepEqual(inspected.Instance.Execution, &expected) {
					t.Fatal("cleanup retry changed original execution result")
				}
				if err := proveObjectsAbsent(); err != nil {
					t.Fatal(err)
				}
			} else if proveObjectsAbsent != nil {
				h.expectDisposableDockerCollectionFailure(inspected.Instance, proveObjectsAbsent)
			}
		})
	}
}

// These observations never authorize removal. The normal fixture journal owns
// reconciliation; exact live Engine objects and the directory owner marker give
// an independent absence check after Destroy, in addition to the closed journal.
func (h *dockerHarness) observeDisposableDockerObjects(id string) func() error {
	h.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	objects := map[string]ownedResource{}
	remember := func(r ownedResource) {
		if err := verifyResource(ctx, h.engine, r); err != nil {
			h.t.Fatal("original fixture object ownership unavailable", err)
		}
		objects[r.Kind+":"+resourceHandle(r)] = r
	}
	list, err := h.engine.ContainerList(ctx, client.ContainerListOptions{All: true, Filters: client.Filters{}.Add("label", "io.backlot.owner")})
	if err != nil {
		h.t.Fatal(err)
	}
	for _, item := range list.Items {
		out, err := h.engine.ContainerInspect(ctx, item.ID, client.ContainerInspectOptions{})
		if errdefs.IsNotFound(err) {
			continue
		}
		if err != nil {
			h.t.Fatal(err)
		}
		c := out.Container
		if c.Config == nil || !contains(c.Config.Env, "BACKLOT_FIXTURE="+h.token) || !contains(c.Config.Env, "INSTANCE="+id) {
			continue
		}
		remember(ownedResource{Kind: "container", ID: c.ID, Token: c.Config.Labels["io.backlot.owner"]})
		for _, m := range c.Mounts {
			if m.Type == "volume" {
				volume, err := h.engine.VolumeInspect(ctx, m.Name, client.VolumeInspectOptions{})
				if err != nil {
					h.t.Fatal(err)
				}
				remember(ownedResource{Kind: "volume", ID: volume.Volume.Name, Token: volume.Volume.Labels["io.backlot.owner"]})
			}
		}
		for _, endpoint := range c.NetworkSettings.Networks {
			network, err := h.engine.NetworkInspect(ctx, endpoint.NetworkID, client.NetworkInspectOptions{})
			if err != nil {
				h.t.Fatal(err)
			}
			remember(ownedResource{Kind: "network", ID: endpoint.NetworkID, Token: network.Network.Labels["io.backlot.owner"]})
		}
	}
	fixtures, err := h.cli.Fixtures(ctx, v1.FixturesRequest{APIVersion: v1.Version, InstanceID: id, Component: "each"})
	if err != nil || len(fixtures.Fixtures) != 1 {
		h.t.Fatal("original fixture directory unavailable", err)
	}
	directory := fixtures.Fixtures[0].Value
	marker, err := os.ReadFile(filepath.Join(directory, ".backlot-owner"))
	if err != nil {
		h.t.Fatal(err)
	}
	remember(ownedResource{Kind: "directory", Name: directory, Token: string(marker), ID: string(marker)})
	for _, kind := range []string{"container", "network", "volume", "directory"} {
		found := false
		for _, r := range objects {
			found = found || r.Kind == kind
		}
		if !found {
			h.t.Fatal("independent original fixture observation missing", kind)
		}
	}
	return func() error {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		for _, r := range objects {
			var err error
			switch r.Kind {
			case "container":
				_, err = h.engine.ContainerInspect(ctx, r.ID, client.ContainerInspectOptions{})
			case "network":
				_, err = h.engine.NetworkInspect(ctx, r.ID, client.NetworkInspectOptions{})
			case "volume":
				_, err = h.engine.VolumeInspect(ctx, r.ID, client.VolumeInspectOptions{})
			case "directory":
				_, err = os.Lstat(r.Name)
				if os.IsNotExist(err) {
					continue
				}
			}
			if !errdefs.IsNotFound(err) {
				return errors.New("original fixture object absence unverified: " + r.Kind)
			}
		}
		return nil
	}
}

// This exception is deliberately limited to the one fully destroyed Docker-only
// disposable attempt. Native/Caddy authority, extra instances, pending effects or
// changed historical results require their own proof and fail this one closed.
func (h *dockerHarness) expectDisposableDockerCollectionFailure(want v1.Instance, proveObjectsAbsent func() error) {
	h.t.Helper()
	if want.Status != v1.Destroyed || want.Execution == nil || want.Execution.CollectionFailure == "" || want.Execution.CleanupFailure != "" || want.Execution.TerminalExitCode == nil || *want.Execution.TerminalExitCode != 0 {
		h.t.Fatal("expected destroyed original collection-failure result missing")
	}
	h.shutdownFailureCode = "collection_failed"
	h.shutdownFailureProof = func() error {
		db, err := bolt.Open(filepath.Join(h.dir, "state.db"), 0600, &bolt.Options{ReadOnly: true, Timeout: time.Second})
		if err != nil {
			return err
		}
		err = db.View(func(tx *bolt.Tx) error {
			count := 0
			if err := tx.Bucket([]byte("instances")).ForEach(func(key, data []byte) error {
				var r record
				if err := decodeRecord(data, string(key), &r); err != nil {
					return err
				}
				count++
				if r.Instance.ID != want.ID || !reflect.DeepEqual(r.Instance.Execution, want.Execution) || r.Instance.Status != v1.Destroyed || len(r.NativeAbandonments) != 0 || r.Instance.Plan.Publish != nil {
					return errors.New("destroyed original attempt/history or authority changed")
				}
				for _, c := range r.Instance.Plan.Components {
					if c.Runtime != v1.Container {
						return errors.New("native authority requires independent authenticated cleanup proof")
					}
				}
				return nil
			}); err != nil {
				return err
			}
			if count != 1 || tx.Bucket([]byte("meta")).Get([]byte("managed_gateway")) != nil {
				return errors.New("unexpected instance or managed gateway authority")
			}
			for _, name := range []string{"resources", "runtime"} {
				entries := 0
				if err := tx.Bucket([]byte(name)).ForEach(func(key, data []byte) error {
					entries++
					if string(key) != want.ID {
						return errors.New("unexpected private resource/runtime journal")
					}
					if name == "resources" {
						g, err := decodeResources(data)
						if err != nil || g.ID != "" {
							return errors.New("recorded resource cleanup incomplete")
						}
					} else {
						r, err := decodeRuntime(data)
						if err != nil || r.LaunchPending || len(r.Groups) != 0 || r.Gateway != nil {
							return errors.New("recorded native/pending/gateway cleanup incomplete")
						}
					}
					return nil
				}); err != nil {
					return err
				}
				if entries != 1 {
					return errors.New("original private resource/runtime journal missing")
				}
			}
			return nil
		})
		if err := errors.Join(err, db.Close()); err != nil {
			return err
		}
		return proveObjectsAbsent()
	}
}
