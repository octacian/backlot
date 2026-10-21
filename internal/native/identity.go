package native

import (
	"encoding/json"
	"errors"
)

// UnmarshalJSON classifies recognized authority discriminators before any control
// or receipt access. Passive unknown metadata is harmless; unknown authority is not.
func (id *Identity) UnmarshalJSON(data []byte) error {
	type current Identity
	var next current
	var fields map[string]json.RawMessage
	if json.Unmarshal(data, &fields) != nil || fields == nil || json.Unmarshal(data, &next) != nil {
		return errors.New("invalid native authority shape; preserve state with its matching binary")
	}
	recognized := false
	for _, key := range []string{"protocol", "purpose", "component", "attempt"} {
		if _, ok := fields[key]; ok {
			recognized = true
		}
	}
	value := Identity(next)
	if (recognized || value.Protocol != 0 || value.Purpose != "" || value.Component != "" || value.Attempt != "") && !validIdentity(value) {
		return errors.New("unsupported native authority protocol or purpose; preserve state with its matching binary")
	}
	// Missing effect/capability or invalid boot facts can be legitimate retained
	// uncertainty. Keep them readable for diagnosis/targeted waiver; Stop validates
	// them before any Control/receipt I/O. Never invent a usable authority here.
	*id = value
	return nil
}
