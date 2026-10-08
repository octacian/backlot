//go:build !windows

package daemon

import (
	"os/exec"
	"syscall"
)

func detach(command *exec.Cmd) { command.SysProcAttr = &syscall.SysProcAttr{Setsid: true} }
