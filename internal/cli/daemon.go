package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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

func daemonFlags(lease bool) []urfave.Flag {
	flags := []urfave.Flag{&urfave.StringFlag{Name: "state-dir", Usage: "Private daemon state/socket directory (absolute path)"}}
	if lease {
		flags = append(flags, &urfave.DurationFlag{Name: "retention-age", Value: daemon.DefaultRetentionAge, Usage: "Completed evidence retention age"}, &urfave.Int64Flag{Name: "retention-bytes", Value: daemon.DefaultRetentionBytes, Usage: "Daemon-wide evidence size cap"}, &urfave.DurationFlag{Name: "lease-duration", Value: daemon.DefaultLeaseDuration, Usage: "Disposable disconnect grace period (100ms to 24h)"})
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
			if c.Name == "plan" {
				detail = &v1.PlanError{Code: "plan_failed", Message: "cannot resolve plan"}
			}
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
	case v1.GatewayResponse:
		for _, warning := range result.NativeOwnershipWarnings {
			if _, e := fmt.Fprintln(c.ErrWriter, "Warning:", warning.Message); e != nil {
				return e
			}
		}
		_, err = fmt.Fprintf(c.Writer, "Gateway %s (%s, HTTPS port %d)\n%s\n", result.Status, result.Mode, result.HTTPSPort, result.Message)
	case v1.SharedResponse:
		for _, service := range result.Services {
			if _, err = fmt.Fprintf(c.Writer, "Shared %s (%s/%s): %s\nAllocations: %d; pending releases: %d\n", service.ID, service.ProjectID, service.Name, service.Status, service.Allocations, service.PendingReleases); err != nil {
				return err
			}
			if service.Failure != "" {
				if _, err = fmt.Fprintln(c.Writer, "Failure:", service.Failure); err != nil {
					return err
				}
			}
		}
	case v1.DaemonStatusResponse:
		_, err = fmt.Fprintf(c.Writer, "Daemon %s (API %s, state %d, PID %d)\n", result.Status, result.APIVersion, result.StateVersion, result.PID)
	case v1.InstanceResponse:
		if result.Error != nil {
			if _, err = fmt.Fprintln(c.ErrWriter, result.Error); err != nil {
				return err
			}
		}
		if result.Instance.Execution != nil {
			for _, warning := range result.Instance.Execution.ImageChangeWarnings {
				if _, e := fmt.Fprintln(c.ErrWriter, "Warning:", warning.Message); e != nil {
					return e
				}
			}
		}
		for _, warning := range result.Instance.NativeOwnershipWarnings {
			if _, e := fmt.Fprintln(c.ErrWriter, "Warning:", warning.Message); e != nil {
				return e
			}
		}
		_, err = fmt.Fprintf(c.Writer, "Instance %s: %s\nOperation: %s\n", result.Instance.ID, result.Instance.Status, result.Instance.Operation.ID)
		if err == nil {
			err = executableOutput(c.Writer, result.Instance.Plan, "recorded selection; original source/context unrecorded")
		}
		if err == nil && result.Instance.Execution != nil && result.Instance.Execution.Origin != "" {
			_, err = fmt.Fprintf(c.Writer, "URL: %s\n", result.Instance.Execution.Origin)
		}
		if result.Instance.Execution != nil && err == nil {
			for _, r := range result.Instance.Execution.NativeCleanup {
				if _, err = fmt.Fprintf(c.Writer, "Native cleanup %s: %s (%s)\n%s\n", r.Component, r.Confidence, r.Reason, r.Detail); err != nil {
					break
				}
				if r.ResourceRelease != "" {
					_, err = fmt.Fprintf(c.Writer, "Native resource release: %s (historical confidence and collection unchanged)\n", r.ResourceRelease)
					if err != nil {
						break
					}
				}
			}
		}
		if result.Instance.Execution != nil && err == nil {
			execution := result.Instance.Execution
			outcome := string(execution.Outcome)
			if outcome == "" {
				outcome = "unknown"
			}
			if _, err = fmt.Fprintf(c.Writer, "Attempt: %s\nOutcome: %s; cancelled: %t; kept resources: %t; evidence expired: %t\n", execution.Attempt, outcome, execution.Cancelled, execution.Kept, execution.EvidenceExpired); err != nil {
				return err
			}
			if len(execution.NativeCleanup) > 0 {
				if _, err = fmt.Fprintln(c.Writer, "Native cleanup is bounded observation, not universal descendant absence."); err != nil {
					return err
				}
			}
			if err = outcomeOutput(c.Writer, execution.TerminalExitCode, execution.Components, execution.Failure, execution.CollectionFailure, execution.CleanupFailure); err != nil {
				return err
			}
			if execution.FailureDetail != nil {
				if _, err = fmt.Fprintf(c.Writer, "Failure detail [%s]: %s\n", execution.FailureDetail.Code, execution.FailureDetail); err != nil {
					return err
				}
			}
			for _, detail := range execution.CleanupDetails {
				if _, err = fmt.Fprintf(c.Writer, "Cleanup detail [%s]: %s\n", detail.Code, &detail); err != nil {
					return err
				}
			}
			if _, err = fmt.Fprintf(c.Writer, "Inspect: backlot inspect %s\nLogs: backlot logs %s\nExport: backlot export %s --output NEW_DIRECTORY\n", result.Instance.ID, result.Instance.ID, result.Instance.ID); err != nil {
				return err
			}
			if execution.Kept || execution.CleanupFailure != "" {
				if _, err = fmt.Fprintf(c.Writer, "Cleanup: inspect ownership, then backlot stop %s or backlot destroy %s for explicit data removal\n", result.Instance.ID, result.Instance.ID); err != nil {
					return err
				}
			}
		}
		if result.Instance.Execution == nil && err == nil {
			_, err = fmt.Fprintln(c.Writer, "Metadata only; no application work executed or readiness observed.")
		}
		if err == nil && (result.ManifestDrift || result.ConfigDrift) {
			_, err = fmt.Fprintln(c.Writer, "Manifest/config drift detected; the original snapshot is preserved.")
		}
		if err == nil && result.Instance.Operation.Reason != "" {
			_, err = fmt.Fprintln(c.Writer, "Operation reason:", result.Instance.Operation.Reason)
		}
		if err == nil && result.LeaseToken != "" {
			_, err = fmt.Fprintf(c.Writer, "Disposable lease expires %s; renew using the private token returned by JSON preparation.\n", result.Instance.LeaseExpiresAt)
		}
	case v1.ListResponse:
		for _, instance := range result.Instances {
			if _, err = fmt.Fprintf(c.Writer, "%s  %s/%s  %s  %s\n", instance.ID, instance.Project, instance.Scene, instance.Status, instance.Origin); err != nil {
				break
			}
		}
		if err == nil && result.NextAfterID != "" {
			_, err = fmt.Fprintf(c.Writer, "Continue with --after %s\n", result.NextAfterID)
		}
	case v1.DoctorResponse:
		for _, path := range result.Paths {
			if _, err = fmt.Fprintf(c.Writer, "%s: %q (%s)\n", path.Name, path.Path, path.State); err != nil {
				return err
			}
		}
		for _, check := range result.Checks {
			if _, err = fmt.Fprintf(c.Writer, "%s [%s]: %s — %s\n", check.Name, check.Context, check.Status, check.Message); err != nil {
				break
			}
		}
	}
	return err
}
func daemonCommand() *urfave.Command {
	children := []*urfave.Command{daemonConfigurationCommand()}
	for _, name := range []string{"serve", "start", "stop", "status"} {
		children = append(children, &urfave.Command{Name: name, ArgsUsage: " ", Description: "Example: backlot daemon " + name, Usage: map[string]string{"serve": "Serve the local daemon in the foreground until shutdown or signal", "start": "Start the local daemon and wait for compatible access", "stop": "Request daemon shutdown (acknowledgment does not prove exit)", "status": "Observe daemon connectivity and API/state compatibility"}[name], Flags: daemonFlags(name == "serve" || name == "start"), Action: func(ctx context.Context, c *urfave.Command) error {
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
	commands := []*urfave.Command{{Name: "prepare", Description: "Preparation does not establish runtime readiness; use run or scene start to execute.\nExample: backlot prepare dev", ArgsUsage: "<scene>", Usage: "Persist scene metadata without executing application work", Flags: append(daemonFlags(false), &urfave.StringFlag{Name: "project", Usage: "Project directory or manifest path (default: discover from working directory)"}, &urfave.StringFlag{Name: "config", Usage: "Machine configuration path (default: per-user configuration)"}), Action: func(ctx context.Context, c *urfave.Command) error {
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
	for _, name := range []string{"inspect", "status", "cancel", "renew"} {
		flags := daemonFlags(false)
		if name == "status" {
			flags = append(flags, statusFollowFlags()...)
		}
		if name == "renew" {
			flags = append(flags, &urfave.StringFlag{Name: "lease-token", Usage: "Private capability from disposable preparation"})
		}
		commands = append(commands, &urfave.Command{Name: name, Description: "Example: backlot " + name + map[string]string{"inspect": " INSTANCE_ID", "status": " --follow INSTANCE_ID", "cancel": " INSTANCE_ID", "renew": " --lease-token TOKEN INSTANCE_ID"}[name], ArgsUsage: "<instance-id>", Usage: map[string]string{"inspect": "Read recorded metadata and execution without changing runtime", "status": "Read a recorded instance; --follow samples without lifecycle ownership", "cancel": "Cancel metadata preparation without executing work", "renew": "Renew a disposable lease with its private capability"}[name], Flags: flags, Action: func(ctx context.Context, c *urfave.Command) error {
			if c.Args().Len() != 1 {
				return output(c, nil, &v1.PlanError{Code: "invalid_arguments", Message: "command requires one recorded instance ID"})
			}
			if c.Name == "status" {
				if _, err := statusFollowOptions(c); err != nil {
					return output(c, nil, err)
				}
				if c.Bool("follow") {
					return followStatus(ctx, c, v1.SceneRequest{APIVersion: v1.Version, Target: v1.SceneTarget{InstanceID: c.Args().First()}})
				}
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
			case "inspect", "status":
				response, err = cli.Inspect(ctx, c.Args().First())
			case "cancel":
				response, err = cli.Cancel(ctx, c.Args().First())
			case "renew":
				response, err = cli.Renew(ctx, c.Args().First(), c.String("lease-token"))
			}
			return output(c, response, err)
		}})
	}
	commands = append(commands, doctorCommand())
	return commands
}

// lastObserved reports only an already validated snapshot and strips transient disclosure.
func lastObserved(response v1.InstanceResponse, err error) v1.InstanceResponse {
	response.LeaseToken, response.Attachment, response.StartupDetails = "", nil, nil
	response.Error = observationError(err)
	return response
}

func observationError(err error) *v1.PlanError {
	detail := &v1.PlanError{Code: "observation_failed", Message: "operation result could not be observed"}
	var typed *v1.PlanError
	if errors.As(err, &typed) {
		detail = typed
	}
	return &v1.PlanError{Code: detail.Code, Message: "Last observed snapshot; current/final state is unknown. " + detail.Message + "; inspect the recorded ID with the same --state-dir before recovery"}
}

func observedFailure(c *urfave.Command, response v1.InstanceResponse, err error) error {
	if writeErr := output(c, lastObserved(response, err), nil); writeErr != nil {
		return writeErr
	}
	return err
}

func outcomeOutput(w io.Writer, exit *int, components []v1.ComponentResult, failure, collection, cleanup string) error {
	exitText := "unknown"
	if exit != nil {
		exitText = fmt.Sprint(*exit)
	}
	if _, err := fmt.Fprintln(w, "Terminal exit:", exitText); err != nil {
		return err
	}
	for _, component := range components {
		code := "unknown"
		if component.ExitCode != nil {
			code = fmt.Sprint(*component.ExitCode)
		}
		if _, err := fmt.Fprintf(w, "Component %s: %s (exit %s)\n", component.Name, component.Status, code); err != nil {
			return err
		}
	}
	for _, item := range []struct{ label, reason string }{{"Runtime failure", failure}, {"Collection failure", collection}, {"Cleanup failure", cleanup}} {
		if item.reason != "" {
			if _, err := fmt.Fprintln(w, item.label+":", item.reason); err != nil {
				return err
			}
		}
	}
	return nil
}
