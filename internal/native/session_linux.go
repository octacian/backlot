package native

import (
	"context"
	"errors"
	"os"
	"strconv"
)

func sessionCandidates(ctx context.Context) ([]int, error) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, err
	}
	var out []int
	count := 0
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		pid, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue
		}
		count++
		if count > sessionCandidateLimit {
			return nil, errors.New("session candidate budget exhausted")
		}
		fields, err := process(pid)
		if vanished(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		sid, err := strconv.Atoi(fields[3])
		if err != nil {
			return nil, err
		}
		if sid == os.Getpid() {
			out = append(out, pid)
		}
	}
	return out, nil
}
