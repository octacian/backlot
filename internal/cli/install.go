//go:build darwin || linux

package cli

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	v1 "github.com/octacian/backlot/api/v1"
	"github.com/octacian/backlot/internal/daemon"
	urfave "github.com/urfave/cli/v3"
	"golang.org/x/term"
)

func installCommand() *urfave.Command {
	return &urfave.Command{Name: "install-replace", ArgsUsage: " ", Usage: "Installer-only verified replacement with selected-state shutdown consent", Description: "Used by install.sh; holds daemon ownership through atomic replacement and leaves the daemon stopped. Requires a verified release, private destination-local stage and terminal consent when running.\nExample: backlot install-replace --stage /private/bin/.stage/backlot --destination /private/bin/backlot --version v0.X.Y", Flags: append(daemonFlags(false), &urfave.StringFlag{Name: "stage", Usage: "Verified staged release executable (absolute path)", Required: true}, &urfave.StringFlag{Name: "destination", Usage: "Selected executable to replace (absolute path)", Required: true}, &urfave.StringFlag{Name: "version", Usage: "Expected canonical stable release tag", Required: true}), Action: func(ctx context.Context, c *urfave.Command) error {
		if c.Args().Len() != 0 || c.Bool("json") {
			return output(c, nil, &v1.PlanError{Code: "invalid_arguments", Message: "install-replace is a terminal/text helper and takes flags only"})
		}
		directory, err := directory(c)
		if err != nil {
			return err
		}
		ctx, cancel := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
		defer cancel()
		return daemon.Install(ctx, daemon.InstallOptions{Directory: directory, Stage: c.String("stage"), Destination: c.String("destination"), Version: c.String("version"), Consent: func(instances []v1.Instance) error {
			// Canonical tty reads must not block past the reader's cancellation check.
			tty, err := os.OpenFile("/dev/tty", os.O_RDWR|syscall.O_NONBLOCK, 0)
			if err != nil {
				return fmt.Errorf("shutdown requires a controlling terminal; installation cancelled")
			}
			defer func() { _ = tty.Close() }()
			if !term.IsTerminal(int(tty.Fd())) {
				return fmt.Errorf("shutdown requires a terminal")
			}
			reader := bufio.NewReader(terminalInput{ctx: ctx, fd: int(tty.Fd())})
			running, unresolved := installCounts(instances)
			if _, err := fmt.Fprintf(tty, "Warning: selected daemon is running; %d running/kept instances; %d unresolved cleanup records observed. Shutdown stops its resources.\n", running, unresolved); err != nil {
				return err
			}
			choices := "y/n/l"
			for {
				if _, err := fmt.Fprintf(tty, "Stop daemon and install? [%s] ", choices); err != nil {
					return err
				}
				answer, err := reader.ReadString('\n')
				if err != nil {
					return err
				}
				switch strings.ToLower(strings.TrimSpace(answer)) {
				case "y":
					return nil
				case "n":
					return fmt.Errorf("installation cancelled")
				case "l":
					if choices == "y/n/l" {
						for _, i := range instances {
							if _, err := fmt.Fprintln(tty, daemon.InstallDescription(i)); err != nil {
								return err
							}
						}
						choices = "y/n"
					}
				}
			}
		}})
	}}
}

func installCounts(instances []v1.Instance) (running, unresolved int) {

	for _, i := range instances {
		if i.Status == v1.Starting || i.Status == v1.RuntimeReady || i.Status == v1.Stopping || i.Execution != nil && i.Execution.Kept {
			running++
		}
		if i.Execution != nil && i.Execution.CleanupFailure != "" {
			unresolved++
		}
	}
	return running, unresolved
}
