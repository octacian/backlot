package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	v1 "github.com/octacian/backlot/api/v1"
)

// Diagnose reads the configured gateway scope and optional exact recorded route.
// It never mutates configuration or publishes raw configuration in an error.
func Diagnose(ctx context.Context, config v1.CaddyConfig, intent *Intent) error {
	client := &http.Client{Timeout: 3 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	data, _, status, err := request(ctx, client, config.Endpoint, http.MethodGet, "/config/", "", nil)
	if err != nil || status != http.StatusOK {
		return diagnostic("gateway_unavailable", "cannot read configured Caddy API; verify its endpoint and running gateway")
	}
	var root any
	if json.Unmarshal(data, &root) != nil {
		return diagnostic("gateway_config", "Caddy returned invalid configuration")
	}
	var scopes []scope
	findScopes(root, "/config", config.Scope, &scopes)
	if len(scopes) != 1 || scopes[0].object["handler"] != "subroute" {
		return diagnostic("gateway_scope", "configure exactly one owned subroute with an explicit routes array")
	}
	children, ok := scopes[0].object["routes"].([]any)
	if !ok {
		return diagnostic("gateway_scope", "owned subroute requires an explicit routes array")
	}
	if intent != nil {
		var expected any
		if json.Unmarshal(intent.Route, &expected) != nil {
			return diagnostic("gateway_ownership", "recorded publication is invalid; preserve private state")
		}
		expectedObject, ok := expected.(map[string]any)
		if !ok || expectedObject["@id"] != intent.ID {
			return diagnostic("gateway_ownership", "recorded route identity disagrees with its effect; preserve state")
		}
		found := false
		for _, child := range children {
			if equalJSON(child, expected) {
				found = true
			}
		}
		if !found || countID(root, intent.ID) != 1 {
			return diagnostic("gateway_ownership", "recorded publication is missing or changed; preserve gateway and inspect instance state")
		}
	}
	return nil
}
