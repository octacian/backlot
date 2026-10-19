package daemon

import "golang.org/x/sys/unix"

func publishExport(parent int, staging, destination string) error {
	return unix.Renameat2(parent, staging, parent, destination, unix.RENAME_NOREPLACE)
}
