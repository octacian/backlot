//go:build !darwin && !linux

package native

import (
	"context"
	"errors"
	"time"
)

type sessionCleanupState struct{}

func sessionAvailable() error { return errors.New("native sessions unsupported") }
func sessionListener(context.Context, string, int) (bool, bool, error) {
	return false, false, sessionAvailable()
}
func (w *ownedWorkload) stopSession(time.Duration) error { return sessionAvailable() }
