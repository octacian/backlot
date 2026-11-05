package plan

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// OutputDirectory inspects an explicitly disposable directory without effects.
// Unlike ordinary outputs, every existing path component must be a real directory.
func OutputDirectory(project, relative string) (string, error) {
	invalid := func() (string, error) {
		return "", problem("invalid_output", relative, "disposable output must be a strict manifest-relative directory without symlink components")
	}
	if relative == "." || !fs.ValidPath(relative) || strings.ContainsAny(relative, "\\:\x00") {
		return invalid()
	}
	root, err := os.OpenRoot(project)
	if err != nil {
		return "", err
	}
	defer func() { _ = root.Close() }()
	current := ""
	for _, part := range strings.Split(relative, "/") {
		current = filepath.Join(current, part)
		info, err := root.Lstat(current)
		if os.IsNotExist(err) {
			break
		}
		if err != nil {
			return "", err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return invalid()
		}
	}
	return filepath.Join(project, filepath.FromSlash(relative)), nil
}

// OutputOverlap compares output paths and bounded ancestor identities, including
// filesystem case aliases once either overlapping target exists. Missing targets
// must be materialized and rechecked before deletion. It never scans subtrees.
func OutputOverlap(a, b string) (same, overlap bool, err error) {
	same = a == b
	overlap = same || strings.HasPrefix(a, b+string(filepath.Separator)) || strings.HasPrefix(b, a+string(filepath.Separator))
	for _, pair := range [][2]string{{a, b}, {b, a}} {
		info, err := os.Stat(pair[0])
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return false, false, err
		}
		for ancestor := pair[1]; ; ancestor = filepath.Dir(ancestor) {
			other, err := os.Stat(ancestor)
			if err != nil && !errors.Is(err, os.ErrNotExist) {
				return false, false, err
			}
			if err == nil && os.SameFile(info, other) {
				return ancestor == pair[1], true, nil
			}
			if filepath.Dir(ancestor) == ancestor {
				break
			}
		}
	}
	return same, overlap, nil
}
