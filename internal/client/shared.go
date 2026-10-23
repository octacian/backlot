package client

import (
	"context"
	"net/http"

	v1 "github.com/octacian/backlot/api/v1"
)

// Shared controls recorded project-owned servers through the daemon.
func (c *Client) Shared(ctx context.Context, action string, request v1.SharedRequest) (v1.SharedResponse, error) {
	var response v1.SharedResponse
	switch action {
	case "list", "inspect", "start", "stop", "restart", "destroy":
	default:
		return response, &v1.PlanError{Code: "invalid_request", Message: "unknown shared action"}
	}
	if request.APIVersion != v1.Version || action == "list" && request.ServiceID != "" || action != "list" && !validID(request.ServiceID) {
		return response, &v1.PlanError{Code: "invalid_request", Message: "shared operation requires the recorded service ID; list takes no ID"}
	}
	if action == "destroy" {
		return c.DestroyShared(ctx, v1.SharedDestroyRequest{APIVersion: request.APIVersion, ServiceID: request.ServiceID})
	}
	return c.sharedCall(ctx, action, request.ServiceID, request)
}

// DestroyShared removes one recorded server without cascading retained allocations.
func (c *Client) DestroyShared(ctx context.Context, request v1.SharedDestroyRequest) (v1.SharedResponse, error) {
	if request.APIVersion != v1.Version || !validID(request.ServiceID) {
		return v1.SharedResponse{}, &v1.PlanError{Code: "invalid_request", Message: "recorded service_id and compatible API version required"}
	}
	return c.sharedCall(ctx, "destroy", request.ServiceID, request)
}
func (c *Client) sharedCall(ctx context.Context, action, serviceID string, request any) (v1.SharedResponse, error) {
	var response v1.SharedResponse
	err := c.call(ctx, http.MethodPost, "/v1/shared/"+action, request, &response)
	if err == nil {
		if response.APIVersion != v1.Version || response.Services == nil {
			return response, responseError()
		}
		seen := map[string]bool{}
		for _, service := range response.Services {
			if !validID(service.ID) || !validID(service.ProjectID) || !validID(service.Configuration) || service.Name == "" || service.Allocations < 0 || service.PendingReleases < 0 || service.PendingReleases > service.Allocations || seen[service.ID] {
				return response, responseError()
			}
			seen[service.ID] = true
			switch service.Status {
			case "intent", "running", "stopped", "uncertain", "destroying", "destroyed":
			default:
				return response, responseError()
			}
		}
		if action != "list" && (len(response.Services) != 1 || response.Services[0].ID != serviceID) {
			return response, responseError()
		}
	}
	return response, err
}
