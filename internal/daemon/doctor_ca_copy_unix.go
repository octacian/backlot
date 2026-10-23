//go:build !windows

package daemon

import (
	"os"
	"syscall"
)

// Public copies deliberately permit read access through a file-only bind mount;
// unlike private state files they must be readable by capless container root.
func diagnosticCAOwned(info os.FileInfo) bool {
	if info == nil {
		return false
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && stat.Uid == uint32(os.Getuid()) && stat.Nlink == 1 && info.Mode().IsRegular()
}
