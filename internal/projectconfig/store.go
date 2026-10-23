package projectconfig

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	"github.com/gofrs/flock"
	v1 "github.com/octacian/backlot/api/v1"
	"github.com/octacian/backlot/internal/localipc"
	"github.com/octacian/backlot/internal/manifest"
)

type entry struct {
	Identity Identity `json:"identity"`
	File     string   `json:"file"`
}
type registry struct {
	Version  int              `json:"version"`
	Revision string           `json:"revision"`
	Projects map[string]entry `json:"projects"`
}

// Provenance records private source selection, distinguishing absent from historical unknown.
type Provenance struct {
	Identity    Identity `json:"identity"`
	Association string   `json:"association,omitempty"`
	Path        string   `json:"path,omitempty"`
	Present     bool     `json:"present"`
	Digest      string   `json:"digest,omitempty"`
}

// Selection is a captured registry generation and immutable private document.
// Keep it private: Config contains raw input values.
type Selection struct {
	State                           string // Safe selection/read status, including partial failures.
	project, checkout, manifestPath string
	Provenance                      Provenance
	Config                          v1.PrivateConfig
	root                            string
	registry                        registry
	registryDigest                  string
	checkpoint                      func(string) error
	stale                           []string
}

// Read bounds regular document reads, including transient setup imports.
func Read(path string) (result []byte, readErr error) {
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return nil, problem("required configuration file unavailable; check project setup and file permissions")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, problem("cannot open configuration file")
	}
	defer func() {
		if err := f.Close(); err != nil && readErr == nil {
			readErr = problem("cannot close configuration input")
		}
	}()
	info, err = f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return nil, problem("expected regular configuration file")
	}
	data, err := io.ReadAll(io.LimitReader(f, manifest.MaxBytes+1))
	if err != nil || len(data) > manifest.MaxBytes {
		return nil, problem("configuration read failed or exceeds 1 MiB")
	}
	return data, nil
}

// selectedRoot is shared by selection and diagnostic provenance. Explicit selection
// never falls back to an operator registry, even when it is invalid or absent.
func selectedRoot() (string, error) {
	if root, present := os.LookupEnv("BACKLOT_PROJECT_CONFIG_DIR"); present {
		if root == "" || !filepath.IsAbs(root) || strings.ContainsRune(root, 0) {
			return "", problem("BACKLOT_PROJECT_CONFIG_DIR must be a nonempty absolute private project registry directory")
		}
		return filepath.Clean(root), nil
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", problem("cannot select user config directory")
	}
	return filepath.Join(dir, "backlot"), nil
}

func rootPath(checkout string) (string, error) {
	root, err := selectedRoot()
	if err != nil {
		return "", err
	}
	// Canonicalize existing ancestors, preserving a missing suffix without creating it.
	ancestor := root
	suffix := ""
	for {
		_, err = os.Lstat(ancestor)
		if err == nil {
			break
		}
		if !os.IsNotExist(err) {
			return "", problem("cannot inspect configuration directory")
		}
		suffix = filepath.Join(filepath.Base(ancestor), suffix)
		parent := filepath.Dir(ancestor)
		if parent == ancestor {
			return "", problem("cannot resolve configuration directory")
		}
		ancestor = parent
	}
	canonical, err := filepath.EvalSymlinks(ancestor)
	if err != nil {
		return "", problem("cannot resolve configuration directory")
	}
	candidate := filepath.Join(canonical, suffix)
	if within(checkout, candidate) {
		return "", problem("private configuration root must be outside checkout")
	}
	// Do not canonicalize away a managed final-component symlink.
	return filepath.Clean(root), nil
}

func readRegistry(root string) (result registry, revision string, readErr error) {
	defer func() { readErr = manifest.SourceError(readErr, "registry", filepath.Join(root, "projects.json")) }()
	empty := registry{Version: 1, Projects: map[string]entry{}}
	if _, err := os.Lstat(root); os.IsNotExist(err) {
		return empty, "", nil
	}
	// An explicitly selected existing root must itself be safe, including when
	// empty. Preserve default machine-only directories without a registry: those
	// were never managed project storage. Actual registries always validate below.
	if _, explicit := os.LookupEnv("BACKLOT_PROJECT_CONFIG_DIR"); explicit {
		if err := localipc.Directory(root, false); err != nil {
			return empty, "", err
		}
	}
	path := filepath.Join(root, "projects.json")
	if _, err := os.Lstat(path); os.IsNotExist(err) {
		return empty, "", nil
	}
	if err := localipc.Directory(root, false); err != nil {
		return empty, "", err
	}
	if err := localipc.File(path, false); err != nil {
		return empty, "", err
	}
	data, err := Read(path)
	if err != nil {
		return empty, "", err
	}
	var r registry
	if err = manifest.Decode(data, ".json", &r); err != nil {
		return empty, "", err
	}
	if r.Version != 1 || r.Projects == nil || r.Revision == "" {
		return empty, "", problem("unsupported or invalid project registry; inspect registry without modifying it")
	}
	for key, e := range r.Projects {
		if key == "" || !filepath.IsAbs(e.Identity.Directory) || e.Identity.Filesystem == "" || e.Identity.Manifest == "" || filepath.IsAbs(e.Identity.Manifest) || (filepath.Clean(e.Identity.Manifest) != e.Identity.Manifest || e.Identity.Manifest == ".." || strings.HasPrefix(e.Identity.Manifest, ".."+string(filepath.Separator))) || filepath.Base(e.File) != e.File || filepath.Ext(e.File) != ".json" {
			return empty, "", problem("invalid project registry entry")
		}
	}
	return r, digest(data), nil
}

