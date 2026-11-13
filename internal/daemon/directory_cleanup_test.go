package daemon

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func directoryCleanupFixture(t *testing.T) (*store, ownedResource) {
	t.Helper()
	s := &store{directory: t.TempDir()}
	r := ownedResource{Kind: "directory", Token: newID()}
	r.ID = r.Token
	r.Name = filepath.Join(s.directory, "data", r.Token)
	if err := os.MkdirAll(filepath.Join(r.Name, "nested"), 0700); err != nil {
		t.Fatal(err)
	}
	for path, data := range map[string]string{filepath.Join(r.Name, ".backlot-owner"): r.Token, filepath.Join(r.Name, "nested", "data"): "owned data"} {
		if err := os.WriteFile(path, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	return s, r
}
func TestDirectoryCleanupRetryWitness(t *testing.T) {
	for _, point := range []string{"directory-authority-durable", "directory-content-removed", "directory-witness-durable", "directory-marker-removed", "directory-root-removed", "directory-witness-removed"} {
		t.Run(point, func(t *testing.T) {
			s, r := directoryCleanupFixture(t)
			injected := errors.New("fixture interruption")
			hit := false
			s.sharedCheckpoint = func(p string) error {
				if p == point {
					hit = true
					return injected
				}
				return nil
			}
			if err := s.removeOwnedDirectory(context.Background(), r); !errors.Is(err, injected) || !hit {
				t.Fatal("interruption not observed", err)
			}
			s.sharedCheckpoint = nil
			if err := s.removeOwnedDirectory(context.Background(), r); err != nil {
				t.Fatal("retry failed", err)
			}
			if _, err := os.Lstat(r.Name); !os.IsNotExist(err) {
				t.Fatal("root remains", err)
			}
			if _, err := os.Lstat(filepath.Join(s.directory, "data", ".backlot-delete-"+r.Token)); !os.IsNotExist(err) {
				t.Fatal("witness remains", err)
			}
		})
	}
}
func TestDirectoryCleanupPreservesUnprovenReplacement(t *testing.T) {
	for _, mode := range []string{"nonempty", "symlink", "token", "witness", "outside", "legacy-markerless"} {
		t.Run(mode, func(t *testing.T) {
			s, r := directoryCleanupFixture(t)
			switch mode {
			case "nonempty", "symlink":
				s.sharedCheckpoint = func(p string) error {
					if p != "directory-marker-removed" {
						return nil
					}
					if err := os.Remove(r.Name); err != nil {
						return err
					}
					if mode == "symlink" {
						return os.Symlink(t.TempDir(), r.Name)
					}
					if err := os.Mkdir(r.Name, 0700); err != nil {
						return err
					}
					return os.WriteFile(filepath.Join(r.Name, "foreign"), []byte("keep"), 0600)
				}
			case "token":
				if err := os.WriteFile(filepath.Join(r.Name, ".backlot-owner"), []byte("foreign"), 0600); err != nil {
					t.Fatal(err)
				}
			case "witness":
				if err := os.WriteFile(filepath.Join(s.directory, "data", ".backlot-delete-"+r.Token), []byte("foreign"), 0600); err != nil {
					t.Fatal(err)
				}
			case "outside":
				r.Name = t.TempDir()
			case "legacy-markerless":
				if err := os.Remove(filepath.Join(r.Name, ".backlot-owner")); err != nil {
					t.Fatal(err)
				}
			}
			if err := s.removeOwnedDirectory(context.Background(), r); err == nil {
				t.Fatal("unproven deletion accepted")
			}
			s.sharedCheckpoint = nil
			if err := s.removeOwnedDirectory(context.Background(), r); err == nil {
				t.Fatal("unproven retry accepted")
			}
			if _, err := os.Lstat(r.Name); err != nil {
				t.Fatal("replacement lost", err)
			}
			if mode == "nonempty" {
				data, err := os.ReadFile(filepath.Join(r.Name, "foreign"))
				if err != nil || string(data) != "keep" {
					t.Fatal("foreign data lost", err)
				}
			}
		})
	}
}
func TestDirectoryCleanupCancellationAndSymlinkContents(t *testing.T) {
	s, r := directoryCleanupFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := s.removeOwnedDirectory(ctx, r); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(r.Name, "nested", "data")); err != nil {
		t.Fatal("cancelled deletion changed contents", err)
	}
	external := t.TempDir()
	if err := os.WriteFile(filepath.Join(external, "keep"), []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(external, filepath.Join(r.Name, "external")); err != nil {
		t.Fatal(err)
	}
	if err := s.removeOwnedDirectory(context.Background(), r); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(external, "keep")); err != nil {
		t.Fatal("external symlink target modified", err)
	}
}

func TestDirectoryCleanupCancelsBetweenContentsAndWitness(t *testing.T) {
	s, r := directoryCleanupFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.sharedCheckpoint = func(point string) error {
		if point == "directory-content-removed" {
			cancel()
		}
		return nil
	}
	if err := s.removeOwnedDirectory(ctx, r); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	marker, err := os.ReadFile(filepath.Join(r.Name, ".backlot-owner"))
	if err != nil || string(marker) != r.Token {
		t.Fatal("cancellation lost original authority", err)
	}
	if _, err = os.Stat(filepath.Join(s.directory, "data", ".backlot-delete-"+r.Token)); !os.IsNotExist(err) {
		t.Fatal("cancelled operation entered terminal deletion", err)
	}
	s.sharedCheckpoint = nil
	if err = s.removeOwnedDirectory(context.Background(), r); err != nil {
		t.Fatal("cancelled directory could not retry", err)
	}
}

func TestDirectoryCleanupPinsOriginalAuthority(t *testing.T) {
	s, r := directoryCleanupFixture(t)
	if err := os.WriteFile(filepath.Join(r.Name, ".backlot-owner"), []byte("foreign"), 0600); err != nil {
		t.Fatal(err)
	}
	moved := filepath.Join(s.directory, "data", "foreign")
	s.sharedCheckpoint = func(point string) error {
		if point != "directory-root-opened" {
			return nil
		}
		if err := os.Rename(r.Name, moved); err != nil {
			return err
		}
		if err := os.Mkdir(r.Name, 0700); err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(r.Name, ".backlot-owner"), []byte(r.Token), 0600)
	}
	err := s.removeOwnedDirectory(context.Background(), r)
	if err == nil {
		t.Fatal("replacement marker authorized another opened directory")
	}
	if strings.Contains(err.Error(), r.Token) {
		t.Fatal("private witness capability exposed in error")
	}
	if _, err = os.Stat(filepath.Join(moved, "nested", "data")); err != nil {
		t.Fatal("unproven original contents removed", err)
	}
}
