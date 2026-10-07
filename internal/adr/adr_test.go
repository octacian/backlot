package adr

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"go.yaml.in/yaml/v3"
)

var fixtureTime = time.Date(2026, 9, 15, 18, 12, 34, 567000000, time.UTC)

func fixtureDirectory(t *testing.T) string {
	t.Helper()
	directory := t.TempDir()
	template, err := os.ReadFile("../../docs/adr/template.md")
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(directory, "template.md"), string(template))
	return directory
}

func writeFile(t *testing.T, file, content string) {
	t.Helper()
	if err := os.WriteFile(file, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, file string) string {
	t.Helper()
	content, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	return string(content)
}

func record(t *testing.T, directory, title string, now time.Time) string {
	t.Helper()
	file, err := Create(directory, title, now)
	if err != nil {
		t.Fatal(err)
	}
	content := strings.ReplaceAll(readFile(t, file), "TODO:", "Example:")
	writeFile(t, file, strings.ReplaceAll(content, "tags: []", "tags: [architecture]"))
	return file
}

func verifyErrors(t *testing.T, directory string) string {
	t.Helper()
	result, err := Verify(directory)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Join(result.Errors, "\n")
}

func requireError(t *testing.T, directory, expected string) {
	t.Helper()
	if errors := verifyErrors(t, directory); !strings.Contains(errors, expected) {
		t.Fatalf("expected %q in validation errors:\n%s", expected, errors)
	}
}

func TestCreatePreservesTitlesAndConcurrentCollisions(t *testing.T) {
	directory := fixtureDirectory(t)
	title := `Use "ADR": café / decisions`
	const count = 8
	files, failures := make(chan string, count), make(chan error, count)
	var workers sync.WaitGroup
	for range count {
		workers.Go(func() {
			file, err := Create(directory, title, fixtureTime)
			files <- file
			failures <- err
		})
	}
	workers.Wait()
	close(files)
	close(failures)
	for err := range failures {
		if err != nil {
			t.Fatal(err)
		}
	}
	seen := make(map[string]bool)
	for file := range files {
		if seen[file] {
			t.Fatalf("duplicate filename %s", file)
		}
		seen[file] = true
		frontmatter, _, ok := splitFrontmatter([]byte(readFile(t, file)))
		if !ok {
			t.Fatal("missing frontmatter")
		}
		metadata, err := decodeMetadata(frontmatter)
		if err != nil || metadata.Title != title || metadata.Status != "proposed" || metadata.Date != "2026-09-15" {
			t.Fatalf("metadata=%+v, error=%v", metadata, err)
		}
	}
	first := filepath.Join(directory, "20260915T181234567Z-use-adr-cafe-decisions.md")
	if !seen[first] {
		t.Fatalf("missing expected timestamped filename %s", first)
	}
	for _, title := range []string{"../", "A\nsecond line", ""} {
		if _, err := Create(directory, title, fixtureTime); err == nil {
			t.Fatalf("accepted invalid title %q", title)
		}
	}
}

func TestCollisionCrossingUTCMidnight(t *testing.T) {
	directory := fixtureDirectory(t)
	now := time.Date(2026, 12, 31, 23, 59, 59, 999000000, time.UTC)
	record(t, directory, "Same title", now)
	next := record(t, directory, "Same title", now)
	if filepath.Base(next) != "20270101T000000000Z-same-title.md" {
		t.Fatalf("unexpected filename %s", next)
	}
	if errors := verifyErrors(t, directory); errors != "" {
		t.Fatal(errors)
	}
}

func TestLiteralMarkdownTitleAndCRLF(t *testing.T) {
	directory := fixtureDirectory(t)
	file := record(t, directory, "Use a trailing #", fixtureTime)
	writeFile(t, file, strings.ReplaceAll(readFile(t, file), "\n", "\r\n"))
	if errors := verifyErrors(t, directory); errors != "" {
		t.Fatal(errors)
	}
}

func TestDraftAndCompletedProposal(t *testing.T) {
	directory := fixtureDirectory(t)
	if errors := verifyErrors(t, directory); errors != "" {
		t.Fatal(errors)
	}
	file, err := Create(directory, "Draft", fixtureTime)
	if err != nil {
		t.Fatal(err)
	}
	requireError(t, directory, "TODO")
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	record(t, directory, "Use durable decisions", fixtureTime)
	result, err := Verify(directory)
	if err != nil || len(result.Errors) != 0 || result.Count != 1 {
		t.Fatalf("result=%+v, error=%v", result, err)
	}
}

func TestMalformedRecords(t *testing.T) {
	cases := []struct {
		name, old, replacement, expected string
	}{
		{"bad status", "status: proposed", "status: pending", "status must"},
		{"bad date", "date: 2026-09-15", "date: 2026-09-16", "date must match"},
		{"missing summary", "summary: 'Example: State the choice and its main reason in one sentence.'", "", "summary must"},
		{"bad tags", "tags: [architecture]", "tags: architecture", "invalid YAML"},
		{"empty tags", "tags: [architecture]", "tags: []", "tags must contain"},
		{"missing list", "supersedes: []", "", "supersedes must"},
		{"duplicate tags", "tags: [architecture]", "tags: [architecture, architecture]", "unique strings"},
		{"bad YAML", "tags: [architecture]", "tags: [", "invalid YAML"},
		{"duplicate key", "status: proposed", "status: proposed\nstatus: accepted", "invalid YAML"},
		{"non-string summary", "summary: 'Example: State the choice and its main reason in one sentence.'", "summary: 123", "summary must be a string"},
		{"custom YAML tag", "tags: [architecture]", "tags: [!custom architecture]", "list of strings"},
		{"unknown field", "tags: [architecture]", "tags: [architecture]\nother: value", "invalid YAML"},
		{"missing section", "## Context", "## Background", "Context"},
		{"fenced fake heading", "## Context", "```markdown\n## Context\n```", "Context"},
		{"comment fake heading", "## Context", "<!--\n## Context\n-->", "Context"},
		{"empty section", "Example: Describe the problem and constraints. Link evidence and label assumptions.", "<!-- empty -->", "Context\" must contain"},
		{"code-only section", "Example: Describe the problem and constraints. Link evidence and label assumptions.", "```text\nOnly an example.\n```", "Context\" must contain"},
		{"duplicate section", "## Context", "## Context\n\nExtra.\n\n## Context", "exactly one"},
		{"missing overview", "Example: State the choice, scope, reason, and practical guidance in one or two short\nparagraphs sufficient for a contributor to act.", "<!-- empty -->", "decision overview"},
		{"title mismatch", "# Use durable decisions", "# Another decision", "heading must match"},
		{"no frontmatter", "---\ntitle:", "title:", "expected YAML frontmatter"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			directory := fixtureDirectory(t)
			file := record(t, directory, "Use durable decisions", fixtureTime)
			content := readFile(t, file)
			if !strings.Contains(content, test.old) {
				t.Fatalf("fixture does not contain replacement target %q", test.old)
			}
			writeFile(t, file, strings.Replace(content, test.old, test.replacement, 1))
			requireError(t, directory, test.expected)
		})
	}
}

