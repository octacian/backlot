//go:build darwin || linux

package native

import (
	"context"
	"errors"
	"testing"
)

func TestListenerCancellationPreservesContextCause(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := OwnsListener(ctx, 2, 1); !errors.Is(err, context.Canceled) {
		t.Fatalf("listener cancellation misclassified: %v", err)
	}
}