// Select captures one registry generation without reading private values.
// Partial selection provenance remains useful on a selection error.
func Select(project, checkout, file string, reassociate bool) (s Selection, loadErr error) {
	s.State = "unavailable"
	layer, path := "registry", ""
	if root, err := selectedRoot(); err == nil {
		path = filepath.Join(root, "projects.json")
	}
	defer func() { loadErr = manifest.SourceError(loadErr, layer, path) }()
	id, err := Identify(project, checkout, file)
	if err != nil {
		return s, err
	}
	root, err := rootPath(checkout)
	if err != nil {
		return s, err
	}
	s.root = root
	s.project, s.checkout, s.manifestPath = project, checkout, file
	path = filepath.Join(root, "projects.json")
	s.registry, s.registryDigest, err = readRegistry(root)
	if err != nil {
		return s, err
	}
	s.Provenance.Identity = id
	s.State = "absent"
	s.Config = v1.PrivateConfig{Version: v1.ManifestVersion, Inputs: map[string]string{}, Tools: map[string]string{}}
	for key, e := range s.registry.Projects {
		if e.Identity == id {
			s.Provenance.Association = key
			s.Provenance.Present = true
			s.State = "unavailable"
			s.Provenance.Path = filepath.Join(root, "projects", e.File)
			break
		}
		if e.Identity.Manifest == id.Manifest && e.Identity.Git == id.Git && (e.Identity.Directory == id.Directory || e.Identity.Filesystem == id.Filesystem) {
			s.stale = append(s.stale, key)
		}
	}
	if !s.Provenance.Present && len(s.stale) > 0 && !reassociate {
		s.State = "rebound"
		e := s.registry.Projects[s.stale[0]]
		s.Provenance.Association = s.stale[0]
		s.Provenance.Path = filepath.Join(root, "projects", e.File)
		layer, path = "private", s.Provenance.Path
		return s, problem("project association moved or was replaced; run project setup --reassociate with explicit new values")
	}
	current, err := Identify(project, checkout, file)
	if err != nil {
		return s, err
	}
	if current != id {
		s.State = "rebound"
		return s, problem("project identity changed while reading; retry setup or scene operation")
	}
	return s, nil
}

// Read validates the private document from this captured selection without a
// second registry lookup. Staged edits defer only declaration membership.
func (s Selection) Read(m v1.Manifest, staged bool) (result Selection, readErr error) {
	defer func() { readErr = manifest.SourceError(readErr, "private", s.Provenance.Path) }()

	if s.Provenance.Present {
		s.State = "invalid"
		if err := localipc.Directory(filepath.Join(s.root, "projects"), false); err != nil {
			return s, err
		}
		if err := localipc.File(s.Provenance.Path, false); err != nil {
			if os.IsNotExist(err) {
				s.State = "missing"
				return s, problem("registered private file missing; inspect project setup and restore the private file")
			}
			return s, err
		}
		data, err := Read(s.Provenance.Path)
		if err != nil {
			return s, err
		}
		s.Config = v1.PrivateConfig{}
		if err := manifest.Decode(data, ".json", &s.Config); err != nil {
			return s, err
		}
		if err := manifest.ValidatePrivate(s.Config, m, !staged); err != nil {
			return s, err
		}
		s.Provenance.Digest = digest(data)
		s.State = "present"
	}
	current, err := Identify(s.project, s.checkout, s.manifestPath)
	if err != nil {
		s.State = "rebound"
		return s, err
	}
	if current != s.Provenance.Identity {
		s.State = "rebound"
		return s, problem("project identity changed while reading; retry setup or scene operation")
	}
	return s, nil
}

// Load selects and validates one registered private generation without writing.
func Load(project, checkout, file string, m v1.Manifest, staged, reassociate bool) (Selection, error) {
	s, err := Select(project, checkout, file, reassociate)
	if err != nil {
		return s, err
	}
	return s.Read(m, staged)
}

