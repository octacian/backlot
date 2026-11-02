package client

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	v1 "github.com/octacian/backlot/api/v1"
)

func TestUnsupportedDiscoveryNeverFallsBack(t *testing.T) {
	c := New("unused")
	defer c.Close()
	calls := 0
	message := "unknown v1 endpoint"
	c.http.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.Method != "POST" || r.URL.Path != "/v1/discovery/instances" {
			t.Fatal("fallback or mutation", r.Method, r.URL.Path)
		}
		data, _ := json.Marshal(v1.ErrorResponse{APIVersion: v1.Version, Error: v1.PlanError{Code: "not_found", Message: message}})
		return &http.Response{StatusCode: 404, Header: http.Header{"Content-Type": []string{"application/json"}, "X-Backlot-Api-Version": []string{v1.Version}}, Body: io.NopCloser(strings.NewReader(string(data)))}, nil
	})
	_, err := c.FilteredList(context.Background(), v1.FilteredListRequest{APIVersion: v1.Version, Scene: "test"})
	var detail *v1.PlanError
	if !errors.As(err, &detail) || detail.Code != "unsupported_feature" || calls != 1 {
		t.Fatal(err, calls)
	}
	message = "instance not found"
	_, err = c.FilteredList(context.Background(), v1.FilteredListRequest{APIVersion: v1.Version, Scene: "test"})
	if !errors.As(err, &detail) || detail.Code != "not_found" || calls != 2 {
		t.Fatal("ordinary not_found translated", err, calls)
	}
}

func TestDiscoveryStrictWireValidation(t *testing.T) {
	c := New("unused")
	defer c.Close()
	id := strings.Repeat("a", 64)
	response := `{"api_version":"v1","instance_id":"` + id + `","records":[],"gaps":[],"historical_collection":"known","more":false,"active":false,"unexpected":true}`
	c.http.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		var q v1.LogPageRequest
		if r.URL.Path != "/v1/logs/page" || json.NewDecoder(r.Body).Decode(&q) != nil || q.InstanceID != id || q.Attempt != id || q.Stream != "stderr" {
			t.Fatal("wrong request", q)
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}, "X-Backlot-Api-Version": []string{v1.Version}}, Body: io.NopCloser(strings.NewReader(response))}, nil
	})
	q := v1.LogPageRequest{APIVersion: v1.Version, InstanceID: id, Attempt: id, Stream: "stderr"}
	if _, err := c.LogPage(context.Background(), q); err == nil {
		t.Fatal("unknown response field accepted")
	}
	response = strings.Replace(response, `,"unexpected":true`, "", 1)
	if _, err := c.LogPage(context.Background(), q); err != nil {
		t.Fatal(err)
	}
	response = strings.Replace(response, `"historical_collection":"known"`, `"historical_collection":"complete"`, 1)
	if _, err := c.LogPage(context.Background(), q); err == nil {
		t.Fatal("invented completeness accepted")
	}
}
