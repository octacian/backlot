package plan

import (
	"os"
	"path/filepath"

	"github.com/octacian/backlot/internal/manifest"
)

// Source binds a discovered manifest to canonical project and checkout paths.
// Discovery reads filesystem metadata only, before any manifest/config/seed bytes.
type Source struct {
	ManifestPath string
	ProjectPath  string
	CheckoutPath string
}

// Locate discovers canonical source paths without reading snapshot inputs.
func Locate(projectPath string) (Source, error) {
	start := ""
	if projectPath == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return Source{}, problem("discovery", "project", "cannot read working directory")
		}
		start = cwd
	}
	file, project, err := Discover(start, projectPath)
	if err != nil {
		path := projectPath
		if path == "" {
			path = start
		}
		if abs, e := filepath.Abs(path); e == nil {
			path = abs
		}
		return Source{}, manifest.SourceError(err, "manifest", path)
	}
	return Source{ManifestPath: file, ProjectPath: project, CheckoutPath: checkoutRoot(project)}, nil
}
