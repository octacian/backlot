package cli

import (
	"context"
	v1 "github.com/octacian/backlot/api/v1"
	"github.com/octacian/backlot/internal/client"
	"github.com/octacian/backlot/internal/daemon"
	urfave "github.com/urfave/cli/v3"
)

func sharedCommand() *urfave.Command {
	var children []*urfave.Command
	for _, name := range []string{"list", "inspect", "start", "stop", "restart", "destroy"} {
		flags := daemonFlags(false)
		if name == "destroy" {
			flags = append(flags, &urfave.BoolFlag{Name: "force", Usage: "Delete recorded shared targets despite ownership proof mismatch; retained allocations/consumers still block"})
		}
		args := "<service-id>"
		if name == "list" {
			args = " "
		}
		children = append(children, &urfave.Command{Name: name, ArgsUsage: args, Usage: map[string]string{"list": "List retained shared server identities and allocation counts", "inspect": "Read one recorded shared server", "start": "Start one recorded shared server", "stop": "Stop one recorded shared server, retaining storage", "restart": "Restart one recorded shared server, retaining storage", "destroy": "Delete one allocation-free recorded shared server and its data"}[name], Flags: flags, Action: func(ctx context.Context, c *urfave.Command) error {
			count := 1
			if c.Name == "list" {
				count = 0
			}
			if c.Args().Len() != count {
				return output(c, nil, &v1.PlanError{Code: "invalid_arguments", Message: "shared list takes flags only; other actions require one recorded service ID after flags"})
			}
			dir, err := directory(c)
			if err != nil {
				return output(c, nil, err)
			}
			if err = daemon.CheckAccess(dir); err != nil {
				return output(c, nil, err)
			}
			cli := client.New(daemon.SocketPath(dir))
			defer cli.Close()
			var response v1.SharedResponse
			if c.Name == "destroy" {
				response, err = cli.DestroyShared(ctx, v1.SharedDestroyRequest{APIVersion: v1.Version, ServiceID: c.Args().First(), Force: c.Bool("force")})
			} else {
				response, err = cli.Shared(ctx, c.Name, v1.SharedRequest{APIVersion: v1.Version, ServiceID: c.Args().First()})
			}
			return output(c, response, err)
		}})
	}
	return &urfave.Command{Name: "shared", Usage: "Inspect and control retained project-owned shared servers", Commands: children}
}
