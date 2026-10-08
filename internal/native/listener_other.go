//go:build !darwin && !linux

package native

import (
	"context"
	"errors"
)

// OwnsListener is unavailable on unsupported execution platforms.
func OwnsListener(_ context.Context, _, _ int) (bool, error) {
	return false, errors.New("native listener ownership unsupported")
}

// ListenerConflict is unavailable on unsupported execution platforms.
func ListenerConflict(_ context.Context, _, _ int) (bool, error) {
	return false, errors.New("native listener ownership unsupported")
}
