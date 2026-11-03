package plan

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	v1 "github.com/octacian/backlot/api/v1"
)

// WorkingDirectory resolves a declared cwd inside the canonical manifest directory.
// It reads metadata only. The manifest directory is represented by an empty string
// so older snapshots and newly planned defaults keep the same retained contract.
func WorkingDirectory(root, relative string) (string, error) {
	invalid := func(message string) (string, error) {
		return "", problem("invalid_working_directory", "working_directory", message)
	}
	if relative == "" {
		relative = "."
	}
	if !fs.ValidPath(relative) || strings.ContainsAny(relative, "\\:\x00") {
		return invalid("use a safe manifest-relative directory without traversal, or dot for the manifest directory")
	}
	target, err := canonical(filepath.Join(root, filepath.FromSlash(relative)))
	if err != nil {
		return invalid("directory is missing or inaccessible; create it or update working_directory")
	}
	info, err := os.Stat(target)
	if err != nil || !info.IsDir() {
		return invalid("working_directory must name an existing directory")
	}
	rel, err := filepath.Rel(root, target)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return invalid("working_directory symlink escapes the manifest directory")
	}
	if target == root {
		return "", nil
	}
	return target, nil
}

func directoryProblem(err error, field string) error {
	p := *err.(*v1.PlanError)
	p.Field = field
	return &p
}

// ManifestDrift includes effective cwd changes that do not alter manifest bytes.
func ManifestDrift(a, b v1.PlanResponse) bool {
	if a.ManifestDigest != b.ManifestDigest {
		return true
	}
	type selection struct{ command, probe string }
	directories := func(p v1.PlanResponse) map[string]selection {
		result := make(map[string]selection, len(p.Components))
		for _, c := range p.Components {
			x := selection{command: c.WorkingDirectory}
			if c.Readiness != nil {
				x.probe = c.Readiness.WorkingDirectory
			}
			result[c.Name] = x
		}
		return result
	}
	x, y := directories(a), directories(b)
	if len(x) != len(y) {
		return true
	}
	for name, directory := range x {
		if other, ok := y[name]; !ok || other != directory {
			return true
		}
	}
	return false
}
