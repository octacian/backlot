package client

import (
	"context"
	"encoding/base64"
	"strconv"
	"strings"
	"time"

	v1 "github.com/octacian/backlot/api/v1"
)

// History reads bounded newest-first attempt summaries, without legacy fallback.
func (c *Client) History(ctx context.Context, q v1.HistoryRequest) (v1.HistoryResponse, error) {
	var r v1.HistoryResponse
	err := c.discoveryCall(ctx, "/v1/attempts/list", q, &r)
	if err != nil {
		return r, err
	}
	limit := q.Limit
	if limit == 0 {
		limit = 100
	}
	if r.APIVersion != v1.Version || r.InstanceID != q.InstanceID || r.Attempts == nil || len(r.Attempts) > limit || len(r.Continuation) > 256 || r.Continuation != "" && len(r.Attempts) == 0 {
		return v1.HistoryResponse{}, responseError()
	}
	before := int64(0)
	if q.After != "" {
		var err error
		before, err = historyCursor(q.After, q.InstanceID)
		if err != nil {
			return v1.HistoryResponse{}, err
		}
	}
	last := int64(0)
	seen := map[string]bool{}
	for _, a := range r.Attempts {
		if !validAttemptProvenance(a.Provenance) || !validID(a.Attempt) || a.Sequence <= 0 || before > 0 && a.Sequence >= before || last != 0 && a.Sequence >= last || seen[a.Attempt] || !historyStatus(a.Status, a.Provenance) {
			return v1.HistoryResponse{}, responseError()
		}
		if a.CompletedAt != "" {
			if _, err := time.Parse(time.RFC3339Nano, a.CompletedAt); err != nil {
				return v1.HistoryResponse{}, responseError()
			}
		}
		last = a.Sequence
		seen[a.Attempt] = true
	}
	if r.Continuation != "" {
		sequence, err := historyCursor(r.Continuation, q.InstanceID)
		if err != nil || sequence != last {
			return v1.HistoryResponse{}, responseError()
		}
	}
	return r, nil
}

func historyCursor(cursor, id string) (int64, error) {
	data, err := base64.RawURLEncoding.DecodeString(cursor)
	parts := strings.Split(string(data), "/")
	if err != nil || len(parts) != 2 || parts[0] != id {
		return 0, responseError()
	}
	n, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil || n <= 0 {
		return 0, responseError()
	}
	return n, nil
}

func attemptStatus(status v1.InstanceStatus) bool {
	switch status {
	case v1.Starting, v1.RuntimeReady, v1.Stopping, v1.Stopped, v1.Failed, v1.Succeeded, v1.Cancelled, v1.Interrupted:
		return true
	}
	return false
}

// Attempt reads an exact retained result or the last completed result.
func (c *Client) Attempt(ctx context.Context, q v1.AttemptRequest) (v1.AttemptResponse, error) {
	var r v1.AttemptResponse
	err := c.discoveryCall(ctx, "/v1/attempts/inspect", q, &r)
	if err != nil {
		return r, err
	}
	a := r.Attempt
	if r.APIVersion != v1.Version || a.InstanceID != q.InstanceID || !validID(a.InstanceID) || a.Sequence <= 0 || !validAttemptProvenance(a.Provenance) || a.CreatedAt == "" || len(a.Result.Fixtures) != 0 || !historyStatus(a.Status, a.Provenance) || q.Attempt != "" && a.Result.Attempt != q.Attempt || q.LastCompleted && !a.Completed() || a.Source.Project == "" || a.Source.Scene == "" || !validID(a.Source.CheckoutID) || (a.Source.Lifetime != v1.Persistent && a.Source.Lifetime != v1.Disposable) {
		return v1.AttemptResponse{}, responseError()
	}
	for _, stamp := range []string{a.CreatedAt, a.StartedAt} {
		if stamp != "" {
			if _, err := time.Parse(time.RFC3339Nano, stamp); err != nil {
				return v1.AttemptResponse{}, responseError()
			}
		}
	}
	for _, artifact := range a.Result.Artifacts {
		if artifact.Attempt != a.Result.Attempt || artifact.Component == "" || artifact.Name == "" {
			return v1.AttemptResponse{}, responseError()
		}
	}
	if a.Result.Outcome != "" && !attemptStatus(a.Result.Outcome) {
		return v1.AttemptResponse{}, responseError()
	}
	if err := validateExecution(v1.Instance{ID: a.InstanceID, Status: a.Status, Execution: &a.Result, NativeOwnershipWarnings: a.NativeOwnershipWarnings}); err != nil {
		return v1.AttemptResponse{}, err
	}
	return r, nil
}

// PruneHistory explicitly removes selected eligible history and evidence.
func (c *Client) PruneHistory(ctx context.Context, q v1.HistoryPruneRequest) (v1.HistoryPruneResponse, error) {
	var r v1.HistoryPruneResponse
	if err := q.Validate(); err != nil {
		return r, err
	}
	err := c.discoveryCall(ctx, "/v1/attempts/prune", q, &r)
	if err != nil {
		return r, err
	}
	if r.APIVersion != v1.Version || r.InstanceID != q.InstanceID || r.DryRun != q.DryRun || r.Attempts == nil {
		return v1.HistoryPruneResponse{}, mutationError("/v1/attempts/prune", q, responseError())
	}
	seen := map[string]bool{}
	for _, a := range r.Attempts {
		if !validID(a.Attempt) || a.Bytes < 0 || a.Reasons == nil || len(a.Reasons) < 1 || len(a.Reasons) > 2 || a.Protected != (a.Protection != "") || seen[a.Attempt] {
			return v1.HistoryPruneResponse{}, mutationError("/v1/attempts/prune", q, responseError())
		}
		for _, reason := range a.Reasons {
			if reason != "older_than" && reason != "keep" {
				return v1.HistoryPruneResponse{}, mutationError("/v1/attempts/prune", q, responseError())
			}
		}
		if len(a.Reasons) == 2 && a.Reasons[0] == a.Reasons[1] {
			return v1.HistoryPruneResponse{}, mutationError("/v1/attempts/prune", q, responseError())
		}
		switch a.Protection {
		case "", "active", "kept", "cleanup_unresolved", "completion_unknown":
		default:
			return v1.HistoryPruneResponse{}, mutationError("/v1/attempts/prune", q, responseError())
		}
		seen[a.Attempt] = true
	}
	return r, nil
}

func validAttemptProvenance(value string) bool {
	return value == "accepted" || value == "legacy_current" || value == "cleanup_only"
}

func historyStatus(status v1.InstanceStatus, provenance string) bool {
	return attemptStatus(status) || status == v1.Destroyed && provenance == "legacy_current"
}
