package cli

import (
	"context"
	"fmt"
	"io"

	v1 "github.com/octacian/backlot/api/v1"
	"github.com/octacian/backlot/internal/plan"
	urfave "github.com/urfave/cli/v3"
)

func planCommand() *urfave.Command {
	return &urfave.Command{Name: "plan", Usage: "Validate an offline scene plan without executing work", ArgsUsage: "<scene> [terminal-job args...]", Description: "Example: backlot plan test --project . -- -run TestExample", Flags: []urfave.Flag{&urfave.StringFlag{Name: "project", Usage: "Project directory or manifest path"}, &urfave.StringFlag{Name: "config", Usage: "Machine configuration path"}}, Action: func(_ context.Context, c *urfave.Command) error {
		if c.Args().Len() == 0 {
			return output(c, nil, &v1.PlanError{Code: "invalid_arguments", Message: "plan requires a scene name"})
		}
		result, err := plan.Resolve(v1.PlanRequest{Scene: c.Args().First(), ProjectPath: c.String("project"), ConfigPath: c.String("config"), TerminalArgs: c.Args().Tail()})
		if err != nil || c.Bool("json") {
			return output(c, result, err)
		}
		if _, err := fmt.Fprintf(c.Writer, "Plan %s/%s (%s)\nCheckout: %s\nNo work executed; allocation values remain symbolic and secrets are redacted.\n", result.Project, result.Scene, result.Lifetime, result.Checkout); err != nil {
			return err
		}
		for _, component := range result.Components {
			if _, err := fmt.Fprintf(c.Writer, "  %s: %s %s\n", component.Name, component.Runtime, component.Kind); err != nil {
				return err
			}
		}
		return executableOutput(c.Writer, result, "caller/offline selection")
	}}
}

func executableOutput(w io.Writer, result v1.PlanResponse, context string) error {
	for _, component := range result.Components {
		readiness := ""
		if component.Readiness != nil {
			readiness = component.Readiness.Executable
		}
		for _, selection := range []struct{ role, path string }{{"command", component.Executable}, {"readiness", readiness}} {
			if selection.path == "" {
				continue
			}
			if _, err := fmt.Fprintf(w, "  %s %s (%s): %q\n", component.Name, selection.role, context, selection.path); err != nil {
				return err
			}
		}
	}
	return nil
}
