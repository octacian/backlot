package native

import (
	"fmt"
	"golang.org/x/sys/unix"
	"os/exec"
	"syscall"
)

func supported() bool         { return true }
func configure(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true} }
func identify(pid int) (Identity, error) {
	all, err := unix.SysctlKinfoProcSlice("kern.proc.pid", pid)
	if err == nil && len(all) == 0 {
		err = syscall.ESRCH
	}
	var p *unix.KinfoProc
	if err == nil {
		p = &all[0]
	}
	if err != nil {
		return Identity{}, err
	}
	return Identity{PID: pid, Birth: fmt.Sprintf("%d:%d", p.Proc.P_starttime.Sec, p.Proc.P_starttime.Usec)}, nil
}
func groupMembers(pgid int) ([]int, error) {
	all, err := unix.SysctlKinfoProcSlice("kern.proc.all")
	if err != nil {
		return nil, err
	}
	var out []int
	for _, p := range all {
		if int(p.Eproc.Pgid) == pgid {
			out = append(out, int(p.Proc.P_pid))
		}
	}
	return out, nil
}
func signalGroup(pgid int, sig syscall.Signal) error { return syscall.Kill(-pgid, sig) }

// SZOMB is 5 in the macOS SDK sys/proc.h. Observation never authorizes a PID signal.
func anchorExited(pid int) (bool, error) {
	all, err := unix.SysctlKinfoProcSlice("kern.proc.pid", pid)
	if err == nil && len(all) == 0 {
		err = syscall.ESRCH
	}
	var p *unix.KinfoProc
	if err == nil {
		p = &all[0]
	}
	if err != nil {
		return false, err
	}
	return p.Proc.P_stat == 5, nil
}

func configureAnchor(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true} }
func configureRoot(cmd *exec.Cmd, pgid int) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Pgid: pgid}
}
