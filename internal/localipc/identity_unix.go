//go:build !windows

package localipc

import (
	"fmt"
	"os"
	"syscall"
)

// Identity returns the filesystem identity of an inspected object.
func Identity(info os.FileInfo) (string, error) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return "", problem("filesystem_identity", "cannot read filesystem identity")
	}
	return fmt.Sprintf("%d:%d", stat.Dev, stat.Ino), nil
}
