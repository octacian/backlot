package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	v1 "github.com/octacian/backlot/api/v1"
	"github.com/octacian/backlot/internal/client"
	"github.com/octacian/backlot/internal/daemon"
	"github.com/octacian/backlot/internal/plan"
	urfave "github.com/urfave/cli/v3"
)

func daemonFlags(lease bool) []urfave.Flag {
	flags := []urfave.Flag{&urfave.StringFlag{Name: "state-dir", Usage: "Private daemon state/socket directory (absolute path)"}, &urfave.BoolFlag{Name: "json", Usage: "Emit shared typed JSON"}}
	if lease {
		flags = append(flags, &urfave.DurationFlag{Name: "retention-age", Value: 168 * time.Hour, Usage: "Completed evidence retention age"}, &urfave.Int64Flag{Name: "retention-bytes", Value: 1 << 30, Usage: "Daemon-wide evidence size cap"}, &urfave.DurationFlag{Name: "lease-duration", Value: daemon.DefaultLeaseDuration, Usage: "Disposable disconnect grace period (100ms to 24h)"})
	}
	return flags
}
func directory(c *urfave.Command) (string, error) {
	dir := c.String("state-dir")
	if dir == "" {
		return daemon.DefaultDirectory()
	}
	return filepath.Abs(dir)
}
func output(c *urfave.Command, value any, err error) error {
	if err != nil {
		if c.Bool("json") {
			detail := &v1.PlanError{Code: "daemon_failed", Message: "local daemon operation failed"}
			var typed *v1.PlanError
			if errors.As(err, &typed) {
				detail = typed
			}
			if writeErr := json.NewEncoder(c.Writer).Encode(v1.ErrorResponse{APIVersion: v1.Version, Error: *detail}); writeErr != nil {
				return writeErr
			}
		}
		return err
	}
	if c.Bool("json") {
		return json.NewEncoder(c.Writer).Encode(value)
	}
	switch result := value.(type) {
	case v1.DaemonStatusResponse:
		_, err = fmt.Fprintf(c.Writer, "Daemon %s (API %s, state %d, PID %d)\n", result.Status, result.APIVersion, result.StateVersion, result.PID)
	case v1.InstanceResponse:
		_, err = fmt.Fprintf(c.Writer, "Instance %s: %s\nOperation: %s\n", result.Instance.ID, result.Instance.Status, result.Instance.Operation.ID)
		if result.Instance.Execution == nil && err == nil {
			_, err = fmt.Fprintln(c.Writer, "Metadata only; no application work executed or readiness observed.")
		}
		if err == nil && (result.ManifestDrift || result.ConfigDrift) {
			_, err = fmt.Fprintln(c.Writer, "Manifest/config drift detected; the original snapshot is preserved.")
		}
		if err == nil && result.LeaseToken != "" {
			_, err = fmt.Fprintf(c.Writer, "Disposable lease expires %s; renew using the private token returned by JSON preparation.\n", result.Instance.LeaseExpiresAt)
		}
	case v1.DoctorResponse:
		for _, check := range result.Checks {
			if _, err = fmt.Fprintf(c.Writer, "%s: %s — %s\n", check.Name, check.Status, check.Message); err != nil {
				break
			}
		}
	}
	return err
}
func daemonCommand() *urfave.Command {
	children := []*urfave.Command{}
	for _, name := range []string{"serve", "start", "stop", "status"} {
		children = append(children, &urfave.Command{Name: name, Flags: daemonFlags(name == "serve" || name == "start"), Action: func(ctx context.Context, c *urfave.Command) error {
			if c.Args().Len() != 0 {
				return output(c, nil, &v1.PlanError{Code: "invalid_arguments", Message: "daemon commands take flags only"})
			}
			dir, err := directory(c)
			if err != nil {
				return output(c, nil, err)
			}
			options := daemon.Options{Directory: dir, LeaseDuration: c.Duration("lease-duration"), RetentionAge: c.Duration("retention-age"), RetentionBytes: c.Int64("retention-bytes")}
			if c.Name == "serve" {
				signalCtx, cancel := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
				defer cancel()
				if err := daemon.Serve(signalCtx, options); err != nil {
					return output(c, nil, err)
				}
				return nil
			}
			var response v1.DaemonStatusResponse
			if c.Name == "start" {
				response, err = daemon.Start(ctx, options)
			} else {
				if err = daemon.CheckAccess(dir); err == nil {
					cli := client.New(daemon.SocketPath(dir))
					defer cli.Close()
					if c.Name == "stop" {
						response, err = cli.Stop(ctx)
					} else {
						response, err = cli.Status(ctx)
					}
				}
			}
			return output(c, response, err)
		}})
	}
	return &urfave.Command{Name: "daemon", Usage: "Explicit local metadata daemon control", Commands: children}
}
func preparationCommands() []*urfave.Command {
	commands := []*urfave.Command{{Name: "prepare", Usage: "Persist scene metadata without executing application work", Flags: append(daemonFlags(false), &urfave.StringFlag{Name: "project"}, &urfave.StringFlag{Name: "config"}), Action: func(ctx context.Context, c *urfave.Command) error {
		if c.Args().Len() != 1 {
			return output(c, nil, &v1.PlanError{Code: "invalid_arguments", Message: "usage: backlot prepare [flags] <scene>"})
		}
		dir, err := directory(c)
		if err != nil {
			return output(c, nil, err)
		}
		cwd, err := os.Getwd()
		if err != nil {
			return output(c, nil, err)
		}
		_, project, err := plan.Discover(cwd, c.String("project"))
		if err != nil {
			return output(c, nil, err)
		}
		config := c.String("config")
		if config != "" {
			config, err = filepath.Abs(config)
			if err != nil {
				return output(c, nil, err)
			}
		}
		if err := daemon.CheckAccess(dir); err != nil {
			return output(c, nil, err)
		}
		cli := client.New(daemon.SocketPath(dir))
		defer cli.Close()
		response, err := cli.Prepare(ctx, v1.PrepareRequest{APIVersion: v1.Version, Plan: v1.PlanRequest{Scene: c.Args().First(), ProjectPath: project, ConfigPath: config}})
		return output(c, response, err)
	}}}
	for _, name := range []string{"inspect", "cancel", "renew"} {
		flags := daemonFlags(false)
		if name == "renew" {
			flags = append(flags, &urfave.StringFlag{Name: "lease-token", Usage: "Private capability from disposable preparation"})
		}
		commands = append(commands, &urfave.Command{Name: name, Usage: "Manage durable preparation metadata", Flags: flags, Action: func(ctx context.Context, c *urfave.Command) error {
			if c.Args().Len() != 1 {
				return output(c, nil, &v1.PlanError{Code: "invalid_arguments", Message: "command requires one recorded instance ID"})
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
			var response v1.InstanceResponse
			switch c.Name {
			case "inspect":
				response, err = cli.Inspect(ctx, c.Args().First())
			case "cancel":
				response, err = cli.Cancel(ctx, c.Args().First())
			case "renew":
				response, err = cli.Renew(ctx, c.Args().First(), c.String("lease-token"))
			}
			return output(c, response, err)
		}})
	}
	commands = append(commands, &urfave.Command{Name: "doctor", Usage: "Diagnose local daemon access (provider checks deferred)", Flags: daemonFlags(false), Action: func(ctx context.Context, c *urfave.Command) error {
		if c.Args().Len() != 0 {
			return output(c, nil, &v1.PlanError{Code: "invalid_arguments", Message: "doctor takes flags only"})
		}
		dir, err := directory(c)
		if err != nil {
			return output(c, nil, err)
		}
		response := daemon.Doctor(ctx, dir)
		if err := output(c, response, nil); err != nil {
			return err
		}
		if !response.Healthy {
			return &v1.PlanError{Code: "doctor_failed", Message: "local daemon checks failed; inspect diagnostic checks"}
		}
		return nil
	}})
	return commands
}
