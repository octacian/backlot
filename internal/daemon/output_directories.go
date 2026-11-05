package daemon

import (
	"context"
	"os"
	"path/filepath"
	"sort"

	"github.com/octacian/backlot/internal/plan"
	"github.com/octacian/backlot/internal/projectconfig"
)

// prepareOutputDirectories runs only for new executions, under all selected
// output locks. Preflight every target before the first irreversible removal.
func (s *service) prepareOutputDirectories(ctx context.Context, snapshot plan.Snapshot, identity capturedSource) error {
	selected := map[string]bool{}
	var targets []string
	for name, output := range snapshot.Plan.Outputs {
		selected[name] = true
		if output.EmptyBeforeExecution {
			targets = append(targets, output.Path)
		}
	}
	if len(targets) == 0 {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := identity.verify(); err != nil {
		return err
	}
	project := identity.source.ProjectPath
	if err := plan.ValidateOutputs(project, snapshot.Manifest.Outputs, selected); err != nil {
		return err
	}
	protected := []string{s.directory, filepath.Join(identity.checkout.Path, ".git")}
	if identity.checkout.GitDirectory != "" {
		common, err := projectconfig.GitCommonDirectory(identity.checkout.GitDirectory)
		if err != nil {
			return err
		}
		protected = append(protected, identity.checkout.GitDirectory, common)
	}
	sort.Strings(targets)
	unique := targets[:0]
	for _, relative := range targets {
		if len(unique) > 0 && unique[len(unique)-1] == relative {
			continue
		}
		target, err := plan.OutputDirectory(project, relative)
		if err != nil {
			return err
		}
		for _, anchor := range protected {
			_, overlap, err := plan.OutputOverlap(target, anchor)
			if err != nil {
				return err
			}
			if overlap {
				return problem("invalid_output", "disposable output overlaps private daemon/resource state or checkout Git metadata")
			}
		}
		duplicate := false
		for _, previous := range unique {
			same, _, err := plan.OutputOverlap(target, filepath.Join(project, previous))
			if err != nil {
				return err
			}
			if same {
				duplicate = true
				break
			}
		}
		if !duplicate {
			unique = append(unique, relative)
		}
	}
	root, err := os.OpenRoot(project)
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	for _, relative := range unique {
		if err := ctx.Err(); err != nil {
			return err
		}
		// os.Root bounds path races; RemoveAll unlinks tree symlinks without following them.
		if err := root.RemoveAll(relative); err != nil {
			return err
		}
		if err := root.MkdirAll(relative, 0700); err != nil {
			return err
		}
	}
	return nil
}
