package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strings"
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
