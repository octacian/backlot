package cli

import (
	"context"
	"path/filepath"

	v1 "github.com/octacian/backlot/api/v1"
	"github.com/octacian/backlot/internal/client"
	"github.com/octacian/backlot/internal/daemon"
	urfave "github.com/urfave/cli/v3"
)

func gatewayCommand() *urfave.Command {
	var children []*urfave.Command
	for _, name := range []string{"start", "stop", "restart", "status"} {
		flags := daemonFlags(false)
		if name == "start" || name == "restart" {
			flags = append(flags, &urfave.StringFlag{Name: "config", Usage: "Machine configuration with explicit managed Caddy mode"})
		}
		children = append(children, &urfave.Command{Name: name, Flags: flags, Action: func(ctx context.Context, c *urfave.Command) error {
			if c.Args().Len() != 0 {
				return output(c, nil, &v1.PlanError{Code: "invalid_arguments", Message: "gateway commands take flags only"})
			}
			dir, err := directory(c)
			if err != nil {
				return output(c, nil, err)
			}
			if err := daemon.CheckAccess(dir); err != nil {
				return output(c, nil, err)
			}
			config := c.String("config")
			if config != "" {
				config, err = filepath.Abs(config)
				if err != nil {
					return output(c, nil, err)
				}
			}
			cli := client.New(daemon.SocketPath(dir))
			defer cli.Close()
			response, err := cli.Gateway(ctx, c.Name, v1.GatewayRequest{APIVersion: v1.Version, ConfigPath: config})
			return output(c, response, err)
		}})
	}
	return &urfave.Command{Name: "gateway", Usage: "Control the daemon-owned native local HTTPS gateway", Commands: children}
}
