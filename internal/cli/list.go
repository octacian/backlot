package cli

import (
	"context"

	v1 "github.com/octacian/backlot/api/v1"
	"github.com/octacian/backlot/internal/client"
	"github.com/octacian/backlot/internal/daemon"
	urfave "github.com/urfave/cli/v3"
)

func listCommand() *urfave.Command {
	return &urfave.Command{Name: "list", Usage: "Discover current and historical instance IDs (one bounded page)", Flags: append(daemonFlags(false), &urfave.StringFlag{Name: "after", Usage: "Continue after the previous page's next_after_id"}, &urfave.IntFlag{Name: "limit", Value: 100, Usage: "Page size, 1 through 100"}), Action: func(ctx context.Context, c *urfave.Command) error {
		if c.Args().Len() != 0 {
			return output(c, nil, &v1.PlanError{Code: "invalid_arguments", Message: "list takes flags only"})
		}
		dir, err := directory(c)
		if err != nil {
			return output(c, nil, err)
		}
		if err := daemon.CheckAccess(dir); err != nil {
			return output(c, nil, err)
		}
		cli := client.New(daemon.SocketPath(dir))
		defer cli.Close()
		response, err := cli.List(ctx, v1.ListRequest{APIVersion: v1.Version, AfterID: c.String("after"), Limit: c.Int("limit")})
		return output(c, response, err)
	}}
}
