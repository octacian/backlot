// Package cli adapts Backlot operations to human and machine-readable commands.
package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strings"

	"github.com/octacian/backlot"
	apiv1 "github.com/octacian/backlot/api/v1"
	urfave "github.com/urfave/cli/v3"
)

// NewCommand builds the CLI with explicit output writers and build metadata.
// Runtime commands are added as their implementations become available.
func NewCommand(version apiv1.VersionResponse, stdout, stderr io.Writer) *urfave.Command {
	version.StateVersion = apiv1.StateVersion
	command := &urfave.Command{
		Name: "backlot", Usage: "Manage isolated local development and testing scenes",
		Writer: stdout, ErrWriter: stderr, HideVersion: true,
		Flags:          []urfave.Flag{&urfave.BoolFlag{Name: "json", Usage: "Emit shared typed JSON (help and licenses remain text)", OnlyOnce: true}},
		ExitErrHandler: func(context.Context, *urfave.Command, error) {},
		Action: func(ctx context.Context, command *urfave.Command) error {
			if command.Args().Len() != 0 {
				return output(command, nil, &apiv1.PlanError{Code: "invalid_arguments", Message: "unknown command; run backlot --help"})
			}
			return urfave.ShowAppHelp(command)
		},
		Commands: append(append(preparationCommands(), runtimeCommands()...), installCommand(), configCommand(), schemaCommand(), contractsCommand(version), projectCommand(), daemonCommand(), sceneCommand(), exportCommand(), gatewayCommand(), sharedCommand(), planCommand(), listCommand(), &urfave.Command{
			Name: "licenses", ArgsUsage: " ", Description: "Example: backlot licenses", Usage: "Show the project license and dependency notices",
			Action: func(_ context.Context, command *urfave.Command) error {
				if command.Args().Len() != 0 {
					return output(command, nil, &apiv1.PlanError{Code: "invalid_arguments", Message: "licenses takes no arguments"})
				}
				return backlot.WriteLicenses(command.Writer)
			},
		}, &urfave.Command{
			Name: "version", ArgsUsage: " ", Description: "Example: backlot version --json", Usage: "Show executable and API versions",
			Action: func(_ context.Context, command *urfave.Command) error {
				if command.Args().Len() != 0 {
					return output(command, nil, &apiv1.PlanError{Code: "invalid_arguments", Message: "version takes no arguments"})
				}
				if command.Bool("json") {
					return json.NewEncoder(command.Writer).Encode(version)
				}
				_, err := fmt.Fprintf(command.Writer, "backlot %s (API %s)\n", version.Version, version.APIVersion)
				return err
			},
		}),
	}
	configureCommand(command)
	return command
}

// configureCommand applies the same framework error policy to leaves and groups.
func configureCommand(c *urfave.Command) {
	c.OnUsageError = func(_ context.Context, command *urfave.Command, err error, _ bool) error {
		return output(command, nil, &apiv1.PlanError{Code: "invalid_arguments", Message: usageMessage(err) + "; run " + command.FullName() + " --help"})
	}
	for _, flag := range c.Flags {
		switch f := flag.(type) {
		case *urfave.BoolFlag:
			f.OnlyOnce = true
		case *urfave.StringFlag:
			f.OnlyOnce = true
		case *urfave.DurationFlag:
			f.OnlyOnce = true
		case *urfave.IntFlag:
			f.OnlyOnce = true
		case *urfave.Int64Flag:
			f.OnlyOnce = true
		}
	}
	if len(c.Commands) != 0 && c.Action == nil {
		c.Action = func(_ context.Context, command *urfave.Command) error {
			if command.Args().Len() != 0 {
				return output(command, nil, &apiv1.PlanError{Code: "invalid_arguments", Message: "unknown subcommand; run " + command.FullName() + " --help"})
			}
			return urfave.ShowSubcommandHelp(command)
		}
	}
	for _, child := range c.Commands {
		configureCommand(child)
	}
}

// Framework errors may include rejected values; retain option names, never values.
func usageMessage(err error) string {
	message := err.Error()
	if match := regexp.MustCompile(`^flag provided but not defined: -([A-Za-z0-9][A-Za-z0-9-]{0,63})$`).FindStringSubmatch(message); match != nil {
		return "unknown option --" + match[1]
	}
	if match := regexp.MustCompile(`^invalid value .* for flag -([A-Za-z0-9][A-Za-z0-9-]{0,63}):`).FindStringSubmatch(message); match != nil {
		reason := "invalid value for --"
		if strings.Contains(message, "can't duplicate this flag") {
			reason = "duplicate option --"
		}
		return reason + match[1]
	}
	if match := regexp.MustCompile(`^flag needs an argument: -?([A-Za-z0-9][A-Za-z0-9-]{0,63})$`).FindStringSubmatch(message); match != nil {
		return "option --" + match[1] + " requires a value"
	}
	if match := regexp.MustCompile(`^Required flag "([A-Za-z0-9][A-Za-z0-9-]{0,63})" not set$`).FindStringSubmatch(message); match != nil {
		return "required option --" + match[1] + " is missing"
	}
	return "invalid command flags or arguments"
}
