// Package localipc enforces the user-owned filesystem boundary for daemon transport.
package localipc

import (
	v1 "github.com/octacian/backlot/api/v1"
	"os"
	"path/filepath"
)

func problem(code, message string) error { return &v1.PlanError{Code: code, Message: message} }

// Access checks a socket and its private parent directory without changing them.
func Access(socket string) error {
	if err := Directory(filepath.Dir(socket), false); err != nil {
		return err
	}
	if err := File(socket, true); err != nil {
		return problem("permissions", "socket unavailable or unsafe; start daemon explicitly or run doctor with the same --state-dir")
	}
	return nil
}

// Directory verifies a private user-owned directory, optionally creating it.
func Directory(directory string, create bool) error {
	if !filepath.IsAbs(directory) {
		return problem("permissions", "daemon directory must be absolute")
	}
	if create {
		if err := os.MkdirAll(directory, 0700); err != nil {
			return problem("permissions", "cannot create private daemon directory")
		}
	}
	canonical, err := filepath.EvalSymlinks(directory)
	if err != nil {
		return problem("permissions", "daemon directory unavailable; start daemon explicitly")
	}
	clean := filepath.Clean(directory)
	// Resolve parent aliases (e.g. macOS /var -> /private/var) but reject the final directory symlink.
	parent, err := filepath.EvalSymlinks(filepath.Dir(clean))
	if err != nil || canonical != filepath.Join(parent, filepath.Base(clean)) {
		return problem("permissions", "daemon directory must not be a symlink")
	}
	info, err := os.Lstat(directory)
	if err != nil || !info.IsDir() || !owned(info) || info.Mode().Perm()&0077 != 0 {
		return problem("permissions", "daemon directory must be owned by this user with mode 0700")
	}
	return nil
}

// File verifies a private user-owned regular file or Unix socket without following symlinks.
func File(path string, socket bool) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	valid := info.Mode().IsRegular()
	if socket {
		valid = info.Mode()&os.ModeSocket != 0
	}
	if !valid || !owned(info) || info.Mode().Perm()&0077 != 0 {
		return problem("permissions", "daemon file must be owned, private, and the expected type")
	}
	return nil
}
