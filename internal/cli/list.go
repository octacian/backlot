package cli

import (
	"context"
	"path/filepath"

	v1 "github.com/octacian/backlot/api/v1"
	"github.com/octacian/backlot/internal/client"
	"github.com/octacian/backlot/internal/daemon"
	urfave "github.com/urfave/cli/v3"
)

func listCommand() *urfave.Command {
	return &urfave.Command{Name: "list", ArgsUsage: " ", Description: "Example: backlot list --json", Usage: "Discover current and historical instance IDs (one bounded page)", Flags: append(daemonFlags(false), &urfave.StringFlag{Name: "project", Usage: "Exact recorded project location"}, &urfave.StringFlag{Name: "checkout", Usage: "Canonical checkout location"}, &urfave.StringFlag{Name: "project-id", Usage: "Recorded project identifier"}, &urfave.StringFlag{Name: "scene", Usage: "Recorded scene name"}, &urfave.StringFlag{Name: "lifetime", Usage: "persistent or disposable"}, &urfave.StringFlag{Name: "state", Usage: "Exact instance status"}, &urfave.StringFlag{Name: "after", Usage: "Continue after the previous page's next_after_id"}, &urfave.IntFlag{Name: "limit", Value: 100, Usage: "Page size, 1 through 100"}), Action: func(ctx context.Context, c *urfave.Command) error {
		if c.Args().Len() != 0 {
			return output(c, nil, &v1.PlanError{Code: "invalid_arguments", Message: "list takes flags only"})
		}
		request := v1.FilteredListRequest{APIVersion: v1.Version, AfterID: c.String("after"), Limit: c.Int("limit"), ProjectID: c.String("project-id"), Scene: c.String("scene"), Lifetime: v1.Lifetime(c.String("lifetime")), State: v1.InstanceStatus(c.String("state"))}
		for name, dst := range map[string]*string{"project": &request.ProjectPath, "checkout": &request.CheckoutPath} {
			if c.IsSet(name) {
				value, err := filepath.Abs(c.String(name))
				if err != nil {
					return output(c, nil, err)
				}
				*dst = value
			}
		}
		if c.IsSet("limit") && request.Limit == 0 {
			return output(c, nil, &v1.PlanError{Code: "invalid_arguments", Message: "limit must be 1 through 100"})
		}
		if err := request.Validate(); err != nil {
			return output(c, nil, err)
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
		if request.ProjectPath != "" || request.CheckoutPath != "" || request.ProjectID != "" || request.Scene != "" || request.Lifetime != "" || request.State != "" {
			response, err := cli.FilteredList(ctx, request)
			return output(c, response, err)
		}
		response, err := cli.List(ctx, v1.ListRequest{APIVersion: v1.Version, AfterID: c.String("after"), Limit: c.Int("limit")})
		return output(c, response, err)
	}}
}
