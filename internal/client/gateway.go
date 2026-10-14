package client

import (
	"context"
	"net/http"

	v1 "github.com/octacian/backlot/api/v1"
)

// Gateway controls the explicitly managed gateway through the daemon.
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
