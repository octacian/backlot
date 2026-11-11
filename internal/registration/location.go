// Package registration resolves local administrative marker locations.
package registration

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

func readMetadata(path string) ([]byte, error) {
	before, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !before.Mode().IsRegular() {
		return nil, fmt.Errorf("invalid Git administrative file")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || !os.SameFile(before, info) {
		return nil, fmt.Errorf("invalid Git administrative file")
	}
	b, err := io.ReadAll(io.LimitReader(f, 4097))
	if err != nil || len(b) > 4096 {
		return nil, fmt.Errorf("git administrative file unavailable or too large")
	}
	return b, nil
}

// Locate resolves Git links without executing Git; non-Git metadata stays local.
func Locate(project, checkout string) (Location, error) {
	l := Location{Checkout: checkout}
	git := filepath.Join(checkout, ".git")
	info, err := os.Lstat(git)
	if os.IsNotExist(err) {
		l.Admin = filepath.Join(project, ".backlot-local")
		l.Common = l.Admin
		return l, nil
	}
	if err != nil {
		return l, err
	}
	l.Git = true
	if !info.IsDir() {
		data, err := readMetadata(git)
		if err != nil || !strings.HasPrefix(string(data), "gitdir: ") {
			return l, fmt.Errorf("invalid Git worktree marker")
		}
		git = strings.TrimSpace(strings.TrimPrefix(string(data), "gitdir: "))
		if !filepath.IsAbs(git) {
			git = filepath.Join(checkout, git)
		}
	}
	l.Admin, err = filepath.EvalSymlinks(git)
	if err != nil {
		return l, err
	}
	info, err = os.Stat(l.Admin)
	if err != nil || !info.IsDir() {
		return l, fmt.Errorf("git directory unavailable")
	}
	l.Common, err = GitCommonDirectory(l.Admin)
	return l, err
}

// GitCommonDirectory resolves bounded sharing metadata, refusing linked admin anchors.
// It does not execute Git or validate repository objects, history or permanence.
func GitCommonDirectory(git string) (string, error) {
	marker := filepath.Join(git, "commondir")
	if _, err := os.Lstat(marker); err == nil {
		data, err := readMetadata(marker)
		if err != nil {
			return "", err
		}
		value := strings.TrimSpace(string(data))
		if value == "" || strings.ContainsAny(value, "\x00\r\n") {
			return "", fmt.Errorf("invalid Git common-directory marker; repair Git metadata")
		}
		if filepath.IsAbs(value) {
			git = value
		} else {
			git = filepath.Join(git, value)
		}
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("cannot inspect Git common-directory marker")
	}
	anchor, err := filepath.EvalSymlinks(git)
	if err != nil {
		return "", fmt.Errorf("git common directory unavailable; repair Git metadata")
	}
	if _, err := os.Lstat(filepath.Join(anchor, "gitdir")); err == nil {
		return "", fmt.Errorf("invalid or ambiguous linked Git metadata; repair commondir before project association")
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("cannot inspect Git common-directory anchor")
	}
	return anchor, nil
}
