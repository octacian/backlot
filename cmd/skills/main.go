// Command skills materializes independent skill bundles from canonical sources.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	v1 "github.com/octacian/backlot/api/v1"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/text"
)

// Supplemental links use the reviewed runtime/docs baseline, never an inferred binary identity.
const sourceURL = "https://github.com/octacian/backlot/blob/6177c64f60654657a914857edbffb69b8316a41b/"

var guides = map[string][]string{
	"adoption": nil, "cli": nil, "platforms": nil, "evidence": nil,
	"agent-skills": {"Compatibility"},
	"operations":   {"Release installer and updates", "Go installation", "Release archive installation", "Upgrade, removal and compatibility", "Daily loop", "Diagnosis and recovery"},
	"manifest":     {"Project declarations", "Components and scenes", "Values and environment", "Machine schema and results", "Registered private project configuration", "Shared-service declarations", "Execution-scoped build images"},
	"daemon":       {"Identity and lifecycle", "Named persistent scenes", "Scoped provider diagnostics", "Declared startup details", "Bounded recorded discovery and active policy"},
	"containers":   {"State compatibility", "Generations, jobs and drift", "Ownership and recovery", "Project-scoped shared services"},
	"https":        {"Managed local HTTPS", "Explicit application trust", "Scene contract", "Conflicts, failures and recovery"},
}

func main() {
	check := flag.Bool("check", false, "check committed bundles without writing")
	flag.Parse()
	if err := run(".", *check); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(root string, check bool) error {
	files, err := generate(root)
	if err != nil {
		return err
	}
	for _, name := range []string{"backlot-setup", "backlot"} {
		base := filepath.Join(root, "skills", name)
		for _, dir := range []string{"references", "assets"} {
			err := filepath.WalkDir(filepath.Join(base, dir), func(path string, entry fs.DirEntry, err error) error {
				if os.IsNotExist(err) {
					return nil
				}
				if err != nil {
					return err
				}
				if entry.IsDir() {
					return nil
				}
				rel, err := filepath.Rel(base, path)
				if err != nil {
					return err
				}
				if _, expected := files[filepath.ToSlash(rel)]; !expected || !entry.Type().IsRegular() {
					return fmt.Errorf("unexpected generated asset %s; remove it before regeneration", path)
				}
				return nil
			})
			if err != nil {
				return err
			}
		}
		for path, data := range files {
			dest := filepath.Join(root, "skills", name, path)
			if check {
				current, err := os.ReadFile(dest)
				if err != nil {
					return err
				}
				if !bytes.Equal(current, data) {
					return fmt.Errorf("%s is stale; run make skills-generate", dest)
				}
			} else {
				if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
					return err
				}
				if err := os.WriteFile(dest, data, 0644); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func generate(root string) (map[string][]byte, error) {
	files := map[string][]byte{}
	for name, sections := range guides {
		source := "docs/" + name + ".md"
		data, err := os.ReadFile(filepath.Join(root, source))
		if err != nil {
			return nil, err
		}
		data, err = selectSections(data, sections)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", source, err)
		}
		files["references/"+name+".md"] = append([]byte("<!-- Generated from "+source+"; run make skills-generate. -->\n\n"), data...)
	}
	files["references/compatibility.md"] = files["references/agent-skills.md"]
	delete(files, "references/agent-skills.md")
	contracts, err := json.MarshalIndent(v1.ContractsResponse{APIVersion: v1.Version, StateVersion: v1.StateVersion, Schemas: []string{"manifest", "machine", "private"}, Contracts: v1.SupportedReadContracts()}, "", "  ")
	if err != nil {
		return nil, err
	}
	files["references/contracts.json"] = append(contracts, '\n')
	for name := range files {
		source := "docs/" + strings.TrimPrefix(name, "references/")
		if name == "references/compatibility.md" {
			source = "docs/agent-skills.md"
		}
		files[name] = append(bytes.TrimRight(rewriteLinks(files[name], source, files), "\n"), '\n')
	}
	for _, name := range []string{"manifest", "machine-config", "private-config"} {
		path := "schemas/" + name + "-v1.schema.json"
		data, err := os.ReadFile(filepath.Join(root, path))
		if err != nil {
			return nil, err
		}
		files["assets/"+path] = data
	}
	for _, name := range []string{"backlot.yaml", "defaults.env"} {
		data, err := os.ReadFile(filepath.Join(root, "examples/adoption", name))
		if err != nil {
			return nil, err
		}
		files["assets/examples/adoption/"+name] = data
	}
	return files, nil
}

func document(data []byte) ast.Node {
	return goldmark.New(goldmark.WithParserOptions(parser.WithAutoHeadingID())).Parser().Parse(text.NewReader(data))
}

func selectSections(data []byte, sections []string) ([]byte, error) {
	if len(sections) == 0 {
		return data, nil
	}
	type heading struct {
		title string
		start int
	}
	var headings []heading
	for node := document(data).FirstChild(); node != nil; node = node.NextSibling() {
		if h, ok := node.(*ast.Heading); ok && h.Level <= 2 {
			line := h.Lines().At(0)
			headings = append(headings, heading{strings.TrimSpace(string(line.Value(data))), bytes.LastIndexByte(data[:line.Start], '\n') + 1})
		}
	}
	var out []byte
	for _, title := range sections {
		found := false
		for i, h := range headings {
			if h.title != title {
				continue
			}
			end := len(data)
			if i+1 < len(headings) {
				end = headings[i+1].start
			}
			out = append(out, data[h.start:end]...)
			found = true
			break
		}
		if !found {
			return nil, fmt.Errorf("missing section %q", title)
		}
	}
	return out, nil
}

func hasAnchor(data []byte, anchor string) bool {
	if anchor == "" {
		return true
	}
	if bytes.Contains(data, []byte(`id="`+anchor+`"`)) {
		return true
	}
	found := false
	_ = ast.Walk(document(data), func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if h, ok := n.(*ast.Heading); ok && entering {
			id, _ := h.AttributeString("id")
			if b, ok := id.([]byte); ok && string(b) == anchor {
				found = true
			}
		}
		return ast.WalkContinue, nil
	})
	return found
}

func rewriteLinks(data []byte, source string, files map[string][]byte) []byte {
	out := bytes.Clone(data)
	_ = ast.Walk(document(data), func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		link, ok := n.(*ast.Link)
		if !ok || !entering {
			return ast.WalkContinue, nil
		}
		dest := string(link.Destination)
		if strings.Contains(dest, ":") || strings.HasPrefix(dest, "//") {
			return ast.WalkContinue, nil
		}
		path, anchor, _ := strings.Cut(dest, "#")
		target := source
		if path != "" {
			target = filepath.ToSlash(filepath.Clean(filepath.Join(filepath.Dir(source), path)))
		}
		local := "references/" + filepath.Base(target)
		if target == "docs/agent-skills.md" {
			local = "references/compatibility.md"
		}
		replacement := sourceURL + target
		if content, exists := files[local]; exists && strings.HasPrefix(target, "docs/") && hasAnchor(content, anchor) {
			replacement = filepath.Base(local)
		}
		if anchor != "" {
			replacement += "#" + anchor
		}
		out = bytes.ReplaceAll(out, []byte("]("+dest+")"), []byte("]("+replacement+")"))
		return ast.WalkContinue, nil
	})
	return out
}