func TestInvalidFilenamesAndTimestamps(t *testing.T) {
	directory := fixtureDirectory(t)
	file := record(t, directory, "Valid", fixtureTime)
	content := readFile(t, file)
	writeFile(t, filepath.Join(directory, "20260230T181234567Z-invalid-date.md"), content)
	writeFile(t, filepath.Join(directory, "0001-old-sequence.md"), content)
	requireError(t, directory, "invalid UTC timestamp")
	requireError(t, directory, "expected YYYYMMDD")
}

func updateMetadata(t *testing.T, file string, update func(*Metadata)) {
	t.Helper()
	frontmatter, body, _ := splitFrontmatter([]byte(readFile(t, file)))
	metadata, err := decodeMetadata(frontmatter)
	if err != nil {
		t.Fatal(err)
	}
	update(&metadata)
	encoded, err := yaml.Marshal(metadata)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, file, "---\n"+string(encoded)+"---\n"+string(body))
}

func TestSupersession(t *testing.T) {
	directory := fixtureDirectory(t)
	old := record(t, directory, "Old choice", fixtureTime)
	replacement := record(t, directory, "New choice", fixtureTime)
	updateMetadata(t, old, func(metadata *Metadata) {
		metadata.Status, metadata.SupersededBy = "superseded", []string{"missing.md"}
	})
	requireError(t, directory, "missing or self")
	updateMetadata(t, old, func(metadata *Metadata) { metadata.SupersededBy = []string{filepath.Base(replacement)} })
	requireError(t, directory, "link back")
	updateMetadata(t, replacement, func(metadata *Metadata) { metadata.Supersedes = []string{filepath.Base(old)} })
	requireError(t, directory, "must be accepted")
	updateMetadata(t, replacement, func(metadata *Metadata) { metadata.Status = "accepted" })
	if errors := verifyErrors(t, directory); errors != "" {
		t.Fatal(errors)
	}
	updateMetadata(t, old, func(metadata *Metadata) { metadata.Supersedes = []string{filepath.Base(replacement)} })
	updateMetadata(t, replacement, func(metadata *Metadata) {
		metadata.Status, metadata.SupersededBy = "superseded", []string{filepath.Base(old)}
	})
	requireError(t, directory, "supersession cycle")
}

func TestFilesystemErrorsAreReturned(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing")
	if _, err := Verify(missing); err == nil {
		t.Fatal("missing directory reported success")
	}
	if _, err := Create(missing, "Valid title", fixtureTime); err == nil {
		t.Fatal("missing template reported success")
	}
}
