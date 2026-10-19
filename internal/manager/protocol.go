package manager

import (
	"errors"
	v1 "github.com/octacian/backlot/api/v1"
	"github.com/octacian/backlot/internal/manifest"
)

// MaxDocumentBytes bounds each private command protocol JSON document.
const MaxDocumentBytes = 64 << 10

// DecodeResult strictly validates one bounded structured command result.
// Neither stderr nor an exit status can substitute for this ownership observation.
// Custom-command execution itself is a later implementation gate.
func DecodeResult(request v1.ManagerRequest, data []byte) (v1.ManagerResult, error) {
	var result v1.ManagerResult
	if len(data) > MaxDocumentBytes {
		return result, errors.New("manager result exceeds bound")
	}
	if err := manifest.Decode(data, ".json", &result); err != nil {
		return result, errors.New("manager result is not one strict structured JSON document")
	}
	if err := ValidateResult(request, result); err != nil {
		return result, err
	}
	return result, nil
}
