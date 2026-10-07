package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestMissingTitleIsRejected(t *testing.T) {
	if err := command().Run(context.Background(), []string{"adr", "create"}); err == nil || !strings.Contains(err.Error(), "usage:") {
		t.Fatalf("expected usage error, got %v", err)
	}
}

func TestExplicitDirectory(t *testing.T) {
	var output bytes.Buffer
	cmd := command()
	cmd.Writer = &output
	if err := cmd.Run(context.Background(), []string{"adr", "--dir", t.TempDir(), "verify"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "Verified 0 ADR(s).") {
		t.Fatalf("unexpected output %q", output.String())
	}
}
