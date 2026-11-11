package daemon

import (
	"encoding/json"
	v1 "github.com/octacian/backlot/api/v1"
	"os"
	"path/filepath"
	"strings"

	"github.com/octacian/backlot/internal/plan"
	"github.com/octacian/backlot/internal/projectconfig"
	bolt "go.etcd.io/bbolt"
)

// sharedProjectIdentity separates sharing authority from per-checkout ownership.
// Paths and filesystem identities are both retained: moves and replacements fail
// closed instead of automatically relinking existing services.
type sharedProjectIdentity = v1.ProjectIdentity

func identifyShared(source plan.Source, project string) (sharedProjectIdentity, error) {
	checkout, err := identify(source.CheckoutPath)
	if err != nil {
		return sharedProjectIdentity{}, err
	}
	anchor := source.ProjectPath
	relative := "."
	if checkout.GitDirectory != "" {
		anchor, err = projectconfig.GitCommonDirectory(checkout.GitDirectory)
		if err != nil {
			return sharedProjectIdentity{}, problem("project_identity", "invalid or ambiguous Git common-directory metadata; repair linked Git metadata before project association")
		}
		relative, err = filepath.Rel(source.CheckoutPath, source.ProjectPath)
		if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return sharedProjectIdentity{}, problem("project_identity", "project must remain inside its checkout")
		}
	}
	anchor, err = filepath.EvalSymlinks(anchor)
	if err != nil {
		return sharedProjectIdentity{}, problem("project_identity", "shared-project anchor unavailable")
	}
	info, err := os.Stat(anchor)
	if err != nil || !info.IsDir() {
		return sharedProjectIdentity{}, problem("project_identity", "shared-project anchor is not a directory")
	}
	fs, err := fileIdentity(info)
	if err != nil {
		return sharedProjectIdentity{}, err
	}
	return sharedProjectIdentity{ID: hash(fs + "/" + relative + "/" + project), Anchor: anchor, Filesystem: fs, Relative: relative, Project: project, Registration: checkout.Registration}, nil
}

func registerSharedProject(tx *bolt.Tx, next sharedProjectIdentity) error {
	if tx.Bucket([]byte("project_bindings")) != nil {
		owner, err := boundShared(tx, next)
		if err != nil {
			return err
		}
		if owner != next {
			binding, err := readProjectBinding(tx, owner.ID)
			if err != nil {
				return err
			}
			if binding.Current.Registration.CommonID == "" && sameProjectPhysical(binding.Current, next) {
				binding.Current.Registration = next.Registration
				return put(tx, "project_bindings", owner.ID, binding)
			}
			return nil
		}
	}
	identities := tx.Bucket([]byte("shared_projects"))
	paths := tx.Bucket([]byte("shared_paths"))
	key := next.Anchor + "/" + next.Relative + "/" + next.Project
	if old := paths.Get([]byte(key)); old != nil && string(old) != next.ID {
		return problem("project_replaced", "shared-project anchor was replaced; existing records preserved")
	}
	if data := identities.Get([]byte(next.ID)); data != nil {
		var old sharedProjectIdentity
		if err := json.Unmarshal(data, &old); err != nil {
			return problem("state_corrupt", "shared-project identity cannot be decoded")
		}
		if old != next {
			return problem("project_moved", "shared-project anchor moved; explicit cleanup required; records preserved")
		}
	}
	if err := put(tx, "shared_projects", next.ID, next); err != nil {
		return err
	}
	if err := paths.Put([]byte(key), []byte(next.ID)); err != nil {
		return err
	}
	if tx.Bucket([]byte("project_bindings")) != nil && tx.Bucket([]byte("project_bindings")).Get([]byte(next.ID)) == nil {
		return put(tx, "project_bindings", next.ID, projectBinding{Current: next})
	}
	return nil
}
