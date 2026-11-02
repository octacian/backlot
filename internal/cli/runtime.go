package cli

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	v1 "github.com/octacian/backlot/api/v1"
	"github.com/octacian/backlot/internal/client"
	"github.com/octacian/backlot/internal/daemon"
	"github.com/octacian/backlot/internal/plan"
	urfave "github.com/urfave/cli/v3"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"
)

func runtimeCommands() []*urfave.Command {
	commands := []*urfave.Command{}
	for _, name := range []string{"run", "restart", "reset"} {
		flags := append(daemonFlags(false), &urfave.StringFlag{Name: "config", Usage: "Machine configuration path"}, &urfave.StringFlag{Name: "startup-timeout", Value: defaultStartupTimeout, Usage: "Startup budget; 0s is unlimited"}, &urfave.StringFlag{Name: "job-timeout", Value: defaultJobTimeout, Usage: "Job budget; 0s is unlimited"}, &urfave.StringFlag{Name: "stop-grace", Value: defaultStopGrace, Usage: "Grace before cleanup escalation"})
		usage, args := "Ensure a persistent scene is running, or wait for disposable completion", "<scene> [terminal-job args...]"
		if name == "run" {
			flags = append(flags, &urfave.BoolFlag{Name: "attached", Usage: "Own a persistent attempt until this client ends"}, &urfave.BoolFlag{Name: "keep-on-failure", Usage: "Keep disposable resources after failure"}, &urfave.StringFlag{Name: "project", Usage: "Project directory or manifest path"})
		} else {
			usage, args = "Restart a recorded persistent instance, retaining data", "<instance-id>"
			if name == "restart" {
				flags = append(flags, &urfave.BoolFlag{Name: "allow-image-change", Usage: "Accept changed build-image IDs with retained-data compatibility risk"})
			} else {
				usage = "Stop and replace a recorded persistent instance's data, then start fresh"
			}
		}
		commands = append(commands, &urfave.Command{Name: name, Usage: usage, ArgsUsage: args, Description: map[string]string{"run": "Persistent runs return at readiness and remain running; disposable runs wait, collect evidence and clean up. --attached owns a persistent attempt and stops it on client loss.\nExample: backlot run test -- -run TestExample", "restart": "Example: backlot restart INSTANCE_ID", "reset": "Example: backlot reset INSTANCE_ID"}[name], Flags: flags, Action: runNative})
	}
	commands = append(commands, &urfave.Command{Name: "stop", ArgsUsage: "<instance-id>", Description: "Retain persistent data; release verified-unused instance networks, including already-stopped records. Cleanup failure retains unresolved networks; restore provider/ownership or remove foreign consumers and retry stop.\nExample: backlot stop INSTANCE_ID", Usage: "Stop runtime; --force acknowledges potentially live native survivors", Flags: append(daemonFlags(false), &urfave.BoolFlag{Name: "force", Usage: "Relinquish unresolved native claims; survivors may still run/write"}), Action: func(ctx context.Context, c *urfave.Command) error {
		if c.Args().Len() != 1 {
			return output(c, nil, &v1.PlanError{Code: "invalid_arguments", Message: "stop requires one recorded instance ID"})
		}
		cli, err := runtimeClient(c)
		if err != nil {
			return output(c, nil, err)
		}
		defer cli.Close()
		request := v1.StopRequest{APIVersion: v1.Version, InstanceID: c.Args().First(), Force: c.Bool("force")}
		if request.Force {
			selected, err := cli.Inspect(ctx, request.InstanceID)
			if err != nil {
				return output(c, nil, err)
			}
			if selected.Instance.Execution != nil {
				request.ExpectedAttempt = selected.Instance.Execution.Attempt
			}
		}
		response, err := cli.StopExecutionWithRequest(ctx, request)
		return output(c, response, err)
	}})
	commands = append(commands, &urfave.Command{Name: "destroy", ArgsUsage: "<instance-id>", Description: "Successful destroy irreversibly erases attempt results, logs and artifacts; export first.\nExample: backlot destroy INSTANCE_ID", Usage: "Remove runtime/data; --force waives shared ownership proof only, retaining consumer/native/private-resource fences", Flags: append(daemonFlags(false), &urfave.BoolFlag{Name: "force", Usage: "Waive shared ownership proof only; consumer, native and private-resource fences still apply"}), Action: func(ctx context.Context, c *urfave.Command) error {
		if c.Args().Len() != 1 {
			return output(c, nil, &v1.PlanError{Code: "invalid_arguments", Message: "destroy requires one recorded instance ID"})
		}
		cli, err := runtimeClient(c)
		if err != nil {
			return output(c, nil, err)
		}
		defer cli.Close()
		response, err := cli.DestroyWithRequest(ctx, v1.DestroyRequest{Contract: v1.DestroyContract, APIVersion: v1.Version, InstanceID: c.Args().First(), Force: c.Bool("force")})
		return output(c, response, err)
	}})
	commands = append(commands, &urfave.Command{Name: "logs", ArgsUsage: "<instance-id>", Description: "Reads retained logs after teardown too; follow interruption stops observation without stopping runtime. JSON emits log NDJSON.\nExample: backlot logs --follow --component app INSTANCE_ID", Usage: "Read retained component logs", Flags: append(daemonFlags(false), &urfave.BoolFlag{Name: "page", Usage: "Emit one finite bounded log page"}, &urfave.StringFlag{Name: "attempt", Usage: "Page attempt ID"}, &urfave.StringFlag{Name: "stream", Usage: "Page stream stdout or stderr"}, &urfave.StringFlag{Name: "after", Usage: "Transient page continuation"}, &urfave.IntFlag{Name: "limit", Value: 100, Usage: "Page records, 1 through 1000"}, &urfave.BoolFlag{Name: "follow", Usage: "Stream new logs until runtime settles or observation is interrupted"}, &urfave.StringFlag{Name: "component", Usage: "Filter logs by component name (default: all components)"}), Action: func(ctx context.Context, c *urfave.Command) error {
		if c.Args().Len() != 1 {
			return output(c, nil, &v1.PlanError{Code: "invalid_arguments", Message: "logs requires one recorded instance ID"})
		}
		if c.Bool("page") {
			return logPageAction(ctx, c)
		}
		if c.IsSet("attempt") || c.IsSet("stream") || c.IsSet("after") || c.IsSet("limit") {
			return output(c, nil, &v1.PlanError{Code: "invalid_arguments", Message: "attempt, stream, after and limit require --page"})
		}
		cli, err := runtimeClient(c)
		if err != nil {
			return output(c, nil, err)
		}
		defer cli.Close()
		ctx, cancel := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
		defer cancel()
		request := v1.LogsRequest{APIVersion: v1.Version, InstanceID: c.Args().First(), Component: c.String("component")}
		for {
			response, err := cli.Logs(ctx, request)
			if err != nil {
				if ctx.Err() != nil {
					return nil
				}
				return output(c, nil, err)
			}
			for _, record := range response.Records {
				if err := writeLogRecord(c, record); err != nil {
					return err
				}
			}
			for _, warning := range response.NativeOwnershipWarnings {
				if _, err := fmt.Fprintln(c.ErrWriter, "Warning:", warning.Message); err != nil {
					return err
				}
			}
			if response.Gap != "" {
				if _, err := fmt.Fprintln(c.ErrWriter, response.Gap); err != nil {
					return err
				}
			}
			previous := request.Offset
			request.Offset = response.NextOffset
			if !c.Bool("follow") {
				if request.Offset == previous || len(response.Records) == 0 {
					return nil
				}
				continue
			}
			if !response.Active && request.Offset == previous {
				return nil
			}
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(100 * time.Millisecond):
			}
		}
	}})
	return append(commands, fixtureCommands()...)
}
func runtimeClient(c *urfave.Command) (*client.Client, error) {
	dir, err := directory(c)
	if err != nil {
		return nil, err
	}
	if err := daemon.CheckAccess(dir); err != nil {
		return nil, err
	}
	return client.New(daemon.SocketPath(dir)), nil
}
func runNative(ctx context.Context, c *urfave.Command) error {
	if c.Args().Len() == 0 || c.Name != "run" && c.Args().Len() != 1 {
		return output(c, nil, &v1.PlanError{Code: "invalid_arguments", Message: "run requires a scene; restart/reset require one recorded instance ID without terminal arguments"})
	}
	terminal := c.Args().Tail()
	if c.Bool("attached") && (c.Bool("keep-on-failure") || len(terminal) != 0) {
		return output(c, nil, &v1.PlanError{Code: "invalid_arguments", Message: "--attached cannot combine with terminal arguments or --keep-on-failure"})
	}
	args := c.Args().Slice()
	cli, err := runtimeClient(c)
	if err != nil {
		return output(c, nil, err)
	}
	defer cli.Close()
	request := v1.RunRequest{AllowImageChange: c.Bool("allow-image-change"), Attached: c.Bool("attached"), APIVersion: v1.Version, Options: v1.ExecutionOptions{StartupTimeout: c.String("startup-timeout"), JobTimeout: c.String("job-timeout"), StopGrace: c.String("stop-grace"), KeepOnFailure: c.Bool("keep-on-failure")}}
	if c.Name == "restart" || c.Name == "reset" {
		previous, err := cli.Inspect(ctx, args[0])
		if err != nil {
			return output(c, nil, err)
		}
		request.InstanceID = args[0]
		request.Plan = v1.PlanRequest{Scene: previous.Instance.Plan.Scene, ProjectPath: previous.Instance.Plan.ManifestPath}
	} else {
		cwd, err := os.Getwd()
		if err != nil {
			return output(c, nil, err)
		}
		_, project, err := plan.Discover(cwd, c.String("project"))
		if err != nil {
			return output(c, nil, err)
		}
		request.Plan = v1.PlanRequest{Scene: args[0], ProjectPath: project, TerminalArgs: terminal}
	}
	if c.String("config") != "" {
		request.Plan.ConfigPath, err = filepath.Abs(c.String("config"))
		if err != nil {
			return output(c, nil, err)
		}
	}
	if request.Attached {
		return runAttached(ctx, c, cli, request.Options.StopGrace, func(start context.Context) (v1.InstanceResponse, error) { return cli.Run(start, request) })
	}
	signalCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(signals)
	signalCode := make(chan int, 1)
	go func() {
		select {
		case received := <-signals:
			code := 130
			if received == syscall.SIGTERM {
				code = 143
			}
			signalCode <- code
			cancel()
		case <-signalCtx.Done():
		}
	}()
	cancellationCode := func() int {
		select {
		case code := <-signalCode:
			return code
		default:
			return 130
		}
	}
	var response v1.InstanceResponse
	if c.Name == "restart" || c.Name == "reset" {
		if c.Name == "reset" {
			response, err = cli.Reset(context.WithoutCancel(signalCtx), request)
		} else {
			response, err = cli.Restart(context.WithoutCancel(signalCtx), request)
		}
	} else {
		response, err = cli.Run(context.WithoutCancel(signalCtx), request)
	}
	if err != nil {
		return output(c, nil, err)
	}
	id := response.Instance.ID
	attempt := startupAttempt(response.Instance)
	manifestDrift, configDrift := response.ManifestDrift, response.ConfigDrift
	token := response.LeaseToken
	for {
		if signalCtx.Err() != nil {
			grace := 10 * time.Second
			if request.Options.StopGrace != "" {
				grace, _ = time.ParseDuration(request.Options.StopGrace)
			}
			cleanup, cancel := context.WithTimeout(context.Background(), cleanupWait(grace))
			stopped, stopErr := cli.StopExecutionWithRequest(cleanup, v1.StopRequest{APIVersion: v1.Version, InstanceID: id, ExpectedAttempt: attempt})
			cancel()
			if stopErr != nil {
				return observedFailure(c, response, stopErr)
			}
			if stopped.Instance.ID != id || startupAttempt(stopped.Instance) != attempt {
				return observedFailure(c, response, startupConflict())
			}
			stopped.ManifestDrift, stopped.ConfigDrift = manifestDrift, configDrift
			response = stopped
			if err := output(c, response, nil); err != nil {
				return err
			}
			return cancelledExit(response.Instance, cancellationCode())
		}
		if attempt == "" || startupAttempt(response.Instance) != attempt {
			return observedFailure(c, response, startupConflict())
		}
		if response.Instance.Status == v1.RuntimeReady {
			return startupOutput(signalCtx, c, cli, response)
		}
		switch response.Instance.Status {
		case v1.Succeeded:
			if err := output(c, response, nil); err != nil {
				return err
			}
			return executionExit(response.Instance)
		case v1.Failed, v1.Interrupted, v1.Cancelled, v1.Stopped, v1.Destroyed:
			if err := output(c, response, nil); err != nil {
				return err
			}
			return executionExit(response.Instance)
		}
		if response.Instance.Status == v1.Stopping {
			token = ""
		}
		if token != "" {
			expiry, err := time.Parse(time.RFC3339Nano, response.Instance.LeaseExpiresAt)
			if err != nil {
				return err
			}
			if time.Until(expiry) < 15*time.Second {
				renewed, renewErr := cli.Renew(signalCtx, id, token)
				if renewErr != nil {
					if signalCtx.Err() != nil {
						continue
					}
					current, inspectErr := cli.Inspect(signalCtx, id)
					if inspectErr == nil && current.Instance.ID == id && startupAttempt(current.Instance) == attempt && current.Instance.Status != v1.Starting && current.Instance.Status != v1.RuntimeReady {
						current.ManifestDrift, current.ConfigDrift = manifestDrift, configDrift
						response = current
						token = ""
						continue
					}
					return observedFailure(c, response, renewErr)
				}
				if renewed.Instance.ID != id || startupAttempt(renewed.Instance) != attempt {
					return observedFailure(c, response, startupConflict())
				}
				renewed.ManifestDrift, renewed.ConfigDrift = manifestDrift, configDrift
				response = renewed
			}
		}
		select {
		case <-signalCtx.Done():
			grace := 10 * time.Second
			if request.Options.StopGrace != "" {
				grace, _ = time.ParseDuration(request.Options.StopGrace)
			}
			cleanup, cancel := context.WithTimeout(context.Background(), cleanupWait(grace))
			stopped, stopErr := cli.StopExecutionWithRequest(cleanup, v1.StopRequest{APIVersion: v1.Version, InstanceID: id, ExpectedAttempt: attempt})
			cancel()
			if stopErr != nil {
				return observedFailure(c, response, stopErr)
			}
			if stopped.Instance.ID != id || startupAttempt(stopped.Instance) != attempt {
				return observedFailure(c, response, startupConflict())
			}
			stopped.ManifestDrift, stopped.ConfigDrift = manifestDrift, configDrift
			response = stopped
			if err := output(c, response, nil); err != nil {
				return err
			}
			return cancelledExit(response.Instance, cancellationCode())
		case <-time.After(50 * time.Millisecond):
		}
		next, inspectErr := cli.Inspect(signalCtx, id)
		err = inspectErr
		if err != nil {
			if signalCtx.Err() != nil {
				continue
			}
			return observedFailure(c, response, err)
		}
		if next.Instance.ID != id || startupAttempt(next.Instance) != attempt {
			return observedFailure(c, response, startupConflict())
		}
		next.ManifestDrift, next.ConfigDrift = manifestDrift, configDrift
		response = next
	}
}

func writeLogRecord(c *urfave.Command, record v1.LogRecord) error {
	if c.Bool("json") {
		return json.NewEncoder(c.Writer).Encode(record)
	}
	_, err := fmt.Fprintf(c.Writer, "%s %s %s %s/%s: %s", record.Time, record.InstanceID, record.Attempt, record.Component, record.Stream, record.Message)
	if err == nil && record.Data != "" {
		var data []byte
		data, err = base64.StdEncoding.DecodeString(record.Data)
		if err == nil {
			_, err = c.Writer.Write(data)
		}
	}
	return err
}
