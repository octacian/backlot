package projectconfig

import (
	"context"
	"encoding/json"
	"errors"
	v1 "github.com/octacian/backlot/api/v1"
	"os"
	"path/filepath"
	"testing"
)

func TestLegacyMetadataEnrollmentPreservesPrivateGeneration(t *testing.T) {
	project, file, m := fixture(t)
	s := load(t, project, file, m)
	first, err := s.Commit(context.Background(), candidate("owned-private-value"), func() error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	r, _, err := readRegistry(s.root)
	if err != nil {
		t.Fatal(err)
	}
	r.Version = 1
	raw, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.root, "projects.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(first.Path)
	if err != nil {
		t.Fatal(err)
	}
	s = load(t, project, file, m)
	enrolled, err := s.EnrollPrivate(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if enrolled.Path != first.Path || enrolled.Association != first.Association || enrolled.Digest != first.Digest || enrolled.CommonID == "" {
		t.Fatal("metadata enrollment rotated private generation")
	}
	after, err := os.ReadFile(first.Path)
	if err != nil || string(after) != string(before) {
		t.Fatal("private generation changed", err)
	}
	r, _, err = readRegistry(s.root)
	if err != nil {
		t.Fatal(err)
	}
	entry := r.Projects[first.Association]
	entry.Identity.Filesystem = "historical-device-observation"
	r.Projects[first.Association] = entry
	raw, err = json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.root, "projects.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	selected := load(t, project, file, m)
	if selected.Provenance.Identity.Filesystem != "historical-device-observation" || selected.Config.Inputs["token"] != "owned-private-value" || selected.Provenance.Path != first.Path {
		t.Fatal("logical recognition rewrote provenance or lost private continuity")
	}
}

func TestPrivateSelectionRejectsSharedCheckoutAdmin(t *testing.T) {
	project, file, m := fixture(t)
	admin := filepath.Join(project, ".git")
	if err := os.Mkdir(admin, 0700); err != nil {
		t.Fatal(err)
	}
	s := load(t, project, file, m)
	first, err := s.Commit(context.Background(), candidate("owned-private-value"), func() error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	s = load(t, project, file, m)
	if _, err := s.EnrollPrivate(context.Background()); err != nil {
		t.Fatal(err)
	}
	copy := t.TempDir()
	copy, err = filepath.EvalSymlinks(copy)
	if err != nil {
		t.Fatal(err)
	}
	copiedManifest := filepath.Join(copy, "backlot.yaml")
	if err := os.WriteFile(copiedManifest, []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(copy, ".git"), []byte("gitdir: "+admin+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	_, err = Load(copy, copy, copiedManifest, m, false, false)
	var detail *v1.PlanError
	if !errors.As(err, &detail) || detail.Code != "project_registration" {
		t.Fatal("copied Git file imported private association", err)
	}
	if current := load(t, project, file, m); current.Provenance.Path != first.Path {
		t.Fatal("refused alias changed original private generation")
	}
}

func TestCheckoutPathDoesNotTransferPrivateRegistrationContext(t *testing.T) {
	for _, mode := range []string{"git-adoption", "different-admin"} {
		t.Run(mode, func(t *testing.T) {
			project, file, m := fixture(t)
			if mode == "different-admin" {
				admin := t.TempDir()
				if err := os.WriteFile(filepath.Join(project, ".git"), []byte("gitdir: "+admin+"\n"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			selected := load(t, project, file, m)
			first, err := selected.Commit(context.Background(), candidate("owned-original"), func() error { return nil })
			if err != nil {
				t.Fatal(err)
			}
			selected = load(t, project, file, m)
			if _, err := selected.EnrollPrivate(context.Background()); err != nil {
				t.Fatal(err)
			}
			before, err := os.ReadFile(first.Path)
			if err != nil {
				t.Fatal(err)
			}
			nextAdmin := t.TempDir()
			if err := os.WriteFile(filepath.Join(project, ".git"), []byte("gitdir: "+nextAdmin+"\n"), 0600); err != nil {
				t.Fatal(err)
			}
			next, err := Load(project, project, file, m, false, false)
			if err != nil {
				t.Fatal("new administrative context rejected by checkout path alone", err)
			}
			if next.Provenance.Present || next.Config.Inputs["token"] != "" {
				t.Fatal("new context imported old private values")
			}
			enrolled, unlock, err := next.Enroll(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			defer unlock()
			saved, err := enrolled.Commit(context.Background(), candidate("owned-new-context"), func() error { return nil })
			if err != nil {
				t.Fatal("explicit new-context setup failed", err)
			}
			after, err := os.ReadFile(first.Path)
			if err != nil || string(after) != string(before) || saved.Association == first.Association || saved.Path == first.Path {
				t.Fatal("context change reused or changed original private generation", err)
			}
		})
	}
}
