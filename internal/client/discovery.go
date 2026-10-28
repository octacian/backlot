package client

import (
	"context"
	"errors"
	"net/http"

	v1 "github.com/octacian/backlot/api/v1"
)

func (c *Client) discoveryCall(ctx context.Context, path string, request, response any) error {
	err := c.call(ctx, http.MethodPost, path, request, response)
	var detail *v1.PlanError
	if errors.As(err, &detail) && detail.Code == "not_found" && detail.Message == "unknown v1 endpoint" {
		return &v1.PlanError{Code: "unsupported_feature", Message: "daemon does not support this read contract; stop it with its matching binary and start a compatible daemon"}
	}
	return err
}

// FilteredList discovers one bounded live page using recorded identity selectors.
func (c *Client) FilteredList(ctx context.Context, request v1.FilteredListRequest) (v1.ListResponse, error) {
	var response v1.ListResponse
	if err := request.Validate(); err != nil {
		return response, err
	}
	err := c.discoveryCall(ctx, "/v1/discovery/instances", request, &response)
	if err == nil {
		limit := request.Limit
		if limit == 0 {
			limit = 100
		}
		if len(response.Instances) > limit {
			return response, responseError()
		}
		for _, i := range response.Instances {
			if i.ID <= request.AfterID || request.ProjectID != "" && i.Project != request.ProjectID || request.Scene != "" && i.Scene != request.Scene || request.Lifetime != "" && i.Lifetime != request.Lifetime || request.State != "" && i.Status != request.State {
				return response, responseError()
			}
		}
	}
	return response, err
}

// Configuration reports active daemon policy, independently of caller configuration.
func (c *Client) Configuration(ctx context.Context) (v1.DaemonConfigurationResponse, error) {
	var r v1.DaemonConfigurationResponse
	err := c.discoveryCall(ctx, "/v1/discovery/configuration", v1.ControlRequest{APIVersion: v1.Version}, &r)
	if err == nil {
		err = r.Validate()
	}
	return r, err
}

// Contracts reports daemon capabilities without attributing the caller build to it.
func (c *Client) Contracts(ctx context.Context) (v1.ContractsResponse, error) {
	var r v1.ContractsResponse
	err := c.discoveryCall(ctx, "/v1/discovery/contracts", v1.ControlRequest{APIVersion: v1.Version}, &r)
	if err == nil {
		err = r.ValidateDaemon()
	}
	return r, err
}

// LogPage reads one finite bounded scan of retained evidence.
func (c *Client) LogPage(ctx context.Context, request v1.LogPageRequest) (v1.LogPageResponse, error) {
	var r v1.LogPageResponse
	if err := request.Validate(); err != nil {
		return r, err
	}
	err := c.discoveryCall(ctx, "/v1/logs/page", request, &r)
	if err == nil {
		err = r.Validate(request)
	}
	return r, err
}
