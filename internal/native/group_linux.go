package native

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
)

func supported() bool         { return true }
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
func groupMembers(pgid int) ([]int, error) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, err
	}
	var out []int
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue
		}
		f, err := process(pid)
		if os.IsNotExist(err) {
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
