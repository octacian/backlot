package client

import (
	"context"
	v1 "github.com/octacian/backlot/api/v1"
	"net/http"
	"path/filepath"
)

// Run accepts daemon-owned native startup and returns its durable identity.
func (c *Client) Run(ctx context.Context, request v1.RunRequest) (v1.InstanceResponse, error) {
	var response v1.InstanceResponse
	err := c.call(ctx, http.MethodPost, "/v1/run", request, &response)
	if err == nil {
		err = mutationError("/v1/run", request, validateAttachment(response, request.Attached))
	}
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
	return c.StopExecutionWithRequest(ctx, v1.StopRequest{APIVersion: v1.Version, InstanceID: id})
}

// StopExecutionWithRequest permits waiver of ExpectedAttempt when Force is true.
// Callers select it from Inspect; retries retain the same precondition.
func (c *Client) StopExecutionWithRequest(ctx context.Context, request v1.StopRequest) (v1.InstanceResponse, error) {
	var response v1.InstanceResponse
	err := c.call(ctx, http.MethodPost, "/v1/runtime/stop", request, &response)
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

// Destroy removes owned runtime/data and erases retained attempt history/evidence.
func (c *Client) Destroy(ctx context.Context, id string) (v1.InstanceResponse, error) {
	return c.DestroyWithRequest(ctx, v1.DestroyRequest{APIVersion: v1.Version, InstanceID: id, Contract: v1.DestroyContract})
}

// DestroyWithRequest removes a recorded instance with explicit invocation authority.
func (c *Client) DestroyWithRequest(ctx context.Context, request v1.DestroyRequest) (v1.InstanceResponse, error) {
	var response v1.InstanceResponse
	if request.APIVersion != v1.Version || !validID(request.InstanceID) || request.Contract != "" && request.Contract != v1.DestroyContract {
		return response, &v1.PlanError{Code: "invalid_request", Message: "recorded instance_id and compatible API version required"}
	}
	request.Contract = v1.DestroyContract
	err := c.call(ctx, http.MethodPost, "/v1/destroy", request, &response)
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
	if response.APIVersion != v1.Version || secret && len(response.Fixtures) != 1 || request.ExpectedAttempt != "" && (response.InstanceID != request.InstanceID || response.Attempt != request.ExpectedAttempt) {
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

// Export publishes a daemon-owned portable snapshot without cancelling the run.
func (c *Client) Export(ctx context.Context, request v1.ExportRequest) (v1.ExportResponse, error) {
	var response v1.ExportResponse
	err := c.call(ctx, http.MethodPost, "/v1/export", request, &response)
	if err != nil {
		if ctx.Err() != nil {
			return v1.ExportResponse{}, exportContextError(ctx.Err())
		}
		return v1.ExportResponse{}, err
	}
	// Never expose rejected wire fields as a usable publication. Valid failed
	// publications below still retain their independently validated run result.
	if response.APIVersion != v1.Version || response.Result.InstanceID != request.InstanceID || request.Attempt != "" && response.Result.Attempt != request.Attempt || !validID(response.Result.Attempt) {
		return v1.ExportResponse{}, mutationError("/v1/export", request, responseError())
	}
	if err := validateExportResult(response.Result); err != nil {
		return v1.ExportResponse{}, mutationError("/v1/export", request, err)
	}
	if response.Published {
		if !response.Complete && !request.AllowPartial || response.Error != nil || !filepath.IsAbs(response.Output) || response.Report != "report.json" || response.HTML != "index.html" {
			return v1.ExportResponse{}, mutationError("/v1/export", request, responseError())
		}
	} else if response.Error == nil || response.Error.Code == "" || response.Error.Message == "" || response.Output != "" || response.Report != "" || response.HTML != "" || response.Complete {
		return v1.ExportResponse{}, mutationError("/v1/export", request, responseError())
	}
	if response.Error != nil {
		return response, response.Error
	}
	return response, nil
}

func exportContextError(err error) error {
	if err == context.DeadlineExceeded {
		return &v1.PlanError{Code: "export_interrupted", Message: "export deadline exceeded; run was not cancelled"}
	}
	return &v1.PlanError{Code: "export_interrupted", Message: "export interrupted by caller; run was not cancelled"}
}
