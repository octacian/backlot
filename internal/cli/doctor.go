package cli

import (
	"context"
	"os"
	"path/filepath"

	v1 "github.com/octacian/backlot/api/v1"
	"github.com/octacian/backlot/internal/client"
	"github.com/octacian/backlot/internal/daemon"
	"github.com/octacian/backlot/internal/plan"
	urfave "github.com/urfave/cli/v3"
)

func doctorCommand() *urfave.Command {
	return &urfave.Command{Name: "doctor", ArgsUsage: " ", Description: "Without a target, check daemon access only; scene and instance targets add provider diagnostics.\nExamples:\n  backlot doctor\n  backlot doctor --scene dev\n  backlot doctor --instance INSTANCE_ID", Usage: "Diagnose daemon access, a selected scene, or a recorded instance", Flags: append(daemonFlags(false),
		&urfave.StringFlag{Name: "project", Usage: "Project directory or manifest path for --scene (default: discover from working directory)"}, &urfave.StringFlag{Name: "scene", Usage: "Scene name to diagnose with current project configuration"}, &urfave.StringFlag{Name: "config", Usage: "Machine configuration for --scene (default: per-user configuration)"}, &urfave.StringFlag{Name: "instance", Usage: "Recorded instance ID; excludes --project, --scene and --config"},
		&urfave.StringFlag{Name: "container-probe-image", Usage: "Opt in to bounded owned probe using an already-present image with the documented shell/getent/timeout/OpenSSL contract"},
		&urfave.BoolFlag{Name: "probe-public-ca", Usage: "Explicitly mount the managed public CA for the diagnostic container only"}),
		Action: func(ctx context.Context, c *urfave.Command) error {
			if c.Args().Len() != 0 {
				return output(c, nil, &v1.PlanError{Code: "invalid_arguments", Message: "doctor takes flags only"})
			}
			request := v1.DoctorRequest{APIVersion: v1.Version, ProjectPath: c.String("project"), Scene: c.String("scene"), ConfigPath: c.String("config"), InstanceID: c.String("instance"), ContainerProbeImage: c.String("container-probe-image"), ProbePublicCA: c.Bool("probe-public-ca")}
			// Reject ambiguity before discovery can read a project.
			if request.InstanceID != "" && (request.ProjectPath != "" || request.Scene != "" || request.ConfigPath != "") {
				return output(c, nil, &v1.PlanError{Code: "invalid_arguments", Message: "--instance cannot be combined with --project, --scene or --config"})
			}
			if request.Scene != "" {
				cwd, err := os.Getwd()
				if err != nil {
					return output(c, nil, err)
				}
				_, project, err := plan.Discover(cwd, request.ProjectPath)
				if err != nil {
					return output(c, nil, err)
				}
				request.ProjectPath = project
			}
			if request.ConfigPath != "" {
				path, err := filepath.Abs(request.ConfigPath)
				if err != nil {
					return output(c, nil, err)
				}
				request.ConfigPath = path
			}
			if err := request.Validate(); err != nil {
				return output(c, nil, err)
			}
			dir, err := directory(c)
			if err != nil {
				return output(c, nil, err)
			}
			response := daemon.Doctor(ctx, dir)
			if request.Scene != "" || request.InstanceID != "" {
				if response.Healthy {
					api := client.New(daemon.SocketPath(dir))
					defer api.Close()
					response, err = api.Doctor(ctx, request)
					if err != nil {
						return output(c, nil, err)
					}
				}
			}
			if err := output(c, response, nil); err != nil {
				return err
			}
			if !response.Healthy {
				return &v1.PlanError{Code: "doctor_failed", Message: "diagnostic checks failed; inspect checks and paths"}
			}
			return nil
		}}
}
