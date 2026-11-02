package cli

import (
	"context"
	"encoding/json"
	"fmt"

	v1 "github.com/octacian/backlot/api/v1"
	"github.com/octacian/backlot/internal/daemon"
	"github.com/octacian/backlot/internal/plan"
	"github.com/octacian/backlot/schemas"
	urfave "github.com/urfave/cli/v3"
)

const (
	defaultStartupTimeout = "5m"
	defaultJobTimeout     = "30m"
	defaultStopGrace      = "10s"
)

func sceneDiscoveryCommands() []*urfave.Command {
	commands := []*urfave.Command{}
	for _, name := range []string{"list", "describe"} {
		commands = append(commands, &urfave.Command{Name: name, Usage: "Read validated scene declarations before setup", Description: map[string]string{"list": "Example: backlot scene list --project . --json", "describe": "Example: backlot scene describe test --project . --json"}[name], ArgsUsage: map[string]string{"list": " ", "describe": "<scene>"}[name], Flags: []urfave.Flag{&urfave.StringFlag{Name: "project", Usage: "Project directory or manifest"}}, Action: func(_ context.Context, c *urfave.Command) error {
			count := 0
			if c.Name == "describe" {
				count = 1
			}
			if c.Args().Len() != count {
				return output(c, nil, &v1.PlanError{Code: "invalid_arguments", Message: "scene list takes no arguments; describe requires one scene name"})
			}
			if c.Name == "list" {
				r, err := plan.ListScenes(c.String("project"))
				if err != nil {
					return output(c, nil, err)
				}
				if c.Bool("json") {
					return output(c, r, nil)
				}
				for _, s := range r.Scenes {
					if _, err := fmt.Fprintf(c.Writer, "%s  %s  %d components  terminal=%s  publication=%t\n", s.Name, s.Lifetime, s.ComponentCount, s.TerminalJob, s.Publication); err != nil {
						return err
					}
				}
				return nil
			}
			r, err := plan.DescribeScene(c.String("project"), c.Args().First())
			return discoveryOutput(c, r, err)
		}})
	}
	return commands
}

// Detailed read views retain their named field labels in human output too.
func discoveryOutput(c *urfave.Command, value any, err error) error {
	if err != nil || c.Bool("json") {
		return output(c, value, err)
	}
	encoder := json.NewEncoder(c.Writer)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}

func configCommand() *urfave.Command {
	return &urfave.Command{Name: "config", Usage: "Inspect effective caller configuration", Commands: []*urfave.Command{{Name: "show", Description: "Example: backlot config show --project . --json", Usage: "Show safe sources, candidates and prospective defaults", ArgsUsage: " ", Flags: append(daemonFlags(false), &urfave.StringFlag{Name: "project", Usage: "Project directory or manifest"}, &urfave.StringFlag{Name: "config", Usage: "Machine configuration path"}), Action: func(_ context.Context, c *urfave.Command) error {
		if c.Args().Len() != 0 {
			return output(c, nil, &v1.PlanError{Code: "invalid_arguments", Message: "config show takes flags only"})
		}
		r, err := plan.Configuration(c.String("project"), c.String("config"))
		if err != nil {
			return output(c, nil, err)
		}
		r.DaemonDirectory, err = directory(c)
		if err != nil {
			return output(c, nil, err)
		}
		r.DaemonSocket = daemon.SocketPath(r.DaemonDirectory)
		r.Defaults = v1.ExecutionDefaults{Execution: v1.ExecutionOptions{StartupTimeout: defaultStartupTimeout, JobTimeout: defaultJobTimeout, StopGrace: defaultStopGrace}, LeaseDuration: daemon.DefaultLeaseDuration.String(), RetentionAge: daemon.DefaultRetentionAge.String(), RetentionBytes: daemon.DefaultRetentionBytes, RetentionAttempts: daemon.DefaultRetentionAttempts}
		return discoveryOutput(c, r, nil)
	}}}}
}

func daemonConfigurationCommand() *urfave.Command {
	return &urfave.Command{Name: "config", Description: "Example: backlot daemon config --state-dir /private/backlot --json", Usage: "Read active daemon startup policy", ArgsUsage: " ", Flags: daemonFlags(false), Action: func(ctx context.Context, c *urfave.Command) error {
		if c.Args().Len() != 0 {
			return output(c, nil, &v1.PlanError{Code: "invalid_arguments", Message: "daemon config takes flags only"})
		}
		cli, err := runtimeClient(c)
		if err != nil {
			return output(c, nil, err)
		}
		defer cli.Close()
		r, err := cli.Configuration(ctx)
		return discoveryOutput(c, r, err)
	}}
}

func schemaCommand() *urfave.Command {
	return &urfave.Command{Name: "schema", Description: "Example: backlot schema manifest > manifest.schema.json", Usage: "Emit an installed editor schema (raw JSON in all modes)", ArgsUsage: "manifest|machine|private", Action: func(_ context.Context, c *urfave.Command) error {
		name := map[string]string{"manifest": "manifest-v1.schema.json", "machine": "machine-config-v1.schema.json", "private": "private-config-v1.schema.json"}[c.Args().First()]
		if c.Args().Len() != 1 || name == "" {
			return output(c, nil, &v1.PlanError{Code: "invalid_arguments", Message: "schema requires manifest, machine or private"})
		}
		data, err := schemas.Documents.ReadFile(name)
		if err != nil {
			return err
		}
		_, err = c.Writer.Write(data)
		return err
	}}
}

func contractsCommand(version v1.VersionResponse) *urfave.Command {
	return &urfave.Command{Name: "contracts", Description: "Example: backlot contracts --daemon --state-dir /private/backlot --json", Usage: "List focused supported read contracts and output modes", ArgsUsage: " ", Flags: append(daemonFlags(false), &urfave.BoolFlag{Name: "daemon", Usage: "Ask the active daemon instead of the installed executable"}), Action: func(ctx context.Context, c *urfave.Command) error {
		if c.Args().Len() != 0 || c.IsSet("state-dir") && !c.Bool("daemon") {
			return output(c, nil, &v1.PlanError{Code: "invalid_arguments", Message: "contracts takes flags only; --state-dir requires --daemon"})
		}
		if c.Bool("daemon") {
			cli, err := runtimeClient(c)
			if err != nil {
				return output(c, nil, err)
			}
			defer cli.Close()
			r, err := cli.Contracts(ctx)
			return discoveryOutput(c, r, err)
		}
		return discoveryOutput(c, v1.ContractsResponse{APIVersion: v1.Version, StateVersion: v1.StateVersion, Context: "installed executable", Executable: &version, Schemas: []string{"manifest", "machine", "private"}, Contracts: v1.SupportedReadContracts()}, nil)
	}}
}

func logPageAction(ctx context.Context, c *urfave.Command) error {
	q := v1.LogPageRequest{APIVersion: v1.Version, InstanceID: c.Args().First(), Component: c.String("component"), Attempt: c.String("attempt"), Stream: c.String("stream"), After: c.String("after"), Limit: c.Int("limit")}
	if c.Bool("follow") || q.Limit < 1 || c.IsSet("attempt") && q.Attempt == "" {
		return output(c, nil, &v1.PlanError{Code: "invalid_arguments", Message: "--page conflicts with --follow and requires limit 1 through 1000"})
	}
	if err := q.Validate(); err != nil {
		return output(c, nil, err)
	}
	cli, err := runtimeClient(c)
	if err != nil {
		return output(c, nil, err)
	}
	defer cli.Close()
	r, err := cli.LogPage(ctx, q)
	return discoveryOutput(c, r, err)
}
