package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"

	apiv1 "github.com/octacian/backlot/api/v1"
)

func TestVersionJSONUsesSharedContract(t *testing.T) {
	var stdout, stderr bytes.Buffer
	want := apiv1.VersionResponse{Version: "0.1.0", APIVersion: apiv1.Version, Commit: "abc123"}
	command := NewCommand(want, &stdout, &stderr)
	if err := command.Run(context.Background(), []string{"backlot", "version", "--json"}); err != nil {
		t.Fatal(err)
	}
	var got apiv1.VersionResponse
	decoder := json.NewDecoder(&stdout)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got != want || stderr.Len() != 0 {
		t.Fatalf("response=%+v, stderr=%q", got, stderr.String())
	}
	if err := decoder.Decode(&got); !errors.Is(err, io.EOF) {
		t.Fatalf("expected one JSON result, got %v", err)
	}
}

func TestRejectsUnsupportedCommandsAndArguments(t *testing.T) {
	for _, args := range [][]string{
		{"backlot", "run", "preview"},
		{"backlot", "version", "extra"},
		{"backlot", "version", "--unsupported"},
	} {
		t.Run(strings.Join(args[1:], "/"), func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if err := NewCommand(apiv1.VersionResponse{}, &stdout, &stderr).Run(context.Background(), args); err == nil {
				t.Fatal("invalid invocation reported success")
			}
		})
	}
}

func TestOutputFailureIsReturned(t *testing.T) {
	command := NewCommand(apiv1.VersionResponse{}, failingWriter{}, io.Discard)
	if err := command.Run(context.Background(), []string{"backlot", "version", "--json"}); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("expected output error, got %v", err)
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }
