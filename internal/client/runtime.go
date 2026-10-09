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

// Reset replaces retained state after stopping all verified owned consumers.
func (c *Client) Reset(ctx context.Context, request v1.RunRequest) (v1.InstanceResponse, error) {
	var response v1.InstanceResponse
	err := c.call(ctx, http.MethodPost, "/v1/reset", request, &response)
	return response, err
}

// Destroy removes owned runtime/data while preserving historical evidence.
func (c *Client) Destroy(ctx context.Context, id string) (v1.InstanceResponse, error) {
	var response v1.InstanceResponse
	err := c.call(ctx, http.MethodPost, "/v1/destroy", v1.InstanceRequest{APIVersion: v1.Version, InstanceID: id}, &response)
	return response, err
}

// Fixtures reads declared fixture metadata without exposing sensitive values.
func (c *Client) Fixtures(ctx context.Context, request v1.FixturesRequest) (v1.FixturesResponse, error) {
	var response v1.FixturesResponse
	err := c.call(ctx, http.MethodPost, "/v1/fixtures", request, &response)
	if err == nil {
		err = validateFixtures(response, request, false)
	}
	return response, err
}

// FixtureSecret explicitly retrieves one declared sensitive fixture value.
func (c *Client) FixtureSecret(ctx context.Context, request v1.FixturesRequest) (v1.FixturesResponse, error) {
	var response v1.FixturesResponse
	err := c.call(ctx, http.MethodPost, "/v1/fixtures/secret", request, &response)
	if err == nil {
		err = validateFixtures(response, request, true)
	}
	return response, err
}

func validateFixtures(response v1.FixturesResponse, request v1.FixturesRequest, secret bool) error {
	if response.APIVersion != v1.Version || secret && len(response.Fixtures) != 1 {
		return responseError()
	}
	seen := map[string]bool{}
	for _, fixture := range response.Fixtures {
		key := fixture.Component + "/" + fixture.Name
		if fixture.Component == "" || fixture.Name == "" || seen[key] || request.Component != "" && fixture.Component != request.Component || !secret && fixture.Sensitive && fixture.Value != "" || secret && (fixture.Name != request.SecretName || !fixture.Sensitive) {
			return responseError()
		}
		seen[key] = true
	}
	return nil
}
