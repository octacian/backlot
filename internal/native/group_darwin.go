package native

import (
	"context"
	"fmt"
	"golang.org/x/sys/unix"
	"os"
	"os/exec"
	"syscall"
	"time"
)

func supported() bool         { return true }
func anchorOwnGroup() bool    { return syscall.Getpgrp() == os.Getpid() }
func configure(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true} }
func identify(pid int) (Identity, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	all, err := darwinProcesses(ctx, 1, int32(pid))
	if err == nil && len(all) == 0 {
		err = syscall.ESRCH
	}

	if err != nil {
		return Identity{}, err
	}
	return Identity{PID: pid, Birth: fmt.Sprintf("%d:%d", all[0].Proc.P_starttime.Sec, all[0].Proc.P_starttime.Usec)}, nil
}

func bootIdentity() (string, error) { return unix.Sysctl("kern.bootsessionuuid") }
func groupMembers(pgid int) ([]int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	return groupMembersContext(ctx, pgid)
}
func groupMembersContext(ctx context.Context, pgid int) ([]int, error) {
	all, err := darwinProcesses(ctx, 0, 0)
	if err != nil {
		return nil, err
	}
	var out []int
	for _, p := range all {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if int(p.Eproc.Pgid) == pgid {
			out = append(out, int(p.Proc.P_pid))
		}
	}
	return out, nil
}
func signalGroup(pgid int, sig syscall.Signal) error { return syscall.Kill(-pgid, sig) }

// SZOMB is 5 in the macOS SDK sys/proc.h. Observation never authorizes a PID signal.
func anchorExited(pid int) (bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	all, err := darwinProcesses(ctx, 1, int32(pid))
	if err == nil && len(all) == 0 {
		err = syscall.ESRCH
	}

	if err != nil {
		return false, err
	}
	return all[0].Proc.P_stat == 5, nil
}

func configureAnchor(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true} }
func configureRoot(cmd *exec.Cmd, pgid int) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Pgid: pgid}
}
