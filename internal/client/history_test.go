package client

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	v1 "github.com/octacian/backlot/api/v1"
)

func historyResponseClient(t *testing.T, value any) *Client {
	t.Helper()
	c := New("unused")
	t.Cleanup(c.Close)
	c.http.Transport = roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		b, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}, "X-Backlot-Api-Version": []string{v1.Version}}, Body: io.NopCloser(strings.NewReader(string(b)))}, nil
	})
	return c
}

func TestHistoryStrictResponseIdentityAndOrder(t *testing.T) {
	id := strings.Repeat("a", 64)
	attempt := strings.Repeat("b", 64)
	q := v1.HistoryRequest{APIVersion: v1.Version, InstanceID: id, Limit: 1}
	valid := v1.HistoryResponse{APIVersion: v1.Version, InstanceID: id, Attempts: []v1.AttemptSummary{{Attempt: attempt, Sequence: 2, Provenance: "accepted", Status: v1.Stopped}}, Continuation: base64.RawURLEncoding.EncodeToString([]byte(id + "/2"))}
	for _, kind := range []string{"valid", "legacy-destroyed", "identity", "attempt", "sequence", "duplicate", "cursor", "status", "time", "request-bound"} {
		t.Run(kind, func(t *testing.T) {
			b, _ := json.Marshal(valid)
			var r v1.HistoryResponse
			_ = json.Unmarshal(b, &r)
			query := q
			switch kind {
			case "legacy-destroyed":
				r.Attempts[0].Status = v1.Destroyed
				r.Attempts[0].Provenance = "legacy_current"
			case "identity":
				r.InstanceID = attempt
			case "attempt":
				r.Attempts[0].Attempt = "bad"
			case "sequence":
				r.Attempts[0].Sequence = -1
			case "duplicate":
				r.Attempts = append(r.Attempts, r.Attempts[0])
			case "cursor":
				r.Continuation = base64.RawURLEncoding.EncodeToString([]byte(id + "/1"))
			case "status":
				r.Attempts[0].Status = v1.Destroyed
			case "time":
				r.Attempts[0].CompletedAt = "yesterday"
			case "request-bound":
				query.After = r.Continuation
			}
			c := historyResponseClient(t, r)
			_, err := c.History(context.Background(), query)
			if (err == nil) != (kind == "valid" || kind == "legacy-destroyed") {
				t.Fatal(kind, err)
			}
		})
	}
}

func TestAttemptStrictSelectionAndResult(t *testing.T) {
	id := strings.Repeat("a", 64)
	attempt := strings.Repeat("b", 64)
	q := v1.AttemptRequest{APIVersion: v1.Version, InstanceID: id, Attempt: attempt}
	stamp := time.Now().UTC().Format(time.RFC3339Nano)
	valid := v1.AttemptResponse{APIVersion: v1.Version, Attempt: v1.Attempt{InstanceID: id, Sequence: 1, Provenance: "accepted", Status: v1.Stopped, CreatedAt: stamp, Source: v1.ExportSource{Project: "project", Scene: "test", Lifetime: v1.Persistent, CheckoutID: id}, Result: v1.ExecutionResult{Attempt: attempt, Components: []v1.ComponentResult{}, CompletedAt: stamp}}}
	for _, kind := range []string{"valid", "legacy-destroyed", "identity", "attempt", "sequence", "source", "completion", "private-fixture", "artifact", "outcome", "result"} {
		t.Run(kind, func(t *testing.T) {
			b, _ := json.Marshal(valid)
			var r v1.AttemptResponse
			_ = json.Unmarshal(b, &r)
			query := q
			switch kind {
			case "legacy-destroyed":
				r.Attempt.Status = v1.Destroyed
				r.Attempt.Provenance = "legacy_current"
				r.Attempt.Result.Outcome = v1.Stopped
			case "identity":
				r.Attempt.InstanceID = attempt
			case "attempt":
				r.Attempt.Result.Attempt = id
			case "sequence":
				r.Attempt.Sequence = 0
			case "source":
				r.Attempt.Source.CheckoutID = "bad"
			case "completion":
				query.Attempt = ""
				query.LastCompleted = true
				r.Attempt.Result.CompletedAt = ""
			case "private-fixture":
				r.Attempt.Result.Fixtures = []v1.FixtureRecord{{Name: "secret", Value: "do-not-show"}}
			case "artifact":
				r.Attempt.Result.Artifacts = []v1.ArtifactRecord{{Attempt: id, Component: "app", Name: "report"}}
			case "outcome":
				r.Attempt.Result.Outcome = "invented"
			case "result":
				exit := 999
				r.Attempt.Result.TerminalExitCode = &exit
			}
			c := historyResponseClient(t, r)
			_, err := c.Attempt(context.Background(), query)
			if (err == nil) != (kind == "valid" || kind == "legacy-destroyed") {
				t.Fatal(kind, err)
			}
		})
	}
}

func TestHistoryOldDaemonDoesNotFallback(t *testing.T) {
	c := New("unused")
	defer c.Close()
	calls := 0
	c.http.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: 404, Header: http.Header{"Content-Type": []string{"application/json"}, "X-Backlot-Api-Version": []string{v1.Version}}, Body: io.NopCloser(strings.NewReader(`{"api_version":"v1","error":{"code":"not_found","message":"unknown v1 endpoint"}}`))}, nil
	})
	_, err := c.Attempt(context.Background(), v1.AttemptRequest{InstanceID: strings.Repeat("a", 64), Attempt: strings.Repeat("b", 64)})
	detail, ok := err.(*v1.PlanError)
	if !ok || detail.Code != "unsupported_feature" || calls != 1 {
		t.Fatal(err, calls)
	}
}

func TestPruneUnusableReplyPreservesUnknownAcceptance(t *testing.T) {
	id := strings.Repeat("a", 64)
	keep := 0
	c := historyResponseClient(t, v1.HistoryPruneResponse{APIVersion: v1.Version, InstanceID: strings.Repeat("b", 64), Attempts: []v1.PrunedAttempt{}})
	_, err := c.PruneHistory(context.Background(), v1.HistoryPruneRequest{APIVersion: v1.Version, InstanceID: id, Keep: &keep})
	if err == nil || !strings.Contains(err.Error(), "acceptance/result is unknown") || !strings.Contains(err.Error(), id) {
		t.Fatal("unusable prune reply implied rejection", err)
	}
}

func TestDestroyDoesNotRewriteUnsupportedContract(t *testing.T) {
	c := New("unused")
	defer c.Close()
	calls := 0
	c.http.Transport = roundTripFunc(func(_ *http.Request) (*http.Response, error) { calls++; return nil, io.EOF })
	_, err := c.DestroyWithRequest(context.Background(), v1.DestroyRequest{APIVersion: v1.Version, InstanceID: strings.Repeat("a", 64), Contract: "unsupported"})
	if err == nil || calls != 0 {
		t.Fatal("unsupported erasure contract reached daemon", err, calls)
	}
}
