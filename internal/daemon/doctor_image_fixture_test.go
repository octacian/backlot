//go:build darwin || linux

package daemon

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/containerd/errdefs"
	moby "github.com/moby/moby/client"
)

// doctorImageFixture owns the build label before effects, including intermediate
// images and containers. It never treats an ancestor or a familiar name as owned.
type doctorImageFixture struct {
	t                     *testing.T
	d                     *dockerEngine
	svc                   *service
	token, base, buildDir string
	buildUncertain        bool
}

func newDoctorImageFixture(t *testing.T, d *dockerEngine, svc *service, image string) *doctorImageFixture {
	t.Helper()
	f := &doctorImageFixture{t: t, d: d, svc: svc, token: newID()}
	// Registered before image inspection, file writes, or the first build effect.
	t.Cleanup(func() {
		if err := f.cleanup(); err != nil {
			t.Error("preserved uncertain fixture; ownership label/directory", f.token, f.buildDir, err)
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	base, err := d.ImageInspect(ctx, image)
	if err != nil {
		t.Fatal(err)
	}
	f.base = base.ID
	f.buildDir, err = os.MkdirTemp("/tmp", "bl-doctor-image-")
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *doctorImageFixture) build(endpoint, fault string) (string, error) {
	// Label first so every later intermediate inherits exact cleanup authority.
	dockerfile := "FROM " + f.base + "\nLABEL io.backlot.fixture=" + f.token + " io.backlot.owner=" + f.token + "\nCOPY getent /usr/local/bin/getent\n"
	if fault == "build-failure" {
		dockerfile += "RUN exit 23\n"
	}
	dockerfile += "HEALTHCHECK --interval=100ms --timeout=1s CMD echo INHERITED_WORKLOAD\n"
	if err := os.WriteFile(filepath.Join(f.buildDir, "getent"), []byte("#!/bin/sh\nsleep 4\nexit 1\n"), 0700); err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(f.buildDir, "Dockerfile"), []byte(dockerfile), 0600); err != nil {
		return "", err
	}
	iid := filepath.Join(f.buildDir, "image-id")
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	// Classic builder accepts an exact local base ID without registry lookup.
	// Force removal also covers the builder's pre-LABEL intermediate container.
	build := exec.CommandContext(ctx, "docker", "--host", endpoint, "build", "--pull=false", "--network=none", "--rm=true", "--force-rm=true", "--iidfile", iid, f.buildDir)
	build.Env = append(os.Environ(), "DOCKER_BUILDKIT=0")
	if out, err := build.CombinedOutput(); err != nil {
		f.buildUncertain = ctx.Err() != nil
		return "", fmt.Errorf("owned local fixture build: %w: %s", err, out)
	}
	if fault == "iidfile-failure" {
		if err := os.Remove(iid); err != nil {
			return "", err
		}
	}
	data, err := os.ReadFile(iid)
	if err != nil {
		return "", err
	}
	id := strings.TrimSpace(string(data))
	observed, err := f.d.ImageInspect(ctx, id)
	if err != nil {
		return "", err
	}
	if observed.Config == nil || observed.Config.Labels["io.backlot.fixture"] != f.token || id == f.base {
		return "", errors.New("fixture image authority unverified")
	}
	f.t.Logf("owned healthcheck fixture image %s from local base %s", id, f.base)
	return id, nil
}

func (f *doctorImageFixture) cleanup() error {
	// Join diagnostic owners and reconcile their separate journal authority before
	// touching images. Control/build consumers use this fixture's retained token.
	if err := f.svc.stopDiagnostics(); err != nil {
		return err
	}
	if f.buildUncertain {
		return fmt.Errorf("build completion uncertain; preserve label %s and directory %s", f.token, f.buildDir)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	filters := moby.Filters{}.Add("label", "io.backlot.owner="+f.token)
	consumers, err := f.d.ContainerList(ctx, moby.ContainerListOptions{All: true, Filters: filters})
	if err != nil {
		return err
	}
	for _, c := range consumers.Items {
		if err := removeContainer(ctx, f.d, ownedResource{Kind: "container", ID: c.ID, Token: f.token}); err != nil {
			return fmt.Errorf("preserved consumer %s: %w", c.ID, err)
		}
		if _, err := f.d.ContainerInspect(ctx, c.ID, moby.ContainerInspectOptions{}); !errdefs.IsNotFound(err) {
			return fmt.Errorf("consumer %s absence unverified: %v", c.ID, err)
		}
	}
	remaining, err := f.d.ContainerList(ctx, moby.ContainerListOptions{All: true, Filters: filters})
	if err != nil || len(remaining.Items) != 0 {
		return fmt.Errorf("fixture consumers remain: %v: %v", remaining.Items, err)
	}
	images, err := f.d.ImageList(ctx, moby.ImageListOptions{All: true, Filters: moby.Filters{}.Add("label", "io.backlot.fixture="+f.token)})
	if err != nil {
		return err
	}
	pending := make(map[string]error, len(images.Items))
	for _, img := range images.Items {
		observed, err := f.d.ImageInspect(ctx, img.ID)
		if err != nil {
			return err
		}
		if img.ID == f.base || observed.Config == nil || observed.Config.Labels["io.backlot.fixture"] != f.token || observed.Config.Labels["io.backlot.owner"] != f.token {
			return fmt.Errorf("image authority uncertain: %s", img.ID)
		}
		pending[img.ID] = nil
	}
	// Docker may list parents before children. Retry only this fixed exact owned
	// set while making progress; never prune ancestors or force foreign consumers.
	for len(pending) > 0 {
		progress := false
		for id := range pending {
			if _, err := f.d.ImageRemove(ctx, id, moby.ImageRemoveOptions{PruneChildren: false}); err != nil {
				pending[id] = err
				continue
			}
			if _, err := f.d.ImageInspect(ctx, id); !errdefs.IsNotFound(err) {
				return fmt.Errorf("fixture image %s absence unverified: %v", id, err)
			}
			f.t.Log("verified owned fixture image absent", id)
			delete(pending, id)
			progress = true
		}
		if !progress {
			return fmt.Errorf("preserved owned fixture images: %v", pending)
		}
	}
	if f.base != "" {
		if _, err := f.d.ImageInspect(ctx, f.base); err != nil {
			return fmt.Errorf("shared base preservation unverified: %w", err)
		}
	}
	if f.buildDir != "" {
		if err := os.RemoveAll(f.buildDir); err != nil {
			return err
		}
	}
	f.t.Logf("verified fixture consumers/images absent and shared base preserved %s (label %s)", f.base, f.token)
	return nil
}
