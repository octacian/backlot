package plan

import "os"

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
		return Source{}, err
	}
	return Source{ManifestPath: file, ProjectPath: project, CheckoutPath: checkoutRoot(project)}, nil
}
