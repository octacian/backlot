package daemon

import (
	"context"
	"errors"
	v1 "github.com/octacian/backlot/api/v1"
	"github.com/octacian/backlot/internal/native"
	"github.com/octacian/backlot/internal/plan"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"
)

func (s *service) awaitProbe(ctx context.Context, id string, c v1.PlannedComponent, snapshot plan.Snapshot, result *v1.ExecutionResult, entry *execution, journal *runtimeRecord, group workload, services map[string]workload) error {
	timeout, err := parseDuration(c.Readiness.Timeout, 0)
	if err != nil {
		return err
	}
	probeCtx, cancel := withDeadline(ctx, timeout)
	defer cancel()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		if err := serviceFailure(services); err != nil {
			for _, port := range c.Ports {
				if c.Runtime == v1.Container {
					continue
				}
				conflict, checkErr := native.ListenerConflict(probeCtx, group.PGID(), result.Ports[port.Resource])
				if checkErr != nil {
					return checkErr
				}
				if conflict {
					return problem("port_conflict", "unrelated listener occupied allocated port")
				}
			}
			return err
		}
		var ready bool
		if c.Readiness.Kind == "command" {
			probe, err := s.launch(probeCtx, id, c.Name, c.Readiness.Executable, c.Readiness.Command.Args, c.Environment, snapshot, result, entry, journal)
			if err != nil {
				return err
			}
			exit, waitErr := waitJob(probeCtx, probe, services)
			cleanupErr := probe.Stop(0)
			if cleanupErr != nil {
				return cleanupErr
			}
			if waitErr == nil && exit != nil && exit.Code == 0 && exit.Error == "" {
				ready = true
			}
		} else {
			value := *c.Readiness.Target
			if value.Redacted {
				value = snapshot.Secrets[c.Name+"/readiness"]
			}
			target, err := runtimeValue(value, id, snapshot.Plan, result.Ports)
			if err != nil {
				return err
			}
			if c.Runtime == v1.Container {
				if err := containerProbeOwned(probeCtx, entry.docker, group.(*dockerWork), c, value, result.Ports); err != nil {
					return err
				}
			}
			ready, err = networkProbe(probeCtx, c.Readiness.Kind, target, group.PGID())
			var typed *v1.PlanError
			if errors.As(err, &typed) && typed.Code == "port_conflict" && value.Symbolic == nil {
				return problem("readiness_unowned", "literal probe target has an unrelated listener; owned runtime stopped without retry")
			}
			if err != nil {
				return err
			}
		}
		if ready {
			return serviceFailure(services)
		}
		select {
		case <-probeCtx.Done():
			return errors.New("readiness/startup deadline or cancellation reached")
		case <-ticker.C:
		}
	}
}
func networkProbe(ctx context.Context, kind, target string, pgid int) (bool, error) {
	address := target
	if kind == "http" {
		u, err := url.Parse(target)
		if err != nil {
			return false, err
		}
		port := u.Port()
		if port == "" {
			port = "80"
			if u.Scheme == "https" {
				port = "443"
			}
		}
		address = net.JoinHostPort(u.Hostname(), port)
	} else if !strings.Contains(address, ":") {
		address = net.JoinHostPort("127.0.0.1", address)
	}
	host, portText, err := net.SplitHostPort(address)
	if err != nil {
		return false, err
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port < 1 || port > 65535 {
		return false, errors.New("invalid probe port")
	}
	resolveCtx, resolveCancel := context.WithTimeout(ctx, time.Second)
	addresses, err := localProbeAddresses(resolveCtx, host, net.DefaultResolver.LookupNetIP)
	resolveCancel()
	if err != nil {
		return false, err
	}
	pinned := ""
	conflict := false
	for _, ip := range addresses {
		endpoint := net.JoinHostPort(ip.String(), portText)
		if pgid == 0 {
			pinned = endpoint
			break
		}
		owned, unrelated, err := native.ListenerEndpoint(ctx, pgid, endpoint)
		if err != nil {
			return false, err
		}
		if owned {
			pinned = endpoint
			break
		}
		conflict = conflict || unrelated
	}
	if pinned == "" {
		if conflict {
			return false, problem("port_conflict", "unrelated destination listener cannot satisfy readiness")
		}
		return false, nil
	}
	attempt, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()
	if kind == "tcp" {
		conn, err := (&net.Dialer{}).DialContext(attempt, "tcp", pinned)
		if err != nil {
			return false, nil
		}
		return true, conn.Close()
	}
	request, err := http.NewRequestWithContext(attempt, http.MethodGet, target, nil)
	if err != nil {
		return false, err
	}
	transport := &http.Transport{DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, pinned)
	}}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(request)
	if err != nil {
		return false, nil
	}
	closeErr := response.Body.Close()
	return response.StatusCode >= 200 && response.StatusCode < 400, closeErr
}

// Resolve once, validate every result, then dial only the chosen numeric endpoint.
// Hostname rebinding or remote listeners cannot substitute for local ownership.
func localProbeAddresses(ctx context.Context, host string, lookup func(context.Context, string, string) ([]netip.Addr, error)) ([]netip.Addr, error) {
	var addresses []netip.Addr
	if ip, err := netip.ParseAddr(host); err == nil {
		addresses = []netip.Addr{ip}
	} else {
		var err error
		addresses, err = lookup(ctx, "ip", host)
		if err != nil {
			return nil, err
		}
	}
	if len(addresses) == 0 {
		return nil, problem("readiness_unowned", "native readiness requires a resolved loopback destination")
	}
	for _, ip := range addresses {
		if !ip.IsLoopback() || ip.Zone() != "" {
			return nil, problem("readiness_unowned", "native readiness destinations must resolve exclusively to loopback addresses")
		}
	}
	return addresses, nil
}
