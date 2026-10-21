package native

import (
	"context"
	"errors"
	"io"
	"os"
	"testing"
)

func TestLinuxProcessCensusBounds(t *testing.T) {
	for _, kind := range []string{"growth", "oversize", "cancel", "late-empty", "no-progress"} {
		t.Run(kind, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			calls := 0
			_, err := boundedProcessEntries(ctx, func(n int) ([]os.DirEntry, error) {
				calls++
				if n != processDirectoryBatch {
					t.Fatal("unbounded batch", n)
				}
				switch kind {
				case "cancel", "late-empty":
					cancel()
					return nil, io.EOF
				case "oversize":
					return make([]os.DirEntry, n+1), nil
				case "no-progress":
					return nil, nil
				}
				return make([]os.DirEntry, n), nil
			})
			if err == nil || calls > sessionCandidateLimit/processDirectoryBatch+1 {
				t.Fatal("census bound", calls, err)
			}
			if (kind == "cancel" || kind == "late-empty") && !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
		})
	}
}
