package client

import (
	"context"
	v1 "github.com/octacian/backlot/api/v1"
	"net/http"
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

// StopAll accepts a finite workload-only stop. Partial failures retain a validated
// response alongside the error; disconnect never cancels accepted cleanup.
func (c *Client) StopAll(ctx context.Context, q v1.StopAllRequest) (v1.StopAllResponse, error) {
	var out v1.StopAllResponse
	if err := q.Validate(); err != nil {
		return out, err
	}
	if err := c.call(ctx, http.MethodPost, "/v1/runtime/stop-all", q, &out); err != nil {
		return v1.StopAllResponse{}, err
	}
	if err := out.Validate(); err != nil {
		return v1.StopAllResponse{}, mutationError("/v1/runtime/stop-all", q, err)
	}
	if out.Error != nil {
		return out, out.Error
	}
	return out, nil
}
