//go:build !darwin && !linux

package cli

import (
	"context"
	"io"

	v1 "github.com/octacian/backlot/api/v1"
	"golang.org/x/term"
)

func setupTerminal(_ context.Context, _ io.Writer) (*term.Terminal, func() error, error) {
	return nil, nil, &v1.PlanError{Code: "unsupported_platform", Message: "interactive setup supports macOS and Linux; use structured input on supported platforms"}
}

// terminalInput keeps interactive cleanup unavailable on unsupported platforms.
type terminalInput struct {
	ctx context.Context
	fd  int
}

func (r terminalInput) Read(_ []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return 0, &v1.PlanError{Code: "unsupported_platform", Message: "interactive confirmation supports macOS and Linux; use explicit preview application on supported platforms"}
}
