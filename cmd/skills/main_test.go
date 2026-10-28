package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	v1 "github.com/octacian/backlot/api/v1"
	"github.com/octacian/backlot/internal/manifest"
	validator "github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/yuin/goldmark/ast"
	"go.yaml.in/yaml/v3"
)

func TestBundles(t *testing.T) {
	files, err := generate("../..")
	if err != nil {
		t.Fatal(err)
	}
	for _, skill := range []string{"backlot-setup", "backlot"} {
		root := filepath.Join("../../skills", skill)
		entry, err := os.ReadFile(filepath.Join(root, "SKILL.md"))
		if err != nil {
			t.Fatal(err)
		}
		parts := bytes.SplitN(entry, []byte("---"), 3)
		if len(parts) != 3 {
			t.Fatal("missing frontmatter", skill)
		}
		var metadata struct{ Name, Description, Compatibility string }
		if yaml.Unmarshal(parts[1], &metadata) != nil || metadata.Name != skill || len(metadata.Description) == 0 || len(metadata.Description) > 1024 || len(metadata.Compatibility) > 500 {
			t.Fatal("invalid skill metadata", skill)
		}
		installed := map[string][]byte{"SKILL.md": entry}
		for path, expected := range files {
			actual, err := os.ReadFile(filepath.Join(root, path))
			if err != nil || !bytes.Equal(actual, expected) {
				t.Fatalf("stale %s/%s: %v", skill, path, err)
			}
			installed[path] = actual
		}
		for path, data := range installed {
			if !strings.HasSuffix(path, ".md") {
				continue
			}
			_ = ast.Walk(document(data), func(n ast.Node, entering bool) (ast.WalkStatus, error) {
				link, ok := n.(*ast.Link)
				if !ok || !entering {
					return ast.WalkContinue, nil
				}
				dest := string(link.Destination)
				if strings.Contains(dest, ":") {
					return ast.WalkContinue, nil
				}
				file, anchor, _ := strings.Cut(dest, "#")
				target := path
				if file != "" {
					target = filepath.ToSlash(filepath.Clean(filepath.Join(filepath.Dir(path), file)))
				}
				content, exists := installed[target]
				if strings.HasPrefix(target, "../") || !exists || !hasAnchor(content, anchor) {
					t.Errorf("broken installed link %s: %s", path, dest)
				}
				return ast.WalkContinue, nil
			})
		}
	}
	data := files["assets/examples/adoption/backlot.yaml"]
	var example v1.Manifest
	if err := manifest.Decode(data, ".yaml", &example); err != nil {
		t.Fatal(err)
	}
	var value any
	if err := yaml.Unmarshal(data, &value); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	instance, err := validator.UnmarshalJSON(bytes.NewReader(encoded))
	if err != nil {
		t.Fatal(err)
	}
	schema, err := validator.UnmarshalJSON(bytes.NewReader(files["assets/schemas/manifest-v1.schema.json"]))
	if err != nil {
		t.Fatal(err)
	}
	compiler := validator.NewCompiler()
	if err := compiler.AddResource("manifest.json", schema); err != nil {
		t.Fatal(err)
	}
	compiled, err := compiler.Compile("manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := compiled.Validate(instance); err != nil {
		t.Fatal(err)
	}
	line, _, _ := strings.Cut(string(data), "\n")
	modeline := strings.TrimPrefix(line, "# yaml-language-server: $schema=")
	if _, exists := files[filepath.ToSlash(filepath.Join("assets/examples/adoption", modeline))]; !exists {
		t.Fatal("missing example schema")
	}
}

func TestGenerationDriftAndMissingSources(t *testing.T) {
	root := t.TempDir()
	for _, path := range []string{"docs", "schemas", "examples/adoption"} {
		if err := os.CopyFS(filepath.Join(root, path), os.DirFS(filepath.Join("../..", path))); err != nil {
			t.Fatal(err)
		}
	}
	if err := run(root, false); err != nil {
		t.Fatal(err)
	}
	if err := run(root, true); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "docs/adoption.md")
	if err := os.WriteFile(path, []byte("# Updated adoption\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := run(root, true); err == nil || !strings.Contains(err.Error(), "stale") {
		t.Fatal("source drift accepted", err)
	}
	if err := run(root, false); err != nil {
		t.Fatal(err)
	}
	if err := run(root, true); err != nil {
		t.Fatal(err)
	}
	obsolete := filepath.Join(root, "skills/backlot/assets/obsolete.txt")
	if err := os.WriteFile(obsolete, []byte("old"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := run(root, true); err == nil {
		t.Fatal("obsolete output accepted")
	}
	if err := os.Remove(obsolete); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(root, "schemas/manifest-v1.schema.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := generate(root); err == nil {
		t.Fatal("missing schema accepted")
	}
}

func TestSectionsAndLinkRewriting(t *testing.T) {
	data := []byte("# Guide\n\n## First\n\n```md\n## Not a heading\n```\n\n### Nested\nKeep this.\n\n## Last\nOmit this.\n")
	section, err := selectSections(data, []string{"First"})
	if err != nil || !bytes.Contains(section, []byte("Keep this.")) || bytes.Contains(section, []byte("Omit this.")) {
		t.Fatal(err, string(section))
	}
	if _, err := selectSections(data, []string{"Missing"}); err == nil {
		t.Fatal("missing section accepted")
	}
	links := []byte("[local](other.md#present) [omitted](other.md#absent) [external](https://example.com) [source](../examples/a.yaml)\n")
	out := string(rewriteLinks(links, "docs/guide.md", map[string][]byte{"references/other.md": []byte("## Present\n")}))
	for _, want := range []string{"(other.md#present)", "(" + sourceURL + "docs/other.md#absent)", "(https://example.com)", "(" + sourceURL + "examples/a.yaml)"} {
		if !strings.Contains(out, want) {
			t.Fatal("missing rewritten destination", want, out)
		}
	}
}
