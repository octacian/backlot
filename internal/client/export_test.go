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

func TestExportNamedContractAndPublicationValidation(t *testing.T) {
	id := strings.Repeat("a", 64)
	attempt := strings.Repeat("b", 64)
	request := v1.ExportRequest{APIVersion: v1.Version, InstanceID: id, Output: "/caller/new", Wait: true, WaitTimeout: "45s"}
	baseline := v1.ExportResponse{APIVersion: v1.Version, Published: true, Complete: true, Output: request.Output, Report: "report.json", HTML: "index.html", Result: v1.ExportResult{InstanceID: id, Attempt: attempt, Status: v1.Failed, Components: []v1.ComponentResult{}}}
	for _, mode := range []string{"published", "failed-with-result", "wrong-id", "wrong-attempt", "relative-output", "contradictory-error", "unpublished-no-error", "unpublished-path"} {
		t.Run(mode, func(t *testing.T) {
			response := baseline
			switch mode {
			case "failed-with-result":
				response.Published = false
				response.Complete = false
				response.Output = ""
				response.Report = ""
				response.HTML = ""
				response.Error = &v1.PlanError{Code: "export_incomplete", Message: "known evidence gap"}
			case "wrong-id":
				response.Result.InstanceID = attempt
			case "wrong-attempt":
				response.Result.Attempt = ""
			case "relative-output":
				response.Output = "relative"
			case "contradictory-error":
				response.Error = &v1.PlanError{Code: "error", Message: "error"}
			case "unpublished-no-error":
				response.Published = false
			case "unpublished-path":
				response.Published = false
				response.Complete = false
				response.Error = &v1.PlanError{Code: "error", Message: "error"}
			}
			cli := New("unused")
			defer cli.Close()
			cli.http.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
				var decoded v1.ExportRequest
				if err := json.NewDecoder(r.Body).Decode(&decoded); err != nil {
					t.Fatal(err)
				}
				if decoded != request || r.URL.Path != "/v1/export" {
					t.Fatal("named request drift", decoded)
				}
				data, err := json.Marshal(response)
				if err != nil {
					t.Fatal(err)
				}
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}, "X-Backlot-Api-Version": {v1.Version}}, Body: io.NopCloser(strings.NewReader(string(data)))}, nil
			})
			actual, err := cli.Export(context.Background(), request)
			if mode == "published" {
				if err != nil || !actual.Published {
					t.Fatal(actual, err)
				}
			} else if err == nil {
				t.Fatal("accepted failed or malformed publication")
			}
			if mode == "failed-with-result" && actual.Result.Attempt != attempt {
				t.Fatal("failed publication lost observed result")
			}
		})
	}
}
