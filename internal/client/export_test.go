package client

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

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

func TestExportOutcomeValidation(t *testing.T) {
	id, attempt := strings.Repeat("a", 64), strings.Repeat("b", 64)
	for _, mode := range []string{"unknown-status", "active-status", "bad-component-status", "empty-component", "duplicate-component", "bad-exit", "bad-component-exit", "success-cancelled", "success-failure", "success-collection", "success-cleanup", "success-exit", "success-stopped-component-exit", "partial-strict", "partial-allowed", "failed-original-exit", "cancelled-original-exit", "failed-publication-original-exit", "unknown-exit"} {
		t.Run(mode, func(t *testing.T) {
			request := v1.ExportRequest{APIVersion: v1.Version, InstanceID: id, Output: "/caller/new"}
			response := v1.ExportResponse{APIVersion: v1.Version, Published: true, Complete: true, Output: request.Output, Report: "report.json", HTML: "index.html", Result: v1.ExportResult{InstanceID: id, Attempt: attempt, Status: v1.Succeeded, Components: []v1.ComponentResult{{Name: "job", Status: "completed"}}}}
			valid := false
			exit := 23
			switch mode {
			case "unknown-status":
				response.Result.Status = "bogus"
			case "active-status":
				response.Result.Status = v1.RuntimeReady
			case "bad-component-status":
				response.Result.Components[0].Status = "bogus"
			case "empty-component":
				response.Result.Components[0].Name = ""
			case "duplicate-component":
				response.Result.Components = append(response.Result.Components, response.Result.Components[0])
			case "bad-exit":
				exit = -999
				response.Result.TerminalExitCode = &exit
			case "bad-component-exit":
				exit = 256
				response.Result.Components[0].ExitCode = &exit
			case "success-cancelled":
				response.Result.Cancelled = true
			case "success-failure":
				response.Result.Failure = "failed"
			case "success-collection":
				response.Result.CollectionFailure = "gap"
			case "success-cleanup":
				response.Result.CleanupFailure = "cleanup failed"
			case "success-exit":
				response.Result.TerminalExitCode = &exit
			case "success-stopped-component-exit":
				// Cleanup can stop a service after a successful terminal job.
				response.Result.Components[0].Status = "stopped"
				response.Result.Components[0].ExitCode = &exit
				valid = true
			case "partial-strict":
				response.Complete = false
			case "partial-allowed":
				response.Complete = false
				request.AllowPartial = true
				valid = true
			case "unknown-exit":
				exit = -1
				response.Result.Status = v1.Interrupted
				response.Result.TerminalExitCode = &exit
				valid = true
			case "failed-original-exit", "cancelled-original-exit", "failed-publication-original-exit":
				response.Result.Status = v1.Failed
				response.Result.TerminalExitCode = &exit
				response.Result.Components[0].ExitCode = &exit
				if mode == "cancelled-original-exit" {
					response.Result.Status = v1.Cancelled
					response.Result.Cancelled = true
				}
				if mode == "failed-publication-original-exit" {
					response.Published = false
					response.Complete = false
					response.Output = ""
					response.Report = ""
					response.HTML = ""
					response.Error = &v1.PlanError{Code: "export_incomplete", Message: "known gap"}
				}
				valid = true
			}
			cli := New("unused")
			defer cli.Close()
			cli.http.Transport = roundTripFunc(func(_ *http.Request) (*http.Response, error) {
				data, err := json.Marshal(response)
				if err != nil {
					t.Fatal(err)
				}
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}, "X-Backlot-Api-Version": {v1.Version}}, Body: io.NopCloser(strings.NewReader(string(data)))}, nil
			})
			actual, err := cli.Export(context.Background(), request)
			if valid && response.Error == nil && err != nil {
				t.Fatal(err)
			}
			if valid && response.Error != nil {
				var detail *v1.PlanError
				if !errors.As(err, &detail) || detail.Code != response.Error.Code || actual.Result.TerminalExitCode == nil || *actual.Result.TerminalExitCode != exit {
					t.Fatalf("lost failed publication outcome: %+v %v", actual, err)
				}
			}
			if !valid {
				if actual.Published || actual.Result.InstanceID != "" || actual.Output != "" {
					t.Fatalf("rejected wire values escaped to caller: %+v", actual)
				}
				var detail *v1.PlanError
				if !errors.As(err, &detail) || detail.Code != "invalid_response" {
					t.Fatalf("accepted invalid outcome: %+v %v", actual, err)
				}
			}
		})
	}
}

func TestExportCallerContextDiagnostics(t *testing.T) {
	for _, deadline := range []bool{false, true} {
		ctx, cancel := context.WithCancel(context.Background())
		if deadline {
			ctx, cancel = context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
		}
		cancel()
		cli := New("unused")
		defer cli.Close()
		cli.http.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) { return nil, r.Context().Err() })
		_, err := cli.Export(ctx, v1.ExportRequest{})
		var detail *v1.PlanError
		if !errors.As(err, &detail) || detail.Code != "export_interrupted" || !strings.Contains(detail.Message, "run was not cancelled") {
			t.Fatal(err)
		}
		if deadline && !strings.Contains(detail.Message, "deadline") {
			t.Fatal(err)
		}
	}
	cli := New("unused")
	defer cli.Close()
	cli.http.Transport = roundTripFunc(func(_ *http.Request) (*http.Response, error) { return nil, errors.New("network unavailable") })
	_, err := cli.Export(context.Background(), v1.ExportRequest{})
	var detail *v1.PlanError
	if !errors.As(err, &detail) || detail.Code != "daemon_unavailable" {
		t.Fatal(err)
	}
}
