package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
	_, err = cli.StopAll(ctx, v1.StopAllRequest{APIVersion: v1.Version, All: true, Contract: v1.StopAllContract}, func(event v1.StopAllRecord) error { return stopAllRecord(c, event) })
	var partial *v1.PlanError
	if errors.As(err, &partial) && partial.Code == "partial_failure" {
		return err
	}
	if err != nil {
		return output(c, nil, err)
	}
	return nil
}

func activityOutput(writer io.Writer, r v1.ActivityResponse) error {
	if _, err := fmt.Fprintf(writer, "Gateway recorded status: %s; preparing admissions: %d; admission closed: %t\n", r.GatewayStatus, r.PreparingAdmissions, r.AdmissionClosed); err != nil {
		return err
	}
	for _, e := range r.Entries {
		kind, _, _ := strings.Cut(e.Key, "/")
		if _, err := fmt.Fprintf(writer, "%s %s: %s; project %q, checkout %q, scene %q; owner %s; kept %t\n", kind, e.ID, e.Status, e.Project, e.Checkout, e.Scene, e.Owner, e.Kept); err != nil {
			return err
		}
		if e.Settlement != nil {
			if _, err := fmt.Fprintf(writer, "  Settlement: %s (%s); details: backlot inspect %s\n", e.Settlement.State, e.Settlement.Cause, e.ID); err != nil {
				return err
			}
		}
		if e.Origin != "" {
			if _, err := fmt.Fprintf(writer, "  Recorded origin: %q\n", e.Origin); err != nil {
				return err
			}
		}
		for name, port := range e.Ports {
			if _, err := fmt.Fprintf(writer, "  Recorded port %s: %d\n", name, port); err != nil {
				return err
			}
		}
		if e.LastTransition != nil {
			t := e.LastTransition
			if _, err := fmt.Fprintf(writer, "  Transition: %s -> %s (%s at %s)\n", t.From, t.To, t.Cause, t.At); err != nil {
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
			if _, err := fmt.Fprintf(writer, "  Blocks %s %s %s; next: %s\n", b.Kind, b.ServiceID, action, next); err != nil {
				return err
			}
		}
	}
	if !r.Complete {
		_, err := fmt.Fprintf(writer, "Incomplete observation; continue: backlot activity --after %s\n", r.NextAfter)
		return err
	}
	return nil
}
func stopAllRecord(c *urfave.Command, r v1.StopAllRecord) error {
	if c.Bool("json") {
		return json.NewEncoder(c.Writer).Encode(r)
	}
	if r.Type == "accepted" {
		_, err := fmt.Fprintf(c.Writer, "Bulk stop %s (daemon %s)\n", r.OperationID, r.DaemonGeneration)
		return err
	}
	if t := r.Target; t != nil {
		if t.Outcome == "stopped" {
			_, err := fmt.Fprintf(c.Writer, "%s: stopped\n", t.ID)
			return err
		}
		_, err := fmt.Fprintf(c.Writer, "%s: %s (%s); inspect: backlot inspect %s\n", t.ID, t.Outcome, t.Reason, t.ID)
		return err
	}
	s := r.Summary
	if _, err := fmt.Fprintf(c.Writer, "%d stopped, %d failed, %d unresolved; selection complete: %t; preceding admissions joined: %t\n", s.Stopped, s.Failed, s.Unresolved, s.SelectionComplete, s.AdmissionsJoined); err != nil {
		return err
	}
	if len(s.FailedIDs)+len(s.UnresolvedIDs) > 0 {
		if _, err := fmt.Fprintln(c.Writer, "Inspect:", strings.Join(append(append([]string{}, s.FailedIDs...), s.UnresolvedIDs...), ", ")); err != nil {
			return err
		}
	}
	if s.Error != nil {
		_, err := fmt.Fprintln(c.Writer, s.Error.Message)
		return err
	}
	return nil
}
