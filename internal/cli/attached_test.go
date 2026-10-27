package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/octacian/backlot/internal/client"
	"github.com/octacian/backlot/internal/daemon"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	v1 "github.com/octacian/backlot/api/v1"
	urfave "github.com/urfave/cli/v3"
)

func TestAttachedFailureReasonsHumanAndJSON(t *testing.T) {
	result := &v1.ExecutionResult{Attempt: strings.Repeat("a", 64), Failure: "required service exited unexpectedly", CollectionFailure: "application output collection failed", CleanupFailure: "owned route cleanup uncertain"}
	record := v1.AttachedRecord{APIVersion: v1.Version, Type: "failure", Result: &v1.InstanceResponse{APIVersion: v1.Version, Instance: v1.Instance{ID: strings.Repeat("b", 64), Status: v1.Failed, Execution: result}}}
	for _, machine := range []bool{false, true} {
		t.Run(map[bool]string{false: "human", true: "json"}[machine], func(t *testing.T) {
			var out bytes.Buffer
			command := &urfave.Command{Name: "renderer", Writer: &out, ErrWriter: &out, Flags: []urfave.Flag{&urfave.BoolFlag{Name: "json"}}, Action: func(_ context.Context, c *urfave.Command) error { return attachedRecord(c, record) }}
			args := []string{"renderer"}
			if machine {
				args = append(args, "--json")
			}
			if err := command.Run(context.Background(), args); err != nil {
				t.Fatal(err)
			}
			for _, reason := range []string{result.Failure, result.CollectionFailure, result.CleanupFailure} {
				if !strings.Contains(out.String(), reason) {
					t.Fatal("failure reason omitted", reason, out.String())
				}
			}
			if machine {
				decoder := json.NewDecoder(&out)
				var decoded v1.AttachedRecord
				if err := decoder.Decode(&decoded); err != nil || decoded.Type != "failure" || decoded.Result == nil || decoded.Result.Attachment != nil || decoded.Result.LeaseToken != "" {
					t.Fatal("invalid/private JSON frame", err)
				}
				if err := decoder.Decode(&decoded); err != io.EOF {
					t.Fatal("extra JSON output", err)
				}
			}
		})
	}
}

func TestAttachedFailedCleanupRetainsSnapshotWithoutRetry(t *testing.T) {
	root, err := os.MkdirTemp("/tmp", "bl-attached-error-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	listener, err := net.Listen("unix", daemon.SocketPath(root))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(daemon.SocketPath(root), 0600); err != nil {
		t.Fatal(err)
	}
	var stops atomic.Int32
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/attached/cancel" {
			t.Error("unexpected operation", r.URL.Path)
		}
		stops.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Backlot-API-Version", v1.Version)
		w.WriteHeader(503)
		_ = json.NewEncoder(w).Encode(v1.ErrorResponse{APIVersion: v1.Version, Error: v1.PlanError{Code: "cleanup_uncertain", Message: "cleanup result unknown"}})
	})}
	done := make(chan struct{})
	go func() { defer close(done); _ = server.Serve(listener) }()
	t.Cleanup(func() { _ = server.Close(); <-done })
	api := client.New(daemon.SocketPath(root))
	t.Cleanup(api.Close)
	id, attempt := strings.Repeat("a", 64), strings.Repeat("b", 64)
	accepted := v1.InstanceResponse{APIVersion: v1.Version, Instance: v1.Instance{ID: id, Status: v1.Starting, Execution: &v1.ExecutionResult{Attempt: attempt}}, Attachment: &v1.Attachment{Owner: v1.AttachedOwner{Attempt: attempt, Token: "attachment-private-canary"}, LeaseDuration: "30s"}}
	var out, diag bytes.Buffer
	command := &urfave.Command{Writer: &out, ErrWriter: &diag, Flags: []urfave.Flag{&urfave.BoolFlag{Name: "json"}}, Action: func(ctx context.Context, c *urfave.Command) error {
		ctx, cancel := context.WithCancel(ctx)
		defer cancel()
		return runAttached(ctx, c, api, "0s", func(context.Context) (v1.InstanceResponse, error) { cancel(); return accepted, nil })
	}}
	err = command.Run(context.Background(), []string{"attached", "--json"})
	var record v1.AttachedRecord
	if err == nil || stops.Load() != 1 || json.Unmarshal(out.Bytes(), &record) != nil || record.Type != "failure" || record.Error == nil || record.Error.Code != "cleanup_uncertain" || record.Result == nil || record.Result.Instance.ID != id || record.Result.Instance.Execution.Attempt != attempt || record.Result.Attachment != nil || strings.Contains(out.String(), "attachment-private-canary") {
		t.Fatal("lost result, authority leak or mutation retry", err, stops.Load(), out.String())
	}
}
