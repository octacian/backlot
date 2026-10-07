// Package adr creates and validates frontmatter-based architecture decisions.
package adr

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/text"
	"go.yaml.in/yaml/v3"
	"golang.org/x/text/unicode/norm"
)

const timestampLayout = "20060102T150405.000Z"

var (
	filenamePattern = regexp.MustCompile(`^(\d{8}T\d{9}Z)-[a-z0-9]+(?:-[a-z0-9]+)*\.md$`)
	unfinished      = regexp.MustCompile(`(?i)\bTODO\b|\{\{(?:TITLE|TITLE_YAML|DATE)\}\}`)
	comments        = regexp.MustCompile(`(?s)<!--[\s\S]*?-->`)
	sections        = []string{"Context", "Decision", "Consequences", "Alternatives considered", "Revisit when", "References"}
	statuses        = []string{"proposed", "accepted", "rejected", "deprecated", "superseded"}
)

// Metadata is the searchable frontmatter and lifecycle of an architecture decision.
type Metadata struct {
	Title        string   `yaml:"title"`
	Date         string   `yaml:"date"`
	Status       string   `yaml:"status"`
	Summary      string   `yaml:"summary"`
	Tags         []string `yaml:"tags"`
	Supersedes   []string `yaml:"supersedes"`
	SupersededBy []string `yaml:"superseded_by"`
}

// Result contains a deterministic inventory and file-specific validation errors.
// Filesystem failures are returned separately by Verify.
type Result struct {
	Count  int
	Errors []string
}

