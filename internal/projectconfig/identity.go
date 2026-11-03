// Package projectconfig owns offline project-private association and atomic publication.
package projectconfig

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"

	v1 "github.com/octacian/backlot/api/v1"
	"github.com/octacian/backlot/internal/localipc"
	"github.com/octacian/backlot/internal/manifest"
)

// Identity binds a manifest location to a canonical local repository object.
type Identity struct {
	Directory  string `json:"directory"`
	Filesystem string `json:"filesystem"`
	Manifest   string `json:"manifest"`
	Git        bool   `json:"git"`
}

func problem(message string) error { return &v1.PlanError{Code: "project_config", Message: message} }
func digest(data []byte) string    { s := sha256.Sum256(data); return hex.EncodeToString(s[:]) }
func within(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// Identify reads bounded Git metadata without executing Git or trusting names/remotes.
func Identify(project, checkout, file string) (Identity, error) {
	var result Identity
	canonical, err := filepath.EvalSymlinks(file)
	if err != nil || !within(checkout, canonical) {
		return result, manifest.SourceError(problem("manifest is unavailable or escapes checkout"), "manifest", file)
	}
	git := filepath.Join(checkout, ".git")
	info, err := os.Lstat(git)
	if err == nil {
		result.Git = true
		if !info.IsDir() {
			data, e := Read(git)
			if e != nil || !strings.HasPrefix(string(data), "gitdir: ") {
				return result, problem("invalid Git marker; repair Git metadata before project setup")
			}
			git = strings.TrimSpace(strings.TrimPrefix(string(data), "gitdir: "))
			if !filepath.IsAbs(git) {
				git = filepath.Join(checkout, git)
			}
		}
		git, err = filepath.EvalSymlinks(git)
		if err != nil {
			return result, problem("Git directory unavailable; repair Git metadata")
		}
		result.Directory, err = GitCommonDirectory(git)
		if err != nil {
			return result, err
		}
		result.Manifest, _ = filepath.Rel(checkout, canonical)
	} else if os.IsNotExist(err) {
		result.Directory, err = filepath.EvalSymlinks(project)
		result.Manifest = filepath.Base(canonical)
	} else {
		return result, problem("cannot inspect Git marker")
	}
	if err != nil {
		return result, problem("association directory unavailable")
	}
	info, err = os.Stat(result.Directory)
	if err != nil || !info.IsDir() {
		return result, problem("association directory must exist")
	}
	result.Filesystem, err = localipc.Identity(info)
	return result, err
}

// GitCommonDirectory resolves bounded sharing metadata, refusing linked admin anchors.
// It does not execute Git or validate repository objects, history or permanence.
func GitCommonDirectory(git string) (string, error) {
	marker := filepath.Join(git, "commondir")
	if _, err := os.Lstat(marker); err == nil {
		data, err := Read(marker)
		if err != nil {
			return "", err
		}
		value := strings.TrimSpace(string(data))
		if value == "" || strings.ContainsAny(value, "\x00\r\n") {
			return "", problem("invalid Git common-directory marker; repair Git metadata")
		}
		if filepath.IsAbs(value) {
			git = value
		} else {
			git = filepath.Join(git, value)
		}
	} else if !os.IsNotExist(err) {
		return "", problem("cannot inspect Git common-directory marker")
	}
	anchor, err := filepath.EvalSymlinks(git)
	if err != nil {
		return "", problem("Git common directory unavailable; repair Git metadata")
	}
	if _, err := os.Lstat(filepath.Join(anchor, "gitdir")); err == nil {
		return "", problem("invalid or ambiguous linked Git metadata; repair commondir before project association")
	} else if !os.IsNotExist(err) {
		return "", problem("cannot inspect Git common-directory anchor")
	}
	return anchor, nil
}
