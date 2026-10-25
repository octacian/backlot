//go:build darwin || linux

package native

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// OwnsListener proves that every listener on the selected port belongs to this group.
// An unrelated port owner is never signaled and never satisfies readiness.
func listenerState(ctx context.Context, pgid, port int, endpoint string) (bool, bool, error) {
	return listenerStateWith(ctx, time.Second, pgid, port, endpoint, syscall.Getpgid)
}

// listenerStateWith bounds the socket query and membership checks by budget.
func listenerStateWith(ctx context.Context, budget time.Duration, pgid, port int, endpoint string, membership func(int) (int, error)) (bool, bool, error) {
	query, cancel := context.WithTimeout(ctx, budget)
	defer cancel()
	data, err := listenerOutput(query, port)
	if err != nil {
		return false, false, err
	}
	found := false
	currentGroup := 0
	currentPID, currentFD := 0, -1
	family := ""
	for _, line := range strings.Split(string(data), "\n") {
		if err := query.Err(); err != nil {
			return false, false, err
		}
		if strings.HasPrefix(line, "p") {
			pid, err := strconv.Atoi(line[1:])
			if err != nil {
				return false, false, err
			}
			currentPID, currentFD = pid, -1
			currentGroup, err = membership(pid)
			if err != nil {
				if errors.Is(err, syscall.ESRCH) {
					return false, false, nil
				}
				return false, false, err
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

// ListenerEndpoint checks ownership at a resolved numeric destination.
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

// listenerOutput observes lsof under the caller's bounded query context.
func listenerOutput(ctx context.Context, port int) ([]byte, error) {
	command := exec.CommandContext(ctx, "lsof", "-nP", "-a", "-iTCP:"+strconv.Itoa(port), "-sTCP:LISTEN", "-Fpftn")
	command.WaitDelay = 100 * time.Millisecond
	data, err := command.Output()
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		var exit *exec.ExitError
		if errors.As(err, &exit) && exit.ExitCode() == 1 && len(data) == 0 {
			return nil, nil
		}
		return nil, errors.New("cannot prove listener ownership; lsof is required for native network probes")
	}
	return data, nil
}

// OwnsListenerAddresses proves every listener on the port belongs to the group
// and binds exactly an allowed numeric address/family, with all allowed addresses
// present. Unlike reachability probes, an IPv6 wildcard never satisfies an IPv4
// bind or a specific loopback bind. Unexpected exposure fails closed.
func OwnsListenerAddresses(ctx context.Context, pgid, port int, addresses []string) (bool, error) {
	allowed := make(map[string]bool, len(addresses))
	for _, endpoint := range addresses {
		host, p, err := net.SplitHostPort(endpoint)
		if err != nil {
			return false, errors.New("invalid expected listener address")
		}
		parsedPort, err := strconv.Atoi(p)
		if err != nil || parsedPort != port {
			return false, errors.New("invalid expected listener port")
		}
		// Existing managed admin URLs may spell a numeric port with leading
		// zeros. Compare its value while preserving saved configuration text.
		p = strconv.Itoa(port)
		address, err := netip.ParseAddr(host)
		if err != nil || address.Zone() != "" {
			return false, errors.New("expected listener address must be numeric without zone")
		}
		family := "IPv6"
		if address.Is4() {
			family = "IPv4"
		}
		allowed[family+"|"+net.JoinHostPort(address.String(), p)] = false
	}
	if len(allowed) == 0 {
		return false, errors.New("expected listener address set is empty")
	}
	query, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	data, err := listenerOutput(query, port)
	if err != nil {
		return false, err
	}
	group := 0
	family := ""
	for _, line := range strings.Split(string(data), "\n") {
		switch {
		case strings.HasPrefix(line, "p"):
			family = ""
			pid, err := strconv.Atoi(line[1:])
			if err != nil {
				return false, err
			}
			group, err = syscall.Getpgid(pid)
			if err != nil {
				return false, err
			}
		case strings.HasPrefix(line, "f"):
			family = ""
		case strings.HasPrefix(line, "t"):
			family = line[1:]
		case strings.HasPrefix(line, "n"):
			if group != pgid {
				return false, nil
			}
			host, p, err := net.SplitHostPort(line[1:])
			if err != nil || p != strconv.Itoa(port) {
				return false, errors.New("observed listener address unavailable")
			}
			if host == "*" {
				switch family {
				case "IPv4":
					host = "0.0.0.0"
				case "IPv6":
					host = "::"
				default:
					return false, errors.New("observed listener family unavailable")
				}
			}
			address, err := netip.ParseAddr(host)
			if err != nil || (family == "IPv4" && !address.Is4()) || (family == "IPv6" && !address.Is6()) || (family != "IPv4" && family != "IPv6") {
				return false, errors.New("observed listener family/address unavailable")
			}
			key := family + "|" + net.JoinHostPort(address.String(), p)
			if _, ok := allowed[key]; !ok {
				return false, nil
			}
			allowed[key] = true
		}
	}
	for _, found := range allowed {
		if !found {
			return false, nil
		}
	}
	return true, nil
}

// ListenerEndpoint verifies every relevant listener through this attempt's policy.
// Application membership is observed by the live authenticated session guardian.
func (g *Group) ListenerEndpoint(ctx context.Context, address string) (bool, bool, error) {
	ctx, cancel := context.WithTimeout(ctx, listenerRequestBudget)
	defer cancel()
	if g.Identity.Purpose != "application" {
		return ListenerEndpoint(ctx, g.PGID(), address)
	}
	host, portText, err := net.SplitHostPort(address)
	if err != nil {
		return false, false, err
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		return false, false, err
	}
	return g.sessionListenerRequest(ctx, net.JoinHostPort(host, strconv.Itoa(port)))
}

func (g *Group) sessionListenerRequest(ctx context.Context, address string) (bool, bool, error) {
	ctx, cancel := context.WithTimeout(ctx, listenerRequestBudget)
	defer cancel()
	conn, err := (&net.Dialer{}).DialContext(ctx, "unix", g.Identity.Control)
	if err != nil {
		return false, false, errors.New("session listener authority unavailable")
	}
	defer func() { _ = conn.Close() }()
	deadline := time.Now().Add(listenerRequestBudget)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	_ = conn.SetDeadline(deadline)
	if err := json.NewEncoder(conn).Encode(cleanupRequest{Identity: g.Identity, ListenerAddress: address}); err != nil {
		return false, false, &authorityError{message: "session listener authority unavailable", cause: errors.Join(err, ctx.Err())}
	}
	var proof cleanupProof
	if err := json.NewDecoder(conn).Decode(&proof); err != nil {
		return false, false, &authorityError{message: "session listener observation unavailable", cause: errors.Join(err, ctx.Err())}
	}
	if proof.Identity != g.Identity || proof.Error != "" {
		cause := "listener response identity mismatch"
		if proof.Identity == g.Identity {
			cause = proof.Error
		}
		if proof.Cause != "" {
			cause += ": " + proof.Cause
		}
		// Readiness reports only the bounded message; the private daemon log keeps the cause.
		_, _ = fmt.Fprintf(os.Stderr, "native diagnostic: listener component=%s attempt=%s cause=%q\n", g.Identity.Component, g.Identity.Attempt, cause)
		return false, false, &authorityError{message: "session listener observation unavailable", cause: errors.New(cause)}
	}
	return proof.Owned, proof.Conflict, nil
}

// ListenerConflict checks all listeners on a port against the application's live authority.
func (g *Group) ListenerConflict(ctx context.Context, port int) (bool, error) {
	if g.Identity.Purpose != "application" {
		return ListenerConflict(ctx, g.PGID(), port)
	}
	_, conflict, err := g.sessionListenerRequest(ctx, net.JoinHostPort("", strconv.Itoa(port)))
	return conflict, err
}
