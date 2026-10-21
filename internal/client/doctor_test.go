package client

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	v1 "github.com/octacian/backlot/api/v1"
	"github.com/octacian/backlot/internal/manifest"
)

func TestDoctorNamedContractAndValidation(t *testing.T) {
	c := New("unused")
	defer c.Close()
	request := v1.DoctorRequest{APIVersion: v1.Version, InstanceID: strings.Repeat("a", 64), ContainerProbeImage: "local:diagnostic", ProbePublicCA: true}
	response := v1.DoctorResponse{APIVersion: v1.Version, Healthy: false, Scope: "instance", InstanceID: request.InstanceID, Paths: []v1.DoctorPath{{Name: "launch_config", State: "unrecorded"}}, Checks: []v1.DoctorCheck{{Name: "tls", Status: v1.DoctorFailed, Context: "daemon/host system trust", Message: "configure trust explicitly"}}}
	calls := 0
	c.http.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/v1/status" {
			return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}, "X-Backlot-Api-Version": {v1.Version}}, Body: io.NopCloser(strings.NewReader(fmt.Sprintf(`{"api_version":"v1","state_version":%d,"status":"running","pid":1,"lease_duration":"30s"}`, v1.StateVersion)))}, nil
		}
		calls++
		if r.Method != http.MethodPost || r.URL.Path != "/v1/doctor" {
			t.Fatal("wrong doctor operation")
		}
		data, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		var decoded v1.DoctorRequest
		if err := manifest.Decode(data, ".json", &decoded); err != nil || decoded != request {
			t.Fatal("named request drift", err, decoded)
		}
		data, err = json.Marshal(response)
		if err != nil {
			t.Fatal(err)
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}, "X-Backlot-Api-Version": []string{v1.Version}}, Body: io.NopCloser(strings.NewReader(string(data)))}, nil
	})
	got, err := c.Doctor(context.Background(), request)
	if err != nil || got.Healthy || got.Checks[0].Status != v1.DoctorFailed {
		t.Fatal(err, got)
	}
	response.Checks[0].Status = "unknown"
	if _, err := c.Doctor(context.Background(), request); err == nil {
		t.Fatal("unvalidated response enum")
	}
	request.Scene = "ambiguous"
	before := calls
	if _, err := c.Doctor(context.Background(), request); err == nil || calls != before {
		t.Fatal("invalid request reached transport")
	}
}
