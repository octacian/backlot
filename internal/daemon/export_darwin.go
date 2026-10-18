package daemon

import "golang.org/x/sys/unix"

func publishExport(parent int, staging, destination string) error {
	return unix.RenameatxNp(parent, staging, parent, destination, unix.RENAME_EXCL)
}
