package client

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"path/filepath"

	v1 "github.com/octacian/backlot/api/v1"
	"github.com/octacian/backlot/internal/manifest"
)

// Infra reads or starts configured infrastructure through the daemon owner.
// A partial startup response preserves daemon availability alongside the error.
func (c *Client) Infra(ctx context.Context, action string, q v1.InfraRequest) (v1.InfraResponse, error) {
	var out v1.InfraResponse
	if action == "stop" {
		return out, &v1.PlanError{Code: "invalid_request", Message: "use streamed infrastructure shutdown"}
	}
	if err := q.Validate(action); err != nil {
		return out, err
	}
	if err := c.call(ctx, http.MethodPost, "/v1/infra/"+action, q, &out); err != nil {
		return v1.InfraResponse{}, err
	}
	if out.APIVersion != v1.Version || !validID(out.DaemonGeneration) || !filepath.IsAbs(out.Directory) || out.Daemon != "running" || out.Gateway == nil && out.Error == nil || out.Guidance == "" || out.Ready && out.Error != nil {
		return v1.InfraResponse{}, responseError()
	}
	if out.Error == nil && out.Activity == nil {
		return v1.InfraResponse{}, responseError()
	}
	g := out.Gateway
	if g != nil {
		if g.APIVersion != v1.Version || g.Mode != "managed" && g.Mode != "external" && g.Mode != "none" {
			return v1.InfraResponse{}, responseError()
		}
		switch g.Status {
		case "unconfigured", "running", "stopped", "interrupted", "unavailable", "uncertain":
		default:
			return v1.InfraResponse{}, responseError()
		}
	}
	if out.Activity != nil && (out.Activity.Validate() != nil || out.Activity.DaemonGeneration != out.DaemonGeneration) {
		return v1.InfraResponse{}, responseError()
	}
	if out.Ready && (out.Activity == nil || out.Activity.AdmissionClosed || out.Gateway == nil || out.Gateway.Mode == "managed" && out.Gateway.Status != "running" && out.Gateway.Status != "unconfigured") {
		return v1.InfraResponse{}, responseError()
	}
	if out.Error != nil {
		if out.Error.Code == "" || out.Error.Message == "" {
			return v1.InfraResponse{}, responseError()
		}
		return out, out.Error
	}
	return out, nil
}

// InfraShutdown consumes transient cleanup observations without a total deadline.
// A missing terminal cleanup record means unknown completion, never permission to retry.
func (c *Client) InfraShutdown(ctx context.Context, generation string, emit func(v1.InfraStopRecord) error) (terminal v1.InfraStopRecord, result error) {
	q := v1.InfraRequest{APIVersion: v1.Version, Contract: v1.InfraStopContract, DaemonGeneration: generation}
	if err := q.Validate("stop"); err != nil {
		return terminal, err
	}
	data, err := json.Marshal(q)
	if err != nil {
		return terminal, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://backlot/v1/infra/stop", bytes.NewReader(data))
	if err != nil {
		return terminal, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Backlot-API-Version", v1.Version)
	httpClient := *c.http
	httpClient.Timeout = 0
	rejected := false
	defer func() {
		if result != nil && !rejected {
			result = &v1.PlanError{Code: "completion_unknown", Message: "infrastructure shutdown acceptance/completion unknown; accepted cleanup continues independently; inspect activity in the same scope; do not automatically retry"}
		}
	}()
	res, err := httpClient.Do(req)
	if err != nil {
		return terminal, err
	}
	defer func() { _ = res.Body.Close() }()
	if res.Header.Get("X-Backlot-API-Version") != v1.Version {
		return terminal, responseError()
	}
	if res.StatusCode != http.StatusOK {
		var envelope v1.ErrorResponse
		data, err := io.ReadAll(io.LimitReader(res.Body, manifest.MaxBytes+1))
		if err != nil || res.Header.Get("Content-Type") != "application/json" || manifest.Decode(data, ".json", &envelope) != nil || envelope.APIVersion != v1.Version || envelope.Error.Code == "" || envelope.Error.Message == "" {
			return terminal, responseError()
		}
		rejected = true
		return terminal, &envelope.Error
	}
	if res.Header.Get("Content-Type") != "application/x-ndjson" {
		return terminal, responseError()
	}
	reader := bufio.NewReader(res.Body)
	operation := ""
	seen := map[string]bool{}
	phases := map[string]string{}
	targetFailed := false
	for {
		line, err := bulkLine(reader, 4096)
		if err != nil {
			return terminal, err
		}
		var event v1.InfraStopRecord
		if manifest.Decode(line, ".json", &event) != nil || event.Validate() != nil || event.DaemonGeneration != generation {
			return terminal, responseError()
		}
		if operation == "" {
			if event.Type != "accepted" {
				return terminal, responseError()
			}
			operation = event.OperationID
		} else if event.Type == "accepted" || event.OperationID != operation {
			return terminal, responseError()
		}
		if event.Type == "phase" {
			before := phases[event.Phase]
			if event.State == "running" && before != "" || event.State != "running" && before != "running" {
				return terminal, responseError()
			}
			phases[event.Phase] = event.State
		}
		if event.Target != nil {
			if seen[event.Target.ID] {
				return terminal, responseError()
			}
			seen[event.Target.ID] = true
			targetFailed = targetFailed || event.Target.Error != nil
		}
		if event.Type == "cleanup" {
			failed := targetFailed
			for _, phase := range []string{"owners", "expiry", "recovery", "gateway", "shared", "records"} {
				if phases[phase] != "succeeded" && phases[phase] != "failed" {
					return terminal, responseError()
				}
				failed = failed || phases[phase] == "failed"
			}
			if failed && event.Error == nil {
				return terminal, responseError()
			}
			if _, err := reader.ReadByte(); !errors.Is(err, io.EOF) {
				return terminal, responseError()
			}
		}
		if emit != nil {
			if err := emit(event); err != nil {
				return terminal, err
			}
		}
		if event.Type == "cleanup" {
			return event, nil
		}
	}
}
