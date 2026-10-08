package main

import (
	"context"
	"fmt"
	"os"

	apiv1 "github.com/octacian/backlot/api/v1"
	"github.com/octacian/backlot/internal/cli"
	"github.com/octacian/backlot/internal/native"
)

// Build metadata can be supplied with linker -X flags for release builds.
var (
	version   = "dev"
	commit    = ""
	buildTime = ""
)

func main() {
	if len(os.Args) == 2 && os.Args[1] == "__native-anchor" {
		if native.Anchor() != nil {
			os.Exit(1)
		}
		return
	}
	if len(os.Args) == 2 && os.Args[1] == "__native-supervisor" {
		if err := native.Supervise(); err != nil {
			os.Exit(1)
		}
		return
	}
	command := cli.NewCommand(apiv1.VersionResponse{
		Version: version, APIVersion: apiv1.Version, Commit: commit, BuildTime: buildTime,
	}, os.Stdout, os.Stderr)
	if err := command.Run(context.Background(), os.Args); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
