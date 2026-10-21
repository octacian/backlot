package native

import (
	"context"
	"errors"
	"golang.org/x/sys/unix"
	"os"
)

func sessionCandidates(ctx context.Context) ([]int, error) {
	all, err := unix.SysctlKinfoProcSlice("kern.proc.all")
	if err != nil {
		return nil, err
	}
	if len(all) > sessionCandidateLimit {
		return nil, errors.New("session candidate budget exhausted")
	}
	var out []int
	for _, p := range all {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		pid := int(p.Proc.P_pid)
		if pid <= 0 {
			continue
		}
		sid, err := currentSession(pid)
		if vanished(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if sid == os.Getpid() {
			out = append(out, pid)
		}
	}
	return out, nil
}
