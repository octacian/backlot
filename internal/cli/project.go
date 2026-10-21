package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	v1 "github.com/octacian/backlot/api/v1"
	"github.com/octacian/backlot/internal/manifest"
	"github.com/octacian/backlot/internal/plan"
	"github.com/octacian/backlot/internal/projectconfig"
	urfave "github.com/urfave/cli/v3"
	"golang.org/x/term"
)

func projectCommand() *urfave.Command {
	return &urfave.Command{Name: "project", Usage: "Manage explicit private project setup", Commands: []*urfave.Command{{Name: "setup", SkipFlagParsing: true, Usage: "Configure once or inspect registered inputs and tools", Flags: []urfave.Flag{
		&urfave.StringFlag{Name: "project"}, &urfave.StringFlag{Name: "config"}, &urfave.StringFlag{Name: "from", Usage: "Import bounded private YAML/JSON document; - reads stdin"},
		&urfave.StringSliceFlag{Name: "generate", Usage: "Explicitly accept generation for a declared eligible input"},
		&urfave.StringSliceFlag{Name: "remove-input"}, &urfave.StringSliceFlag{Name: "remove-tool"},
		&urfave.BoolFlag{Name: "edit"}, &urfave.BoolFlag{Name: "reassociate"},
		&urfave.BoolFlag{Name: "dangerously-reveal-secrets", Usage: "Reveal configured secrets in local inspection only"}, &urfave.BoolFlag{Name: "json"},
	}, Action: func(ctx context.Context, c *urfave.Command) error {
		if c.Args().Len() == 1 && (c.Args().First() == "--help" || c.Args().First() == "-h") {
			return urfave.ShowSubcommandHelp(c)
		}
		if err := parseSetupFlags(c); err != nil {
			return output(c, nil, err)
		}
		return projectSetup(ctx, c)
	}}}}
}

func projectSetup(ctx context.Context, c *urfave.Command) error {
	request := v1.ProjectSetupRequest{ProjectPath: c.String("project"), ConfigPath: c.String("config"), Generate: c.StringSlice("generate"), RemoveInputs: c.StringSlice("remove-input"), RemoveTools: c.StringSlice("remove-tool"), Edit: c.Bool("edit"), Reassociate: c.Bool("reassociate"), RevealSecrets: c.Bool("dangerously-reveal-secrets")}
	bad := func(message string) error {
		return output(c, nil, &v1.PlanError{Code: "invalid_arguments", Message: message})
	}

	from := c.String("from")
	mutation := from != "" || request.Edit || request.Reassociate || len(request.Generate) > 0 || len(request.RemoveInputs) > 0 || len(request.RemoveTools) > 0
	if request.RevealSecrets && mutation {
		return bad("--dangerously-reveal-secrets is inspection-only and cannot be combined with edits")
	}
	update := v1.PrivateConfig{Version: v1.ManifestVersion}
	if from != "" {
		var data []byte
		var err error
		format := filepath.Ext(from)
		if from == "-" {
			data, err = io.ReadAll(io.LimitReader(os.Stdin, manifest.MaxBytes+1))
			format = ".yaml"
		} else {
			data, err = projectconfig.Read(from)
		}
		if err != nil {
			return output(c, nil, err)
		}
		update = v1.PrivateConfig{}
		if err := manifest.Decode(data, format, &update); err != nil {
			return output(c, nil, err)
		}
		request.Update = &update
	}
	setup, err := plan.OpenSetup(request)
	if err != nil {
		return output(c, nil, err)
	}
	result := setup.Inspect()
	interactive := term.IsTerminal(int(os.Stdin.Fd()))
	if !mutation && result.Registered {
		return setupOutput(c, result)
	}
	if !mutation && request.RevealSecrets {
		return setupOutput(c, result)
	}
	if from == "" && len(request.Generate) == 0 && (request.Edit || request.Reassociate || (len(request.RemoveInputs) == 0 && len(request.RemoveTools) == 0)) {
		if !interactive {
			return bad("first setup or --edit requires a terminal, --from FILE/- or explicit --generate NAME")
		}
		update, err = walkSetup(ctx, c, result)
		if err != nil {
			return output(c, nil, err)
		}
	}
	result, err = setup.Save(ctx, update, request.Generate, request.RemoveInputs, request.RemoveTools)
	if err != nil {
		return output(c, nil, err)
	}
	if err := setupOutput(c, result); err != nil {
		return fmt.Errorf("%w: %w", &v1.PlanError{Code: "setup_committed_uncertain", Message: "setup committed but response delivery failed; inspect project setup before retrying; rollback was not performed"}, err)
	}
	return nil
}

