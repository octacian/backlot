package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	v1 "github.com/octacian/backlot/api/v1"
	"github.com/octacian/backlot/internal/client"
	urfave "github.com/urfave/cli/v3"
)

func TestStartupSummaryAuthorizationAndFailure(t *testing.T) {
	root, err := os.MkdirTemp("/tmp", "bl-summary-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	id, attempt := strings.Repeat("a", 64), strings.Repeat("b", 64)
	for _, mode := range []string{"success", "unavailable", "replaced", "partial", "missing", "disposable", "disabled"} {
		t.Run(mode, func(t *testing.T) {
			for _, asJSON := range []bool{false, true} {
				var calls atomic.Int32
				socket := filepath.Join(root, "s")
				listener, err := net.Listen("unix", socket)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.Chmod(socket, 0600); err != nil {
					t.Fatal(err)
				}
				server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					call := calls.Add(1)
					var request v1.FixturesRequest
					if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
						t.Error(err)
					}
					if r.URL.Path != "/v1/fixtures/secret" || request.InstanceID != id || request.ExpectedAttempt != attempt || request.Component != "seed" {
						t.Error("retrieval authority/attempt changed")
					}
					w.Header().Set("Content-Type", "application/json")
					w.Header().Set("X-Backlot-API-Version", v1.Version)
					if mode == "unavailable" || mode == "partial" && call == 2 {
						w.WriteHeader(http.StatusConflict)
						_ = json.NewEncoder(w).Encode(v1.ErrorResponse{APIVersion: v1.Version, Error: v1.PlanError{Code: "fixture_unavailable", Message: "raw-private-error-canary"}})
						return
					}
					response := v1.FixturesResponse{APIVersion: v1.Version, InstanceID: id, Attempt: attempt, Fixtures: []v1.FixtureRecord{{Component: "seed", Name: request.SecretName, Sensitive: true, Value: "selected-private-canary"}}}
					if mode == "replaced" {
						response.Attempt = strings.Repeat("c", 64)
					}
					_ = json.NewEncoder(w).Encode(response)
				})}
				done := make(chan struct{})
				t.Cleanup(func() { _ = server.Close(); <-done })
				go func() { defer close(done); _ = server.Serve(listener) }()
				cli := client.New(socket)
				t.Cleanup(cli.Close)
				selection := []v1.StartupDetailSelection{{Component: "seed", Fixture: "email", Label: "Email"}, {Component: "seed", Fixture: "password", Label: "Password"}}
				fixtures := []v1.FixtureRecord{{Component: "seed", Name: "email", Value: "demo@example.invalid"}, {Component: "seed", Name: "password", Sensitive: true}, {Component: "seed", Name: "unselected", Sensitive: true}}
				if mode == "partial" {
					selection = append(selection, v1.StartupDetailSelection{Component: "seed", Fixture: "unselected", Label: "Second secret"})
				}
				if mode == "missing" {
					fixtures = fixtures[:1]
				}
				lifetime := v1.Persistent
				if mode == "disposable" {
					lifetime = v1.Disposable
				}
				if mode == "disabled" {
					selection = nil
				}
				response := v1.InstanceResponse{APIVersion: v1.Version, Instance: v1.Instance{ID: id, Status: v1.RuntimeReady, Operation: v1.Operation{ID: id}, Plan: v1.PlanResponse{Lifetime: lifetime, StartupDetails: selection}, Execution: &v1.ExecutionResult{Attempt: attempt, Fixtures: fixtures}}}
				var out, stderr bytes.Buffer
				c := &urfave.Command{Name: "startup", Writer: &out, ErrWriter: &stderr, Flags: []urfave.Flag{&urfave.BoolFlag{Name: "json"}}, Action: func(ctx context.Context, c *urfave.Command) error { return startupOutput(ctx, c, cli, response) }}
				args := []string{"startup"}
				if asJSON {
					args = append(args, "--json")
				}
				if err := c.Run(context.Background(), args); err != nil {
					t.Fatal("summary failure changed startup success", err)
				}
				cli.Close()
				if err := server.Close(); err != nil {
					t.Fatal(err)
				}
				<-done
				visible := out.String() + stderr.String()
				if strings.Contains(visible, "raw-private-error-canary") {
					t.Fatal("warning leaked raw error")
				}
				wantSecret := mode == "success"
				if strings.Contains(out.String(), "selected-private-canary") != wantSecret {
					t.Fatal("incorrect selected/partial disclosure", mode)
				}
				if mode == "disabled" || mode == "disposable" {
					if calls.Load() != 0 {
						t.Fatal("unused summary retrieved secrets")
					}
				}
				failed := mode == "unavailable" || mode == "replaced" || mode == "partial" || mode == "missing"
				if asJSON {
					var result v1.InstanceResponse
					if err := json.Unmarshal(out.Bytes(), &result); err != nil {
						t.Fatal("not one JSON document", err)
					}
					if result.Instance.ID != id {
						t.Fatal("startup identity lost")
					}
					if failed && (result.StartupDetails == nil || result.StartupDetails.Warning == nil || len(result.StartupDetails.Details) != 0) {
						t.Fatal("missing typed all-or-unavailable warning")
					}
				} else if failed && !strings.Contains(stderr.String(), "Startup details unavailable") {
					t.Fatal("missing human warning")
				}
			}
		})
	}
}
