package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	v1 "github.com/octacian/backlot/api/v1"
	urfave "github.com/urfave/cli/v3"
	"golang.org/x/term"
)

func orphanCommand() *urfave.Command {
	return &urfave.Command{Name: "orphan", Usage: "Discover and explicitly clean missing-checkout instances", Commands: []*urfave.Command{cleanupCommand(true)}}
}
func cleanupCommand(orphans bool) *urfave.Command {
	name, usage := "destroy", "Preview and confirm destruction of all instances owned by one checkout"
	if orphans {
		name, usage = "cleanup", "Preview and confirm cleanup of eligible missing-checkout instances"
	}
	flags := append(daemonFlags(false), &urfave.StringFlag{Name: "apply-preview", Usage: "Apply exactly this reviewed preview digest"})
	if !orphans {
		flags = append(flags, &urfave.StringFlag{Name: "checkout", Usage: "Existing exact checkout root"}, &urfave.StringFlag{Name: "checkout-id", Usage: "Recorded immutable checkout ID, including deleted checkouts"})
	}
	return &urfave.Command{Name: name, Usage: usage, ArgsUsage: " ", Description: "Permanently removes owned data, shared allocations, attempt results, logs and artifacts. Shared servers, caller-owned exports and unowned data survive. Export first; incomplete evidence may need export --allow-partial. Human terminals preview then ask y/N; JSON and noninteractive output preview only. --apply-preview DIGEST applies the reviewed selection after revalidation. Apply streams live progress; Ctrl-C requests safe cancellation, without undoing completed deletions. A missing checkout may be on an unplugged drive; review its path before applying.\nExample: backlot " + map[bool]string{false: "checkout destroy", true: "orphan cleanup"}[orphans] + " --json", Flags: flags, Action: func(ctx context.Context, c *urfave.Command) error {
		if c.Args().Len() != 0 {
			return output(c, nil, &v1.PlanError{Code: "invalid_arguments", Message: "cleanup accepts flags only"})
		}
		for _, name := range []string{"apply-preview", "checkout", "checkout-id"} {
			if c.IsSet(name) && c.String(name) == "" {
				return output(c, nil, &v1.PlanError{Code: "invalid_arguments", Message: "cleanup selectors and preview digest require nonempty values"})
			}
		}
		q := v1.CleanupRequest{APIVersion: v1.Version, Contract: v1.CleanupContract, Orphans: orphans, ApplyPreview: c.String("apply-preview")}
		if !orphans {
			q.CheckoutID = c.String("checkout-id")
			if c.IsSet("checkout") {
				var err error
				q.CheckoutPath, err = filepath.Abs(c.String("checkout"))
				if err != nil {
					return output(c, nil, err)
				}
			}
			if q.CheckoutID == "" && q.CheckoutPath == "" {
				var err error
				q.CWD, err = os.Getwd()
				if err != nil {
					return output(c, nil, err)
				}
			}
		}
		if err := q.Validate(); err != nil {
			return output(c, nil, &v1.PlanError{Code: "invalid_arguments", Message: "select one --checkout or --checkout-id, and a valid --apply-preview digest when applying"})
		}
		client, err := runtimeClient(c)
		if err != nil {
			return output(c, nil, err)
		}
		defer client.Close()
		signals, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
		defer stop()
		if q.ApplyPreview == "" {
			summary, err := client.Cleanup(signals, q, func(r v1.CleanupRecord) error { return cleanupRecord(c, r) })
			if err != nil {
				return output(c, nil, err)
			}
			if c.Bool("json") || !term.IsTerminal(int(os.Stdin.Fd())) || !cleanupOutputTerminal(c.Writer) || summary.Targets == 0 {
				return nil
			}
			yes, err := confirmCleanup(signals, os.Stdin, c.ErrWriter)
			if err != nil {
				return err
			}
			if !yes {
				return nil
			}
			q.ApplyPreview = summary.Preview
		}
		if err := signals.Err(); err != nil {
			return &ExitError{Code: 130}
		}
		// Keep the observation alive while a signal is delivered through the explicit
		// cancellation endpoint after acceptance. Earlier interruption cancels the
		// pending exchange; a hidden acceptance must remain explicitly unknown.
		observation, finish := context.WithCancel(context.WithoutCancel(ctx))
		defer finish()
		accepted := make(chan v1.CleanupRecord, 1)
		done := make(chan struct{})
		watchDone := make(chan struct{})
		go func() {
			defer close(watchDone)
			select {
			case <-done:
				return
			case <-signals.Done():
			}
			var record v1.CleanupRecord
			select {
			case <-done:
				return
			case record = <-accepted:
			default:
				finish()
				_, _ = fmt.Fprintln(c.ErrWriter, "Cancellation before observed acceptance; acceptance/completion may be unknown. Inspect selected state and known targets.")
				return
			}
			_, err := client.CancelCleanup(observation, v1.CleanupCancelRequest{APIVersion: v1.Version, Contract: v1.CleanupContract, DaemonGeneration: record.DaemonGeneration, OperationID: record.OperationID})
			if err != nil {
				_, _ = fmt.Fprintln(c.ErrWriter, "Cancellation not acknowledged; completion unknown. Inspect selected state and known targets.")
			} else {
				_, _ = fmt.Fprintln(c.ErrWriter, "Cancellation acknowledged; waiting for safe cleanup settlement.")
			}
		}()
		_, err = client.Cleanup(observation, q, func(r v1.CleanupRecord) error {
			if r.Type == "accepted" {
				accepted <- r
			}
			return cleanupRecord(c, r)
		})
		close(done)
		finish()
		<-watchDone
		var detail *v1.PlanError
		if errors.As(err, &detail) && (detail.Code == "partial_failure" || detail.Code == "cancelled") {
			if detail.Code == "cancelled" {
				return &ExitError{Code: 130}
			}
			return err
		}
		if err != nil {
			return output(c, nil, err)
		}
		return nil
	}}
}
func cleanupOutputTerminal(w io.Writer) bool {
	f, ok := w.(*os.File)
	return ok && term.IsTerminal(int(f.Fd()))
}
func confirmCleanup(ctx context.Context, reader *os.File, writer io.Writer) (bool, error) {
	if _, err := fmt.Fprint(writer, "Apply this cleanup? [y/N] "); err != nil {
		return false, err
	}
	input := bufio.NewReader(terminalInput{ctx: ctx, fd: int(reader.Fd())})
	line, err := input.ReadString('\n')
	if err != nil {
		if errors.Is(err, io.EOF) {
			return false, nil
		}
		return false, err
	}
	return strings.EqualFold(strings.TrimSpace(line), "y"), nil
}
func cleanupRecord(c *urfave.Command, r v1.CleanupRecord) error {
	if c.Bool("json") {
		return json.NewEncoder(c.Writer).Encode(r)
	}
	switch r.Type {
	case "preview_target":
		t := r.Target
		if _, err := fmt.Fprintf(c.Writer, "%s: %s/%s (%s); checkout %s at %q; missing path: %t\n", t.ID, t.Project, t.Scene, t.Status, t.CheckoutID, t.CheckoutPath, t.MissingPath); err != nil {
			return err
		}
		for _, b := range t.Blockers {
			if _, err := fmt.Fprintf(c.Writer, "  Blocked: %s; inspect: backlot inspect %s\n", b, t.ID); err != nil {
				return err
			}
		}
		for _, resource := range t.Resources {
			if _, err := fmt.Fprintf(c.Writer, "  Permanently remove %s: %s\n", resource.Kind, resource.Name); err != nil {
				return err
			}
		}
		partial := ""
		if t.ExportPartial {
			partial = " --allow-partial"
		}
		state := ""
		if c.String("state-dir") != "" {
			state = " --state-dir " + cleanupShellQuote(c.String("state-dir"))
		}
		_, err := fmt.Fprintf(c.Writer, "  Export first: backlot export%s%s --output BUNDLE %s (stop/join active producers first)\n", state, partial, t.ID)
		return err
	case "preview":
		if _, err := fmt.Fprintln(c.Writer, "Preserved: shared servers, caller-owned exports and unowned external data."); err != nil {
			return err
		}
		command := "backlot checkout destroy"
		if c.Name == "cleanup" {
			command = "backlot orphan cleanup"
		} else if c.String("checkout-id") != "" {
			command += " --checkout-id " + c.String("checkout-id")
		} else if c.String("checkout") != "" {
			command += " --checkout " + cleanupShellQuote(c.String("checkout"))
		}
		if c.String("state-dir") != "" {
			command += " --state-dir " + cleanupShellQuote(c.String("state-dir"))
		}
		_, err := fmt.Fprintf(c.Writer, "%d reviewed targets. Apply: %s --apply-preview %s\n", r.Summary.Targets, command, r.Summary.Preview)
		return err
	case "accepted":
		_, err := fmt.Fprintf(c.Writer, "Cleanup %s accepted (daemon %s)\n", r.OperationID, r.DaemonGeneration)
		return err
	case "progress":
		_, err := fmt.Fprintf(c.Writer, "%s: stopping and joining consumers\n", r.InstanceID)
		return err
	case "resource":
		_, err := fmt.Fprintf(c.Writer, "%s: %s %s %s\n", r.InstanceID, r.Resource.Kind, r.Resource.Name, r.Resource.Outcome)
		return err
	case "target":
		_, err := fmt.Fprintf(c.Writer, "%s: %s %s\n", r.InstanceID, r.Outcome, r.Reason)
		return err
	case "summary":
		s := r.Summary
		_, err := fmt.Fprintf(c.Writer, "%d done, %d failed, %d unresolved, %d unattempted; cancelled: %t\n", s.Done, s.Failed, s.Unresolved, s.Unattempted, s.Cancelled)
		return err
	}
	return nil
}

func cleanupShellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'" }
