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
	"github.com/octacian/backlot/internal/registration"
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
	location, err := registration.Locate(project, checkout)
	if err != nil {
		return result, problem("Git directory unavailable; repair Git metadata")
	}
	result.Git = location.Git
	if result.Git {
		result.Directory = location.Common
		result.Manifest, _ = filepath.Rel(checkout, canonical)
	} else {
		result.Directory, err = filepath.EvalSymlinks(project)
		result.Manifest = filepath.Base(canonical)
	}
	if err != nil {
		return result, problem("association directory unavailable")
	}
	info, err := os.Stat(result.Directory)
	if err != nil || !info.IsDir() {
		return result, problem("association directory must exist")
	}
	result.Filesystem, err = localipc.Identity(info)
	return result, err
}

// GitCommonDirectory resolves bounded sharing metadata without invoking Git.
func GitCommonDirectory(git string) (string, error) { return registration.GitCommonDirectory(git) }
