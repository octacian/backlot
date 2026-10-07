package daemon

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"

	v1 "github.com/octacian/backlot/api/v1"
	"github.com/octacian/backlot/internal/plan"
)

func problem(code, message string) error { return &v1.PlanError{Code: code, Message: message} }

func newID() string        { return hex.EncodeToString(randomBytes()) }
func randomBytes() []byte  { b := make([]byte, 32); _, _ = rand.Read(b); return b }
func hash(s string) string { sum := sha256.Sum256([]byte(s)); return hex.EncodeToString(sum[:]) }

func identify(path string) (v1.CheckoutIdentity, error) {
	canonical, err := filepath.EvalSymlinks(path)
	if err != nil {
		return v1.CheckoutIdentity{}, problem("checkout_identity", "cannot resolve checkout")
	}
	info, err := os.Stat(canonical)
	if err != nil || !info.IsDir() {
		return v1.CheckoutIdentity{}, problem("checkout_identity", "checkout must be an existing directory")
	}
	identity, err := fileIdentity(info)
	if err != nil {
		return v1.CheckoutIdentity{}, err
	}
	result := v1.CheckoutIdentity{Path: canonical}
	git := filepath.Join(canonical, ".git")
	if stat, err := os.Stat(git); err == nil {
		if !stat.IsDir() {
			data, err := readSmall(git)
			if err != nil || !strings.HasPrefix(string(data), "gitdir: ") {
				return result, problem("checkout_identity", "invalid Git worktree marker")
			}
			git = strings.TrimSpace(strings.TrimPrefix(string(data), "gitdir: "))
			if !filepath.IsAbs(git) {
				git = filepath.Join(canonical, git)
			}
		}
		git, err = filepath.EvalSymlinks(git)
		if err != nil {
			return result, problem("checkout_identity", "Git directory is unavailable; repair checkout explicitly")
		}
		stat, err = os.Stat(git)
		if err != nil || !stat.IsDir() {
			return result, problem("checkout_identity", "invalid Git directory")
		}
		gitID, err := fileIdentity(stat)
		if err != nil {
			return result, err
		}
		identity += "/" + gitID
		result.GitDirectory = git
		head, err := readSmall(filepath.Join(git, "HEAD"))
		if err != nil {
			return result, problem("checkout_identity", "cannot read Git HEAD provenance")
		}
		result.GitHead = strings.TrimSpace(string(head))
	} else if !os.IsNotExist(err) {
		return result, problem("checkout_identity", "cannot inspect Git marker")
	}
	result.ID = hash(identity)
	return result, nil
}

// capturedSource binds every snapshot read to the original canonical project and
// checkout. Acceptance rechecks both identities but always records the original,
// so a path replacement can never adopt an already-read snapshot as its own.
type capturedSource struct {
	source          plan.Source
	checkout        v1.CheckoutIdentity
	projectIdentity string
}

func captureSource(source plan.Source) (capturedSource, error) {
	result := capturedSource{source: source}
	info, err := os.Stat(source.ProjectPath)
	if err != nil || !info.IsDir() {
		return result, problem("checkout_identity", "cannot inspect project directory")
	}
	result.projectIdentity, err = fileIdentity(info)
	if err != nil {
		return result, err
	}
	result.checkout, err = identify(source.CheckoutPath)
	return result, err
}

func (original capturedSource) verify() error {
	changed := func() error {
		return problem("checkout_changed", "project or checkout changed during preparation; no snapshot accepted; retry from a stable checkout")
	}
	source, err := plan.Locate(original.source.ProjectPath)
	if err != nil || source != original.source {
		return changed()
	}
	current, err := captureSource(source)
	if err != nil {
		return changed()
	}
	if current.projectIdentity != original.projectIdentity || current.checkout.ID != original.checkout.ID || current.checkout.Path != original.checkout.Path || current.checkout.GitDirectory != original.checkout.GitDirectory {
		return changed()
	}
	return nil
}
