// Command audit validates action evidence independently of the selected Backlot runtime.
package main

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
)

var privateKey = regexp.MustCompile(`-----BEGIN (?:[A-Z0-9 ]* )?PRIVATE KEY-----`)

func check(name string, data []byte, depth int) error {
	lower := strings.ToLower(name)
	if privateKey.Match(data) || strings.HasSuffix(lower, ".key") || strings.HasSuffix(lower, ".p12") || strings.HasSuffix(lower, ".pfx") {
		return fmt.Errorf("private key rejected: %s", name)
	}
	if depth > 2 {
		return fmt.Errorf("nested evidence archive rejected")
	}
	if strings.HasSuffix(lower, ".zip") {
		archive, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			return fmt.Errorf("read ZIP %s: %w", name, err)
		}
		for _, item := range archive.File {
			// ZIP paths always use slash; reject Windows paths too rather than exporting
			// names that another artifact consumer could interpret as traversal.
			if path.IsAbs(item.Name) || strings.Contains(item.Name, `\`) || strings.Contains(item.Name, ":") || item.Name == "" {
				return fmt.Errorf("unsafe archive entry: %s", item.Name)
			}
			for _, part := range strings.Split(item.Name, "/") {
				if part == ".." {
					return fmt.Errorf("unsafe archive entry: %s", item.Name)
				}
			}
			if item.Mode()&fs.ModeSymlink != 0 || (!item.FileInfo().IsDir() && !item.Mode().IsRegular()) {
				return fmt.Errorf("unsafe archive entry type: %s", item.Name)
			}
			if item.FileInfo().IsDir() {
				continue
			}
			reader, err := item.Open()
			if err != nil {
				return err
			}
			contents, readErr := io.ReadAll(reader)
			closeErr := reader.Close()
			if readErr != nil {
				return fmt.Errorf("read ZIP entry: %w", readErr)
			}
			if closeErr != nil {
				return closeErr
			}
			if err := check(item.Name, contents, depth+1); err != nil {
				return err
			}
		}
	}
	if strings.HasSuffix(name, ".trace") || strings.HasSuffix(name, ".network") || strings.HasSuffix(name, ".ndjson") {
		lines := bytes.Split(data, []byte("\n"))
		if len(lines[len(lines)-1]) == 0 {
			lines = lines[:len(lines)-1]
		}
		for _, line := range lines {
			if !json.Valid(line) {
				return fmt.Errorf("invalid JSON line: %s", name)
			}
		}
	}
	if strings.HasSuffix(name, "playwright.json") {
		var report struct {
			Suites []json.RawMessage          `json:"suites"`
			Stats  map[string]json.RawMessage `json:"stats"`
		}
		if err := json.Unmarshal(data, &report); err != nil {
			return err
		}
		if report.Suites == nil || report.Stats == nil {
			return fmt.Errorf("invalid Playwright report: %s", name)
		}
	}
	if strings.HasSuffix(name, ".png") && !bytes.HasPrefix(data, []byte("\x89PNG\r\n\x1a\n")) {
		return fmt.Errorf("invalid PNG: %s", name)
	}
	return nil
}

func audit(root string) error {
	return filepath.WalkDir(root, func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("evidence link or special file rejected: %s", name)
		}
		data, err := os.ReadFile(name)
		if err != nil {
			return err
		}
		return check(name, data, 0)
	})
}

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: audit EVIDENCE_DIRECTORY")
		os.Exit(1)
	}
	if err := audit(os.Args[1]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println("Evidence audit passed: formats and no private keys (including ZIP entries)")
}
