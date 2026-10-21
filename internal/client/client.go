// Package client provides the typed v1 Unix-socket client shared by CLI adapters.
package client

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	v1 "github.com/octacian/backlot/api/v1"
	"github.com/octacian/backlot/internal/localipc"
	"github.com/octacian/backlot/internal/manifest"
)

// Client transports named v1 contracts over one local Unix socket.
type Client struct {
	http      *http.Client
	transport *http.Transport
}

// New creates a bounded local client. Close releases idle transport connections.
func New(socket string) *Client {
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		if err := localipc.Access(socket); err != nil {
			return nil, err
		}
		return (&net.Dialer{Timeout: time.Second}).DialContext(ctx, "unix", socket)
	}}
	return &Client{http: &http.Client{Transport: transport, Timeout: 20 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}, transport: transport}
}

// Close releases idle socket connections without affecting accepted preparations.
func (c *Client) Close() { c.transport.CloseIdleConnections() }

func (c *Client) call(ctx context.Context, method, path string, request, response any) error {
	// Resolve compatibility afresh at every configuration-consuming boundary.
	// Historical reads and snapshot-based cleanup remain available; incompatible
	// state must be cleaned up with its matching binary (see docs/containers.md).
	checkCompatibility := false
	switch path {
	case "/v1/scenes/start", "/v1/prepare", "/v1/run", "/v1/restart", "/v1/reset", "/v1/gateway/start", "/v1/gateway/restart":
		checkCompatibility = true
	case "/v1/doctor":
		// An explicit diagnostic image authorizes container creation/start, unlike
		// ordinary doctor reads of current or recorded configuration and evidence.
		if probe, ok := request.(v1.DoctorRequest); ok {
			checkCompatibility = probe.ContainerProbeImage != ""
		}
	}
	if checkCompatibility {
		status, err := c.Status(ctx)
		if err != nil || status.Status != "running" {
			code := "api_version"
			var detail *v1.PlanError
			if errors.As(err, &detail) {
				code = detail.Code
			}
			return &v1.PlanError{Code: code, Message: "cannot verify running daemon configuration compatibility; use the matching older binary to inspect, clean up and shut down its daemon, then select a fresh --state-dir with this binary; preserve old state until cleanup is verified"}
		}
	}
	var body io.Reader
	if request != nil {
		data, err := json.Marshal(request)
		if err != nil {
			return err
		}
		if len(data) > manifest.MaxBytes {
			return &v1.PlanError{Code: "invalid_request", Message: "request exceeds 1 MiB limit"}
		}
		body = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://backlot"+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("X-Backlot-API-Version", v1.Version)
	if request != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	httpClient := *c.http
	// Cleanup/restart can use a configured grace exceeding the transport default.
	// The daemon bounds cleanup; callers retain context cancellation authority.
	if path == "/v1/scenes/stop" || path == "/v1/export" || path == "/v1/doctor" || path == "/v1/runtime/stop" || path == "/v1/reset" || path == "/v1/destroy" || path == "/v1/restart" || path == "/v1/stop" || path == "/v1/cancel" {
		httpClient.Timeout = 0
	}
	res, err := httpClient.Do(req)
	if err != nil {
		if strings.HasPrefix(path, "/v1/scenes/") && ctx.Err() != nil && errors.Is(err, ctx.Err()) {
			return context.Cause(ctx)
		}
		var detail *v1.PlanError
		if errors.As(err, &detail) {
			return detail
		}
		return &v1.PlanError{Code: "daemon_unavailable", Message: "cannot contact local daemon; run daemon start or doctor with the same --state-dir"}
	}
	defer func() { _ = res.Body.Close() }()
	if res.Header.Get("X-Backlot-API-Version") != v1.Version {
		return &v1.PlanError{Code: "api_version", Message: "daemon API is incompatible; use a matching client"}
	}
	if res.Header.Get("Content-Type") != "application/json" {
		return &v1.PlanError{Code: "invalid_response", Message: "daemon response must have application/json content type"}
	}
	data, err := io.ReadAll(io.LimitReader(res.Body, manifest.MaxBytes+1))
	if strings.HasPrefix(path, "/v1/scenes/") && err != nil && ctx.Err() != nil && errors.Is(err, ctx.Err()) {
		return context.Cause(ctx)
	}
	if err != nil || len(data) > manifest.MaxBytes {
		return &v1.PlanError{Code: "invalid_response", Message: "daemon response unavailable or exceeds 1 MiB limit"}
	}
	if res.StatusCode != http.StatusOK {
		var envelope v1.ErrorResponse
		if err := manifest.Decode(data, ".json", &envelope); err != nil || envelope.APIVersion != v1.Version || envelope.Error.Code == "" || envelope.Error.Message == "" {
			return &v1.PlanError{Code: "invalid_response", Message: "invalid daemon error envelope"}
		}
		return &envelope.Error
	}
	if err := manifest.Decode(data, ".json", response); err != nil {
		return &v1.PlanError{Code: "invalid_response", Message: "invalid daemon response contract"}
	}
	switch value := response.(type) {
	case *v1.DoctorResponse:
		return value.Validate()
	case *v1.GatewayResponse:
		if value.APIVersion != v1.Version || value.Mode != "managed" {
			return responseError()
		}
		switch value.Status {
		case "unconfigured", "running", "stopped", "interrupted", "unavailable", "uncertain":
		default:
			return responseError()
		}
	case *v1.ListResponse:
		if value.APIVersion != v1.Version || len(value.Instances) > 100 || value.Instances == nil {
			return responseError()
		}
		last := ""
		for _, instance := range value.Instances {
			if !validID(instance.ID) || instance.ID <= last || instance.Project == "" || instance.Scene == "" || !filepath.IsAbs(instance.Checkout) || (instance.Lifetime != v1.Persistent && instance.Lifetime != v1.Disposable) {
				return responseError()
			}
			if err := validateOrigin(instance.ID, instance.Origin); err != nil {
				return err
			}
			if _, err := time.Parse(time.RFC3339Nano, instance.CreatedAt); err != nil {
				return responseError()
			}
			switch instance.Status {
			case v1.Preparing, v1.Prepared, v1.Cancelled, v1.Interrupted, v1.Starting, v1.RuntimeReady, v1.Stopping, v1.Stopped, v1.Destroyed, v1.Succeeded, v1.Failed:
			default:
				return responseError()
			}
			last = instance.ID
		}
		if value.NextAfterID != "" && (last == "" || value.NextAfterID != last) {
			return responseError()
		}
	case *v1.LogsResponse:
		if value.APIVersion != v1.Version || value.NextOffset < 0 {
			return responseError()
		}
		for _, record := range value.Records {
			if !validID(record.InstanceID) || !validID(record.Attempt) || record.Component == "" || (record.Stream != "stdout" && record.Stream != "stderr") {
				return responseError()
			}
			if record.Data != "" {
				if record.Message != "" {
					return responseError()
				}
				if _, err := base64.StdEncoding.DecodeString(record.Data); err != nil {
					return responseError()
				}
			}
			if _, err := time.Parse(time.RFC3339Nano, record.Time); err != nil {
				return responseError()
			}
		}
	case *v1.DaemonStatusResponse:
		if value.APIVersion != v1.Version || value.StateVersion != v1.StateVersion {
			return &v1.PlanError{Code: "api_version", Message: "incompatible daemon API or state format"}
		}
		lease, err := time.ParseDuration(value.LeaseDuration)
		if value.PID <= 0 || (value.Status != "running" && value.Status != "stopping") || err != nil || lease < 100*time.Millisecond || lease > 24*time.Hour {
			return responseError()
		}
	case *v1.InstanceResponse:
		if value.APIVersion != v1.Version {
			return &v1.PlanError{Code: "api_version", Message: "incompatible daemon response version"}
		}
		return validateInstance(*value)
	}
	return nil
}

// List discovers bounded pages of current and historical recorded instances.
func (c *Client) List(ctx context.Context, request v1.ListRequest) (v1.ListResponse, error) {
	var response v1.ListResponse
	err := c.call(ctx, http.MethodPost, "/v1/instances", request, &response)
	return response, err
}

// Status checks daemon connectivity and API/state compatibility.
func (c *Client) Status(ctx context.Context) (v1.DaemonStatusResponse, error) {
	var r v1.DaemonStatusResponse
	err := c.call(ctx, http.MethodGet, "/v1/status", nil, &r)
	return r, err
}

// Stop requests graceful daemon shutdown.
func (c *Client) Stop(ctx context.Context) (v1.DaemonStatusResponse, error) {
	var r v1.DaemonStatusResponse
	err := c.call(ctx, http.MethodPost, "/v1/stop", v1.ControlRequest{APIVersion: v1.Version}, &r)
	return r, err
}

// Prepare accepts metadata-only preparation; it never executes the scene.
func (c *Client) Prepare(ctx context.Context, request v1.PrepareRequest) (v1.InstanceResponse, error) {
	var r v1.InstanceResponse
	err := c.call(ctx, http.MethodPost, "/v1/prepare", request, &r)
	return r, err
}

// Inspect reads a redacted durable instance without exposing lease credentials.
func (c *Client) Inspect(ctx context.Context, id string) (v1.InstanceResponse, error) {
	var r v1.InstanceResponse
	err := c.call(ctx, http.MethodPost, "/v1/inspect", v1.InstanceRequest{APIVersion: v1.Version, InstanceID: id}, &r)
	return r, err
}

// Cancel explicitly cancels metadata preparation, preserving snapshots and records.
func (c *Client) Cancel(ctx context.Context, id string) (v1.InstanceResponse, error) {
	var r v1.InstanceResponse
	err := c.call(ctx, http.MethodPost, "/v1/cancel", v1.InstanceRequest{APIVersion: v1.Version, InstanceID: id}, &r)
	return r, err
}

// Renew extends an active disposable lease using its initial private capability.
func (c *Client) Renew(ctx context.Context, id, token string) (v1.InstanceResponse, error) {
	var r v1.InstanceResponse
	err := c.call(ctx, http.MethodPost, "/v1/renew", v1.LeaseRequest{APIVersion: v1.Version, InstanceID: id, LeaseToken: token}, &r)
	return r, err
}

// Doctor performs explicitly scoped provider diagnostics through the daemon.
func (c *Client) Doctor(ctx context.Context, request v1.DoctorRequest) (v1.DoctorResponse, error) {
	var response v1.DoctorResponse
	if err := request.Validate(); err != nil {
		return response, err
	}
	err := c.call(ctx, http.MethodPost, "/v1/doctor", request, &response)
	return response, err
}
