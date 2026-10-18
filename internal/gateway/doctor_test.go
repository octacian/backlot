package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	v1 "github.com/octacian/backlot/api/v1"
)

func TestDoctorGatewayOnlyReadsExactScopeAndRoute(t *testing.T) {
	route := json.RawMessage(`{"@id":"recorded-route","handle":[{"handler":"static_response","body":"private-config-marker"}]}`)
	body := `{"apps":{"http":{"servers":{"server":{"routes":[{"handle":[{"@id":"owned","handler":"subroute","routes":[` + string(route) + `]}]}]}}}}}`
	gets := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/config/" {
			t.Errorf("diagnostic attempted mutation %s %s", r.Method, r.URL.Path)
		}
		gets++
		_, _ = w.Write([]byte(body))
	}))
	defer server.Close()
	config := v1.CaddyConfig{Endpoint: server.URL, Scope: "owned"}
	intent := Intent{ID: "recorded-route", Route: route}
	if err := Diagnose(context.Background(), config, &intent); err != nil {
		t.Fatal(err)
	}
	intent.ID = "changed-route"
	if err := Diagnose(context.Background(), config, &intent); err == nil {
		t.Fatal("changed recorded identity accepted")
	}
	config.Scope = "foreign"
	if err := Diagnose(context.Background(), config, nil); err == nil {
		t.Fatal("foreign scope accepted")
	}
	if gets != 3 {
		t.Fatal("unexpected diagnostic requests", gets)
	}
}
