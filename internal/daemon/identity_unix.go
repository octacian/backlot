//go:build !windows

package daemon

import (
	"fmt"
	"os"
	"syscall"
)

func fileIdentity(info os.FileInfo) (string, error) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return "", problem("checkout_identity", "cannot read filesystem identity")
	}
	return fmt.Sprintf("%d:%d", stat.Dev, stat.Ino), nil
}
