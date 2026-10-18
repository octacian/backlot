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
	"github.com/moby/moby/api/types/container"
	moby "github.com/moby/moby/client"
	v1 "github.com/octacian/backlot/api/v1"
	"github.com/octacian/backlot/internal/plan"
)

func TestDoctorOwnedProbeCancellationAndRecovery(t *testing.T) {
	endpoint, image := os.Getenv("BACKLOT_DOCKER_TEST_ENDPOINT"), os.Getenv("BACKLOT_DOCTOR_TEST_IMAGE")
	if endpoint == "" || image == "" {
		t.Skip("requires owned Docker fixture grant and preexisting diagnostic image")
	}
	root, err := os.MkdirTemp("/tmp", "bl-doctor-owned-")
	if err != nil {
		t.Fatal(err)
	}
	state, err := openStore(root)
	if err != nil {
		t.Fatal(err)
	}
	d, err := newDocker(endpoint)
	if err != nil {
		t.Fatal(err)
	}
	// Every cleanup authority is retained before the first effect. Unknown effects
	// preserve the fixture directory; never delete a familiar name without labels.
	records := map[string]diagnosticProbeRecord{}
	t.Cleanup(func() {
		var failures error
		for key, record := range records {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			e := cleanupDiagnosticProbe(ctx, d, record)
			cancel()
			if e == nil {
				e = state.forgetDiagnosticProbe(key)
			}
			failures = errors.Join(failures, e)
		}
		failures = errors.Join(failures, state.recoverDiagnosticProbes(), state.db.Close(), d.Close())
		if failures != nil {
			t.Error("preserved uncertain diagnostic fixture", root, failures)
		} else if err := os.RemoveAll(root); err != nil {
			t.Error(err)
		}
	})
	svc := service{store: state, directory: root}
	snap := plan.Snapshot{Docker: &v1.DockerConfig{Endpoint: endpoint}}
	instance := v1.Instance{Execution: &v1.ExecutionResult{Origin: "https://unavailable.example.invalid:443"}}
	request := v1.DoctorRequest{ContainerProbeImage: image}
	t.Run("inherited-healthcheck-disabled", func(t *testing.T) {
		base, err := d.ImageInspect(context.Background(), image)
		if err != nil {
			t.Fatal(err)
		}
		buildDir := t.TempDir()
		// Delay DNS so the actual diagnostic stays alive across healthcheck ticks.
		if err := os.WriteFile(filepath.Join(buildDir, "getent"), []byte("#!/bin/sh\nsleep 4\nexit 1\n"), 0700); err != nil {
			t.Fatal(err)
		}
		dockerfile := "FROM " + base.ID + "\nCOPY getent /usr/local/bin/getent\nHEALTHCHECK --interval=100ms --timeout=1s CMD echo INHERITED_WORKLOAD\n"
		if err := os.WriteFile(filepath.Join(buildDir, "Dockerfile"), []byte(dockerfile), 0600); err != nil {
			t.Fatal(err)
		}
		iid := filepath.Join(buildDir, "image-id")
		build := exec.Command("docker", "--host", endpoint, "build", "--pull=false", "--network=none", "--label", "io.backlot.fixture="+newID(), "--iidfile", iid, buildDir)
		// The classic builder accepts an exact local image ID without registry metadata lookup.
		build.Env = append(os.Environ(), "DOCKER_BUILDKIT=0")
		if out, err := build.CombinedOutput(); err != nil {
			t.Fatalf("owned local fixture build: %v: %s", err, out)
		}
		data, err := os.ReadFile(iid)
		if err != nil {
			t.Fatal(err)
		}
		fixtureImage := strings.TrimSpace(string(data))
		t.Logf("owned healthcheck fixture image %s from local base %s", fixtureImage, base.ID)
		t.Cleanup(func() {
			if _, err := d.ImageRemove(context.Background(), fixtureImage, moby.ImageRemoveOptions{PruneChildren: false}); err != nil {
				t.Error("preserved fixture image after cleanup failure", fixtureImage, err)
			} else if _, err := d.ImageInspect(context.Background(), fixtureImage); !errdefs.IsNotFound(err) {
				t.Error("fixture image absence unverified", fixtureImage, err)
			}
		})
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		// Positive control proves this exact image's inherited healthcheck executes.
		key := newID()
		record := diagnosticProbeRecord{Endpoint: endpoint, Resource: ownedResource{Kind: "container", Name: "backlot-doctor-" + key, Token: newID()}}
		records[key] = record
		created, err := d.ContainerCreate(ctx, moby.ContainerCreateOptions{Name: record.Resource.Name, Config: &container.Config{Image: fixtureImage, Entrypoint: []string{"/bin/sh"}, Cmd: []string{"-c", "sleep 30"}, Labels: resourceLabels(record.Resource)}, HostConfig: &container.HostConfig{NetworkMode: "none"}})
		if err != nil {
			t.Fatal(err)
		}
		record.Resource.ID = created.ID
		records[key] = record
		if _, err := d.ContainerStart(ctx, created.ID, moby.ContainerStartOptions{}); err != nil {
			t.Fatal(err)
		}
		for {
			observed, err := d.ContainerInspect(ctx, created.ID, moby.ContainerInspectOptions{})
			if err != nil {
				t.Fatal(err)
			}
			if health := observed.Container.State.Health; health != nil && len(health.Log) > 0 && strings.Contains(health.Log[0].Output, "INHERITED_WORKLOAD") {
				break
			}
			select {
			case <-ctx.Done():
				t.Fatal("positive control never executed inherited healthcheck")
			case <-time.After(50 * time.Millisecond):
			}
		}
		if err := cleanupDiagnosticProbe(ctx, d, record); err != nil {
			t.Fatal(err)
		}
		delete(records, key)
		inspected := false
		svc.checkpoint = func(point string) error {
			if point != "doctor-started" {
				return nil
			}
			list, err := d.ContainerList(ctx, moby.ContainerListOptions{All: true, Filters: moby.Filters{}.Add("ancestor", fixtureImage)})
			if err != nil {
				return err
			}
			if len(list.Items) != 1 {
				t.Errorf("expected exactly one owned probe, got %d", len(list.Items))
				return context.Canceled
			}
			id := list.Items[0].ID
			// Inspect the Engine's merged effective config, not the create request.
			observed, err := d.ContainerInspect(ctx, id, moby.ContainerInspectOptions{})
			if err != nil {
				return err
			}
			config := observed.Container.Config
			if config.Image != fixtureImage || config.Healthcheck == nil || !reflect.DeepEqual(config.Healthcheck.Test, []string{"NONE"}) || len(config.Entrypoint) < 3 || config.Entrypoint[0] != "/bin/sh" {
				t.Error("effective diagnostic config inherited image workload", config)
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(2 * time.Second):
			}
			observed, err = d.ContainerInspect(ctx, id, moby.ContainerInspectOptions{})
			if err != nil {
				return err
			}
			if !observed.Container.State.Running || observed.Container.State.Health != nil {
				t.Error("probe did not remain diagnostic-only across healthcheck ticks", observed.Container.State)
			}
			inspected = true
			return nil
		}
		defer func() { svc.checkpoint = nil }()
		r := v1.DoctorResponse{Healthy: true, Checks: []v1.DoctorCheck{}}
		svc.doctorContainer(ctx, &r, v1.DoctorRequest{ContainerProbeImage: fixtureImage}, snap, instance)
		if !inspected || !svc.ownersJoined() {
			t.Fatal("running diagnostic was not inspected/joined")
		}
		assertDoctorCheck(t, r, "container_dns", v1.DoctorFailed)
		assertDoctorCheck(t, r, "container_probe_cleanup", v1.DoctorOK)
		list, err := d.ContainerList(ctx, moby.ContainerListOptions{All: true, Filters: moby.Filters{}.Add("ancestor", fixtureImage)})
		if err != nil || len(list.Items) != 0 {
			t.Fatal("fixture consumers remain", list, err)
		}
	})

	for _, stage := range []string{"doctor-effect", "doctor-started"} {
		t.Run(stage, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			svc.checkpoint = func(point string) error {
				if point == stage {
					cancel()
					return ctx.Err()
				}
				return nil
			}
			r := v1.DoctorResponse{Healthy: true, Checks: []v1.DoctorCheck{}}
			svc.doctorContainer(ctx, &r, request, snap, instance)
			assertDoctorCheck(t, r, "container_probe", v1.DoctorFailed)
			assertDoctorCheck(t, r, "container_probe_cleanup", v1.DoctorOK)
			if !svc.ownersJoined() {
				t.Fatal("cancelled owner not joined")
			}
			if err := state.recoverDiagnosticProbes(); err != nil {
				t.Fatal(err)
			}
		})
	}
	t.Run("shutdown-joins-running-owner", func(t *testing.T) {
		entered := make(chan struct{})
		release := make(chan struct{})
		finished := make(chan struct{})
		svc.checkpoint = func(point string) error {
			if point == "doctor-started" {
				close(entered)
				<-release
				return context.Canceled
			}
			return nil
		}
		go func() {
			defer close(finished)
			r := v1.DoctorResponse{Healthy: true, Checks: []v1.DoctorCheck{}}
			svc.doctorContainer(context.Background(), &r, request, snap, instance)
		}()
		select {
		case <-entered:
		case <-time.After(10 * time.Second):
			close(release)
			<-finished
			t.Fatal("probe did not start")
		}
		// Simulate the owner acknowledging cancellation before shutdown releases state.
		svc.mu.Lock()
		for _, owner := range svc.diagnosticOwners {
			owner.cancel()
		}
		svc.mu.Unlock()
		close(release)
		if err := svc.stopDiagnostics(); err != nil {
			t.Fatal(err)
		}
		<-finished
		if !svc.ownersJoined() {
			t.Fatal("shutdown released unjoined diagnostic owner")
		}
	})
	svc.checkpoint = nil
	for _, mismatch := range []bool{false, true} {
		t.Run(map[bool]string{false: "lost-create-effect", true: "mismatched-authority-preserved"}[mismatch], func(t *testing.T) {
			key := newID()
			resource := ownedResource{Kind: "container", Name: "backlot-doctor-" + key, Token: newID()}
			network := ownedResource{Kind: "network", Name: "backlot-doctor-network-" + key, Token: newID()}
			record := diagnosticProbeRecord{Endpoint: endpoint, Resource: resource, Network: network}
			records[key] = record
			if err := state.saveDiagnosticProbe(key, record); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			networkID, err := createDockerResource(ctx, d, network)
			if err != nil {
				t.Fatal(err)
			}
			created, err := d.ContainerCreate(ctx, moby.ContainerCreateOptions{Name: resource.Name, Config: &container.Config{Image: image, Entrypoint: []string{"/bin/sh"}, Cmd: []string{"-c", "sleep 30"}, Labels: resourceLabels(resource)}, HostConfig: &container.HostConfig{NetworkMode: container.NetworkMode(networkID)}})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := d.ContainerStart(ctx, created.ID, moby.ContainerStartOptions{}); err != nil {
				t.Fatal(err)
			}
			if mismatch {
				changed := record
				changed.Resource.Token = newID()
				if err := state.saveDiagnosticProbe(key, changed); err != nil {
					t.Fatal(err)
				}
				if err := state.recoverDiagnosticProbes(); err == nil {
					t.Fatal("mismatched ownership accepted")
				}
				if _, err := d.ContainerInspect(ctx, created.ID, moby.ContainerInspectOptions{}); err != nil {
					t.Fatal("uncertain container was changed", err)
				}
				// Restore this fixture's original known authority for its own cleanup only.
				if err := state.saveDiagnosticProbe(key, record); err != nil {
					t.Fatal(err)
				}
			}
			// Names plus capability labels recover the intentionally missing effect IDs.
			if err := state.recoverDiagnosticProbes(); err != nil {
				t.Fatal(err)
			}
			if _, err := d.ContainerInspect(ctx, created.ID, moby.ContainerInspectOptions{}); !errdefs.IsNotFound(err) {
				t.Fatal("container removal unverified", err)
			}
			if _, err := d.NetworkInspect(ctx, networkID, moby.NetworkInspectOptions{}); !errdefs.IsNotFound(err) {
				t.Fatal("network removal unverified", err)
			}
			delete(records, key)
		})
	}
	// No probe images, existing gateways, DNS/trust settings or foreign resources
	// are modified by these fault/recovery controls.
}
