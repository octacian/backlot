package projectconfig

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	v1 "github.com/octacian/backlot/api/v1"
)

func fixture(t *testing.T) (string, string, v1.Manifest) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", home)
	project := t.TempDir()
	project, err := filepath.EvalSymlinks(project)
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(project, "backlot.yaml")
	if err := os.WriteFile(file, []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	return project, file, v1.Manifest{Inputs: map[string]v1.Input{"token": {Secret: true}}, Tools: map[string]string{"shell": "sh"}}
}
func load(t *testing.T, project, file string, m v1.Manifest) Selection {
	t.Helper()
	s, err := Load(project, project, file, m, false, false)
	if err != nil {
		t.Fatal(err)
	}
	return s
}
func candidate(value string) v1.PrivateConfig {
	return v1.PrivateConfig{Version: v1.ManifestVersion, Inputs: map[string]string{"token": value}}
}

func TestAtomicPublicationConflictsAndFailureOutcomes(t *testing.T) {
	for _, stage := range []string{"generation", "before_commit", "after_commit"} {
		t.Run(stage, func(t *testing.T) {
			project, file, m := fixture(t)
			initial := load(t, project, file, m)
			first, err := initial.Commit(context.Background(), candidate("first"), func() error { return nil })
			if err != nil {
				t.Fatal(err)
			}
			s := load(t, project, file, m)
			registryPath := filepath.Join(s.root, "projects.json")
			before, err := os.ReadFile(registryPath)
			if err != nil {
				t.Fatal(err)
			}
			s.checkpoint = func(point string) error {
				if point == stage {
					return errors.New("injected publication fault")
				}
				return nil
			}
			_, err = s.Commit(context.Background(), candidate("next"), func() error { return nil })
			if err == nil {
				t.Fatal("fault ignored")
			}
			current := load(t, project, file, m)
			after, err2 := os.ReadFile(registryPath)
			if err2 != nil {
				t.Fatal(err2)
			}
			if stage == "after_commit" {
				var detail *v1.PlanError
				if !errors.As(err, &detail) || detail.Code != "setup_committed_uncertain" || current.Config.Inputs["token"] != "next" {
					t.Fatalf("postcommit outcome %v", err)
				}
			} else if !bytes.Equal(before, after) || current.Provenance.Path != first.Path || current.Config.Inputs["token"] != "first" {
				t.Fatal("precommit failure changed active configuration")
			}
			if _, err := os.Stat(first.Path); err != nil {
				t.Fatal("old generation removed")
			}
			temps, err := filepath.Glob(filepath.Join(s.root, "*.tmp"))
			if err != nil || len(temps) != 0 {
				t.Fatal("handled temporary file leaked")
			}
			// A stale reader cannot overwrite the committed association.
			if _, err := initial.Commit(context.Background(), candidate("overwrite"), func() error { return nil }); err == nil {
				t.Fatal("stale writer accepted")
			}
		})
	}
}

func TestManagedSafetyAndRegisteredFailure(t *testing.T) {
	for _, kind := range []string{"missing", "symlink", "mode", "directory", "invalid", "missing-version", "unknown-version"} {
		t.Run(kind, func(t *testing.T) {
			project, file, m := fixture(t)
			s := load(t, project, file, m)
			p, err := s.Commit(context.Background(), candidate("private"), func() error { return nil })
			if err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "missing":
				err = os.Remove(p.Path)
			case "symlink":
				err = os.Rename(p.Path, p.Path+".real")
				if err == nil {
					err = os.Symlink(p.Path+".real", p.Path)
				}
			case "mode":
				err = os.Chmod(p.Path, 0644)
			case "directory":
				err = os.Remove(p.Path)
				if err == nil {
					err = os.Mkdir(p.Path, 0700)
				}
			case "invalid":
				err = os.WriteFile(p.Path, []byte(`{"version":"backlot/v1","inputs":{"typo":"private"}}`), 0600)
			case "missing-version":
				err = os.WriteFile(p.Path, []byte(`{"inputs":{"token":"private"}}`), 0600)
			case "unknown-version":
				err = os.WriteFile(filepath.Join(s.root, "projects.json"), []byte(`{"version":2,"revision":"x","projects":{}}`), 0600)
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err := Load(project, project, file, m, false, false); err == nil {
				t.Fatal("unsafe registered source fell back")
			}
		})
	}
}

func TestReadOnlyAbsenceAndOutsideCheckout(t *testing.T) {
	project, file, m := fixture(t)
	s := load(t, project, file, m)
	if s.Provenance.Present {
		t.Fatal("auto enrollment")
	}
	if _, err := os.Stat(s.root); !os.IsNotExist(err) {
		t.Fatal("read created config root")
	}
	t.Setenv("HOME", project)
	t.Setenv("XDG_CONFIG_HOME", project)
	if _, err := Load(project, project, file, m, false, false); err == nil {
		t.Fatal("checkout private storage allowed")
	}
}

