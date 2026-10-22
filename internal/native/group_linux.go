package native

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"
)

func supported() bool         { return true }
func anchorOwnGroup() bool    { return syscall.Getpgrp() == os.Getpid() }
func configure(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true} }
func process(pid int) ([]string, error) {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return nil, err
	}
	end := strings.LastIndex(string(data), ")")
	if end < 0 {
		return nil, fmt.Errorf("invalid process identity")
	}
	fields := strings.Fields(string(data)[end+1:])
	if len(fields) < 20 {
		return nil, fmt.Errorf("incomplete process identity")
	}
	return fields, nil
}
func identify(pid int) (Identity, error) {
	f, err := process(pid)
	if err != nil {
		return Identity{}, err
	}
	return Identity{PID: pid, Birth: f[19]}, nil
}

func bootIdentity() (string, error) {
	data, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	return strings.TrimSpace(string(data)), err
}
func groupMembers(pgid int) ([]int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	return groupMembersContext(ctx, pgid)
}
func groupMembersContext(ctx context.Context, pgid int) ([]int, error) {
	entries, err := processEntries(ctx)
	if err != nil {
		return nil, err
	}
	return scanGroupMembersContext(ctx, pgid, entries, process)
}

// scanGroupMembers tolerates processes disappearing between enumeration and
// observation. Other observation failures must still prevent cleanup proof.
func scanGroupMembers(pgid int, entries []os.DirEntry, readProcess func(int) ([]string, error)) ([]int, error) {
	return scanGroupMembersContext(context.Background(), pgid, entries, readProcess)
}
func scanGroupMembersContext(ctx context.Context, pgid int, entries []os.DirEntry, readProcess func(int) ([]string, error)) ([]int, error) {
	var out []int
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		pid, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue
		}
		f, err := readProcess(pid)
		if os.IsNotExist(err) || errors.Is(err, syscall.ESRCH) {
			continue
		}
		if err != nil {
			return nil, err
		}
		group, err := strconv.Atoi(f[2])
		if err != nil {
			return nil, err
		}
		if group == pgid {
			out = append(out, pid)
		}
	}
	return out, nil
}
func signalGroup(pgid int, sig syscall.Signal) error { return syscall.Kill(-pgid, sig) }

func anchorExited(pid int) (bool, error) {
	f, err := process(pid)
	if err != nil {
		return false, err
	}
	return f[0] == "Z", nil
}

func configureAnchor(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true} }
func configureRoot(cmd *exec.Cmd, pgid int) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Pgid: pgid}
}
