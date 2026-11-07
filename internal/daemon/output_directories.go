package daemon

import (
	"context"
	"os"
	"path/filepath"
	"sort"

	"github.com/octacian/backlot/internal/native"
	"github.com/octacian/backlot/internal/plan"
	bolt "go.etcd.io/bbolt"
)

// prepareOutputDirectories runs only for new executions, under all selected
// output locks or task reservations. Preflight all targets before removal.
// instanceID excludes only this accepted execution, including borrowed tasks,
// from the final consumer check; parent and sibling reservations still apply.
func (s *service) prepareOutputDirectories(ctx context.Context, snapshot plan.Snapshot, identity capturedSource, instanceID string) error {
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
	protected := []string{s.directory, filepath.Join(identity.checkout.Path, ".git"), identity.registrationLocation.Admin, identity.registrationLocation.Common}
	sort.Strings(targets)
	preflight := func() ([]string, error) {
		if err := plan.ValidateOutputs(project, snapshot.Manifest.Outputs, selected); err != nil {
			return nil, err
		}
		var unique []string
		for _, relative := range targets {
			if len(unique) > 0 && unique[len(unique)-1] == relative {
				continue
			}
			target, err := plan.OutputDirectory(project, relative)
			if err != nil {
				return nil, err
			}
			for _, anchor := range protected {
				_, overlap, err := plan.OutputOverlap(target, anchor)
				if err != nil {
					return nil, err
				}
				if overlap {
					return nil, problem("invalid_output", "disposable output overlaps private daemon/resource state or checkout registration metadata")
				}
			}
			duplicate := false
			for _, previous := range unique {
				same, _, err := plan.OutputOverlap(target, filepath.Join(project, previous))
				if err != nil {
					return nil, err
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
		return unique, nil
	}
	unique, err := preflight()
	if err != nil {
		return err
	}
	root, err := os.OpenRoot(project)
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	// Materialize safe targets without deletion so the filesystem resolves absent case aliases.
	for _, relative := range unique {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := root.MkdirAll(relative, 0700); err != nil {
			return err
		}
	}
	unique, err = preflight()
	if err != nil {
		return err
	}
	paths, err := outputPaths(snapshot.Plan)
	if err != nil {
		return err
	}
	boot, _ := native.CurrentBoot()
	if err := s.store.db.View(func(tx *bolt.Tx) error {
		return outputConflictTx(tx, snapshot.Plan, paths, instanceID, true, boot)
	}); err != nil {
		return err
	}
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
