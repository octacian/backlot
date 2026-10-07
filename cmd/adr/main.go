package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/octacian/backlot/internal/adr"
	"github.com/urfave/cli/v3"
)

func main() {
	if err := command().Run(context.Background(), os.Args); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func command() *cli.Command {
	return &cli.Command{
		Name: "adr", Usage: "Create and verify architecture decision records",
		Flags: []cli.Flag{&cli.StringFlag{Name: "dir", Value: "docs/adr", Usage: "ADR directory relative to the working directory"}},
		Action: func(_ context.Context, command *cli.Command) error {
			if command.Args().Len() > 0 {
				return fmt.Errorf("unknown command %q", command.Args().First())
			}
			return cli.ShowAppHelp(command)
		},
		Commands: []*cli.Command{
			{
				Name: "create", Usage: "Create a proposed record: adr create \"Decision title\"",
				Action: func(_ context.Context, command *cli.Command) error {
					if command.Args().Len() == 0 {
						return fmt.Errorf("usage: adr create \"Decision title\"")
					}
					file, err := adr.Create(command.String("dir"), strings.Join(command.Args().Slice(), " "), time.Now())
					if err != nil {
						return err
					}
					_, err = fmt.Fprintln(command.Writer, file)
					return err
				},
			},
			{
				Name: "verify", Usage: "Verify metadata, prose, and supersession relationships",
				Action: func(_ context.Context, command *cli.Command) error {
					if command.Args().Len() != 0 {
						return fmt.Errorf("verify takes no arguments")
					}
					result, err := adr.Verify(command.String("dir"))
					if err != nil {
						return err
					}
					if len(result.Errors) > 0 {
						return fmt.Errorf("%s", strings.Join(result.Errors, "\n"))
					}
					_, err = fmt.Fprintf(command.Writer, "Verified %d ADR(s).\n", result.Count)
					return err
				},
			},
		},
	}
}
