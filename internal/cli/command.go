// Package cli adapts Backlot operations to human and machine-readable commands.
package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"

	apiv1 "github.com/octacian/backlot/api/v1"
	urfave "github.com/urfave/cli/v3"
)

// NewCommand builds the CLI with explicit output writers and build metadata.
// Runtime commands are added as their implementations become available.
func NewCommand(version apiv1.VersionResponse, stdout, stderr io.Writer) *urfave.Command {
	return &urfave.Command{
		Name: "backlot", Usage: "Manage isolated local development and testing scenes",
		Writer: stdout, ErrWriter: stderr, HideVersion: true,
		Action: func(ctx context.Context, command *urfave.Command) error {
			if command.Args().Len() != 0 {
				return fmt.Errorf("unknown command %q; run backlot --help", command.Args().First())
			}
			return urfave.ShowAppHelp(command)
		},
		Commands: append(append(preparationCommands(), runtimeCommands()...), daemonCommand(), planCommand(), &urfave.Command{
			Name: "version", Usage: "Show executable and API versions",
			Flags: []urfave.Flag{&urfave.BoolFlag{Name: "json", Usage: "Emit the shared JSON version response"}},
			Action: func(_ context.Context, command *urfave.Command) error {
				if command.Args().Len() != 0 {
					return fmt.Errorf("version takes no arguments")
				}
				if command.Bool("json") {
					return json.NewEncoder(command.Writer).Encode(version)
				}
				_, err := fmt.Fprintf(command.Writer, "backlot %s (API %s)\n", version.Version, version.APIVersion)
				return err
			},
		}),
	}
}
