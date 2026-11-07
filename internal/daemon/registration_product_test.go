//go:build darwin || linux

package daemon

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	v1 "github.com/octacian/backlot/api/v1"
	"github.com/octacian/backlot/internal/native"
	"github.com/octacian/backlot/internal/plan"
	"github.com/octacian/backlot/internal/projectconfig"
	"github.com/octacian/backlot/internal/registration"
)

func TestRegistrationProductChoices(t *testing.T) {
	binary := buildFixtureBinary(t, t.TempDir(), "../../cmd/backlot")
	for _, choice := range []string{"copy", "original", "copy-nongit", "original-nongit"} {
		gitProject := !strings.HasSuffix(choice, "-nongit")
		chooseOriginal := strings.HasPrefix(choice, "original")
		t.Run(choice, func(t *testing.T) {
			registry := shortTemp(t)
			t.Setenv("BACKLOT_PROJECT_CONFIG_DIR", registry)
			h := newNativeHarness(t, binary)
			h.manifest.Inputs = map[string]v1.Input{"token": {Secret: true}}
			server := h.manifest.Components["server"]
			server.Environment.Assign["PRIVATE_TOKEN"] = fixtureRef("input", "token", "")
			h.manifest.Components["server"] = server
			h.write()
			if gitProject {
				git(t, h.project, "init", "-q", "-b", "main")
				git(t, h.project, "add", "backlot.json")
				git(t, h.project, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "-qm", "fixture")
			}
			setup := func(path string, reassociate bool) v1.ProjectSetupResponse {
				t.Helper()
				args := []string{"project", "setup", "--project", path, "--json"}
				input := ""
				if reassociate {
					args = append(args, "--reassociate")
				} else {
					args = append(args, "--from", "-")
					input = "version: backlot/v1\ninputs: {token: owned-private-token}\n"
				}
				cmd := exec.Command(binary, args...)
				cmd.Stdin = strings.NewReader(input)
				out, err := cmd.CombinedOutput()
				if err != nil {
					t.Fatalf("setup: %v %s", err, out)
				}
				var r v1.ProjectSetupResponse
				if err := json.Unmarshal(out, &r); err != nil {
					t.Fatal(err)
				}
				return r
			}
			originalSetup := setup(h.project, false)
			started := h.run("dev")
			h.state(started.Instance.ID, v1.RuntimeReady)
			peers := []string{}
			for _, name := range []string{"peer-a", "peer-b"} {
				if !gitProject {
					break
				}
				peer := filepath.Join(filepath.Dir(h.project), name)
				git(t, h.project, "worktree", "add", "-qb", name, peer)
				peers = append(peers, peer)
				if _, err := h.cli.Prepare(context.Background(), v1.PrepareRequest{APIVersion: v1.Version, Plan: v1.PlanRequest{ProjectPath: peer, Scene: "dev"}}); err != nil {
					t.Fatal(err)
				}
			}
			copyPath := filepath.Join(filepath.Dir(h.project), "copy")
			if out, err := exec.Command("cp", "-R", h.project, copyPath).CombinedOutput(); err != nil {
				t.Fatalf("copy: %v %s", err, out)
			}
			q := v1.PrepareRequest{APIVersion: v1.Version, Plan: v1.PlanRequest{ProjectPath: copyPath, Scene: "dev"}}
			_, err := h.cli.Prepare(context.Background(), q)
			code(t, err, "project_registration")
			chosen := copyPath
			owner := ""
			if chooseOriginal {
				chosen = h.project
				owner = checkoutParent(started.Instance)
			}
			repair := v1.ReassociationRequest{APIVersion: v1.Version, ProjectPath: copyPath, RegistryRoot: registry, FreshIdentityAt: chosen, CheckoutID: owner}
			preview, err := h.cli.Reassociate(context.Background(), repair)
			if err != nil {
				t.Fatal(err)
			}
			if chooseOriginal {
				if len(preview.Blockers) == 0 {
					t.Fatal("active selected original was not fenced")
				}
				if _, err := h.cli.StopExecution(context.Background(), started.Instance.ID); err != nil {
					t.Fatal(err)
				}
				preview, err = h.cli.Reassociate(context.Background(), repair)
				if err != nil {
					t.Fatal(err)
				}
			}
			if len(preview.Blockers) > 0 {
				t.Fatal("settled rotation blocked", preview.Blockers)
			}
			repair.Apply = true
			repair.Preview = preview.Preview
			applied, err := h.cli.Reassociate(context.Background(), repair)
			if err != nil || !applied.Applied {
				t.Fatal("rotation failed", err)
			}
			if !chooseOriginal {
				h.state(started.Instance.ID, v1.RuntimeReady)
			} else {
				after := setup(h.project, true)
				if after.Association != originalSetup.Association || after.PrivatePath != originalSetup.PrivatePath {
					t.Fatal("original rotation changed private generation")
				}
				for _, peer := range peers {
					if _, err := h.cli.Prepare(context.Background(), v1.PrepareRequest{APIVersion: v1.Version, Plan: v1.PlanRequest{ProjectPath: peer, Scene: "dev"}}); err != nil {
						t.Fatal("linked peer lost common continuity", err)
					}
				}
			}
			copySource, err := plan.Locate(copyPath)
			if err != nil {
				t.Fatal(err)
			}
			selection, err := projectconfig.Select(copySource.ProjectPath, copySource.CheckoutPath, copySource.ManifestPath, false)
			if err != nil {
				t.Fatal(err)
			}
			if selection.Provenance.Present {
				t.Fatal("copy imported original private association")
			}
			setup(copyPath, false)
			prepared, err := h.cli.Prepare(context.Background(), q)
			if err != nil {
				t.Fatal(err)
			}
			if checkoutParent(prepared.Instance) == checkoutParent(started.Instance) {
				t.Fatal("copy adopted original owner")
			}
			inspected, err := h.cli.Inspect(context.Background(), started.Instance.ID)
			if err != nil || checkoutParent(inspected.Instance) != checkoutParent(started.Instance) {
				t.Fatal("retained original owner changed", err)
			}
			h.close()
			if !h.waited || h.cleanupUnverified {
				t.Fatal("owned native cleanup unverified")
			}
		})
	}
}

