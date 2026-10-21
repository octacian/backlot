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
	urfave "github.com/urfave/cli/v3"
)

func sceneCommand() *urfave.Command {
	children := []*urfave.Command{}
	for _, name := range []string{"start", "status", "stop"} {
		flags := append(daemonFlags(false), &urfave.StringFlag{Name: "project", Usage: "Exact project directory or recognized manifest path"})
		if name == "start" {
			flags = append(flags, &urfave.StringFlag{Name: "config"}, &urfave.StringFlag{Name: "startup-timeout", Value: "5m"}, &urfave.StringFlag{Name: "job-timeout", Value: "30m"}, &urfave.StringFlag{Name: "stop-grace", Value: "10s"})
		}
		if name == "status" {
			flags = append(flags, statusFollowFlags()...)
		}
		children = append(children, &urfave.Command{Name: name, Usage: "Target a persistent scene through the daemon", Flags: flags, Action: sceneAction})
	}
	return &urfave.Command{Name: "scene", Usage: "Start, observe or stop recorded persistent scenes", Commands: children}
}

func sceneAction(ctx context.Context, c *urfave.Command) error {
	if c.Args().Len() != 1 {
		return output(c, nil, &v1.PlanError{Code: "invalid_arguments", Message: "scene command requires one scene name"})
	}
	target := v1.SceneTarget{Scene: c.Args().First()}
	var err error
	target.CWD, err = os.Getwd()
	if err != nil {
		return output(c, nil, err)
	}
	if c.String("project") != "" {
		target.ProjectPath, err = filepath.Abs(c.String("project"))
		if err != nil {
			return output(c, nil, err)
		}
	}
	if err := target.Validate(); err != nil {
		return output(c, nil, err)
	}
	if c.Name == "status" {
		if _, err := statusFollowOptions(c); err != nil {
			return output(c, nil, err)
		}
		if c.Bool("follow") {
			return followStatus(ctx, c, v1.SceneRequest{APIVersion: v1.Version, Target: target})
		}
	}
	cli, err := runtimeClient(c)
	if err != nil {
		return output(c, nil, err)
	}
	defer cli.Close()
	request := v1.SceneRequest{APIVersion: v1.Version, Target: target}
	if c.Name != "start" && !c.Bool("follow") {
		var response v1.SceneResponse
		if c.Name == "stop" {
			response, err = cli.StopScene(ctx, request)
		} else {
			response, err = cli.SceneStatus(ctx, request)
		}
		return sceneOutput(c, response, err)
	}
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(signals)
	return startScene(ctx, c, cli, target, signals)
}

func statusFollowFlags() []urfave.Flag {
	return []urfave.Flag{&urfave.BoolFlag{Name: "follow", Usage: "Observe initial and changed sampled status"}, &urfave.DurationFlag{Name: "interval", Value: time.Second, Usage: "Sample interval (100ms through 1m)"}, &urfave.DurationFlag{Name: "duration", Value: 30 * time.Minute, Usage: "Total follow budget (1s through 24h)"}}
}

func statusFollowOptions(c *urfave.Command) (client.SceneFollowOptions, error) {
	options := client.SceneFollowOptions{Interval: c.Duration("interval"), Duration: c.Duration("duration")}
	if !c.Bool("follow") && (c.IsSet("interval") || c.IsSet("duration")) {
		return options, &v1.PlanError{Code: "invalid_arguments", Message: "--interval and --duration require --follow"}
	}
	if c.Bool("follow") {
		if options.Interval == 0 || options.Duration == 0 {
			return options, &v1.PlanError{Code: "invalid_observation", Message: "follow interval and duration must be positive and bounded"}
		}
		return options, options.Validate()
	}
	return options, nil
}

func followStatus(ctx context.Context, c *urfave.Command, request v1.SceneRequest) error {
	options, err := statusFollowOptions(c)
	if err != nil {
		return output(c, nil, err)
	}
	cli, err := runtimeClient(c)
	if err != nil {
		return sceneFollowError(c, err)
	}
	defer cli.Close()
	// One joined signal owner for both named and exact-ID observation. Cancellation
	// only stops sampling; it never invokes runtime cleanup or lease renewal.
	signalCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(signals)
	done := make(chan struct{})
	exitCode := 130
	go func() {
		defer close(done)
		select {
		case received := <-signals:
			if received == syscall.SIGTERM {
				exitCode = 143
			}
			cancel()
		case <-signalCtx.Done():
		}
	}()
	err = cli.FollowScene(signalCtx, request, options, func(record v1.SceneFollowRecord) error { return sceneRecord(c, record) })
	cancel()
	<-done
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return &ExitError{Code: exitCode}
	}
	return err
}

