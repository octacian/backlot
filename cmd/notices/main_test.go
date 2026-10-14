package main

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReviewedEmbeddedNoticeRejectsDrift(t *testing.T) {
	dir := t.TempDir()
	source := "package example\n// Copyright example\n// Permission is hereby granted\n// THE SOFTWARE.\nfunc example() {}\n"
	path := filepath.Join(dir, "source.go")
	if err := os.WriteFile(path, []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	m := module{Path: "example", Version: "v1.0.0", Dir: dir}
	s := supplement{Module: m.Path, Version: m.Version, Source: "source.go", SHA256: fmt.Sprintf("%x", sha256.Sum256([]byte(source))), Start: "// Copyright example", End: "// THE SOFTWARE."}
	text, err := embeddedNotice(m, s)
	if err != nil || text != "Copyright example\nPermission is hereby granted\nTHE SOFTWARE." {
		t.Fatalf("complete notice extraction: %q %v", text, err)
	}
	newer := m
	newer.Version = "v1.0.1"
	if _, err := embeddedNotice(newer, s); err == nil || !strings.Contains(err.Error(), "review required") {
		t.Fatal("version drift silently accepted")
	}
	if err := os.WriteFile(path, []byte(source+"// upstream changed\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := embeddedNotice(m, s); err == nil || !strings.Contains(err.Error(), "source changed") {
		t.Fatal("source drift silently accepted")
	}
}