func TestMovedAndReplacedAssociationNeedsExplicitSetup(t *testing.T) {
	project, file, m := fixture(t)
	s := load(t, project, file, m)
	if _, err := s.Commit(context.Background(), candidate("old"), func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	moved := project + "-moved"
	if err := os.Rename(project, moved); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := os.RemoveAll(moved); err != nil {
			t.Error(err)
		}
	}()
	movedFile := filepath.Join(moved, "backlot.yaml")
	if _, err := Load(moved, moved, movedFile, m, false, false); err == nil {
		t.Fatal("moved identity reused credentials")
	}
	replacement, err := Load(moved, moved, movedFile, m, true, true)
	if err != nil {
		t.Fatal(err)
	}
	if replacement.Provenance.Present || len(replacement.Config.Inputs) > 0 {
		t.Fatal("reassociation imported old credentials")
	}
	if err := os.Mkdir(project, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte("new"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(project, project, file, m, false, false); err == nil {
		t.Fatal("replacement reused credentials")
	}
}

func TestCancelledPublicationLeavesAssociationUnchanged(t *testing.T) {
	project, file, m := fixture(t)
	s := load(t, project, file, m)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.Commit(ctx, candidate("cancelled"), func() error { return nil }); err == nil {
		t.Fatal("cancelled setup committed")
	}
	if current := load(t, project, file, m); current.Provenance.Present {
		t.Fatal("cancelled enrollment published")
	}
}

func TestCapturedSelectionReadDoesNotReselectRegistry(t *testing.T) {
	project, file, m := fixture(t)
	initial := load(t, project, file, m)
	first, err := initial.Commit(context.Background(), candidate("captured-first"), func() error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	selected, err := Select(project, project, file, false)
	if err != nil {
		t.Fatal(err)
	}
	publisher := load(t, project, file, m)
	second, err := publisher.Commit(context.Background(), candidate("published-second"), func() error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	captured, err := selected.Read(m, false)
	if err != nil {
		t.Fatal(err)
	}
	if captured.State != "present" || captured.Provenance.Path != first.Path || captured.Config.Inputs["token"] != "captured-first" {
		t.Fatal("captured source was replaced by a second lookup")
	}
	current := load(t, project, file, m)
	if current.Provenance.Path != second.Path || current.Config.Inputs["token"] != "published-second" || current.Provenance.Digest == captured.Provenance.Digest {
		t.Fatal("fixture did not cross complete generations")
	}
}

func TestExplicitRegistrySelectionNeverFallsBack(t *testing.T) {
	project, file, m := fixture(t)
	original := load(t, project, file, m)
	if _, err := original.Commit(context.Background(), candidate("default-only"), func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filepath.Join(original.root, "projects.json"))
	if err != nil {
		t.Fatal(err)
	}
	explicit := filepath.Join(t.TempDir(), "registry")
	t.Setenv("BACKLOT_PROJECT_CONFIG_DIR", explicit)
	selected := load(t, project, file, m)
	if selected.root != explicit || selected.Provenance.Present {
		t.Fatal("selected default registry")
	}
	p, err := selected.Commit(context.Background(), candidate("isolated"), func() error { return nil })
	if err != nil || !strings.HasPrefix(p.Path, explicit+string(filepath.Separator)) {
		t.Fatal("explicit publication", err)
	}
	after, err := os.ReadFile(filepath.Join(original.root, "projects.json"))
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("default registry changed", err)
	}
	for _, root := range []string{"", "relative", project, filepath.Join(project, "nested")} {
		t.Setenv("BACKLOT_PROJECT_CONFIG_DIR", root)
		if _, err := Select(project, project, file, false); err == nil {
			t.Fatal("unsafe explicit root accepted")
		}
	}
	// Safety checks apply even when an existing root contains no registry file.
	for _, kind := range []string{"mode", "symlink", "file"} {
		t.Run(kind, func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "unsafe")
			switch kind {
			case "mode":
				err = os.Mkdir(root, 0755)
			case "symlink":
				err = os.Symlink(explicit, root)
			case "file":
				err = os.WriteFile(root, nil, 0600)
			}
			if err != nil {
				t.Fatal(err)
			}
			t.Setenv("BACKLOT_PROJECT_CONFIG_DIR", root)
			_, err := Select(project, project, file, false)
			var detail *v1.PlanError
			if !errors.As(err, &detail) || detail.SourcePath != filepath.Join(root, "projects.json") {
				t.Fatal("unsafe selected root/provenance", err)
			}
		})
	}
}
