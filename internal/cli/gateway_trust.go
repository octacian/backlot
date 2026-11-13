package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	v1 "github.com/octacian/backlot/api/v1"
	"github.com/octacian/backlot/internal/client"
	"github.com/octacian/backlot/internal/daemon"
	"github.com/octacian/backlot/internal/trust"
	urfave "github.com/urfave/cli/v3"
)

func gatewayTrustCommand() *urfave.Command {
	var children []*urfave.Command
	for _, name := range []string{"status", "install", "retire"} {
		flags := daemonFlags(false)
		if name != "status" {
			flags = append(flags, &urfave.StringFlag{Name: "fingerprint", Usage: "Exact lowercase DER SHA-256 root fingerprint", Required: true})
		}
		if name == "status" {
			flags = append(flags, &urfave.StringFlag{Name: "fingerprint", Usage: "Observe this exact retained root without a live source daemon"})
		}
		if name == "install" {
			flags = append(flags, &urfave.BoolFlag{Name: "new-tracking", Usage: "Preserve old evidence and track only new validated effects; old unknown effects stay manual"})
		}
		if name == "retire" {
			flags = append(flags, &urfave.BoolFlag{Name: "acknowledge-impact", Usage: "Accept breakage of known and unknown consumers; does not waive ownership"}, &urfave.BoolFlag{Name: "replacement", Usage: "Require verified OS trust in the selected daemon's replacement root first"})
		}
		children = append(children, &urfave.Command{Name: name, ArgsUsage: " ", Description: "Example: backlot gateway trust " + name + map[string]string{"status": " --json", "install": " --fingerprint ROOT_SHA256 --json", "retire": " --fingerprint OLD_ROOT_SHA256 --acknowledge-impact --json"}[name], Usage: map[string]string{"status": "Observe exact managed CA host/effective trust without effects", "install": "Explicitly authorize stock host trust installation and distro hooks", "retire": "Retire only exact receipt-proven Backlot effects; preserve external trust"}[name], Flags: flags, Action: func(ctx context.Context, c *urfave.Command) error {
			if c.Args().Len() != 0 {
				return output(c, nil, &v1.PlanError{Code: "invalid_arguments", Message: "gateway trust commands take flags only"})
			}
			ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
			defer stop()
			tracking, err := trust.Directory()
			if err != nil {
				return output(c, nil, err)
			}
			var ca v1.GatewayCA
			if (c.Name != "retire" || c.Bool("replacement")) && (c.Name != "status" || c.String("fingerprint") == "") {
				dir, err := directory(c)
				if err != nil {
					return output(c, nil, err)
				}
				if err = daemon.CheckAccess(dir); err != nil {
					return output(c, nil, err)
				}
				cli := client.New(daemon.SocketPath(dir))
				defer cli.Close()
				ca, err = cli.GatewayCA(ctx)
				if err != nil {
					return output(c, nil, err)
				}
			}
			var result v1.GatewayTrustResult
			switch c.Name {
			case "status":
				if c.String("fingerprint") != "" {
					result, err = trust.StoredStatus(ctx, tracking, c.String("fingerprint"))
				} else {
					result = trust.Status(ctx, ca)
				}
			case "install":
				if c.String("fingerprint") != ca.Fingerprint {
					return output(c, nil, &v1.PlanError{Code: "gateway_ca", Message: "selected managed CA fingerprint changed or mismatches; inspect trust status before authorizing installation"})
				}
				result, err = trust.Install(ctx, tracking, ca, c.Bool("new-tracking"))
			case "retire":
				var replacement *v1.GatewayCA
				if c.Bool("replacement") {
					replacement = &ca
				}
				result, err = trust.Retire(ctx, tracking, c.String("fingerprint"), c.Bool("acknowledge-impact"), replacement)
			}
			result.Retained, result.Warnings = appendTracking(tracking, result.Warnings)
			if c.Name == "install" && result.CA.Fingerprint != "" {
				result.Effective = v1.TrustObservation{Status: "unknown", Store: result.Host.Store, Context: "daemon-effective", Message: "source daemon could not be freshly observed after mutation"}
				dir, dirErr := directory(c)
				if dirErr == nil {
					observer := client.New(daemon.SocketPath(dir))
					current, observeErr := observer.GatewayCA(ctx)
					observer.Close()
					if observeErr == nil && current.Fingerprint == result.CA.Fingerprint {
						result.Effective = current.DaemonEffective
					}
				}
			}
			if err != nil && result.Error == nil {
				result.Error = &v1.PlanError{Code: "gateway_trust", Message: "foreground trust has a tracking/cleanup error; inspect retained receipt and OS state before retrying"}
				result.Outcome = "partial"
			}
			// Partial effects remain visible in a typed result even when the exit fails.
			if result.Error != nil {
				if writeErr := output(c, result, nil); writeErr != nil {
					return writeErr
				}
				return &ExitError{Code: 1}
			}
			return output(c, result, err)
		}})
	}
	return &urfave.Command{Name: "trust", Usage: "Explicit foreground host CA trust; browsers and applications remain separate", Commands: children}
}

func gatewayTrustOutput(w io.Writer, r v1.GatewayTrustResult) error {
	if _, err := fmt.Fprintf(w, "CA SHA-256: %s\nSource: %s\nHost trust: %s (%s)\nEffective trust: %s\nOutcome: %s; entry: %s\nReceipt: %s\n%s\n", r.CA.Fingerprint, r.CA.StateDirectory, r.Host.Status, r.Host.Store, r.Effective.Status, r.Outcome, r.Entry, r.Receipt, r.Host.Message); err != nil {
		return err
	}
	for _, warning := range r.Warnings {
		if _, err := fmt.Fprintln(w, "Warning:", warning); err != nil {
			return err
		}
	}
	for _, receipt := range r.Retained {
		if _, err := fmt.Fprintf(w, "Retained %s: %s, owned effects: %t; sources: %v\n", receipt.Fingerprint, receipt.Phase, receipt.OwnedEffects, receipt.Sources); err != nil {
			return err
		}
	}
	if r.Error != nil {
		_, err := fmt.Fprintln(w, "Failure:", r.Error.Message)
		return err
	}
	return nil
}

func appendTracking(dir string, warnings []string) ([]v1.TrustReceiptSummary, []string) {
	retained, damaged := trust.Retained(dir)
	return retained, append(warnings, damaged...)
}
