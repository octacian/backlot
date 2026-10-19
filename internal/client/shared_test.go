package client

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	v1 "github.com/octacian/backlot/api/v1"
	"github.com/octacian/backlot/internal/manifest"
)

func TestSharedNamedWireContract(t *testing.T) {
	c := New("unused")
	defer c.Close()
	request := v1.SharedRequest{APIVersion: v1.Version, ServiceID: strings.Repeat("a", 64)}
	response := v1.SharedResponse{APIVersion: v1.Version, Services: []v1.SharedStatus{{ID: request.ServiceID, ProjectID: strings.Repeat("b", 64), Name: "sql", Configuration: strings.Repeat("c", 64), Status: "running", Allocations: 2}}}
	c.http.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/shared/inspect" {
			t.Fatal("wrong shared operation")
		}
		data, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		var got v1.SharedRequest
		if err = manifest.Decode(data, ".json", &got); err != nil || got != request {
			t.Fatal("named shared request drift", err)
		}
		data, err = json.Marshal(response)
		if err != nil {
			t.Fatal(err)
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}, "X-Backlot-Api-Version": []string{v1.Version}}, Body: io.NopCloser(strings.NewReader(string(data)))}, nil
	})
	if _, err := c.Shared(context.Background(), "inspect", request); err != nil {
		t.Fatal(err)
	}
	response.Services[0].Status = "guessing"
	if _, err := c.Shared(context.Background(), "inspect", request); err == nil {
		t.Fatal("unvalidated shared state")
	}
	response.Services[0].Status = "running"
	for _, pending := range []int{-1, 3} {
		response.Services[0].PendingReleases = pending
		if _, err := c.Shared(context.Background(), "inspect", request); err == nil {
			t.Fatal("invalid pending release count accepted")
		}
	}
	response.Services[0].PendingReleases = 0
	response.Services[0].ID = strings.Repeat("d", 64)
	if _, err := c.Shared(context.Background(), "inspect", request); err == nil {
		t.Fatal("wrong shared identity accepted")
	}
}
