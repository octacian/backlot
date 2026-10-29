package cli

import (
	"context"
	v1 "github.com/octacian/backlot/api/v1"
	urfave "github.com/urfave/cli/v3"
)

func historyCommand() *urfave.Command {
	return &urfave.Command{Name: "history", Description: "Example: backlot history INSTANCE_ID --limit 100 --json", Usage: "List retained attempts, newest first", ArgsUsage: "<instance-id>", Flags: append(daemonFlags(false), &urfave.IntFlag{Name: "limit", Value: 100, Usage: "Page size, 1 through 100"}, &urfave.StringFlag{Name: "after", Usage: "History continuation"}), Action: func(ctx context.Context, c *urfave.Command) error {
		if c.Args().Len() != 1 || c.Int("limit") < 1 || c.Int("limit") > 100 {
			return output(c, nil, &v1.PlanError{Code: "invalid_arguments", Message: "history requires one instance and limit 1 through 100"})
		}
		cli, err := runtimeClient(c)
		if err != nil {
			return output(c, nil, err)
		}
		defer cli.Close()
		r, err := cli.History(ctx, v1.HistoryRequest{APIVersion: v1.Version, InstanceID: c.Args().First(), Limit: c.Int("limit"), After: c.String("after")})
		return discoveryOutput(c, r, err)
	}, Commands: []*urfave.Command{{Name: "prune", Description: "Example: backlot history prune INSTANCE_ID --keep 10 --dry-run --json", Usage: "Remove the union of selected old/excess completed attempts and evidence", ArgsUsage: "<instance-id>", Flags: []urfave.Flag{&urfave.StringFlag{Name: "older-than", Usage: "Positive completed age, such as 168h"}, &urfave.IntFlag{Name: "keep", Usage: "Keep newest N completed attempts (zero allowed)"}, &urfave.BoolFlag{Name: "dry-run", Usage: "Preview selection and protected entries without mutation"}}, Action: func(ctx context.Context, c *urfave.Command) error {
		if c.Args().Len() != 1 || !c.IsSet("keep") && !c.IsSet("older-than") || c.IsSet("limit") || c.IsSet("after") {
			return output(c, nil, &v1.PlanError{Code: "invalid_arguments", Message: "prune requires one instance and older-than and/or keep"})
		}
		q := v1.HistoryPruneRequest{APIVersion: v1.Version, InstanceID: c.Args().First(), OlderThan: c.String("older-than"), DryRun: c.Bool("dry-run")}
		if c.IsSet("keep") {
			n := c.Int("keep")
			q.Keep = &n
		}
		if err := q.Validate(); err != nil {
			return output(c, nil, err)
		}
		cli, err := runtimeClient(c)
		if err != nil {
			return output(c, nil, err)
		}
		defer cli.Close()
		r, err := cli.PruneHistory(ctx, q)
		return discoveryOutput(c, r, err)
	}}}}
}
