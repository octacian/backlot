package daemon

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/octacian/backlot/internal/plan"
	bolt "go.etcd.io/bbolt"
)

// sharedProjectIdentity separates sharing authority from per-checkout ownership.
// Paths and filesystem identities are both retained: moves and replacements fail
// closed instead of automatically relinking existing services.
type sharedProjectIdentity struct {
	ID         string `json:"id"`
	Anchor     string `json:"anchor"`
	Filesystem string `json:"filesystem"`
	Relative   string `json:"relative"`
	Project    string `json:"project"`
}

func identifyShared(source plan.Source, project string) (sharedProjectIdentity, error) {
	checkout, err := identify(source.CheckoutPath)
	if err != nil {
		return sharedProjectIdentity{}, err
	}
	anchor := source.ProjectPath
	relative := "."
	if checkout.GitDirectory != "" {
		anchor = checkout.GitDirectory
		commonPath := filepath.Join(anchor, "commondir")
		_, readErr := os.Stat(commonPath)
		if readErr == nil {
			common, err := readSmall(commonPath)
			if err != nil {
				return sharedProjectIdentity{}, err
			}
			value := strings.TrimSpace(string(common))
			if value == "" {
				return sharedProjectIdentity{}, problem("project_identity", "empty Git common-directory marker")
			}
			if !filepath.IsAbs(value) {
				value = filepath.Join(anchor, value)
			}
			anchor = value
		} else if !os.IsNotExist(readErr) {
			return sharedProjectIdentity{}, readErr
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
	return sharedProjectIdentity{ID: hash(fs + "/" + relative + "/" + project), Anchor: anchor, Filesystem: fs, Relative: relative, Project: project}, nil
}

func registerSharedProject(tx *bolt.Tx, next sharedProjectIdentity) error {
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
	return paths.Put([]byte(key), []byte(next.ID))
}
