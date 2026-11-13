package cli

import (
	"context"
	"os"
	"path/filepath"

	v1 "github.com/octacian/backlot/api/v1"
	"github.com/octacian/backlot/internal/client"
	"github.com/octacian/backlot/internal/daemon"
	urfave "github.com/urfave/cli/v3"
)

func listCommand() *urfave.Command { return scopedListCommand(false) }

func checkoutCommand() *urfave.Command {
	return &urfave.Command{Name: "checkout", Usage: "Discover recorded instances in one checkout", Commands: []*urfave.Command{scopedListCommand(true), cleanupCommand(false)}}
}

func scopedListCommand(checkout bool) *urfave.Command {
	command := &urfave.Command{Name: "list", ArgsUsage: " ", Description: "Example: backlot list --json", Usage: "Discover current and historical instance IDs (one bounded page)", Flags: append(daemonFlags(false), &urfave.StringFlag{Name: "project", Usage: "Exact recorded project location"}, &urfave.StringFlag{Name: "checkout", Usage: "Exact checkout root, including non-Git roots"}, &urfave.StringFlag{Name: "project-id", Usage: "Recorded project identifier"}, &urfave.StringFlag{Name: "scene", Usage: "Recorded scene name"}, &urfave.StringFlag{Name: "lifetime", Usage: "persistent or disposable"}, &urfave.StringFlag{Name: "state", Usage: "Exact instance status"}, &urfave.StringFlag{Name: "after", Usage: "Continue after the previous page's next_after_id"}, &urfave.IntFlag{Name: "limit", Value: 100, Usage: "Page size, 1 through 100"}), Action: func(ctx context.Context, c *urfave.Command) error {
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
		request.IncludeCheckout = request.ProjectPath != "" || request.CheckoutPath != ""
		if checkout && request.CheckoutPath == "" {
			cwd, err := os.Getwd()
			request.CWD = cwd
			if err != nil {
				return output(c, nil, err)
			}
			request.IncludeCheckout = true
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
		if request.IncludeCheckout || request.ProjectID != "" || request.Scene != "" || request.Lifetime != "" || request.State != "" {
			response, err := cli.FilteredList(ctx, request)
			return output(c, response, err)
		}
		response, err := cli.List(ctx, v1.ListRequest{APIVersion: v1.Version, AfterID: c.String("after"), Limit: c.Int("limit")})
		return output(c, response, err)
	}}
	if checkout {
		command.Usage = "Discover instances in the enclosing Git checkout, or an exact --checkout root"
		command.Description = "Example: backlot checkout list --json"
		for i, flag := range command.Flags {
			if flag.Names()[0] == "project" {
				command.Flags = append(command.Flags[:i], command.Flags[i+1:]...)
				break
			}
		}
	}
	return command
}
