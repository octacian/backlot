package daemon

import "os"

// Diagnostic certificate export is unsupported without Unix ownership checks.
func diagnosticCAOwned(os.FileInfo) bool { return false }
