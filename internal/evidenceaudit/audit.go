// Package evidenceaudit rejects unsafe portable evidence, including nested archives.
package evidenceaudit

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"regexp"
	"strings"
)

const maxFileBytes = 128 << 20
const maxAuditBytes = 512 << 20
const maxEntries = 10000

type budget struct {
	bytes   int64
	entries int
}

var privateKey = regexp.MustCompile(`-----BEGIN (?:[A-Z0-9 ]* )?PRIVATE KEY-----`)

func check(name string, data []byte, depth int) error {
	return checkBudget(name, data, depth, &budget{})
}

func checkBudget(name string, data []byte, depth int, limit *budget) error {
	limit.bytes += int64(len(data))
	limit.entries++
	if len(data) > maxFileBytes || limit.bytes > maxAuditBytes || limit.entries > maxEntries {
		return fmt.Errorf("evidence audit size or entry bound exceeded")
	}
	lower := strings.ToLower(name)
	for _, part := range strings.Split(lower, "/") {
		if part == ".." || strings.ContainsAny(part, `\:`) || part == ".git" || part == ".pki" || part == "state.db" || part == "key4.db" || part == "cert9.db" || part == "pkcs11.txt" || part == "owner" {
			return fmt.Errorf("private or unsafe evidence path rejected: %s", name)
		}
	}
	for _, suffix := range []string{".tar", ".gz", ".tgz", ".bz2", ".xz", ".7z", ".rar", ".zst"} {
		if strings.HasSuffix(lower, suffix) {
			return fmt.Errorf("unsupported evidence archive: %s", name)
		}
	}
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
			contents, readErr := io.ReadAll(io.LimitReader(reader, maxFileBytes+1))
			closeErr := reader.Close()
			if readErr != nil {
				return fmt.Errorf("read ZIP entry: %w", readErr)
			}
			if closeErr != nil {
				return closeErr
			}
			if err := checkBudget(item.Name, contents, depth+1, limit); err != nil {
				return err
			}
		}
	}
	return nil
}

// Audit rejects links, special files, private keys and unsafe ZIP entries.
func Audit(root string) error {
	return AuditFS(os.DirFS(root))
}

// AuditFS audits a rooted evidence filesystem.
func AuditFS(root fs.FS) error {
	limit := &budget{}
	return fs.WalkDir(root, ".", func(name string, entry fs.DirEntry, err error) error {
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
		if info.Size() > maxFileBytes {
			return fmt.Errorf("evidence file exceeds audit bound: %s", name)
		}
		data, err := fs.ReadFile(root, name)
		if err != nil {
			return err
		}
		return checkBudget(name, data, 0, limit)
	})
}
