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
	"slices"
	"strings"
	"syscall"
	"time"
)

func runtimeCommands() []*urfave.Command {
	commands := []*urfave.Command{}
	for _, name := range []string{"run", "restart"} {
		commands = append(commands, &urfave.Command{Name: name, SkipFlagParsing: true, Usage: "Execute native commands whose descendants stay in their supervised group", Flags: append(daemonFlags(false), &urfave.StringFlag{Name: "project"}, &urfave.StringFlag{Name: "config"}, &urfave.StringFlag{Name: "startup-timeout", Value: "5m"}, &urfave.StringFlag{Name: "job-timeout", Value: "30m"}, &urfave.StringFlag{Name: "stop-grace", Value: "10s"}), Action: runNative})
	}
	commands = append(commands, &urfave.Command{Name: "stop", Usage: "Stop native runtime with verified group cleanup", Flags: daemonFlags(false), Action: func(ctx context.Context, c *urfave.Command) error {
		if c.Args().Len() != 1 {
			return fmt.Errorf("stop requires one recorded instance ID")
		}
		cli, err := runtimeClient(c)
		if err != nil {
			return output(c, nil, err)
		}
		defer cli.Close()
		response, err := cli.StopExecution(ctx, c.Args().First())
		return output(c, response, err)
	}})
	commands = append(commands, &urfave.Command{Name: "logs", Usage: "Read retained component logs", Flags: append(daemonFlags(false), &urfave.BoolFlag{Name: "follow"}, &urfave.StringFlag{Name: "component"}), Action: func(ctx context.Context, c *urfave.Command) error {
		if c.Args().Len() != 1 {
			return fmt.Errorf("logs requires one recorded instance ID")
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
				if c.Bool("json") {
					err = json.NewEncoder(c.Writer).Encode(record)
				} else {
					_, err = fmt.Fprintf(c.Writer, "%s %s/%s: %s", record.Time, record.Component, record.Stream, record.Message)
					if err == nil && len(record.Data) > 0 {
						var data []byte
						data, err = base64.StdEncoding.DecodeString(record.Data)
						if err == nil {
							_, err = c.Writer.Write(data)
						}
					}
				}
				if err != nil {
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
	return commands
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
	if c.Args().Len() == 1 && (c.Args().First() == "--help" || c.Args().First() == "-h") {
		return urfave.ShowSubcommandHelp(c)
	}
	target, terminal, err := parseRuntimeArgs(c)
	if err != nil {
		return output(c, nil, err)
	}
	args := []string{target}
	cli, err := runtimeClient(c)
	if err != nil {
		return output(c, nil, err)
	}
	defer cli.Close()
	request := v1.RunRequest{APIVersion: v1.Version, Options: v1.ExecutionOptions{StartupTimeout: c.String("startup-timeout"), JobTimeout: c.String("job-timeout"), StopGrace: c.String("stop-grace")}}
	if c.Name == "restart" {
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
	signalCtx, cancel := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer cancel()
	var response v1.InstanceResponse
	if c.Name == "restart" {
		response, err = cli.Restart(context.WithoutCancel(signalCtx), request)
	} else {
		response, err = cli.Run(context.WithoutCancel(signalCtx), request)
	}
	if err != nil {
		return output(c, nil, err)
	}
	id := response.Instance.ID
	token := response.LeaseToken
	for {
		if signalCtx.Err() != nil {
			grace := 10 * time.Second
			if request.Options.StopGrace != "" {
				grace, _ = time.ParseDuration(request.Options.StopGrace)
			}
			cleanup, cancel := context.WithDeadline(context.Background(), time.Now().Add(grace).Add(10*time.Second))
			response, err = cli.StopExecution(cleanup, id)
			cancel()
			if err != nil {
				return output(c, nil, err)
			}
			if err := output(c, response, nil); err != nil {
				return err
			}
			return &v1.PlanError{Code: "cancelled", Message: "native execution cancelled"}
		}
		if response.Instance.Status == v1.RuntimeReady {
			return output(c, response, nil)
		}
		switch response.Instance.Status {
		case v1.Succeeded:
			return output(c, response, nil)
		case v1.Failed, v1.Interrupted, v1.Cancelled, v1.Stopped:
			if err := output(c, response, nil); err != nil {
				return err
			}
			return &v1.PlanError{Code: "execution_failed", Message: "native execution failed or was interrupted; inspect execution result"}
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
					if inspectErr == nil && current.Instance.Status != v1.Starting && current.Instance.Status != v1.RuntimeReady {
						response = current
						token = ""
						continue
					}
					return output(c, nil, renewErr)
				}
				response = renewed
			}
		}
		select {
		case <-signalCtx.Done():
			grace := 10 * time.Second
			if request.Options.StopGrace != "" {
				grace, _ = time.ParseDuration(request.Options.StopGrace)
			}
			cleanup, cancel := context.WithDeadline(context.Background(), time.Now().Add(grace).Add(10*time.Second))
			response, err = cli.StopExecution(cleanup, id)
			cancel()
			if err != nil {
				return output(c, nil, err)
			}
			if err := output(c, response, nil); err != nil {
				return err
			}
			return &v1.PlanError{Code: "cancelled", Message: "native execution cancelled"}
		case <-time.After(50 * time.Millisecond):
		}
		response, err = cli.Inspect(signalCtx, id)
		if err != nil {
			if signalCtx.Err() != nil {
				continue
			}
			return output(c, nil, err)
		}
	}
}

func parseRuntimeArgs(c *urfave.Command) (string, []string, error) {
	args := c.Args().Slice()
	var terminal []string
	if index := slices.Index(args, "--"); index >= 0 {
		terminal = slices.Clone(args[index+1:])
		args = args[:index]
	}
	var target string
	seen := map[string]bool{}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if !strings.HasPrefix(arg, "--") {
			if target != "" {
				return "", nil, &v1.PlanError{Code: "invalid_arguments", Message: "forward terminal job arguments after --"}
			}
			target = arg
			continue
		}
		if seen[arg] {
			return "", nil, &v1.PlanError{Code: "invalid_arguments", Message: "duplicate runtime flag"}
		}
		seen[arg] = true
		switch arg {
		case "--json":
			if err := c.Set("json", "true"); err != nil {
				return "", nil, err
			}
		case "--state-dir", "--project", "--config", "--startup-timeout", "--job-timeout", "--stop-grace":
			if i+1 >= len(args) {
				return "", nil, &v1.PlanError{Code: "invalid_arguments", Message: "runtime flag requires a value"}
			}
			i++
			if err := c.Set(strings.TrimPrefix(arg, "--"), args[i]); err != nil {
				return "", nil, err
			}
		default:
			return "", nil, &v1.PlanError{Code: "invalid_arguments", Message: "unknown runtime flag"}
		}
	}
	if target == "" {
		return "", nil, &v1.PlanError{Code: "invalid_arguments", Message: "run requires a scene; restart requires an instance ID"}
	}
	if c.Name == "restart" && len(terminal) > 0 {
		return "", nil, &v1.PlanError{Code: "invalid_arguments", Message: "persistent restart does not accept terminal arguments"}
	}
	return target, terminal, nil
}
