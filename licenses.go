// Package backlot provides the attribution bundled with the Backlot executable.
package backlot

import (
	_ "embed"
	"io"
)

//go:embed LICENSE
var license string

//go:embed THIRD_PARTY_NOTICES.md
var notices string

// WriteLicenses writes the full project license and generated dependency notices.
func WriteLicenses(w io.Writer) error {
	_, err := io.WriteString(w, license+"\n"+notices)
	return err
}
