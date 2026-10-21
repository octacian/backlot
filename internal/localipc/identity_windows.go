package localipc

import "os"

// Identity rejects unsupported filesystem identity on Windows.
func Identity(_ os.FileInfo) (string, error) {
	return "", problem("unsupported_platform", "filesystem identity is not supported on Windows")
}
