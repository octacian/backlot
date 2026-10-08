//go:build darwin || linux

package native

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// OwnsListener proves that every listener on the selected port belongs to this group.
// An unrelated port owner is never signaled and never satisfies readiness.
func listenerState(ctx context.Context, pgid, port int, endpoint string) (bool, bool, error) {
	query, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	command := exec.CommandContext(query, "lsof", "-nP", "-a", "-iTCP:"+strconv.Itoa(port), "-sTCP:LISTEN", "-Fpftn")
	data, err := command.Output()
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) && exit.ExitCode() == 1 && len(data) == 0 {
			return false, false, nil
		}
		return false, false, errors.New("cannot prove listener ownership; lsof is required for native network probes")
	}
	found := false
	currentGroup := 0
	currentPID, currentFD := 0, -1
	family := ""
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "p") {
			pid, err := strconv.Atoi(line[1:])
			if err != nil {
				return false, false, err
			}
			currentPID, currentFD = pid, -1
			currentGroup, err = syscall.Getpgid(pid)
			if err != nil {
				currentGroup = 0
			}
			if endpoint == "" && currentGroup != 0 {
				if currentGroup != pgid {
					return false, true, nil
				}
				found = true
			}
		}
		if strings.HasPrefix(line, "f") {
			var err error
			currentFD, err = strconv.Atoi(line[1:])
			if err != nil {
				return false, false, errors.New("listener descriptor unavailable")
			}
		}
		if strings.HasPrefix(line, "t") {
			family = line[1:]
		}
		if endpoint != "" && strings.HasPrefix(line, "n") && currentGroup != 0 {
			host, _, err := net.SplitHostPort(line[1:])
			if err != nil {
				return false, false, errors.New("listener endpoint unavailable")
			}
			wanted, err := netip.ParseAddr(endpoint)
			if err != nil {
				return false, false, err
			}
			matched := host == endpoint || (host == "*" && ((family == "IPv4" && wanted.Is4()) || (family == "IPv6" && wanted.Is6())))
			if host == "*" && family == "IPv6" && wanted.Is4() {
				if currentFD < 0 {
					return false, false, errors.New("listener descriptor missing")
				}
				var err error
				matched, err = wildcardServesIPv4(query, currentPID, currentFD, port)
				if err != nil {
					return false, false, err
				}
			}
			if matched {
				if currentGroup != pgid {
					return false, true, nil
				}
				found = true
			}
		}
	}

	return found, false, nil
}

// OwnsListener proves that the current listener belongs to the supervised group.
func OwnsListener(ctx context.Context, pgid, port int) (bool, error) {
	owned, _, err := listenerState(ctx, pgid, port, "")
	return owned, err
}

// ListenerConflict identifies an unrelated listener without signaling it.
func ListenerConflict(ctx context.Context, pgid, port int) (bool, error) {
	_, conflict, err := listenerState(ctx, pgid, port, "")
	return conflict, err
}

// ListenerEndpoint checks ownership at a resolved numeric loopback destination.
func ListenerEndpoint(ctx context.Context, pgid int, address string) (bool, bool, error) {
	host, portText, err := net.SplitHostPort(address)
	if err != nil {
		return false, false, err
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		return false, false, err
	}
	return listenerState(ctx, pgid, port, host)
}
