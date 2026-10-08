// Package plan resolves offline scene plans without executing or provisioning work.
package plan

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	v1 "github.com/octacian/backlot/api/v1"
	"github.com/octacian/backlot/internal/manifest"
)

func problem(code, field, message string) error {
	return &v1.PlanError{Code: code, Field: field, Message: message}
}

func readFile(file string) (data []byte, readErr error) {
	// Inspect first so opening a FIFO or device cannot block an offline plan.
	info, err := os.Stat(file)
	if err != nil {
		return nil, problem("missing_file", file, "cannot read file; check path and permissions")
	}
	if !info.Mode().IsRegular() {
		return nil, problem("invalid_file", file, "expected a regular file")
	}
	f, err := os.Open(file)
	if err != nil {
		return nil, problem("missing_file", file, "cannot read file; check path and permissions")
	}
	defer func() {
		if err := f.Close(); err != nil && readErr == nil {
			readErr = problem("invalid_file", file, "cannot close input file")
		}
	}()
	info, err = f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return nil, problem("invalid_file", file, "expected a regular file")
	}
	data, err = io.ReadAll(io.LimitReader(f, manifest.MaxBytes+1))
	if err != nil {
		return nil, problem("invalid_file", file, "cannot read file")
	}
	if len(data) > manifest.MaxBytes {
		return nil, problem("invalid_file", file, "file exceeds 1 MiB limit")
	}
	return data, nil
}

func canonical(p string) (string, error) {
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(abs)
}

// Discover searches upward, stopping at a Git checkout boundary or filesystem root.
// Explicit project paths select exactly one directory or a supported manifest file.
func Discover(start, override string) (string, string, error) {
	if override != "" {
		resolved, err := canonical(override)
		if err != nil {
			return "", "", problem("discovery", "project", "project path does not exist")
		}
		info, err := os.Stat(resolved)
		if err != nil {
			return "", "", problem("discovery", "project", "cannot inspect project path")
		}
		if !info.IsDir() {
			base := filepath.Base(resolved)
			if base != "backlot.yaml" && base != "backlot.yml" && base != "backlot.json" {
				return "", "", problem("discovery", "project", "select a project directory or backlot.yaml, backlot.yml, backlot.json")
			}
			resolved = filepath.Dir(resolved)
		}
		file, err := findManifest(resolved)
		if err != nil {
			return "", "", err
		}
		if file == "" {
			return "", "", problem("discovery", "project", "no manifest in explicit project directory")
		}
		return file, resolved, nil
	}
	directory, err := canonical(start)
	if err != nil {
		return "", "", problem("discovery", "project", "cannot resolve working directory")
	}
	for {
		file, err := findManifest(directory)
		if err != nil {
			return "", "", err
		}
		if file != "" {
			return file, directory, nil
		}
		if _, err := os.Lstat(filepath.Join(directory, ".git")); err == nil {
			break
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", "", problem("discovery", directory, "cannot inspect checkout boundary")
		}
		parent := filepath.Dir(directory)
		if parent == directory {
			break
		}
		directory = parent
	}
	return "", "", problem("discovery", "project", "no manifest before checkout root; use --project PATH")
}

func findManifest(directory string) (string, error) {
	var found string
	for _, name := range []string{"backlot.yaml", "backlot.yml", "backlot.json"} {
		file := filepath.Join(directory, name)
		if _, err := os.Lstat(file); err == nil {
			if found != "" {
				return "", problem("discovery", directory, "multiple manifests; retain exactly one of backlot.yaml, backlot.yml, backlot.json")
			}
			found = file
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", problem("discovery", directory, "cannot inspect manifest candidates")
		}
	}
	return found, nil
}

func checkoutRoot(project string) string {
	for directory := project; ; directory = filepath.Dir(directory) {
		if _, err := os.Lstat(filepath.Join(directory, ".git")); err == nil {
			return directory
		}
		if filepath.Dir(directory) == directory {
			return project
		}
	}
}

func containedFile(root, relative string) (string, error) {
	file, err := canonical(filepath.Join(root, filepath.FromSlash(relative)))
	if err != nil {
		return "", problem("missing_file", relative, "required seed does not exist; create it or update seeds")
	}
	rel, err := filepath.Rel(root, file)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", problem("invalid_file", relative, "seed symlink escapes project directory")
	}
	return file, nil
}

// OutputPath resolves existing ancestors and retains the missing suffix without
// creating directories. It rejects symlink escapes from the canonical project root.
// Executors recheck it before mounting checkout output.
func OutputPath(root, relative string) (string, error) {
	candidate := filepath.Join(root, filepath.FromSlash(relative))
	for {
		_, err := os.Lstat(candidate)
		if err == nil {
			resolved, err := canonical(candidate)
			if err != nil {
				return "", problem("invalid_output", relative, "cannot resolve output ancestor")
			}
			rel, err := filepath.Rel(root, resolved)
			if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				return "", problem("invalid_output", relative, "output symlink escapes project directory")
			}
			suffix, err := filepath.Rel(candidate, filepath.Join(root, filepath.FromSlash(relative)))
			if err != nil {
				return "", problem("invalid_output", relative, "cannot resolve output suffix")
			}
			return filepath.Join(resolved, suffix), nil
		}
		if !os.IsNotExist(err) {
			return "", problem("invalid_output", relative, "cannot inspect output ancestor")
		}
		parent := filepath.Dir(candidate)
		if parent == candidate {
			return "", problem("invalid_output", relative, "cannot resolve output ancestor")
		}
		candidate = parent
	}
}

// validateOutputs compares reusable declarations across scenes so separate plans
// cannot assign different groups to the same storage. Unavailable unselected
// paths do not block planning; selected paths still require containment checks.
func validateOutputs(root string, outputs map[string]v1.Output, selected map[string]bool) error {
	paths := map[string]string{}
	for name, output := range outputs {
		resolved, err := OutputPath(root, output.Path)
		if err != nil {
			if selected[name] {
				return err
			}
			continue
		}
		for existing, group := range paths {
			separator := string(filepath.Separator)
			if (resolved == existing || strings.HasPrefix(resolved, existing+separator) || strings.HasPrefix(existing, resolved+separator)) && group != output.ConcurrencyGroup {
				return problem("invalid_output", "outputs."+name, "overlapping canonical output paths must share a concurrency group; update concurrency_group")
			}
		}
		paths[resolved] = output.ConcurrencyGroup
	}
	return nil
}
