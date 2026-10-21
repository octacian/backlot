package native

import (
	"context"
	"os"
)

func sessionCandidates(ctx context.Context) ([]int, error) {
	all, err := darwinProcesses(ctx, 0, 0)
	if err != nil {
		return nil, err
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
