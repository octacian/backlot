//go:build !darwin && !linux

package cli

import (
	"context"
	"fmt"
	urfave "github.com/urfave/cli/v3"
)

func installCommand() *urfave.Command {
	return &urfave.Command{Name: "install-replace", ArgsUsage: " ", Description: "Example: backlot install-replace --help (requires a supported platform)", Usage: "Installer requires macOS or Linux", Action: func(context.Context, *urfave.Command) error {
		return fmt.Errorf("installer requires macOS ARM64 or Linux amd64")
	}}
}
