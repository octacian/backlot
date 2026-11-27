//go:build darwin && arm64

package acceptance

import (
	"errors"
	"os"
	"path/filepath"
)

// Keep the original short default: macOS's normal TMPDIR can exceed Unix socket
// limits once public job/proxy paths are appended. An explicit durable override
// must fit the longest fixture path; rejecting it avoids silent durability loss.
func newAcceptanceRoot(prefix string) (string, error) {
	parent := os.Getenv("BACKLOT_ACCEPTANCE_TEMP_ROOT")
	if parent == "" {
		parent = "/tmp"
	}
	if !filepath.IsAbs(parent) {
		return "", errors.New("BACKLOT_ACCEPTANCE_TEMP_ROOT must be absolute")
	}
	longest := filepath.Join(parent, "bl-public-4294967295", "job-loss-4294967295", "engine.sock.proxy")
	if len(longest) > 103 {
		return "", errors.New("BACKLOT_ACCEPTANCE_TEMP_ROOT too long for macOS Unix fixture sockets; choose a short private directory")
	}
	return os.MkdirTemp(parent, prefix)
}
