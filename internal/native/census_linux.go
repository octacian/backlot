package native

import (
	"context"
	"errors"
	"io"
	"os"
)

const processDirectoryBatch = 128

func processEntries(ctx context.Context) ([]os.DirEntry, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	directory, err := os.Open("/proc")
	if err != nil {
		return nil, err
	}
	defer func() { _ = directory.Close() }()
	return boundedProcessEntries(ctx, directory.ReadDir)
}

// Bound both each acquisition and the complete census, including non-PID names;
// ReadDir(n) avoids os.ReadDir's unbounded allocation and sort of the full tree.
func boundedProcessEntries(ctx context.Context, read func(int) ([]os.DirEntry, error)) ([]os.DirEntry, error) {
	var all []os.DirEntry
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		batch, err := read(processDirectoryBatch)
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if len(batch) > processDirectoryBatch || len(all)+len(batch) > sessionCandidateLimit {
			return nil, errors.New("process census candidate budget exhausted")
		}
		all = append(all, batch...)
		if errors.Is(err, io.EOF) {
			return all, nil
		}
		if err != nil {
			return nil, err
		}
		if len(batch) == 0 {
			return nil, errors.New("process census made no progress")
		}
	}
}
