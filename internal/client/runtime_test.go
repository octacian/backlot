package client

import (
	"context"
	"encoding/json"
	v1 "github.com/octacian/backlot/api/v1"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestNativeNamedLogContract(t *testing.T) {
	id := strings.Repeat("a", 64)
	request := v1.LogsRequest{APIVersion: v1.Version, InstanceID: id, Component: "server", Offset: 7}
	cli := New("unused")
	defer cli.Close()
	cli.http.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		var decoded v1.LogsRequest
		if err := json.NewDecoder(r.Body).Decode(&decoded); err != nil {
			t.Fatal(err)
		}
		if decoded != request || r.URL.Path != "/v1/logs" {
			t.Fatal("named request changed", decoded)
		}
		response := v1.LogsResponse{APIVersion: v1.Version, NextOffset: 8, Records: []v1.LogRecord{{InstanceID: id, Component: "server", Attempt: id, Stream: "stderr", Time: "2026-10-08T00:00:00Z", Data: "/wA="}}}
		data, err := json.Marshal(response)
		if err != nil {
			t.Fatal(err)
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}, "X-Backlot-Api-Version": {v1.Version}}, Body: io.NopCloser(strings.NewReader(string(data)))}, nil
	})
	response, err := cli.Logs(context.Background(), request)
	if err != nil || response.NextOffset != 8 || len(response.Records) != 1 || response.Records[0].Data != "/wA=" {
		t.Fatal(response, err)
	}
}
