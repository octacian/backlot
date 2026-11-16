package daemon

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/octacian/backlot/internal/localipc"
)

// removeOwnedDirectory preserves the original marker through content deletion and
// retains terminal removal authority outside the directory until absence is durable.
func (s *store) removeOwnedDirectory(ctx context.Context, r ownedResource) (cleanupErr error) {
	defer func() {
		if cleanupErr != nil {
			cleanupErr = &directoryCleanupError{cause: cleanupErr}
		}
	}()
	parentPath := filepath.Join(s.directory, "data")
	if !validID(r.Token) || r.Name != filepath.Join(parentPath, r.Token) || r.ID != "" && r.ID != r.Token {
		return problem("cleanup_failed", "directory journal location or authority differs; preserved")
	}
	if _, err := os.Lstat(parentPath); os.IsNotExist(err) {
		return nil
	}
	if err := localipc.Directory(parentPath, false); err != nil {
		return err
	}
	parent, err := os.OpenRoot(parentPath)
	if err != nil {
		return err
	}
	defer func() { _ = parent.Close() }()
	syncDir := func(root *os.Root) error {
		f, e := root.Open(".")
		if e != nil {
			return e
		}
		return errors.Join(f.Sync(), f.Close())
	}
	witness := ".backlot-delete-" + r.Token
	checkMarker := func(path string) error {
		if err := localipc.File(filepath.Join(parentPath, path), false); err != nil {
			return err
		}
		f, e := parent.Open(path)
		if e != nil {
			return e
		}
		data, e := io.ReadAll(io.LimitReader(f, 65))
		e = errors.Join(e, f.Close())
		if e != nil {
			return e
		}
		if string(data) != r.Token {
			return problem("cleanup_failed", "directory ownership uncertain; preserved")
		}
		return nil
	}
	_, witnessErr := parent.Lstat(witness)
	if witnessErr != nil && !errors.Is(witnessErr, fs.ErrNotExist) {
		return witnessErr
	}
	hasWitness := witnessErr == nil
	if hasWitness {
		if err := checkMarker(witness); err != nil {
			return err
		}
	}
	info, rootErr := parent.Lstat(r.Token)
	if rootErr != nil && !errors.Is(rootErr, fs.ErrNotExist) {
		return rootErr
	}
	if rootErr == nil {
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return problem("cleanup_failed", "directory root changed; preserved")
		}
		if err := localipc.Directory(r.Name, false); err != nil {
			return err
		}
		root, e := parent.OpenRoot(r.Token)
		if e != nil {
			return e
		}
		defer func() { _ = root.Close() }()
		if e = s.sharedPoint("directory-root-opened"); e != nil {
			return e
		}
		marker := filepath.Join(r.Token, ".backlot-owner")
		markerInfo, markerErr := root.Lstat(".backlot-owner")
		if markerErr == nil {
			if err := checkMarker(marker); err != nil {
				return err
			}

			if hasWitness {
				a, e := parent.Lstat(witness)
				if e != nil {
					return e
				}
				b, e := root.Lstat(".backlot-owner")
				if e != nil {
					return e
				}
				if !os.SameFile(a, b) {
					return problem("cleanup_failed", "directory witness differs from original marker; preserved")
				}
			}
			f, e := root.Open(".backlot-owner")
			if e != nil {
				return e
			}
			data, readErr := io.ReadAll(io.LimitReader(f, 65))
			opened, statErr := f.Stat()
			if readErr != nil || statErr != nil || string(data) != r.Token || !os.SameFile(markerInfo, opened) {
				_ = f.Close()
				return problem("cleanup_failed", "opened directory ownership differs; preserved")
			}
			if e = errors.Join(f.Sync(), f.Close()); e != nil {
				return e
			}
			if e = syncDir(root); e != nil {
				return e
			}
			if e = syncDir(parent); e != nil {
				return e
			}
			if e = s.sharedPoint("directory-authority-durable"); e != nil {
				return e
			}
			dir, e := root.Open(".")
			if e != nil {
				return e
			}
			entries, e := dir.ReadDir(-1)
			closeErr := dir.Close()
			if e = errors.Join(e, closeErr); e != nil {
				return e
			}
			for _, entry := range entries {
				if entry.Name() == ".backlot-owner" {
					continue
				}
				if e = cleanupBefore(ctx); e != nil {
					return e
				}
				if e = root.RemoveAll(entry.Name()); e != nil {
					return e
				}
				if e = s.sharedPoint("directory-content-removed"); e != nil {
					return e
				}
			}
			remaining, e := root.Open(".")
			if e != nil {
				return e
			}
			last, e := remaining.ReadDir(-1)
			closeErr = remaining.Close()
			if e = errors.Join(e, closeErr); e != nil {
				return e
			}
			if len(last) != 1 || last[0].Name() != ".backlot-owner" {
				return problem("cleanup_failed", "directory contents changed; original authority retained")
			}
			if e = syncDir(root); e != nil {
				return e
			}
			if e = cleanupBefore(ctx); e != nil {
				return e
			}
			if !hasWitness {
				if e = parent.Link(marker, witness); e != nil {
					return e
				}
			}
			w, e := parent.Lstat(witness)
			if e != nil {
				return e
			}
			m, e := root.Lstat(".backlot-owner")
			if e != nil {
				return e
			}
			if !os.SameFile(w, m) || !os.SameFile(w, markerInfo) {
				return problem("cleanup_failed", "witness does not preserve original authority; preserved")
			}
			if e = syncDir(parent); e != nil {
				return e
			}
			if e = s.sharedPoint("directory-witness-durable"); e != nil {
				return e
			}
			if e = root.Remove(".backlot-owner"); e != nil {
				return e
			}
			if e = syncDir(root); e != nil {
				return e
			}
			if e = s.sharedPoint("directory-marker-removed"); e != nil {
				return e
			}
		} else if !errors.Is(markerErr, fs.ErrNotExist) || !hasWitness {
			return problem("cleanup_failed", "directory ownership marker unavailable; preserved")
		}
		// Witness-only authority never permits recursive removal of a replacement.
		current, e := parent.Lstat(r.Token)
		if e != nil {
			return e
		}
		opened, e := root.Stat(".")
		if e != nil {
			return e
		}
		if !os.SameFile(current, opened) {
			return problem("cleanup_failed", "directory root changed; preserved")
		}
		if e = parent.Remove(r.Token); e != nil {
			return e
		}
	} else if !hasWitness {
		return syncDir(parent)
	}
	if err := syncDir(parent); err != nil {
		return err
	}
	if err := s.sharedPoint("directory-root-removed"); err != nil {
		return err
	}
	if err := parent.Remove(witness); err != nil {
		return err
	}
	if err := syncDir(parent); err != nil {
		return err
	}
	return s.sharedPoint("directory-witness-removed")
}

// Error text stays safe for exact-ID responses; the cause remains available for
// cancellation classification without exposing private witness paths/capabilities.
type directoryCleanupError struct{ cause error }

func (e *directoryCleanupError) Error() string {
	return "owned directory deletion incomplete; retain private state, restore ownership/filesystem durability and retry the recorded instance"
}
func (e *directoryCleanupError) Unwrap() error { return e.cause }
