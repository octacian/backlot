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

func TestFixtureContractsRejectOrdinarySecretLeakAndMismatchedSelection(t *testing.T) {
	request := v1.FixturesRequest{APIVersion: v1.Version, InstanceID: strings.Repeat("a", 64), Component: "tests", SecretName: "password"}
	record := v1.FixtureRecord{Component: "tests", Name: "password", Sensitive: true, Value: "explicit-secret"}
	response := v1.FixturesResponse{APIVersion: v1.Version, Fixtures: []v1.FixtureRecord{record}}
	if err := validateFixtures(response, request, false); err == nil {
		t.Fatal("ordinary response exposed sensitive value")
	}
	if err := validateFixtures(response, request, true); err != nil {
		t.Fatal(err)
	}
	response.Fixtures[0].Component = "other"
	if err := validateFixtures(response, request, true); err == nil {
		t.Fatal("mismatched component accepted")
	}
	response.Fixtures[0] = record
	response.Fixtures = append(response.Fixtures, record)
	if err := validateFixtures(response, request, true); err == nil {
		t.Fatal("multiple sensitive values accepted")
	}
}

func TestNamedFixtureRequestAndSecretResponse(t *testing.T) {
	for _, secret := range []bool{false, true} {
		cli := New("unused")
		request := v1.FixturesRequest{APIVersion: v1.Version, InstanceID: strings.Repeat("b", 64), Component: "tests"}
		endpoint := "/v1/fixtures"
		if secret {
			request.SecretName = "password"
			endpoint += "/secret"
		}
		cli.http.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
			var decoded v1.FixturesRequest
			if err := json.NewDecoder(r.Body).Decode(&decoded); err != nil {
				t.Fatal(err)
			}
			if decoded != request || r.URL.Path != endpoint {
				t.Fatal("named fixture request changed", decoded)
			}
			fixture := v1.FixtureRecord{Component: "tests", Name: "password", Sensitive: true}
			if secret {
				fixture.Value = "explicit-sensitive-value"
			}
			encoded, err := json.Marshal(v1.FixturesResponse{APIVersion: v1.Version, Fixtures: []v1.FixtureRecord{fixture}})
			if err != nil {
				t.Fatal(err)
			}
			return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}, "X-Backlot-Api-Version": {v1.Version}}, Body: io.NopCloser(strings.NewReader(string(encoded)))}, nil
		})
		var response v1.FixturesResponse
		var err error
		if secret {
			response, err = cli.FixtureSecret(context.Background(), request)
		} else {
			response, err = cli.Fixtures(context.Background(), request)
		}
		cli.Close()
		if err != nil || len(response.Fixtures) != 1 || (response.Fixtures[0].Value != "") != secret {
			t.Fatal(response, err)
		}
	}
}
