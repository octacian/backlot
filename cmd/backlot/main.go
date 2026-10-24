package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"runtime/debug"

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
	info, _ := debug.ReadBuildInfo()
	command := cli.NewCommand(buildVersion(info), os.Stdout, os.Stderr)
	if err := command.Run(context.Background(), os.Args); err != nil {
		var exit *cli.ExitError
		if errors.As(err, &exit) {
			os.Exit(exit.Code)
		}
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func buildVersion(info *debug.BuildInfo) apiv1.VersionResponse {
	result := apiv1.VersionResponse{Version: version, APIVersion: apiv1.Version, Commit: commit, BuildTime: buildTime}
	if info == nil || version != "dev" {
		return result
	}
	if info.Main.Version != "" && info.Main.Version != "(devel)" {
		result.Version = info.Main.Version
	}
	for _, setting := range info.Settings {
		switch setting.Key {
		case "vcs.revision":
			// A checkout build is development even when Go infers a nearby tag.
			if result.Version != "dev-dirty" {
				result.Version = "dev"
			}
			if result.Commit == "" {
				result.Commit = setting.Value
			}
		case "vcs.modified":
			if setting.Value == "true" {
				result.Version = "dev-dirty"
			}
		}
	}
	return result
}
