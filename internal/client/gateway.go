package client

import (
	"context"
	"net/http"

	v1 "github.com/octacian/backlot/api/v1"
)

// Gateway controls the explicitly managed gateway through the daemon. Force stop
// requires ExpectedClaimRevision selected from status; retries retain that value.
func (c *Client) Gateway(ctx context.Context, action string, request v1.GatewayRequest) (v1.GatewayResponse, error) {
	var response v1.GatewayResponse
	switch action {
	case "start", "stop", "restart", "status":
	default:
		return response, &v1.PlanError{Code: "invalid_request", Message: "unknown gateway action"}
	}
	err := c.call(ctx, http.MethodPost, "/v1/gateway/"+action, request, &response)
	return response, err
}

// GatewayCA retrieves and validates public managed CA provenance without effects.
func (c *Client) GatewayCA(ctx context.Context) (v1.GatewayCA, error) {
	var response v1.GatewayCA
	if err := c.call(ctx, http.MethodPost, "/v1/gateway/ca", v1.GatewayCARequest{APIVersion: v1.Version}, &response); err != nil {
		return response, err
	}
	if err := response.Validate(); err != nil {
		return response, err
	}
	if err := response.DaemonEffective.Validate(); err != nil {
		return response, responseError()
	}
	return response, nil
}