// Create writes a proposed record from directory/template.md without overwriting
// an existing file. Timestamp collisions advance one millisecond, including dates.
func Create(directory, title string, now time.Time) (string, error) {
	title = strings.TrimSpace(title)
	if title == "" || strings.ContainsAny(title, "\r\n") {
		return "", errors.New("provide a nonempty, single-line decision title")
	}
	slug := slugify(title)
	if slug == "" {
		return "", errors.New("title must contain letters or numbers usable in a filename")
	}
	template, err := os.ReadFile(filepath.Join(directory, "template.md"))
	if err != nil {
		return "", fmt.Errorf("read ADR template: %w", err)
	}
	quotedTitle, err := json.Marshal(title)
	if err != nil {
		return "", fmt.Errorf("encode title: %w", err)
	}
	for attempt := range 1000 {
		stamp := now.UTC().Truncate(time.Millisecond).Add(time.Duration(attempt) * time.Millisecond)
		file := filepath.Join(directory, compactTimestamp(stamp)+"-"+slug+".md")
		content := strings.NewReplacer(
			"'{{TITLE_YAML}}'", string(quotedTitle),
			"'{{DATE}}'", stamp.Format("2006-01-02"),
			"{{TITLE}}", title,
		).Replace(string(template))
		output, err := os.OpenFile(file, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if errors.Is(err, os.ErrExist) {
			continue
		}
		if err != nil {
			return "", fmt.Errorf("create ADR: %w", err)
		}
		_, writeErr := io.WriteString(output, content)
		if err := errors.Join(writeErr, output.Close()); err != nil {
			return "", fmt.Errorf("write ADR %s: %w", file, err)
		}
		return file, nil
	}
	return "", errors.New("unable to allocate an unused timestamp; try again")
}

// Verify checks every record directly in directory, including reciprocal links
// and cycles. README.md and template.md are not decision records.
func Verify(directory string) (Result, error) {
	result := Result{}
	entries, err := os.ReadDir(directory)
	if err != nil {
		return result, fmt.Errorf("read ADR directory: %w", err)
	}
	records := make(map[string]Metadata)
	for _, entry := range entries {
		name := entry.Name()
		if name == "README.md" || name == "template.md" {
			continue
		}
		fail := func(message string) { result.Errors = append(result.Errors, name+": "+message) }
		match := filenamePattern.FindStringSubmatch(name)
		if !entry.Type().IsRegular() || match == nil {
			fail("expected YYYYMMDDTHHmmssSSSZ-short-title.md directly in docs/adr")
			continue
		}
		stamp, stampErr := time.Parse(timestampLayout, match[1][:15]+"."+match[1][15:])
		if stampErr != nil || compactTimestamp(stamp) != match[1] {
			fail("filename contains an invalid UTC timestamp")
		}
		content, err := os.ReadFile(filepath.Join(directory, name))
		if err != nil {
			return result, fmt.Errorf("read ADR %s: %w", name, err)
		}
		content = bytes.ReplaceAll(content, []byte("\r\n"), []byte("\n"))
		if unfinished.Match(content) {
			fail("complete all TODO prompts and template placeholders")
		}
		frontmatter, body, ok := splitFrontmatter(content)
		if !ok {
			fail("expected YAML frontmatter at the start of the file")
			continue
		}
		metadata, err := decodeMetadata(frontmatter)
		if err != nil {
			fail("invalid YAML metadata: " + err.Error())
			continue
		}
		validateMetadata(metadata, stamp, fail)
		validateBody(body, metadata.Title, fail)
		records[name] = metadata
	}
	result.Count = len(records)
	validateLinks(records, &result)
	slices.Sort(result.Errors)
	return result, nil
}

func splitFrontmatter(content []byte) ([]byte, []byte, bool) {
	if !bytes.HasPrefix(content, []byte("---\n")) {
		return nil, nil, false
	}
	metadata, body, found := bytes.Cut(content[4:], []byte("\n---\n"))
	return metadata, body, found
}

func decodeMetadata(source []byte) (Metadata, error) {
	var metadata Metadata
	decoder := yaml.NewDecoder(bytes.NewReader(source))
	decoder.KnownFields(true)
	if err := decoder.Decode(&metadata); err != nil {
		return metadata, err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return metadata, errors.New("expected exactly one YAML mapping")
	}
	// Typed YAML decoding accepts some scalar coercions. Check source tags so
	// malformed metadata is rejected rather than silently becoming valid strings.
	var document yaml.Node
	if err := yaml.Unmarshal(source, &document); err != nil {
		return metadata, err
	}
	if len(document.Content) != 1 || document.Content[0].Kind != yaml.MappingNode {
		return metadata, errors.New("frontmatter must be a mapping")
	}
	fields := document.Content[0].Content
	for index := 0; index < len(fields); index += 2 {
		key, value := fields[index].Value, fields[index+1]
		if slices.Contains([]string{"tags", "supersedes", "superseded_by"}, key) {
			if value.Kind != yaml.SequenceNode || value.Tag != "!!seq" {
				return metadata, fmt.Errorf("%s must be a YAML list of strings", key)
			}
			for _, item := range value.Content {
				if item.Kind != yaml.ScalarNode || item.Tag != "!!str" {
					return metadata, fmt.Errorf("%s must be a YAML list of strings", key)
				}
			}
		} else {
			validTag := value.Tag == "!!str" || (key == "date" && value.Tag == "!!timestamp")
			if value.Kind != yaml.ScalarNode || !validTag {
				return metadata, fmt.Errorf("%s must be a string", key)
			}
		}
	}
	return metadata, nil
}

func validateMetadata(metadata Metadata, stamp time.Time, fail func(string)) {
	for _, field := range []struct{ name, value string }{
		{"title", metadata.Title}, {"date", metadata.Date}, {"status", metadata.Status}, {"summary", metadata.Summary},
	} {
		if strings.TrimSpace(field.value) == "" || strings.ContainsAny(field.value, "\r\n") {
			fail(field.name + " must be a nonempty single-line string")
		}
	}
	if !slices.Contains(statuses, metadata.Status) {
		fail("status must be one of: " + strings.Join(statuses, ", "))
	}
	if metadata.Date != stamp.Format("2006-01-02") {
		fail("date must match the filename date (YYYY-MM-DD)")
	}
	for _, field := range []struct {
		name   string
		values []string
	}{{"tags", metadata.Tags}, {"supersedes", metadata.Supersedes}, {"superseded_by", metadata.SupersededBy}} {
		if field.values == nil {
			fail(field.name + " must be a YAML list of strings")
		}
		seen := make(map[string]bool)
		for _, value := range field.values {
			if strings.TrimSpace(value) == "" || seen[value] {
				fail(field.name + " must contain nonempty, unique strings")
			}
			seen[value] = true
		}
	}
	if len(metadata.Tags) == 0 {
		fail("tags must contain at least one searchable area")
	}
	if (metadata.Status == "superseded") != (len(metadata.SupersededBy) > 0) {
		fail("only superseded records must have a nonempty superseded_by list")
	}
}

func validateBody(source []byte, title string, fail func(string)) {
	// Parse Markdown using an established parser so fenced examples and comments
	// cannot masquerade as required sections. Only top-level sections count.
	root := goldmark.DefaultParser().Parse(text.NewReader(source))
	first := root.FirstChild()
	firstLine, _, _ := bytes.Cut(bytes.TrimSpace(source), []byte("\n"))
	if heading, ok := first.(*ast.Heading); !ok || heading.Level != 1 || string(firstLine) != "# "+title {
		fail("first heading must match title")
	}
	overview := false
	var current string
	counts := make(map[string]int)
	content := make(map[string]bool)
	for node := root.FirstChild(); node != nil; node = node.NextSibling() {
		if heading, ok := node.(*ast.Heading); ok && heading.Level == 2 {
			current = headingText(heading, source)
			counts[current]++
			continue
		}
		if node == first {
			continue
		}
		if strings.TrimSpace(comments.ReplaceAllString(proseText(node, source), "")) != "" {
			if current == "" {
				overview = true
			} else {
				content[current] = true
			}
		}
	}
	if !overview {
		fail("add a decision overview before the first section")
	}
	for _, section := range sections {
		if counts[section] != 1 {
			fail(fmt.Sprintf("expected exactly one %q section", section))
		} else if !content[section] {
			fail(fmt.Sprintf("%q must contain content, not just comments", section))
		}
	}
}

func headingText(heading *ast.Heading, source []byte) string {
	return strings.TrimSpace(string(heading.Lines().Value(source)))
}

// proseText extracts prose blocks using the parser's source segments. Examples
// and HTML comments do not satisfy the required overview or section content.
func proseText(node ast.Node, source []byte) string {
	var prose strings.Builder
	_ = ast.Walk(node, func(current ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch current.(type) {
		case *ast.HTMLBlock, *ast.FencedCodeBlock, *ast.CodeBlock:
			return ast.WalkSkipChildren, nil
		case *ast.Paragraph, *ast.TextBlock:
			prose.Write(current.Lines().Value(source))
			return ast.WalkSkipChildren, nil
		default:
			return ast.WalkContinue, nil
		}
	})
	return prose.String()
}

func validateLinks(records map[string]Metadata, result *Result) {
	for name, metadata := range records {
		for _, relation := range []struct {
			field, reciprocal string
			targets           []string
		}{{"supersedes", "superseded_by", metadata.Supersedes}, {"superseded_by", "supersedes", metadata.SupersededBy}} {
			for _, target := range relation.targets {
				other, exists := records[target]
				if target == name || !exists {
					result.Errors = append(result.Errors, fmt.Sprintf("%s: %s references missing or self ADR %s", name, relation.field, target))
					continue
				}
				backlinks := other.SupersededBy
				if relation.field == "superseded_by" {
					backlinks = other.Supersedes
				}
				if !slices.Contains(backlinks, name) {
					result.Errors = append(result.Errors, fmt.Sprintf("%s: %s must link back via %s", name, target, relation.reciprocal))
				}
			}
		}
		if len(metadata.Supersedes) > 0 && !slices.Contains([]string{"accepted", "superseded", "deprecated"}, metadata.Status) {
			result.Errors = append(result.Errors, name+": a replacement decision must be accepted before superseding another")
		}
	}
	visiting, visited := make(map[string]bool), make(map[string]bool)
	var visit func(string)
	visit = func(name string) {
		if visiting[name] {
			result.Errors = append(result.Errors, name+": supersession cycle")
			return
		}
		if visited[name] {
			return
		}
		visiting[name] = true
		for _, target := range records[name].SupersededBy {
			if _, exists := records[target]; exists {
				visit(target)
			}
		}
		delete(visiting, name)
		visited[name] = true
	}
	for _, name := range slices.Sorted(maps.Keys(records)) {
		visit(name)
	}
}

func compactTimestamp(stamp time.Time) string {
	return strings.ReplaceAll(stamp.Format(timestampLayout), ".", "")
}

func slugify(title string) string {
	var slug strings.Builder
	separator := false
	for _, character := range strings.ToLower(norm.NFKD.String(title)) {
		if unicode.Is(unicode.Mn, character) {
			continue
		}
		if character >= 'a' && character <= 'z' || character >= '0' && character <= '9' {
			if separator && slug.Len() > 0 {
				slug.WriteByte('-')
			}
			slug.WriteRune(character)
			separator = false
		} else {
			separator = true
		}
	}
	return strings.TrimRight(slug.String()[:min(slug.Len(), 80)], "-")
}
