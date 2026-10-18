//go:build darwin || linux

package daemon

import (
	"context"
	"errors"
	"os"
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
	for _, fault := range []string{"build-failure", "iidfile-failure", "positive-control-failure"} {
		t.Run("fixture-cleanup/"+fault, func(t *testing.T) {
			fixture := newDoctorImageFixture(t, d, &svc, image)
			fixtureImage, buildErr := fixture.build(endpoint, fault)
			if fault == "positive-control-failure" {
				if buildErr != nil {
					t.Fatal(buildErr)
				}
				ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
				defer cancel()
				created, err := d.ContainerCreate(ctx, moby.ContainerCreateOptions{Config: &container.Config{Image: fixtureImage, Entrypoint: []string{"/bin/sh"}, Cmd: []string{"-c", "sleep 30"}, Labels: resourceLabels(ownedResource{Token: fixture.token})}, HostConfig: &container.HostConfig{NetworkMode: "none"}})
				if err != nil {
					t.Fatal(err)
				}
				if _, err := d.ContainerStart(ctx, created.ID, moby.ContainerStartOptions{}); err != nil {
					t.Fatal(err)
				}
				observed, err := d.ContainerInspect(ctx, created.ID, moby.ContainerInspectOptions{})
				if err != nil || !observed.Container.State.Running {
					t.Fatal("failure control must be live before cleanup", err)
				}
				// Simulate returning on a failed positive-control assertion, without
				// weakening the real control's assertions below or failing this test.
				buildErr = errors.New("injected positive-control failure")
			}
			if buildErr == nil {
				t.Fatal("fixture fault did not fail")
			}
			if fault == "build-failure" && !strings.Contains(buildErr.Error(), "23") {
				t.Fatal("build failed before injected RUN exit 23", buildErr)
			}
			if fault == "iidfile-failure" && !errors.Is(buildErr, os.ErrNotExist) {
				t.Fatal("expected missing iidfile after successful build", buildErr)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			ownedImages, err := d.ImageList(ctx, moby.ImageListOptions{All: true, Filters: moby.Filters{}.Add("label", "io.backlot.fixture="+fixture.token)})
			if err != nil || len(ownedImages.Items) == 0 {
				t.Fatal("fault must leave actual owned images for cleanup", ownedImages, err)
			}
			t.Log("observed expected fixture failure:", buildErr)
			// No iid/effect ID is needed: the same pre-registered cleanup must
			// reconcile exact labels, remove live consumers, then owned images.
			if err := fixture.cleanup(); err != nil {
				t.Fatal(err)
			}
		})
	}
	t.Run("inherited-healthcheck-disabled", func(t *testing.T) {
		fixture := newDoctorImageFixture(t, d, &svc, image)
		fixtureImage, err := fixture.build(endpoint, "")
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		// Positive control proves this exact image's inherited healthcheck executes.
		key := newID()
		record := diagnosticProbeRecord{Endpoint: endpoint, Resource: ownedResource{Kind: "container", Name: "backlot-doctor-" + key, Token: fixture.token}}
		created, err := d.ContainerCreate(ctx, moby.ContainerCreateOptions{Name: record.Resource.Name, Config: &container.Config{Image: fixtureImage, Entrypoint: []string{"/bin/sh"}, Cmd: []string{"-c", "sleep 30"}, Labels: resourceLabels(record.Resource)}, HostConfig: &container.HostConfig{NetworkMode: "none"}})
		if err != nil {
			t.Fatal(err)
		}
		record.Resource.ID = created.ID
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
	// Existing images, gateways, DNS/trust settings and foreign resources
	// are preserved by these fault/recovery controls.
}
