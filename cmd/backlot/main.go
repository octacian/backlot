package main

import (
	"context"
	"fmt"
	"os"

	apiv1 "github.com/octacian/backlot/api/v1"
	"github.com/octacian/backlot/internal/cli"
)

// Build metadata can be supplied with linker -X flags for release builds.
var (
	version   = "dev"
	commit    = ""
	buildTime = ""
)

func main() {
	command := cli.NewCommand(apiv1.VersionResponse{
		Version: version, APIVersion: apiv1.Version, Commit: commit, BuildTime: buildTime,
	}, os.Stdout, os.Stderr)
	if err := command.Run(context.Background(), os.Args); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
