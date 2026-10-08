package daemon

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	v1 "github.com/octacian/backlot/api/v1"
	"github.com/octacian/backlot/internal/plan"
)

func TestMixedReferenceAddresses(t *testing.T) {
	snapshot := plan.Snapshot{Docker: &v1.DockerConfig{HostAddress: "host.docker.internal"}, Plan: v1.PlanResponse{Publish: &v1.Publication{Resource: "origin"}, Components: []v1.PlannedComponent{{Name: "native", Runtime: v1.Native, Ports: map[string]v1.ServicePort{"http": {Resource: "np"}}}, {Name: "container", Runtime: v1.Container, Ports: map[string]v1.ServicePort{"http": {Resource: "cp", ContainerPort: 8080}}}}}}
	result := &v1.ExecutionResult{Origin: "https://fixture.example.com", Ports: map[string]int{"np": 10001, "cp": 10002}}
	for _, test := range []struct {
		ref     v1.Reference
		runtime v1.Runtime
		want    string
	}{
		{v1.Reference{Kind: "service", Name: "native", Field: "host"}, v1.Container, "host.docker.internal"},
		{v1.Reference{Kind: "service", Name: "native", Field: "port", Port: "http"}, v1.Container, "10001"},
		{v1.Reference{Kind: "service", Name: "container", Field: "host"}, v1.Container, "container"},
		{v1.Reference{Kind: "service", Name: "container", Field: "port", Port: "http"}, v1.Container, "8080"},
		{v1.Reference{Kind: "service", Name: "container", Field: "port", Port: "http"}, v1.Native, "10002"},
		{v1.Reference{Kind: "resource", Name: "origin", Field: "url"}, v1.Container, result.Origin},
		{v1.Reference{Kind: "resource", Name: "origin", Field: "url"}, v1.Native, result.Origin},
	} {
		value, err := resolvedValue(v1.PlannedValue{Symbolic: &test.ref}, "", "", snapshot, result, &execution{}, test.runtime)
		if err != nil || value != test.want {
			t.Fatal(test, value, err)
		}
	}
	snapshot.Docker.HostAddress = ""
	_, err := resolvedValue(v1.PlannedValue{Symbolic: &v1.Reference{Kind: "service", Name: "native", Field: "host"}}, "", "", snapshot, result, &execution{}, v1.Container)
	var detail *v1.PlanError
	if !errors.As(err, &detail) || detail.Code != "missing_host_address" {
		t.Fatal("missing container host address not actionable", err)
	}
	for bind, want := range map[string]string{"192.0.2.10": "192.0.2.10", "0.0.0.0": "127.0.0.1", "::": "::1", "::1": "::1"} {
		snapshot.Docker.PublishAddress = bind
		value, err := resolvedValue(v1.PlannedValue{Symbolic: &v1.Reference{Kind: "service", Name: "container", Field: "host"}}, "", "", snapshot, result, &execution{}, v1.Native)
		if err != nil || value != want {
			t.Fatal("native-to-container bind address", bind, want, value, err)
		}
	}
}

func TestAggregateRequiresPublishedRoute(t *testing.T) {
	for _, owned := range []bool{false, true} {
		t.Run(map[bool]string{false: "unrelated-success", true: "published-success"}[owned], func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if owned {
					w.Header().Set("Backlot-Instance", "127.0.0.1")
				}
				w.WriteHeader(200)
			}))
			defer server.Close()
			err := awaitPublication(context.Background(), server.URL, "50ms", nil)
			if (err == nil) != owned {
				t.Fatal("unrelated fallback satisfied publication readiness", owned, err)
			}
		})
	}
}

func TestAggregateRedirectAndCleanupDiagnostics(t *testing.T) {
	requestURL, _ := url.Parse("https://fixture.example.com/")
	for location, want := range map[string]bool{"/login": true, "https://fixture.example.com/login": true, "http://fixture.example.com/login": false, "https://elsewhere.example.com": false, "https://user@fixture.example.com/login": false, "": false} {
		response := &http.Response{StatusCode: 302, Header: http.Header{"Location": []string{location}}, Request: &http.Request{URL: requestURL}}
		if sameOriginRedirect(response, "https://fixture.example.com") != want {
			t.Fatal(location, want)
		}
	}
	details := cleanupDetails(errors.Join(&v1.PlanError{Code: "gateway_ownership", Message: "preserved"}, &v1.PlanError{Code: "cleanup_failed", Message: "unjoined"}, errors.New("other cleanup failure")))
	if len(details) != 2 || details[0].Code != "gateway_ownership" || details[1].Code != "cleanup_failed" {
		t.Fatal(details)
	}
}