func setupOutput(c *urfave.Command, result v1.ProjectSetupResponse) error {
	if c.Bool("json") {
		return output(c, result, nil)
	}
	if _, err := fmt.Fprintf(c.Writer, "Project setup: registered=%t changed=%t\nAssociation: %s\nPrivate configuration: %s\n", result.Registered, result.Changed, result.Association, result.PrivatePath); err != nil {
		return err
	}
	for _, input := range result.Inputs {
		status := "missing"
		if input.Configured {
			status = "configured"
		}
		if _, err := fmt.Fprintf(c.Writer, "  input %s: %s", input.Name, status); err != nil {
			return err
		}
		if input.RevealedValue != nil {
			if _, err := fmt.Fprintf(c.Writer, " = %s", *input.RevealedValue); err != nil {
				return err
			}
		}
		if _, err := fmt.Fprintln(c.Writer); err != nil {
			return err
		}
	}
	for _, tool := range result.Tools {
		if _, err := fmt.Fprintf(c.Writer, "  tool %s: %s\n", tool.Name, tool.Executable); err != nil {
			return err
		}
	}
	return nil
}

func walkSetup(ctx context.Context, c *urfave.Command, status v1.ProjectSetupResponse) (result v1.PrivateConfig, resultErr error) {
	update := v1.PrivateConfig{Version: v1.ManifestVersion, Inputs: map[string]string{}, Tools: map[string]string{}}
	// Raw terminal reads return Ctrl-C/Ctrl-D as cancellation; deferred restoration
	// restores the original terminal state on every return, including hidden entry.
	terminal, restore, err := setupTerminal(ctx, c.ErrWriter)
	if err != nil {
		return update, err
	}
	defer func() {
		if err := restore(); err != nil && resultErr == nil {
			resultErr = err
		}
	}()
	read := func(prompt string, secret bool) (string, error) {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		var value string
		var err error
		if secret {
			value, err = terminal.ReadPassword(prompt)
		} else {
			terminal.SetPrompt(prompt)
			value, err = terminal.ReadLine()
		}
		if err != nil {
			return "", &v1.PlanError{Code: "setup_cancelled", Message: "setup cancelled or EOF; active configuration unchanged"}
		}
		return value, nil
	}
	for _, item := range status.Inputs {
		state := "missing"
		if item.Configured {
			state = "configured; empty keeps current"
		}
		if _, err := fmt.Fprintf(c.ErrWriter, "Input %s (%s): %s\n", item.Name, state, item.Description); err != nil {
			return update, err
		}
		if item.Generate != "" {
			answer, err := read("Accept generating a new random-hex-32 secret? Type yes to accept, no for custom, or cancel: ", false)
			if err != nil {
				return update, err
			}
			if answer == "cancel" {
				return update, &v1.PlanError{Code: "setup_cancelled", Message: "setup cancelled; active configuration unchanged"}
			}
			if answer == "yes" {
				value, err := plan.GenerateSetupSecret()
				if err != nil {
					return update, err
				}
				update.Inputs[item.Name] = value
				continue
			}
		}
		value, err := read("Value (Ctrl-D cancels): ", item.Secret)
		if err != nil {
			return update, err
		}
		if value != "" || !item.Configured {
			update.Inputs[item.Name] = value
		}
	}
	for _, item := range status.Tools {
		value, err := read("Tool "+item.Name+" ["+item.Executable+"] (empty keeps current): ", false)
		if err != nil {
			return update, err
		}
		if value != "" {
			update.Tools[item.Name] = value
		}
	}
	return update, nil
}

func parseSetupFlags(c *urfave.Command) error {
	args := c.Args().Slice()
	for _, arg := range args {
		if arg == "--json" {
			if err := c.Set("json", "true"); err != nil {
				return err
			}
		}
	}
	seen := map[string]bool{}
	for i := 0; i < len(args); i++ {
		flag := args[i]
		value := "true"
		repeat := false
		switch flag {
		case "--generate", "--remove-input", "--remove-tool":
			repeat = true
			fallthrough
		case "--project", "--config", "--from":
			if i+1 >= len(args) {
				return &v1.PlanError{Code: "invalid_arguments", Message: "setup path/name flag requires a separate value"}
			}
			i++
			value = args[i]
		case "--edit", "--reassociate", "--dangerously-reveal-secrets", "--json":
		default:
			return &v1.PlanError{Code: "invalid_arguments", Message: "unknown setup argument; use --from FILE/- or hidden terminal entry for values"}
		}
		if seen[flag] && !repeat {
			return &v1.PlanError{Code: "invalid_arguments", Message: "duplicate setup flag"}
		}
		seen[flag] = true
		if flag == "--json" {
			continue
		}
		if err := c.Set(flag[2:], value); err != nil {
			return &v1.PlanError{Code: "invalid_arguments", Message: "invalid setup flag value"}
		}
	}
	return nil
}