// Commit atomically publishes one complete candidate, failing on any captured-source conflict.
// verify rechecks manifest/machine bytes and identity under the publication lock.
func (s Selection) Commit(ctx context.Context, c v1.PrivateConfig, verify func() error) (result Provenance, commitErr error) {
	result = s.Provenance
	layer, sourcePath := "registry", filepath.Join(s.root, "projects.json")
	defer func() { commitErr = manifest.SourceError(commitErr, layer, sourcePath) }()
	if ctx.Err() != nil {
		return result, problem("setup cancelled before publication; active configuration unchanged")
	}
	if err := localipc.Directory(s.root, true); err != nil {
		return result, err
	}
	lockPath := filepath.Join(s.root, "projects.lock")
	if err := localipc.File(lockPath, false); err != nil && !os.IsNotExist(err) {
		return result, err
	}
	lock := flock.New(lockPath, flock.SetPermissions(0600))
	ok, err := lock.TryLockContext(ctx, 25*time.Millisecond)
	if err != nil || !ok {
		return result, problem("cannot lock project registry; retry setup")
	}
	defer func() { _ = lock.Unlock() }()
	if err := localipc.File(lockPath, false); err != nil {
		return result, err
	}
	r, revision, err := readRegistry(s.root)
	if err != nil {
		return result, err
	}
	if revision != s.registryDigest {
		return result, problem("project registry changed during setup; retry without overwriting another writer")
	}
	if err := verify(); err != nil {
		return result, err
	}
	if s.Provenance.Present {
		layer, sourcePath = "private", s.Provenance.Path
		data, err := Read(s.Provenance.Path)
		if err != nil || digest(data) != s.Provenance.Digest {
			return result, problem("private source changed during setup; retry")
		}
		if err := localipc.File(s.Provenance.Path, false); err != nil {
			return result, err
		}
	}
	layer, sourcePath = "registry", filepath.Join(s.root, "projects.json")
	dir := filepath.Join(s.root, "projects")
	if err := localipc.Directory(dir, true); err != nil {
		return result, err
	}
	id, err := randomID()
	if err != nil {
		return result, err
	}
	data, err := json.Marshal(c)
	if err != nil {
		return result, problem("cannot encode private configuration")
	}
	if len(data) > manifest.MaxBytes {
		return result, problem("private configuration exceeds 1 MiB")
	}
	path := filepath.Join(dir, id+".json")
	if s.checkpoint != nil {
		if err := s.checkpoint("generation"); err != nil {
			return result, err
		}
	}
	if err := writeExclusive(path, data); err != nil {
		return result, err
	}
	if err := syncDir(dir); err != nil {
		return result, err
	}
	association := s.Provenance.Association
	if association == "" {
		association = id
	}
	for _, key := range s.stale {
		delete(r.Projects, key)
	}
	r.Projects[association] = entry{Identity: s.Provenance.Identity, File: id + ".json"}
	r.Revision = id
	encoded, err := json.Marshal(r)
	if err != nil || len(encoded) > manifest.MaxBytes {
		return result, problem("registry exceeds supported size")
	}
	temp := filepath.Join(s.root, ".projects-"+id+".tmp")
	defer func() { _ = os.Remove(temp) }()
	if err := writeExclusive(temp, encoded); err != nil {
		return result, err
	}
	if s.checkpoint != nil {
		if err := s.checkpoint("before_commit"); err != nil {
			return result, err
		}
	}
	if ctx.Err() != nil {
		return result, problem("setup cancelled before publication; active configuration unchanged")
	}
	if err := os.Rename(temp, filepath.Join(s.root, "projects.json")); err != nil {
		return result, problem("registry publication failed before commit; active setup unchanged")
	}
	result = Provenance{Identity: s.Provenance.Identity, Association: association, Path: path, Present: true, Digest: digest(data)}
	syncErr := syncDir(s.root)
	if s.checkpoint != nil {
		syncErr = errors.Join(syncErr, s.checkpoint("after_commit"))
	}
	if syncErr != nil {
		return result, &v1.PlanError{Code: "setup_committed_uncertain", Message: "setup committed but directory sync failed; inspect project setup before retrying; rollback was not performed"}
	}
	return result, nil
}

func randomID() (string, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", problem("secure random generation failed")
	}
	return hex.EncodeToString(b[:]), nil
}
func writeExclusive(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return problem("cannot create private publication file")
	}
	_, writeErr := f.Write(data)
	err = errors.Join(writeErr, f.Sync(), f.Close())
	if err != nil {
		_ = os.Remove(path)
		return problem("private publication write/flush/close failed; active setup unchanged")
	}
	return nil
}
func syncDir(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	return errors.Join(f.Sync(), f.Close())
}

// Same reports whether the captured private selection is unchanged.
func (s Selection) Same(other Selection) bool {
	return s.registryDigest == other.registryDigest && reflect.DeepEqual(s.Provenance, other.Provenance)
}
