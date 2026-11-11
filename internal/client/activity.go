package client

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"reflect"
	"strings"

	v1 "github.com/octacian/backlot/api/v1"
	"github.com/octacian/backlot/internal/manifest"
)

// Activity reads bounded recorded activity without contacting providers.
func (c *Client) Activity(ctx context.Context, q v1.ActivityRequest) (v1.ActivityResponse, error) {
	var out v1.ActivityResponse
	if err := q.Validate(); err != nil {
		return out, err
	}
	if err := c.call(ctx, http.MethodPost, "/v1/activity", q, &out); err != nil {
		return v1.ActivityResponse{}, err
	}
	if err := out.Validate(); err != nil {
		return v1.ActivityResponse{}, err
	}
	if len(out.Entries) > q.Limit && q.Limit != 0 {
		return v1.ActivityResponse{}, responseError()
	}
	for _, e := range out.Entries {
		if err := validateOrigin(e.ID, e.Origin); err != nil {
			return v1.ActivityResponse{}, err
		}
		if e.Key <= q.After {
			return v1.ActivityResponse{}, responseError()
		}
	}
	if out.NextAfter != "" && (out.NextAfter <= q.After || len(out.Entries) > 0 && out.NextAfter < out.Entries[len(out.Entries)-1].Key) {
		return v1.ActivityResponse{}, responseError()
	}
	return out, nil
}

// StopAll observes live compact stop outcomes. Partial failures retain validated
// observations; disconnect ends observation without cancelling accepted cleanup.
func (c *Client) StopAll(ctx context.Context, q v1.StopAllRequest, emit func(v1.StopAllRecord) error) (out v1.StopAllResponse, callErr error) {
	if err := q.Validate(); err != nil {
		return out, err
	}
	data, err := json.Marshal(q)
	if err != nil {
		return out, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://backlot/v1/runtime/stop-all", bytes.NewReader(data))
	if err != nil {
		return out, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Backlot-API-Version", v1.Version)
	httpClient := *c.http
	httpClient.Timeout = 0
	rejected := false
	defer func() {
		if !rejected {
			callErr = mutationError("/v1/runtime/stop-all", q, callErr)
		}
	}()
	res, err := httpClient.Do(req)
	if err != nil {
		return out, &v1.PlanError{Code: "daemon_unavailable", Message: "bulk stop exchange failed"}
	}
	defer func() { _ = res.Body.Close() }()
	if res.Header.Get("X-Backlot-API-Version") != v1.Version {
		return out, responseError()
	}
	if res.StatusCode != http.StatusOK {
		var envelope v1.ErrorResponse
		data, err := io.ReadAll(io.LimitReader(res.Body, manifest.MaxBytes+1))
		if err != nil || res.Header.Get("Content-Type") != "application/json" || manifest.Decode(data, ".json", &envelope) != nil || envelope.APIVersion != v1.Version || envelope.Error.Code == "" {
			return out, responseError()
		}
		rejected = true
		if envelope.Error.Code == "invalid_document" && strings.Contains(envelope.Error.Message, "unknown field") {
			return out, &v1.PlanError{Code: "unsupported_feature", Message: "daemon does not support streaming bulk stop; use matching client and daemon binaries; no mutation was accepted"}
		}
		return out, &envelope.Error
	}
	if res.Header.Get("Content-Type") != "application/x-ndjson" {
		return out, responseError()
	}
	reader := bufio.NewReader(res.Body)
	seen := map[string]bool{}
	out.Targets = []v1.StopAllTarget{}
	for {
		// Target records are compact. Only the final summary may grow, and only
		// in proportion to the validated target identities already received.
		line, err := bulkLine(reader, 2048+140*len(seen))
		if err != nil {
			return out, responseError()
		}
		var event v1.StopAllRecord
		if manifest.DecodeWithLimit(line, ".json", &event, 2048+140*len(seen)) != nil || event.Validate() != nil || event.Type != "summary" && len(line) > 2048 {
			return out, responseError()
		}
		if out.OperationID == "" {
			if event.Type != "accepted" {
				return out, responseError()
			}
			out.APIVersion, out.OperationID, out.DaemonGeneration = event.APIVersion, event.OperationID, event.DaemonGeneration
		} else {
			if event.Type == "accepted" || event.OperationID != out.OperationID || event.DaemonGeneration != out.DaemonGeneration {
				return out, responseError()
			}
		}
		if t := event.Target; t != nil {
			if seen[t.ID] {
				return out, responseError()
			}
			seen[t.ID] = true
			out.Targets = append(out.Targets, *t)
		}
		if summary := event.Summary; summary != nil {
			candidate := out
			candidate.AdmissionsJoined, candidate.SelectionComplete, candidate.SelectionFailure, candidate.Error = summary.AdmissionsJoined, summary.SelectionComplete, summary.SelectionFailure, summary.Error
			if candidate.Validate() != nil || !reflect.DeepEqual(candidate.Summary(), *summary) {
				return out, responseError()
			}
			out = candidate
		}
		if event.Summary != nil {
			if _, err := reader.ReadByte(); !errors.Is(err, io.EOF) {
				return out, responseError()
			}
		}
		if emit != nil {
			if err := emit(event); err != nil {
				return out, &v1.PlanError{Code: "result_unknown", Message: "bulk stop output ended; accepted cleanup continues; inspect activity and known targets; do not automatically retry"}
			}
		}
		if event.Summary != nil {
			if out.Error != nil {
				return out, out.Error
			}
			return out, nil
		}
	}
}

func bulkLine(reader *bufio.Reader, limit int) ([]byte, error) {
	var line []byte
	for {
		part, err := reader.ReadSlice('\n')
		if len(line)+len(part) > limit {
			return nil, responseError()
		}
		line = append(line, part...)
		if !errors.Is(err, bufio.ErrBufferFull) {
			return line, err
		}
	}
}
