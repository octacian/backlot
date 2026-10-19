//go:build !linux && !darwin

package daemon

import "errors"

func publishExport(_ int, _, _ string) error {
	return errors.New("atomic no-replacement export unsupported on this platform")
}
