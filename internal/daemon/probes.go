package daemon

import (
	"context"
	"errors"
	v1 "github.com/octacian/backlot/api/v1"
	"github.com/octacian/backlot/internal/native"
	"github.com/octacian/backlot/internal/plan"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

func (s *service) awaitProbe(ctx context.Context, id string, c v1.PlannedComponent, snapshot plan.Snapshot, result *v1.ExecutionResult, entry *execution, journal *runtimeRecord, group *native.Group, services map[string]*native.Group) error {
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
	_, portText, err := net.SplitHostPort(address)
	if err != nil {
		return false, err
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		return false, err
	}
	owned, err := native.OwnsListener(ctx, pgid, port)
	if err != nil {
		return false, err
	}
	if !owned {
		conflict, err := native.ListenerConflict(ctx, pgid, port)
		if err != nil {
			return false, err
		}
		if conflict {
			return false, problem("port_conflict", "unrelated listener cannot satisfy readiness")
		}
		return false, nil
	}
	attempt, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()
	if kind == "tcp" {
		conn, err := (&net.Dialer{}).DialContext(attempt, "tcp", address)
		if err != nil {
			return false, nil
		}
		return true, conn.Close()
	}
	request, err := http.NewRequestWithContext(attempt, http.MethodGet, target, nil)
	if err != nil {
		return false, err
	}
	transport := &http.Transport{}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(request)
	if err != nil {
		return false, nil
	}
	closeErr := response.Body.Close()
	return response.StatusCode >= 200 && response.StatusCode < 400, closeErr
}
