package cli

import (
	"context"
	"fmt"

	v1 "github.com/octacian/backlot/api/v1"
	urfave "github.com/urfave/cli/v3"
)

func fixtureCommands() []*urfave.Command {
	var commands []*urfave.Command
	for _, name := range []string{"fixtures", "secret"} {
		commands = append(commands, &urfave.Command{Name: name, Usage: "Discover fixtures or explicitly read one sensitive fixture", Flags: append(daemonFlags(false), &urfave.StringFlag{Name: "component"}), Action: func(ctx context.Context, c *urfave.Command) error {
			secret := c.Name == "secret"
			if c.Args().Len() != 1 && !secret || secret && (c.Args().Len() != 2 || c.String("component") == "") {
				return output(c, nil, &v1.PlanError{Code: "invalid_arguments", Message: "fixtures requires an instance ID; secret requires --component NAME and instance ID plus fixture name"})
			}
			cli, err := runtimeClient(c)
			if err != nil {
				return output(c, nil, err)
			}
			defer cli.Close()
			request := v1.FixturesRequest{APIVersion: v1.Version, InstanceID: c.Args().First(), Component: c.String("component")}
			var response v1.FixturesResponse
			if secret {
				request.SecretName = c.Args().Get(1)
				response, err = cli.FixtureSecret(ctx, request)
			} else {
				response, err = cli.Fixtures(ctx, request)
			}
			if err != nil || c.Bool("json") {
				return output(c, response, err)
			}
			for _, fixture := range response.Fixtures {
				value := fixture.Value
				if fixture.Sensitive && !secret {
					value = "[redacted]"
				}
				if _, err := fmt.Fprintf(c.Writer, "%s/%s: %s (%s)\n", fixture.Component, fixture.Name, value, fixture.Description); err != nil {
					return err
				}
			}
			return nil
		}})
	}
	return commands
}
