package daemon

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"time"

	v1 "github.com/octacian/backlot/api/v1"
	"github.com/octacian/backlot/internal/gateway"
	"github.com/octacian/backlot/internal/plan"
)

func routesReady(publication v1.Publication, services map[string]workload) bool {
	for _, route := range publication.Routes {
		if services[route.Service] == nil {
			return false
		}
	}
	return true
}

func (s *service) publish(ctx context.Context, id string, snapshot plan.Snapshot, result *v1.ExecutionResult, journal *runtimeRecord) error {
	origin, err := url.Parse(result.Origin)
	if err != nil {
		return err
	}
	intent, err := gateway.NewIntent(*snapshot.Caddy, origin.Hostname(), newID(), *snapshot.Plan.Publish, snapshot.Plan.Components, result.Ports)
	if err != nil {
		return err
	}
	journal.Gateway = &intent
	if err := s.store.saveRuntime(id, *journal); err != nil {
		return err
	}
	if s.checkpoint != nil {
		if err := s.checkpoint("gateway-intent"); err != nil {
			return err
		}
	}
	if err := gateway.Publish(ctx, intent); err != nil {
		var detail *v1.PlanError
		if errors.As(err, &detail) && detail.Code != "gateway_uncertain" {
			// These failures precede an effect (including Caddy's atomic rejected
			// mutations and precondition retries). Clear rejected intent so cleanup
			// never treats an observed foreign hostname as an owned allocation.
			journal.Gateway = nil
			if saveErr := s.store.saveRuntime(id, *journal); saveErr != nil {
				journal.Gateway = &intent
				return errors.Join(err, saveErr)
			}
		}
		return err
	}
	if s.checkpoint != nil {
		if err := s.checkpoint("gateway-effect"); err != nil {
			return err
		}
	}
	journal.Gateway.Applied = true
	return s.store.saveRuntime(id, *journal)
}

func (s *store) removeGateway(ctx context.Context, id string, journal *runtimeRecord) error {
	if journal.Gateway == nil {
		return nil
	}
	if err := gateway.Remove(ctx, *journal.Gateway); err != nil {
		return err
	}
	journal.Gateway = nil
	return s.saveRuntime(id, *journal)
}

// Aggregate readiness uses normal host DNS and TLS trust, independent of native
// listener probes. Redirects cannot substitute an unrelated origin for readiness.
func awaitPublication(ctx context.Context, origin, timeout string, services map[string]workload) error {
	duration, err := parseDuration(timeout, 0)
	if err != nil {
		return err
	}
	ctx, cancel := withDeadline(ctx, duration)
	defer cancel()
	transport := &http.Transport{}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 2 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		if err := serviceFailure(services); err != nil {
			return err
		}
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, origin, nil)
		if err != nil {
			return err
		}
		response, err := client.Do(request)
		if err == nil {
			_ = response.Body.Close()
			if response.Header.Get("Backlot-Instance") == request.URL.Hostname() && (response.StatusCode >= 200 && response.StatusCode < 300 || sameOriginRedirect(response, origin)) {
				return nil
			}
		}
		select {
		case <-ctx.Done():
			if errors.Is(ctx.Err(), context.Canceled) {
				return ctx.Err()
			}
			return &v1.PlanError{Code: "publication_unreachable", Field: "scene.publish.probe", Message: "canonical HTTPS origin did not become ready with host DNS and TLS verification; check wildcard application DNS, certificate trust, gateway listener and upstream reachability"}
		case <-ticker.C:
		}
	}
}

func sameOriginRedirect(response *http.Response, origin string) bool {
	if response.StatusCode < 300 || response.StatusCode >= 400 {
		return false
	}
	location, err := response.Location()
	if err != nil {
		return false
	}
	base, err := url.Parse(origin)
	return err == nil && location.Scheme == base.Scheme && location.Host == base.Host && location.User == nil
}

func cleanupDetails(err error) []v1.PlanError {
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		var result []v1.PlanError
		for _, cause := range joined.Unwrap() {
			result = append(result, cleanupDetails(cause)...)
		}
		return result
	}
	var detail *v1.PlanError
	if errors.As(err, &detail) {
		return []v1.PlanError{*detail}
	}
	return nil
}
