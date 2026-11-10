package cli

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	v1 "github.com/octacian/backlot/api/v1"
	urfave "github.com/urfave/cli/v3"
)

func activityCommand() *urfave.Command {
	return &urfave.Command{Name: "activity", ArgsUsage: " ", Usage: "Inspect one bounded recorded activity page across the selected daemon scope", Description: "No provider probes: ports, origins and infrastructure status are recorded intent. Owner observations and settlement are separate. Incomplete pages never prove global absence.\nExample: backlot activity --json", Flags: append(daemonFlags(false), &urfave.StringFlag{Name: "after", Usage: "Previous page next_after cursor"}, &urfave.IntFlag{Name: "limit", Value: 100, Usage: "Page results, 1 through 100"}), Action: func(ctx context.Context, c *urfave.Command) error {
		if c.Args().Len() != 0 || c.Int("limit") < 1 {
			return output(c, nil, &v1.PlanError{Code: "invalid_arguments", Message: "activity takes flags only and a positive limit"})
		}
		q := v1.ActivityRequest{APIVersion: v1.Version, After: c.String("after"), Limit: c.Int("limit")}
		if err := q.Validate(); err != nil {
			return output(c, nil, err)
		}
		cli, err := runtimeClient(c)
		if err != nil {
			return output(c, nil, err)
		}
		defer cli.Close()
		out, err := cli.Activity(ctx, q)
		return output(c, out, err)
	}}
}
func stopAllAction(ctx context.Context, c *urfave.Command) error {
	if c.Args().Len() != 0 || c.IsSet("force") {
		return output(c, nil, &v1.PlanError{Code: "invalid_arguments", Message: "stop --all accepts no instance ID or force option"})
	}
	cli, err := runtimeClient(c)
	if err != nil {
		return output(c, nil, err)
	}
	defer cli.Close()
	ctx, cancel := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer cancel()
	out, err := cli.StopAll(ctx, v1.StopAllRequest{APIVersion: v1.Version, All: true})
	if out.OperationID == "" {
		return output(c, nil, err)
	}
	if writeErr := output(c, out, nil); writeErr != nil {
		return writeErr
	}
	return err
}

func activityOutput(c *urfave.Command, r v1.ActivityResponse) error {
	if _, err := fmt.Fprintf(c.Writer, "Gateway recorded status: %s; preparing admissions: %d; admission closed: %t\n", r.GatewayStatus, r.PreparingAdmissions, r.AdmissionClosed); err != nil {
		return err
	}
	for _, e := range r.Entries {
		kind, _, _ := strings.Cut(e.Key, "/")
		if _, err := fmt.Fprintf(c.Writer, "%s %s: %s; project %q, checkout %q, scene %q; owner %s; kept %t\n", kind, e.ID, e.Status, e.Project, e.Checkout, e.Scene, e.Owner, e.Kept); err != nil {
			return err
		}
		if e.Settlement != nil {
			if _, err := fmt.Fprintf(c.Writer, "  Settlement: %s (%s); details: backlot inspect %s\n", e.Settlement.State, e.Settlement.Cause, e.ID); err != nil {
				return err
			}
		}
		if e.Origin != "" {
			if _, err := fmt.Fprintf(c.Writer, "  Recorded origin: %q\n", e.Origin); err != nil {
				return err
			}
		}
		for name, port := range e.Ports {
			if _, err := fmt.Fprintf(c.Writer, "  Recorded port %s: %d\n", name, port); err != nil {
				return err
			}
		}
		if e.LastTransition != nil {
			t := e.LastTransition
			if _, err := fmt.Fprintf(c.Writer, "  Transition: %s -> %s (%s at %s)\n", t.From, t.To, t.Cause, t.At); err != nil {
				return err
			}
		}
		for _, b := range e.Blockers {
			action := "stop/restart"
			if !b.Stop {
				action = "destroy"
			} else if b.Destroy {
				action += "/destroy"
			}
			next := "backlot stop " + e.ID
			if b.Destroy && !b.Stop {
				next = "backlot destroy " + e.ID + " (explicitly removes retained data)"
			}
			if _, err := fmt.Fprintf(c.Writer, "  Blocks %s %s %s; next: %s\n", b.Kind, b.ServiceID, action, next); err != nil {
				return err
			}
		}
	}
	if !r.Complete {
		_, err := fmt.Fprintf(c.Writer, "Incomplete observation; continue: backlot activity --after %s\n", r.NextAfter)
		return err
	}
	return nil
}
func stopAllOutput(c *urfave.Command, r v1.StopAllResponse) error {
	if _, err := fmt.Fprintf(c.Writer, "Bulk stop %s (daemon %s); selection complete: %t (%s); preceding admissions joined: %t\n", r.OperationID, r.DaemonGeneration, r.SelectionComplete, r.SelectionFailure, r.AdmissionsJoined); err != nil {
		return err
	}
	for _, t := range r.Targets {
		if _, err := fmt.Fprintf(c.Writer, "%s: %s; settlement %s (%s); collection failed: %t\n", t.ID, t.Outcome, t.Settlement.State, t.SettlementObservation, t.CollectionFailed); err != nil {
			return err
		}
	}
	if r.Error != nil {
		_, err := fmt.Fprintln(c.Writer, r.Error.Message)
		return err
	}
	return nil
}
