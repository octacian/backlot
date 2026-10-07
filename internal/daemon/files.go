package daemon

import (
	"io"
	"os"
	"path/filepath"
)

// DefaultDirectory returns the per-user daemon directory; commands may override it.
func DefaultDirectory() (string, error) {
	root, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "backlot", "daemon"), nil
}

// SocketPath returns the local administrative socket within the protected directory.
func SocketPath(directory string) string { return filepath.Join(directory, "daemon.sock") }

func readSmall(path string) ([]byte, error) {
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return nil, problem("checkout_identity", "expected regular Git provenance file")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	data, readErr := io.ReadAll(io.LimitReader(file, 4097))
	closeErr := file.Close()
	if readErr != nil || len(data) > 4096 {
		return nil, problem("checkout_identity", "Git provenance file exceeds limit or cannot be read")
	}
	return data, closeErr
}
