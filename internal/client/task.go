package client

import (
	"context"
	"net/http"

	v1 "github.com/octacian/backlot/api/v1"
)

// RunTask accepts one independent leased execution against an already-ready parent.
// A lost reply has unknown acceptance and must never be retried automatically.
func (c *Client) RunTask(ctx context.Context, request v1.TaskRunRequest) (v1.InstanceResponse, error) {
	var response v1.InstanceResponse
	if err := request.Validate(); err != nil {
		return response, err
	}
	err := c.call(ctx, http.MethodPost, "/v1/tasks/run", request, &response)
	if err == nil {
		b := response.Instance.Task
		if b == nil || b.Name != request.Task || request.Target.InstanceID != "" && b.InstanceID != request.Target.InstanceID || response.Instance.Execution == nil || response.LeaseToken == "" {
			err = mutationError("/v1/tasks/run", request, responseError())
		}
	}
	return response, err
}
