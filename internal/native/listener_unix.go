//go:build darwin || linux

package native

import (
	"context"
	"errors"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// OwnsListener proves that every listener on the selected port belongs to this group.
// An unrelated port owner is never signaled and never satisfies readiness.
func listenerState(ctx context.Context, pgid, port int) (bool, bool, error) {
	query, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	command := exec.CommandContext(query, "lsof", "-nP", "-a", "-iTCP:"+strconv.Itoa(port), "-sTCP:LISTEN", "-Fp")
	data, err := command.Output()
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) && exit.ExitCode() == 1 && len(data) == 0 {
			return false, false, nil
		}
		return false, false, errors.New("cannot prove listener ownership; lsof is required for native network probes")
	}
	found := false
	for _, line := range strings.Split(string(data), "\n") {
		if !strings.HasPrefix(line, "p") {
			continue
		}
		pid, err := strconv.Atoi(line[1:])
		if err != nil {
			return false, false, err
		}
		group, err := syscall.Getpgid(pid)
		if err != nil {
			return false, false, nil
		}
		if group != pgid {
			return false, true, nil
		}
		found = true
	}
	return found, false, nil
}

// OwnsListener proves that the current listener belongs to the supervised group.
func OwnsListener(ctx context.Context, pgid, port int) (bool, error) {
	owned, _, err := listenerState(ctx, pgid, port)
	return owned, err
}

// ListenerConflict identifies an unrelated listener without signaling it.
func ListenerConflict(ctx context.Context, pgid, port int) (bool, error) {
	_, conflict, err := listenerState(ctx, pgid, port)
	return conflict, err
}
