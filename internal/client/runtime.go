package client

import (
	"context"
	v1 "github.com/octacian/backlot/api/v1"
	"net/http"
)

// Run accepts daemon-owned native startup and returns its durable identity.
func (c *Client) Run(ctx context.Context, request v1.RunRequest) (v1.InstanceResponse, error) {
	var response v1.InstanceResponse
	err := c.call(ctx, http.MethodPost, "/v1/run", request, &response)
	return response, err
}

// Restart reconciles compatible drift after verified persistent runtime stop.
func (c *Client) Restart(ctx context.Context, request v1.RunRequest) (v1.InstanceResponse, error) {
	var response v1.InstanceResponse
	err := c.call(ctx, http.MethodPost, "/v1/restart", request, &response)
	return response, err
}

// StopExecution cancels and joins owned native work before returning success.
func (c *Client) StopExecution(ctx context.Context, id string) (v1.InstanceResponse, error) {
	var response v1.InstanceResponse
	err := c.call(ctx, http.MethodPost, "/v1/runtime/stop", v1.InstanceRequest{APIVersion: v1.Version, InstanceID: id}, &response)
	return response, err
}

// Logs reads a bounded retained output page, also used for live polling.
func (c *Client) Logs(ctx context.Context, request v1.LogsRequest) (v1.LogsResponse, error) {
	var response v1.LogsResponse
	err := c.call(ctx, http.MethodPost, "/v1/logs", request, &response)
	if err == nil {
		if response.NextOffset < request.Offset || len(response.Records) > response.NextOffset-request.Offset {
			return response, responseError()
		}
		for _, record := range response.Records {
			if record.InstanceID != request.InstanceID || (request.Component != "" && record.Component != request.Component) {
				return response, responseError()
			}
		}
	}
	return response, err
}
