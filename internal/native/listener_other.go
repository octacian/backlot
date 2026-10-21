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

// ListenerEndpoint is unavailable on unsupported execution platforms.
func ListenerEndpoint(_ context.Context, _ int, _ string) (bool, bool, error) {
	return false, false, errors.New("native listener ownership unsupported")
}

// OwnsListenerAddresses is unavailable on unsupported execution platforms.
func OwnsListenerAddresses(_ context.Context, _, _ int, _ []string) (bool, error) {
	return false, errors.New("native listener ownership unsupported")
}

// ListenerEndpoint is unavailable on unsupported execution platforms.
func (g *Group) ListenerEndpoint(ctx context.Context, address string) (bool, bool, error) {
	return ListenerEndpoint(ctx, g.PGID(), address)
}

// ListenerConflict is unavailable on unsupported execution platforms.
func (g *Group) ListenerConflict(ctx context.Context, port int) (bool, error) {
	return ListenerConflict(ctx, g.PGID(), port)
}
