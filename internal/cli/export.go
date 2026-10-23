package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	v1 "github.com/octacian/backlot/api/v1"
	urfave "github.com/urfave/cli/v3"
)

func exportCommand() *urfave.Command {
	return &urfave.Command{Name: "export", Usage: "Publish a portable report for the latest settled execution attempt", Flags: append(daemonFlags(false),
		&urfave.StringFlag{Name: "output", Required: true, Usage: "New caller-owned directory (existing destinations are rejected)"},
		&urfave.BoolFlag{Name: "allow-partial", Usage: "Publish explicitly incomplete evidence; safety checks still apply"},
		&urfave.GenericFlag{Name: "wait", Value: &exportWaitValue{}, Usage: "Wait indefinitely or use --wait=SECONDS_OR_DURATION", OnlyOnce: true}),
		Action: func(ctx context.Context, c *urfave.Command) error {
			if c.Args().Len() != 1 {
				return output(c, nil, &v1.PlanError{Code: "invalid_arguments", Message: "export requires one recorded instance ID"})
			}
			destination, err := filepath.Abs(c.String("output"))
			if err != nil {
				return output(c, nil, err)
			}
			request := v1.ExportRequest{APIVersion: v1.Version, InstanceID: c.Args().First(), Output: destination, AllowPartial: c.Bool("allow-partial"), Wait: c.IsSet("wait")}
			if value := c.Generic("wait").String(); request.Wait && value != "" && value != "true" {
				if seconds, err := strconv.ParseFloat(value, 64); err == nil {
					value = fmt.Sprintf("%gs", seconds)
				}
				d, err := time.ParseDuration(value)
				if err != nil || d <= 0 {
					return output(c, nil, &v1.PlanError{Code: "invalid_arguments", Message: "--wait accepts positive seconds or a Go duration"})
				}
				request.WaitTimeout = d.String()
			}
			cli, err := runtimeClient(c)
			if err != nil {
				return output(c, nil, err)
			}
			defer cli.Close()
			ctx, cancel := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
			defer cancel()
			response, err := cli.Export(ctx, request)
			if c.Bool("json") && response.Result.InstanceID != "" {
				if writeErr := json.NewEncoder(c.Writer).Encode(response); writeErr != nil {
					return writeErr
				}
				return err
			}
			if err != nil {
				return output(c, nil, err)
			}
			for _, warning := range response.Result.NativeOwnershipWarnings {
				if _, err := fmt.Fprintln(c.ErrWriter, "Warning:", warning.Message); err != nil {
					return err
				}
			}
			_, err = fmt.Fprintf(c.Writer, "Exported %s\nOpen %s\nEvidence complete: %t\n", response.Output, filepath.Join(response.Output, response.HTML), response.Complete)
			return err
		}}
}

// exportWaitValue uses flag.Value's optional-value convention: bare --wait sets true.
type exportWaitValue struct{ value string }

// Set records the explicit value, or true for a bare flag.
func (v *exportWaitValue) Set(value string) error { v.value = value; return nil }
func (v *exportWaitValue) String() string         { return v.value }

// Get returns the optional wait value.
func (v *exportWaitValue) Get() any { return v }

// IsBoolFlag permits a bare --wait without consuming the instance ID.
func (v *exportWaitValue) IsBoolFlag() bool { return true }
