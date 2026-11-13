package client

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	v1 "github.com/octacian/backlot/api/v1"
	"github.com/octacian/backlot/internal/manifest"
)

// Cleanup reads a complete typed preview or observes accepted destruction. The
// caller explicitly cancels accepted work with CancelCleanup; disconnect is unknown.
func (c *Client) Cleanup(ctx context.Context, q v1.CleanupRequest, emit func(v1.CleanupRecord) error) (summary v1.CleanupSummary, callErr error) {
	if err := q.Validate(); err != nil {
		return summary, err
	}
	data, err := json.Marshal(q)
	if err != nil {
		return summary, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://backlot/v1/cleanup", bytes.NewReader(data))
	if err != nil {
		return summary, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Backlot-API-Version", v1.Version)
	transport := *c.http
	transport.Timeout = 0
	accepted := false
	defer func() {
		if q.ApplyPreview != "" && accepted && callErr != nil {
			var typed *v1.PlanError
			if !errors.As(callErr, &typed) || typed.Code != "partial_failure" && typed.Code != "cancelled" {
				callErr = &v1.PlanError{Code: "result_unknown", Message: "cleanup observation ended; acceptance or completion may be unknown; inspect the selected state root and known targets before explicit preview/retry"}
			}
		}
	}()
	res, err := transport.Do(req)
	if err != nil {
		accepted = q.ApplyPreview != ""
		return summary, err
	}
	defer func() { _ = res.Body.Close() }()
	accepted = q.ApplyPreview != ""
	if res.Header.Get("X-Backlot-API-Version") != v1.Version {
		return summary, responseError()
	}
	if res.StatusCode != http.StatusOK {
		var envelope v1.ErrorResponse
		data, err := io.ReadAll(io.LimitReader(res.Body, manifest.MaxBytes+1))
		if err != nil || res.Header.Get("Content-Type") != "application/json" || manifest.Decode(data, ".json", &envelope) != nil || envelope.APIVersion != v1.Version || envelope.Error.Code == "" {
			accepted = q.ApplyPreview != ""
			return summary, responseError()
		}
		accepted = false
		return summary, &envelope.Error
	}
	accepted = q.ApplyPreview != ""
	if res.Header.Get("Content-Type") != "application/x-ndjson" {
		return summary, responseError()
	}
	reader := bufio.NewReader(res.Body)
	generation, operation := "", ""
	seen := map[string]bool{}
	totals := v1.CleanupSummary{}
	for {
		line, err := bulkLine(reader, manifest.MaxBytes)
		if err != nil {
			return summary, responseError()
		}
		var r v1.CleanupRecord
		if manifest.Decode(line, ".json", &r) != nil || r.Validate() != nil {
			return summary, responseError()
		}
		if generation == "" {
			generation = r.DaemonGeneration
			if accepted {
				if r.Type != "accepted" {
					return summary, responseError()
				}
				operation = r.OperationID
			}
		} else if generation != r.DaemonGeneration || r.OperationID != operation || r.Type == "accepted" {
			return summary, responseError()
		}
		if accepted {
			if r.Type == "preview" || r.Type == "preview_target" {
				return summary, responseError()
			}
			if r.Type == "target" {
				if seen[r.InstanceID] {
					return summary, responseError()
				}
				seen[r.InstanceID] = true
				switch r.Outcome {
				case "done":
					totals.Done++
				case "failed":
					totals.Failed++
				case "unresolved":
					totals.Unresolved++
				case "unattempted":
					totals.Unattempted++
				}
			}
		} else {
			if r.Type != "preview" && r.Type != "preview_target" {
				return summary, responseError()
			}
			if r.Target != nil {
				if seen[r.Target.ID] {
					return summary, responseError()
				}
				seen[r.Target.ID] = true
			}
		}
		if r.Summary != nil {
			summary = *r.Summary
			if summary.Targets != len(seen) || accepted && (summary.Preview != q.ApplyPreview || summary.Done != totals.Done || summary.Failed != totals.Failed || summary.Unresolved != totals.Unresolved || summary.Unattempted != totals.Unattempted) {
				return v1.CleanupSummary{}, responseError()
			}
			if _, err := reader.ReadByte(); !errors.Is(err, io.EOF) {
				return summary, responseError()
			}
		}
		if emit != nil {
			if err := emit(r); err != nil {
				return summary, err
			}
		}
		if r.Summary != nil {
			if summary.Failed+summary.Unresolved+summary.Unattempted > 0 {
				return summary, &v1.PlanError{Code: "partial_failure", Message: "cleanup incomplete; inspect failed/unresolved targets and preview remaining cleanup before retry"}
			}
			if summary.Cancelled {
				return summary, &v1.PlanError{Code: "cancelled", Message: "cleanup cancelled"}
			}
			return summary, nil
		}
	}
}

// CancelCleanup acknowledges cancellation intent separately from observation loss.
func (c *Client) CancelCleanup(ctx context.Context, q v1.CleanupCancelRequest) (v1.CleanupCancelResponse, error) {
	var out v1.CleanupCancelResponse
	if err := q.Validate(); err != nil {
		return out, err
	}
	if err := c.call(ctx, http.MethodPost, "/v1/cleanup/cancel", q, &out); err != nil {
		return out, err
	}
	if out.APIVersion != v1.Version || out.DaemonGeneration != q.DaemonGeneration || out.OperationID != q.OperationID || !out.Cancelled {
		return v1.CleanupCancelResponse{}, responseError()
	}
	return out, nil
}
