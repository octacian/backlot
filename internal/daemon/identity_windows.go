package daemon

import "os"

func fileIdentity(_ os.FileInfo) (string, error) {
	return "", problem("unsupported_platform", "daemon filesystem identity is not supported on Windows")
}
