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
