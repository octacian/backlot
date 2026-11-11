package v1

import (
	"bytes"
	"encoding/json"
	"errors"
)

// FormatValue substitutes sequential %s operands and supports %% literal percent.
type FormatValue struct {
	// Template contains only sequential %s and %% escapes.
	Template string `json:"template" yaml:"template"`
	// Values supplies exactly one operand per %s.
	Values []Value `json:"values" yaml:"values"`
}

// HostPortValue joins a validated host and decimal port without numeric coercion.
type HostPortValue struct {
	// Host is a hostname or numeric IP without IPv6 brackets.
	Host Value `json:"host" yaml:"host"`
	// Port is a decimal string in 1..65535.
	Port Value `json:"port" yaml:"port"`
}

// URLPathValue appends ordered paths beneath an absolute HTTP(S) base.
type URLPathValue struct {
	// Base is an absolute credential-free HTTP(S) URL without query or fragment.
	Base Value `json:"base" yaml:"base"`
	// Parts append beneath Base without traversal or authority replacement.
	Parts []Value `json:"parts" yaml:"parts"`
}

// FilePathValue joins and cleans paths using the actual consumer's filesystem syntax.
// Traversal is allowed; construction grants no filesystem access or containment.
type FilePathValue struct {
	// Base is the first filesystem path operand.
	Base Value `json:"base" yaml:"base"`
	// Parts are appended in order; an empty array still normalizes Base.
	Parts []Value `json:"parts" yaml:"parts"`
}

// Operands returns a composition's immediate operands in declaration order.
func (v Value) Operands() []Value {
	switch {
	case v.Format != nil:
		return v.Format.Values
	case v.HostPort != nil:
		return []Value{v.HostPort.Host, v.HostPort.Port}
	case v.URLPath != nil:
		return append([]Value{v.URLPath.Base}, v.URLPath.Parts...)
	case v.FilePath != nil:
		return append([]Value{v.FilePath.Base}, v.FilePath.Parts...)
	default:
		return nil
	}
}

// Walk visits the value and every nested operand, stopping at the first error.
func (v Value) Walk(visit func(Value) error) error {
	if err := visit(v); err != nil {
		return err
	}
	for _, child := range v.Operands() {
		if err := child.Walk(visit); err != nil {
			return err
		}
	}
	return nil
}

// UnmarshalJSON requires both format fields, including an explicitly empty template.
func (f *FormatValue) UnmarshalJSON(data []byte) error {
	type wire FormatValue
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return errors.New("invalid format shape")
	}
	if fields["template"] == nil || fields["values"] == nil || bytes.Equal(bytes.TrimSpace(fields["template"]), []byte("null")) || bytes.Equal(bytes.TrimSpace(fields["values"]), []byte("null")) {
		return errors.New("format requires template and values")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var value wire
	if err := decoder.Decode(&value); err != nil {
		return errors.New("invalid format shape")
	}
	*f = FormatValue(value)
	return nil
}

// UnmarshalJSON requires base and an explicit non-null parts array.
func (v *URLPathValue) UnmarshalJSON(data []byte) error {
	type wire URLPathValue
	var value wire
	if err := decodePath(data, &value); err != nil {
		return err
	}
	*v = URLPathValue(value)
	return nil
}

// UnmarshalJSON requires base and an explicit non-null parts array.
func (v *FilePathValue) UnmarshalJSON(data []byte) error {
	type wire FilePathValue
	var value wire
	if err := decodePath(data, &value); err != nil {
		return err
	}
	*v = FilePathValue(value)
	return nil
}

func decodePath(data []byte, value any) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return errors.New("invalid path shape")
	}
	for _, key := range []string{"base", "parts"} {
		if fields[key] == nil || bytes.Equal(bytes.TrimSpace(fields[key]), []byte("null")) {
			return errors.New("path requires base and parts array")
		}
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return errors.New("invalid path shape")
	}
	return nil
}
