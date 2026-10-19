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
	err := c.call(ctx, http.MethodPost, "/v1/shared/"+action, request, &response)
	if err == nil {
		if response.APIVersion != v1.Version || response.Services == nil {
			return response, responseError()
		}
		seen := map[string]bool{}
		for _, service := range response.Services {
			if !validID(service.ID) || !validID(service.ProjectID) || !validID(service.Configuration) || service.Name == "" || service.Allocations < 0 || seen[service.ID] {
				return response, responseError()
			}
			seen[service.ID] = true
			switch service.Status {
			case "intent", "running", "stopped", "uncertain", "destroying", "destroyed":
			default:
				return response, responseError()
			}
		}
		if action != "list" && (len(response.Services) != 1 || response.Services[0].ID != request.ServiceID) {
			return response, responseError()
		}
	}
	return response, err
}
