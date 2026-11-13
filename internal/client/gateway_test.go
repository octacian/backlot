package client

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	v1 "github.com/octacian/backlot/api/v1"
)

func TestGatewayCARejectsUnvalidatedWireSnapshot(t *testing.T) {
	c := New("/unused/socket")
	defer c.Close()
	c.http.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/gateway/ca" {
			t.Fatal(r.Method, r.URL.Path)
		}
		var request v1.GatewayCARequest
		if json.NewDecoder(r.Body).Decode(&request) != nil || request.APIVersion != v1.Version {
			t.Fatal(request)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"api_version":"v1","provenance":"name-only"}`)), Header: make(http.Header)}, nil
	})
	if _, err := c.GatewayCA(context.Background()); err == nil {
		t.Fatal("unvalidated snapshot accepted")
	}
}
