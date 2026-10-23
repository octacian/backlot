package client

import (
	"context"
	v1 "github.com/octacian/backlot/api/v1"
	"net/http"
	"time"
)

func validateAttachment(response v1.InstanceResponse, requested bool) error {
	a := response.Attachment
	if (a != nil) != requested {
		return responseError()
	}
	if a == nil {
		return nil
	}
	lease, err := time.ParseDuration(a.LeaseDuration)
	if err != nil || lease < 100*time.Millisecond || lease > 24*time.Hour || response.Instance.Plan.Lifetime != v1.Persistent || response.Instance.Execution == nil || a.Owner.Attempt != response.Instance.Execution.Attempt || !validID(a.Owner.Attempt) || !validID(a.Owner.DaemonGeneration) || !validID(a.Owner.Token) {
		return responseError()
	}
	if _, err := time.Parse(time.RFC3339Nano, a.ExpiresAt); err != nil {
		return responseError()
	}
	return nil
}

// Attached renews or cancels a pinned owner and returns its recorded status.
func (c *Client) Attached(ctx context.Context, request v1.AttachedRequest, cancel bool) (v1.SceneResponse, error) {
	path := "/v1/attached/renew"
	if cancel {
		path = "/v1/attached/cancel"
	}
	var response v1.SceneResponse
	err := c.call(ctx, http.MethodPost, path, request, &response)
	if err == nil {
		err = validateScene(response, v1.SceneTarget{InstanceID: request.InstanceID}, false)
	}
	if err == nil && (response.Outcome != v1.SceneFound || response.DaemonGeneration != request.Owner.DaemonGeneration || response.Result == nil || response.Result.Attachment != nil || response.Result.Instance.Execution == nil || response.Result.Instance.Execution.Attempt != request.Owner.Attempt) {
		err = responseError()
	}
	return response, err
}