func sceneFollowError(c *urfave.Command, err error) error {
	detail := &v1.PlanError{Code: "observation_failed", Message: "cannot begin status observation"}
	var typed *v1.PlanError
	if errors.As(err, &typed) {
		detail = typed
	}
	if writeErr := sceneRecord(c, v1.SceneFollowRecord{APIVersion: v1.Version, Type: "error", Time: time.Now().UTC().Format(time.RFC3339Nano), Error: detail}); writeErr != nil {
		return writeErr
	}
	return err
}

func sceneRecord(c *urfave.Command, record v1.SceneFollowRecord) error {
	if c.Bool("json") {
		return json.NewEncoder(c.Writer).Encode(record)
	}
	if record.Snapshot != nil {
		return sceneOutput(c, *record.Snapshot, nil)
	}
	if record.Error != nil {
		_, err := fmt.Fprintln(c.ErrWriter, record.Error.Error())
		return err
	}
	_, err := fmt.Fprintf(c.Writer, "Observation %s\n", record.Reason)
	return err
}

func sceneOutput(c *urfave.Command, response v1.SceneResponse, err error) error {
	if err != nil {
		return output(c, nil, err)
	}
	if c.Bool("json") {
		return json.NewEncoder(c.Writer).Encode(response)
	}
	if _, err := fmt.Fprintln(c.Writer, "Scene:", response.Outcome); err != nil {
		return err
	}
	if response.Result != nil {
		if err := output(c, *response.Result, nil); err != nil {
			return err
		}
		if result := response.Result.Instance.Execution; result != nil && result.Failure != "" {
			_, err := fmt.Fprintln(c.Writer, "Runtime failure:", result.Failure)
			return err
		}
	}
	return nil
}

func startScene(ctx context.Context, c *urfave.Command, cli *client.Client, target v1.SceneTarget, signals <-chan os.Signal) error {
	request := v1.SceneStartRequest{APIVersion: v1.Version, CWD: target.CWD, Plan: v1.PlanRequest{Scene: target.Scene, ProjectPath: target.ProjectPath}, Options: v1.ExecutionOptions{StartupTimeout: c.String("startup-timeout"), JobTimeout: c.String("job-timeout"), StopGrace: c.String("stop-grace")}}
	if c.String("config") != "" {
		var err error
		request.Plan.ConfigPath, err = filepath.Abs(c.String("config"))
		if err != nil {
			return output(c, nil, err)
		}
	}
	response, err := cli.StartScene(context.WithoutCancel(ctx), request)
	if err != nil {
		return sceneOutput(c, response, err)
	}
	id := response.Result.Instance.ID
	checkout := response.Checkout
	canonicalTarget := response.CanonicalTarget
	driftManifest, driftConfig := response.Result.ManifestDrift, response.Result.ConfigDrift
	cancelStart := func(code int) error {
		grace, _ := time.ParseDuration(request.Options.StopGrace)
		cleanup, cancel := context.WithTimeout(context.Background(), grace+30*time.Second)
		defer cancel()
		result, err := cli.StopExecution(cleanup, id)
		if err != nil {
			return output(c, nil, err)
		}
		response.Result = &result
		if err := sceneOutput(c, response, nil); err != nil {
			return err
		}
		return cancelledExit(result.Instance, code)
	}
	for {
		instance := response.Result.Instance
		select {
		case received := <-signals:
			code := 130
			if received == syscall.SIGTERM {
				code = 143
			}
			return cancelStart(code)
		default:
		}
		switch instance.Status {
		case v1.RuntimeReady:
			return sceneOutput(c, response, nil)
		case v1.Failed, v1.Interrupted, v1.Cancelled, v1.Stopped, v1.Destroyed:
			if err := sceneOutput(c, response, nil); err != nil {
				return err
			}
			return executionExit(instance)
		}
		timer := time.NewTimer(50 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return cancelStart(130)
		case received := <-signals:
			timer.Stop()
			code := 130
			if received == syscall.SIGTERM {
				code = 143
			}
			return cancelStart(code)
		case <-timer.C:
		}
		next, inspectErr := cli.SceneStatus(ctx, v1.SceneRequest{APIVersion: v1.Version, Target: v1.SceneTarget{InstanceID: instance.ID}})
		err = inspectErr
		if err != nil {
			if ctx.Err() != nil {
				return cancelStart(130)
			}
			return sceneOutput(c, response, err)
		}
		response = next
		if response.Result == nil {
			return output(c, nil, &v1.PlanError{Code: "not_found", Message: "accepted startup instance is unavailable"})
		}
		response.Checkout = checkout
		response.CanonicalTarget = canonicalTarget
		response.Result.ManifestDrift, response.Result.ConfigDrift = driftManifest, driftConfig
	}
}
