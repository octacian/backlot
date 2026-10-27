package cli

import (
	"context"
	"path/filepath"

	v1 "github.com/octacian/backlot/api/v1"
	"github.com/octacian/backlot/internal/client"
	"github.com/octacian/backlot/internal/daemon"
	"github.com/octacian/backlot/internal/plan"
	urfave "github.com/urfave/cli/v3"
)

func gatewayCommand() *urfave.Command {
	var children []*urfave.Command
	for _, name := range []string{"start", "stop", "restart", "status"} {
		flags := daemonFlags(false)
		if name == "stop" {
			flags = append(flags, &urfave.BoolFlag{Name: "force", Usage: "Relinquish unresolved native claims; survivors may still run/write"})
		}
		if name == "start" || name == "restart" {
			flags = append(flags, &urfave.StringFlag{Name: "config", Usage: "Machine configuration with explicit managed Caddy mode"})
		}
		children = append(children, &urfave.Command{Name: name, Usage: map[string]string{"start": "Start the configured managed HTTPS gateway", "stop": "Stop the recorded gateway; preserve uncertain ownership", "restart": "Stop and restart the configured managed gateway", "status": "Observe gateway status without starting it"}[name], Flags: flags, Action: func(ctx context.Context, c *urfave.Command) error {
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
			request := v1.GatewayRequest{APIVersion: v1.Version, ConfigPath: config, Force: c.Bool("force")}
			if config == "" && (c.Name == "start" || c.Name == "restart") {
				request.DefaultConfigPath, err = plan.DefaultConfigPath()
				if err != nil {
					return output(c, nil, err)
				}
			}
			if request.Force {
				selected, err := cli.Gateway(ctx, "status", v1.GatewayRequest{APIVersion: v1.Version})
				if err != nil {
					return output(c, nil, err)
				}
				request.ExpectedClaimRevision = selected.ClaimRevision
			}
			response, err := cli.Gateway(ctx, c.Name, request)
			return output(c, response, err)
		}})
	}
	return &urfave.Command{Name: "gateway", Usage: "Control the daemon-owned native local HTTPS gateway", Commands: children}
}
