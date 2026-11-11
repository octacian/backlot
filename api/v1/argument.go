package v1

import (
	"bytes"
	"encoding/json"
	"errors"

	"github.com/invopop/jsonschema"
)

// Argument is one scalar string or explicit value, preserving its wire representation.
type Argument struct {
	// Value carries the explicit payload and sensitivity.
	Value
	scalar bool
}

// StringArguments preserves literal argv without interpreting its contents.
func StringArguments(values ...string) []Argument {
	result := make([]Argument, len(values))
	for i := range values {
		result[i] = Argument{Value: Value{Literal: &values[i]}, scalar: true}
	}
	return result
}

// MarshalJSON retains historical scalar arguments.
func (a Argument) MarshalJSON() ([]byte, error) {
	if a.scalarLiteral() {
		return json.Marshal(*a.Literal)
	}
	return json.Marshal(a.Value)
}

// UnmarshalJSON accepts the scalar/value union with strict object fields.
func (a *Argument) UnmarshalJSON(data []byte) error {
	*a = Argument{}
	if len(data) > 0 && data[0] == '"' {
		var text string
		if err := json.Unmarshal(data, &text); err != nil {
			return errors.New("invalid argument")
		}
		a.Literal = &text
		a.scalar = true
		return nil
	}
	if len(data) == 0 || data[0] != '{' {
		return errors.New("argument requires string or value")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&a.Value); err != nil {
		return errors.New("invalid argument value")
	}
	return nil
}

// JSONSchema describes the scalar/value union used by argv.
func (Argument) JSONSchema() *jsonschema.Schema {
	return &jsonschema.Schema{OneOf: []*jsonschema.Schema{{Type: "string"}, {Ref: "#/$defs/Value"}}}
}

// MarshalYAML preserves the same scalar/value union as JSON.
func (a Argument) MarshalYAML() (any, error) {
	if a.scalarLiteral() {
		return *a.Literal, nil
	}
	return a.Value, nil
}

// Preserve scalar encoding only while the argument remains a plain literal.
func (a Argument) scalarLiteral() bool {
	return a.scalar && a.Literal != nil && !a.Secret && a.Ref == nil && a.Format == nil && a.HostPort == nil && a.URLPath == nil && a.FilePath == nil
}
