// Package schema generates editor schemas from the shared versioned Go contracts.
package schema

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/invopop/jsonschema"
	v1 "github.com/octacian/backlot/api/v1"
)

// Generate returns deterministic, self-contained editor artifacts. root is the
// repository root used by the library to read GoDoc descriptions.
func Generate(root string) (map[string][]byte, error) {
	r := &jsonschema.Reflector{}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	// AddGoComments keys include the scanned filesystem path. Normalize those keys
	// to the module import path so generation is independent of cwd/checkout path.
	if err := r.AddGoComments("", filepath.Join(absolute, "api")); err != nil {
		return nil, fmt.Errorf("read contract comments: %w", err)
	}
	comments := make(map[string]string, len(r.CommentMap))
	for key, value := range r.CommentMap {
		comments["github.com/octacian/backlot"+strings.TrimPrefix(key, filepath.ToSlash(absolute))] = value
	}
	r.CommentMap = comments
	artifacts := map[string][]byte{}
	for name, contract := range map[string]any{"manifest-v1.schema.json": v1.Manifest{}, "machine-config-v1.schema.json": v1.MachineConfig{}} {
		s := r.Reflect(contract)
		s.ID = jsonschema.ID("https://raw.githubusercontent.com/octacian/backlot/main/schemas/" + name)
		s.Comments = "Generated from api/v1; run make schema-generate. Editor assistance only; backlot plan remains authoritative."
		encoded, err := json.MarshalIndent(s, "", "  ")
		if err != nil {
			return nil, fmt.Errorf("encode %s: %w", name, err)
		}
		artifacts[name] = append(encoded, '\n')
	}
	return artifacts, nil
}
