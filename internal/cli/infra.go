package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	v1 "github.com/octacian/backlot/api/v1"
	"github.com/octacian/backlot/internal/client"
	"github.com/octacian/backlot/internal/daemon"
	"github.com/octacian/backlot/internal/plan"
	urfave "github.com/urfave/cli/v3"
)

func infraCommand() *urfave.Command {
	children := []*urfave.Command{}
	for _, name := range []string{"start", "status", "stop"} {
		flags := daemonFlags(false)
		if name == "start" {
			flags = append(flags, &urfave.StringFlag{Name: "config", Usage: "Machine configuration; omitted uses retained gateway settings or the default file"})
		}
		children = append(children, &urfave.Command{Name: name, ArgsUsage: " ", Usage: map[string]string{"start": "Start the selected daemon and configured managed gateway; report partial startup", "status": "Probe infrastructure readiness and show bounded recorded blockers", "stop": "Clean up workloads and infrastructure, then verify ownership release"}[name], Description: "One selected state directory. Stop retains data, credentials, CA and history, with live progress and no aggregate deadline. Cancelling observation does not cancel accepted cleanup.\nExample: backlot infra " + name + " --json", Flags: flags, Action: infraAction})
	}
	return &urfave.Command{Name: "infra", Usage: "Start, inspect or cleanly stop infrastructure in one state directory", Commands: children}
}
func infraAction(ctx context.Context, c *urfave.Command) error {
	if c.Args().Len() != 0 {
		return output(c, nil, &v1.PlanError{Code: "invalid_arguments", Message: "infra commands take flags only"})
	}
	directory, err := directory(c)
	if err != nil {
		return output(c, nil, err)
	}
	ctx, cancel := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if c.Name == "stop" {
		if !c.Bool("json") {
			if _, err := fmt.Fprintf(c.Writer, "Infrastructure cleanup scope: %q\n", directory); err != nil {
				return err
			}
		}
		return daemon.InfraStop(ctx, directory, func(r v1.InfraStopRecord) error { return infraStopOutput(c, r) })
	}
	q := v1.InfraRequest{APIVersion: v1.Version}
	if c.Name == "start" {
		if c.String("config") != "" {
			q.ConfigPath, err = filepath.Abs(c.String("config"))
		} else {
			q.DefaultConfigPath, err = plan.DefaultConfigPath()
		}
		if err != nil {
			return output(c, nil, err)
		}
	}
	out := v1.InfraResponse{APIVersion: v1.Version, Directory: directory, Daemon: "unknown", Guidance: fmt.Sprintf("Inspect: backlot doctor --state-dir %q; no provider cleanup outside the daemon", directory)}
	if c.Name == "start" {
		_, err = daemon.Start(ctx, daemon.Options{Directory: directory})
		if err == nil {
			out.Daemon = "running"
		}
	} else if _, statErr := os.Lstat(daemon.SocketPath(directory)); os.IsNotExist(statErr) {
		// Only held ownership establishes absence; reuse the private lock check.
		err = daemon.InfraAbsent(directory)
		if err == nil {
			out.Daemon = "absent"
		}
		out.Guidance = "Daemon socket absent; retained resources and gateway readiness unknown. Use explicit infra start for daemon-owned diagnosis."
	} else {
		err = daemon.CheckAccess(directory)
	}
	if err == nil && out.Daemon != "absent" {
		cli := client.New(daemon.SocketPath(directory))
		defer cli.Close()
		observed, readErr := cli.Infra(ctx, c.Name, q)
		if observed.Daemon != "" {
			out = observed
		}
		err = readErr
	}
	if err != nil {
		if out.Error == nil {
			out.Error = observationError(err)
		}
	}
	if writeErr := infraOutput(c, out); writeErr != nil {
		return writeErr
	}
	return err
}
func infraOutput(c *urfave.Command, r v1.InfraResponse) error {
	if c.Bool("json") {
		return json.NewEncoder(c.Writer).Encode(r)
	}
	if _, err := fmt.Fprintf(c.Writer, "Infrastructure %q: ready %t\nDaemon: %s (compatible API probe when running)\n", r.Directory, r.Ready, r.Daemon); err != nil {
		return err
	}
	if g := r.Gateway; g != nil {
		if _, err := fmt.Fprintf(c.Writer, "Gateway: %s (%s; managed status probes listeners, external readiness unknown)\n", g.Status, g.Mode); err != nil {
			return err
		}
		if g.Message != "" {
			if _, err := fmt.Fprintln(c.Writer, g.Message); err != nil {
				return err
			}
		}
	}
	if r.Activity != nil {
		if err := activityOutput(c.Writer, *r.Activity); err != nil {
			return err
		}
	}
	if r.Error != nil {
		if _, err := fmt.Fprintln(c.Writer, r.Error); err != nil {
			return err
		}
	}
	_, err := fmt.Fprintln(c.Writer, r.Guidance)
	return err
}
func infraStopOutput(c *urfave.Command, r v1.InfraStopRecord) error {
	if c.Bool("json") {
		return json.NewEncoder(c.Writer).Encode(r)
	}
	switch r.Type {
	case "accepted":
		_, err := fmt.Fprintf(c.Writer, "Infrastructure shutdown accepted (%s); cleanup continues if observation ends\n", r.OperationID)
		return err
	case "phase":
		_, err := fmt.Fprintf(c.Writer, "Shutdown %s: %s%s\n", r.Phase, r.State, infraFailureCode(r.Error))
		return err
	case "target":
		_, err := fmt.Fprintf(c.Writer, "Workload %s: %s%s\n", r.Target.ID, r.Target.Outcome, infraFailureCode(r.Target.Error))
		return err
	case "cleanup":
		state := "succeeded; waiting for ownership release"
		if r.Error != nil {
			state = "failed; ownership completion not claimed"
		}
		_, err := fmt.Fprintln(c.Writer, "Cleanup:", state)
		return err
	case "completion":
		_, err := fmt.Fprintf(c.Writer, "Infrastructure %q: %s\n", r.Directory, r.State)
		if err == nil && r.Error != nil {
			_, err = fmt.Fprintln(c.Writer, r.Error)
		}
		return err
	}
	return nil
}

func infraFailureCode(detail *v1.PlanError) string {
	if detail == nil {
		return ""
	}
	return " (" + detail.Code + "; inspect activity/workload in the same scope)"
}
