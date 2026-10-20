package native

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
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

func processSnapshot() ([]Identity, error) {
	// A filtered procfs cannot prove absence of another user's session members.
	data, err := os.ReadFile("/proc/mounts")
	if err != nil {
		return nil, err
	}
	visible := false
	for line := range strings.SplitSeq(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 4 || fields[1] != "/proc" || fields[2] != "proc" {
			continue
		}
		visible = true
		for option := range strings.SplitSeq(fields[3], ",") {
			if strings.HasPrefix(option, "hidepid=") && option != "hidepid=0" && option != "hidepid=off" {
				return nil, errors.New("filtered procfs cannot verify native session absence")
			}
		}
	}
	if !visible {
		return nil, errors.New("unfiltered procfs unavailable")
	}
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, err
	}
	var out []Identity
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue
		}
		id, err := identify(pid)
		if err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, nil
}

func sessionID(pid int) (int, error) { return unix.Getsid(pid) }

func bootIdentity() (string, error) {
	data, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	return strings.TrimSpace(string(data)), err
}
func groupMembers(pgid int) ([]int, error) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, err
	}
	return scanGroupMembers(pgid, entries, process)
}

// scanGroupMembers tolerates processes disappearing between enumeration and
// observation. Other observation failures must still prevent cleanup proof.
func scanGroupMembers(pgid int, entries []os.DirEntry, readProcess func(int) ([]string, error)) ([]int, error) {
	var out []int
	for _, entry := range entries {
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
