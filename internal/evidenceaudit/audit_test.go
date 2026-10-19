package evidenceaudit

import (
	"archive/zip"
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func archive(t *testing.T, name string, data []byte, mode os.FileMode) []byte {
	t.Helper()
	var b bytes.Buffer
	w := zip.NewWriter(&b)
	header := &zip.FileHeader{Name: name, Method: zip.Deflate}
	header.SetMode(mode)
	f, err := w.CreateHeader(header)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func TestAudit(t *testing.T) {
	key := []byte("-----BEGIN PRIVATE KEY-----")
	for name, data := range map[string][]byte{
		"trace.zip":    archive(t, "resources/root.pem", key, 0o600),
		"trace.ZIP":    archive(t, "inner.ZIP", archive(t, "root.pem", key, 0o600), 0o600),
		"bad.ZIP":      []byte("not zip"),
		"link.zip":     archive(t, "link", []byte("target"), os.ModeSymlink|0o600),
		"path.zip":     archive(t, "../root", []byte("x"), 0o600),
		"absolute.zip": archive(t, "/root", []byte("x"), 0o600),
		"windows.zip":  archive(t, `C:\root`, []byte("x"), 0o600),
		"deep.zip":     archive(t, "inner.zip", archive(t, "inner.zip", archive(t, "file", []byte("x"), 0o600), 0o600), 0o600),

		"root.KEY": []byte("x"),
	} {
		t.Run(name, func(t *testing.T) {
			if err := check(name, data, 0); err == nil {
				t.Fatal("accepted unsafe evidence")
			}
		})
	}
	for name, data := range map[string][]byte{"playwright.json": []byte(`{"suites":[],"stats":{}}`), "trace.zip": archive(t, "0-trace.trace", []byte("{\"type\":\"context-options\"}\n"), 0o600)} {
		if err := check(name, data, 0); err != nil {
			t.Fatal(err)
		}
	}
	if err := check("large.trace", []byte(`{"payload":"`+strings.Repeat("x", 200000)+`"}`+"\n"), 0); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	if err := os.Symlink("/etc/passwd", filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	if err := Audit(root); err == nil {
		t.Fatal("accepted filesystem symlink")
	}
}

func directoryArchive(t *testing.T, count int) []byte {
	t.Helper()
	var b bytes.Buffer
	w := zip.NewWriter(&b)
	for i := 0; i < count; i++ {
		if _, err := w.Create(fmt.Sprintf("directory-%d/", i)); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func TestAuditZIPDirectoryBudget(t *testing.T) {
	if err := check("dirs.zip", directoryArchive(t, 10001), 0); err == nil {
		t.Fatal("accepted 10001 ZIP directory headers")
	}
	// The outer file and all nested ZIP headers share one budget.
	if err := check("dirs.zip", directoryArchive(t, maxEntries-1), 0); err != nil {
		t.Fatal(err)
	}
	if err := check("outer.zip", archive(t, "dirs.zip", directoryArchive(t, maxEntries-1), 0600), 0); err == nil {
		t.Fatal("nested archive reset the header budget")
	}
	root := t.TempDir()
	for _, name := range []string{"first.zip", "second.zip"} {
		if err := os.WriteFile(filepath.Join(root, name), directoryArchive(t, maxEntries/2), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := Audit(root); err == nil {
		t.Fatal("sibling archives reset the shared budget")
	}
}
