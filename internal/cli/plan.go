package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	v1 "github.com/octacian/backlot/api/v1"
	"github.com/octacian/backlot/internal/plan"
	urfave "github.com/urfave/cli/v3"
)

func planCommand() *urfave.Command {
	return &urfave.Command{Name: "plan", Usage: "Validate an offline scene plan without executing work", ArgsUsage: "<scene> [--project PATH] [--config PATH] [--json] -- <terminal-job args>", SkipFlagParsing: true, Action: func(_ context.Context, c *urfave.Command) error {
		if c.Args().Len() == 1 && (c.Args().First() == "--help" || c.Args().First() == "-h") {
			return urfave.ShowSubcommandHelp(c)
		}
		request, jsonMode, err := parsePlanArgs(c.Args().Slice())
		var result v1.PlanResponse
		if err == nil {
			result, err = plan.Resolve(request)
		}
		if err != nil {
			if jsonMode {
				var detail *v1.PlanError
				if !errors.As(err, &detail) {
					detail = &v1.PlanError{Code: "plan_failed", Message: "cannot resolve plan"}
				}
				if writeErr := json.NewEncoder(c.Writer).Encode(v1.ErrorResponse{APIVersion: v1.Version, Error: *detail}); writeErr != nil {
					return writeErr
				}
			}
			return err
		}
		if jsonMode {
			return json.NewEncoder(c.Writer).Encode(result)
		}
		if _, err := fmt.Fprintf(c.Writer, "Plan %s/%s (%s)\nCheckout: %s\nNo work executed; allocation values remain symbolic and secrets are redacted.\n", result.Project, result.Scene, result.Lifetime, result.Checkout); err != nil {
			return err
		}
		for _, component := range result.Components {
			if _, err := fmt.Fprintf(c.Writer, "  %s: %s %s\n", component.Name, component.Runtime, component.Kind); err != nil {
				return err
			}
		}
		return nil
	}}
}

func parsePlanArgs(args []string) (v1.PlanRequest, bool, error) {
	var request v1.PlanRequest
	separator := slices.Index(args, "--")
	if separator >= 0 {
		request.TerminalArgs = slices.Clone(args[separator+1:])
		args = args[:separator]
	}
	jsonMode := slices.Contains(args, "--json")
	fail := func(message string) (v1.PlanRequest, bool, error) {
		return request, jsonMode, &v1.PlanError{Code: "invalid_arguments", Field: "plan", Message: message}
	}
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return fail("usage: backlot plan <scene> [--project PATH] [--config PATH] [--json] -- <terminal-job args>")
	}
	request.Scene = args[0]
	seen := map[string]bool{}
	for i := 1; i < len(args); i++ {
		flag := args[i]
		if seen[flag] {
			return fail("duplicate plan flag")
		}
		seen[flag] = true
		switch flag {
		case "--json":
		case "--project", "--config":
			if i+1 >= len(args) || strings.HasPrefix(args[i+1], "--") {
				return fail("plan path flag requires a value")
			}
			i++
			if flag == "--project" {
				request.ProjectPath = args[i]
			} else {
				request.ConfigPath = args[i]
			}
		default:
			return fail("unknown argument; put terminal-job arguments after --")
		}
	}
	return request, jsonMode, nil
}
