//go:build !windows

package daemon

import (
	"os"
	"syscall"
)

func exportParentOwned(info os.FileInfo) bool {
	if info == nil {
		return false
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && stat.Uid == uint32(os.Getuid()) && info.IsDir() && info.Mode().Perm()&0022 == 0
}