func TestRegistrationSameBootNativeStopRepair(t *testing.T) {
	t.Setenv("BACKLOT_PROJECT_CONFIG_DIR", shortTemp(t))
	binary := buildFixtureBinary(t, t.TempDir(), "../../cmd/backlot")
	h := newNativeHarness(t, binary)
	boot, err := native.CurrentBoot()
	if err != nil {
		t.Fatal(err)
	}
	started := h.run("dev")
	h.state(started.Instance.ID, v1.RuntimeReady)
	stopped, err := h.cli.StopExecution(context.Background(), started.Instance.ID)
	if err != nil || stopped.Instance.Execution == nil || len(stopped.Instance.Execution.NativeCleanup) == 0 {
		t.Fatal("native stop did not return cleanup observations", err)
	}
	for _, observation := range stopped.Instance.Execution.NativeCleanup {
		if observation.Confidence != v1.ObservedEmpty {
			t.Fatal("native stop was not verified empty", observation.Confidence)
		}
	}
	if current, err := native.CurrentBoot(); err != nil || current != boot {
		t.Fatal("boot changed during actual native stop", err)
	}
	t.Log("actual same-boot native stop returned observed-empty cleanup")
	moved := h.project + "-moved"
	if err := os.Rename(h.project, moved); err != nil {
		t.Fatal(err)
	}
	h.project = moved
	root, err := projectconfig.Root(moved)
	if err != nil {
		t.Fatal(err)
	}
	q := v1.ReassociationRequest{APIVersion: v1.Version, ProjectPath: moved, RegistryRoot: root, CheckoutID: checkoutParent(started.Instance)}
	p, err := h.cli.Reassociate(context.Background(), q)
	if err != nil || len(p.Blockers) != 0 {
		t.Fatal("same-boot verified native stop blocked exact repair", err, p.Blockers)
	}
	q.Apply, q.Preview = true, p.Preview
	if applied, err := h.cli.Reassociate(context.Background(), q); err != nil || !applied.Applied {
		t.Fatal("same-boot exact repair did not apply", err)
	}
}

func TestRegistrationSourceReplacementAndMissingFence(t *testing.T) {
	f := newRepairFixture(t)
	source, err := plan.Locate(f.project)
	if err != nil {
		t.Fatal(err)
	}
	captured, err := captureSource(source)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(captured.registrationLocation.Common, "backlot-common.json")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	_, err = f.s.prepare(context.Background(), request(f.project, f.config, "dev"))
	code(t, err, "project_registration")
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("retained missing marker was republished", err)
	}
	if captured.verify() == nil {
		t.Fatal("marker disappearance escaped source guard")
	}
	if err := os.WriteFile(path, before, 0600); err != nil {
		t.Fatal(err)
	}
	loc := captured.registrationLocation
	unlock, err := registration.Lock(context.Background(), loc)
	if err != nil {
		t.Fatal(err)
	}
	_, err = registration.Rotate(loc, "checkout", captured.registrationIdentity.CheckoutID)
	unlock()
	if err != nil {
		t.Fatal(err)
	}
	if captured.verify() == nil {
		t.Fatal("marker rotation escaped source guard")
	}
}
