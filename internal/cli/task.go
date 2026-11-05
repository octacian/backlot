package cli

import (
	"context"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	v1 "github.com/octacian/backlot/api/v1"
	urfave "github.com/urfave/cli/v3"
)

func taskCommand() *urfave.Command {
	return &urfave.Command{Name: "task", Usage: "Run declared native tasks against an already-ready scene", Commands: []*urfave.Command{{Name: "run", ArgsUsage: "TASK --scene SCENE|--instance ID [-- literal-args...]", Usage: "Run an enabled task with an independent client lease and result", Description: "Example: backlot task run admin --scene dev -- --dry-run\nRequires accepted task configuration and a ready recorded parent. Never starts or restarts it. Inspect/status, renew and stop address the returned task instance ID; logs/history use that same ID. Outputs held by parent or peers reject immediately. Client loss expires the task lease; cancellation never stops the parent.", Flags: append(daemonFlags(false), &urfave.StringFlag{Name: "scene", Usage: "Recorded persistent scene"}, &urfave.StringFlag{Name: "instance", Usage: "Exact recorded persistent parent ID"}, &urfave.StringFlag{Name: "project", Usage: "Exact project directory or manifest for named targeting"}, &urfave.StringFlag{Name: "job-timeout", Value: defaultJobTimeout, Usage: "Finite job budget; 0s is unlimited subject to client lease"}, &urfave.StringFlag{Name: "stop-grace", Value: defaultStopGrace, Usage: "Grace before cleanup escalation"}), Action: runDeclaredTask}}}
}

func runDeclaredTask(ctx context.Context, c *urfave.Command) error {
	if c.Args().Len() < 1 || (c.String("scene") == "") == (c.String("instance") == "") || c.String("instance") != "" && c.IsSet("project") {
		return output(c, nil, &v1.PlanError{Code: "invalid_arguments", Message: "task run requires TASK and exactly one --scene SCENE or --instance ID; project applies only to scene targeting"})
	}
	target := v1.SceneTarget{Scene: c.String("scene"), InstanceID: c.String("instance")}
	if target.InstanceID == "" {
		var err error
		target.CWD, err = os.Getwd()
		if err != nil {
			return output(c, nil, err)
		}
		if c.IsSet("project") {
			target.ProjectPath, err = filepath.Abs(c.String("project"))
			if err != nil {
				return output(c, nil, err)
			}
		}
	}
	request := v1.TaskRunRequest{APIVersion: v1.Version, Target: target, Task: c.Args().First(), Args: c.Args().Tail(), Options: v1.ExecutionOptions{JobTimeout: c.String("job-timeout"), StopGrace: c.String("stop-grace")}}
	if err := request.Validate(); err != nil {
		return output(c, nil, err)
	}
	cli, err := runtimeClient(c)
	if err != nil {
		return output(c, nil, err)
	}
	defer cli.Close()
	signalCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(signals)
	code := make(chan int, 1)
	go func() {
		select {
		case sig := <-signals:
			n := 130
			if sig == syscall.SIGTERM {
				n = 143
			}
			code <- n
			cancel()
		case <-signalCtx.Done():
		}
	}()
	response, err := cli.RunTask(context.WithoutCancel(signalCtx), request)
	if err != nil {
		return output(c, nil, err)
	}
	return waitExecution(signalCtx, c, cli, request.Options, response, func() int {
		select {
		case n := <-code:
			return n
		default:
			return 130
		}
	})
}
