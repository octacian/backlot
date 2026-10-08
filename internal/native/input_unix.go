//go:build darwin || linux

package native

import (
	"golang.org/x/sys/unix"
	"os"
	"time"
)

// A pollable pipe lets authority cancellation interrupt a pending activation read.
func controlInput() (*os.File, error) {
	if err := unix.SetNonblock(0, true); err != nil {
		return nil, err
	}
	input := os.NewFile(0, "guardian-control")
	if err := input.SetReadDeadline(time.Time{}); err != nil {
		_ = input.Close()
		return nil, err
	}
	return input, nil
}
