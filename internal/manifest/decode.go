// Package manifest decodes and validates the shared versioned project contracts.
package manifest

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"

	v1 "github.com/octacian/backlot/api/v1"
	"go.yaml.in/yaml/v3"
)

// MaxBytes bounds each manifest, config, or seed file read by the planner.
const MaxBytes = 1 << 20

// Decode accepts one strict YAML or JSON document into a named contract.
// Aliases, merge keys, complex keys, non-string keys, and custom tags are forbidden.
// Parser diagnostics are deliberately sanitized because documents may hold secrets.
func Decode(data []byte, format string, target any) error {
	fail := func(message string) error { return &v1.PlanError{Code: "invalid_document", Message: message} }
	if format != ".json" && format != ".yaml" && format != ".yml" {
		return fail("use a .yaml, .yml or .json document")
	}
	if len(data) > MaxBytes {
		return fail("document exceeds 1 MiB limit")
	}
	if format == ".json" && !json.Valid(data) {
		return fail("expected one valid JSON document")
	}
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	var node yaml.Node
	if err := decoder.Decode(&node); err != nil {
		return fail("invalid document syntax")
	}
	var extra yaml.Node
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return fail("expected exactly one document; remove trailing input")
	}
	if len(node.Content) != 1 {
		return fail("expected a nonempty object")
	}
	value, err := convert(node.Content[0], reflect.TypeOf(target).Elem(), "$", 0)
	if err != nil {
		return err
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return fail("unsupported scalar value")
	}
	dec := json.NewDecoder(bytes.NewReader(encoded))
	dec.DisallowUnknownFields()
	if err := dec.Decode(target); err != nil {
		return fail("field has incorrect type; strings must be quoted")
	}
	return nil
}

func convert(n *yaml.Node, typ reflect.Type, path string, depth int) (any, error) {
	fail := func(message string) (any, error) {
		return nil, &v1.PlanError{Code: "invalid_document", Field: path, Message: message}
	}
	if depth > 64 {
		return fail("maximum nesting depth exceeded")
	}
	for typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}
	if n.Kind == yaml.AliasNode || n.Anchor != "" {
		return fail("YAML anchors and aliases are not supported")
	}
	if n.Tag == "!!null" {
		return fail("null is not supported; omit optional fields")
	}
	switch n.Kind {
	case yaml.MappingNode:
		if n.Tag != "!!map" {
			return fail("custom YAML tags are not supported")
		}
		if typ.Kind() != reflect.Struct && typ.Kind() != reflect.Map {
			return fail("expected a scalar or list")
		}
		values := map[string]any{}
		fields := map[string]reflect.Type{}
		if typ.Kind() == reflect.Struct {
			for i := 0; i < typ.NumField(); i++ {
				f := typ.Field(i)
				fields[strings.Split(f.Tag.Get("json"), ",")[0]] = f.Type
			}
		}
		for i := 0; i < len(n.Content); i += 2 {
			key := n.Content[i]
			if key.Kind != yaml.ScalarNode || key.Tag != "!!str" || key.Anchor != "" {
				return fail("keys must be strings; merge keys are unsupported")
			}
			if _, exists := values[key.Value]; exists {
				return fail("duplicate field or declaration")
			}
			var childType reflect.Type
			if typ.Kind() == reflect.Map {
				childType = typ.Elem()
			} else {
				var ok bool
				childType, ok = fields[key.Value]
				if !ok {
					return fail("unknown field (field names are case-sensitive)")
				}
			}
			child, err := convert(n.Content[i+1], childType, path+"."+key.Value, depth+1)
			if err != nil {
				return nil, err
			}
			values[key.Value] = child
		}
		return values, nil
	case yaml.SequenceNode:
		if n.Tag != "!!seq" {
			return fail("custom YAML tags are not supported")
		}
		if typ.Kind() != reflect.Slice {
			return fail("expected an object or scalar")
		}
		values := make([]any, 0, len(n.Content))
		for i, child := range n.Content {
			value, err := convert(child, typ.Elem(), fmt.Sprintf("%s[%d]", path, i), depth+1)
			if err != nil {
				return nil, err
			}
			values = append(values, value)
		}
		return values, nil
	case yaml.ScalarNode:
		switch n.Tag {
		case "!!str":
			if typ.Kind() != reflect.String {
				return fail("incorrect scalar type; expected the declared field type")
			}
			return n.Value, nil
		case "!!bool":
			if typ.Kind() != reflect.Bool {
				return fail("incorrect scalar type; quote string values")
			}
			var b bool
			if err := n.Decode(&b); err != nil {
				return fail("invalid boolean")
			}
			return b, nil
		case "!!int":
			if typ.Kind() != reflect.Int && typ.Kind() != reflect.Int64 {
				return fail("incorrect scalar type; quote string values")
			}
			var number int64
			if err := n.Decode(&number); err != nil {
				return fail("integer outside supported range")
			}
			return number, nil
		default:
			return fail("unsupported scalar tag; use strings, booleans or integers")
		}
	default:
		return fail("unsupported document node")
	}
}
