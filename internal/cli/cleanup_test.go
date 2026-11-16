//go:build darwin || linux

package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"

	v1 "github.com/octacian/backlot/api/v1"
)

func TestCleanupConfirmationDefaultsToNo(t *testing.T) {
	for _, input := range []string{"y\n", "Y\n", "n\n", "\n", "yes\n", "y", ""} {
		t.Run(strings.ReplaceAll(input, "\n", "newline"), func(t *testing.T) {
			r, w, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = r.Close() }()
			_, err = io.WriteString(w, input)
			if err != nil {
				t.Fatal(err)
			}
			_ = w.Close()
			var output bytes.Buffer
			yes, err := confirmCleanup(context.Background(), r, &output)
			if err != nil || yes != (input == "y\n" || input == "Y\n") || !strings.Contains(output.String(), "[y/N]") {
				t.Fatal(yes, err, output.String())
			}
		})
	}
}
func TestCleanupCLIRejectsAmbiguousConsent(t *testing.T) {
	for _, args := range [][]string{{"checkout", "destroy", "--checkout", "/tmp", "--checkout-id", strings.Repeat("a", 64)}, {"checkout", "destroy", "--apply-preview", "yes"}, {"orphan", "cleanup", "--force"}, {"orphan", "cleanup", "unexpected"}} {
		var out, diagnostic bytes.Buffer
		c := NewCommand(v1.VersionResponse{APIVersion: v1.Version}, &out, &diagnostic)
		if err := c.Run(context.Background(), append([]string{"backlot", "--json"}, args...)); err == nil {
			t.Fatal("invalid cleanup accepted", args)
		}
		var envelope v1.ErrorResponse
		if json.Unmarshal(out.Bytes(), &envelope) != nil || envelope.Error.Code != "invalid_arguments" {
			t.Fatal("invalid arguments lost typed framing", out.String())
		}
	}
}
