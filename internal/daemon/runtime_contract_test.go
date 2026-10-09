package daemon

import (
	"encoding/json"
	v1 "github.com/octacian/backlot/api/v1"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestExecutionBudgetsAndPreflight(t *testing.T) {
	d, err := executionDeadlines(v1.ExecutionOptions{})
	if err != nil || d.startup != 5*time.Minute || d.job != 30*time.Minute || d.grace != 10*time.Second {
		t.Fatal(d, err)
	}
	d, err = executionDeadlines(v1.ExecutionOptions{StartupTimeout: "0s", JobTimeout: "0s", StopGrace: "0s"})
	if err != nil || d.startup != 0 || d.job != 0 || d.grace != 0 {
		t.Fatal(d, err)
	}
	d, err = executionDeadlines(v1.ExecutionOptions{StopGrace: "15s"})
	if err != nil || d.grace != 15*time.Second {
		t.Fatal("finite configured grace rejected", d, err)
	}
	for _, options := range []v1.ExecutionOptions{{StartupTimeout: "-1s"}, {JobTimeout: "-1s"}, {StopGrace: "-1s"}, {JobTimeout: "invalid"}} {
		if _, err := executionDeadlines(options); err == nil {
			t.Fatal("invalid deadline accepted", options)
		}
	}
	for _, plan := range []v1.PlanResponse{{Resources: map[string]v1.Resource{"url": {Kind: "origin"}}}} {
		if err := executablePlan(plan); err == nil {
			t.Fatal("unsupported selected work accepted")
		}
	}
}
func TestStrictNativeWireContracts(t *testing.T) {
	svc := &service{}
	handler := handler(svc, v1.DaemonStatusResponse{}, func() {})
	for _, test := range []struct{ path, body string }{
		{"/v1/run", `{"api_version":"v1","plan":{},"options":{"job_timeout":"-1s"}}`},
		{"/v1/run", `{"api_version":"v1","plan":{},"options":{},"extra":true}`},
		{"/v1/run", `{"api_version":"v1","plan":{},"options":{"job_timeout":null}}`},
		{"/v1/restart", `{"api_version":"v1","plan":{},"options":{}}`},
		{"/v1/reset", `{"api_version":"v1","plan":{},"options":{}}`},
		{"/v1/destroy", `{"api_version":"v1","instance_id":"bad"}`},
		{"/v1/runtime/stop", `{"api_version":"v1","instance_id":"bad"}`},
		{"/v1/logs", `{"api_version":"v1","instance_id":"bad","offset":-1}`},
		{"/v1/logs", `{"api_version":"v2","instance_id":"bad","offset":0}`},
	} {
		t.Run(test.path+test.body, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, test.path, strings.NewReader(test.body))
			request.Header.Set("X-Backlot-API-Version", v1.Version)
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code < 400 {
				t.Fatal("invalid request accepted", response.Body.String())
			}
			var envelope v1.ErrorResponse
			if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil || envelope.APIVersion != v1.Version || envelope.Error.Code == "" {
				t.Fatal(envelope, err)
			}
		})
	}
}
