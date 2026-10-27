package main

import (
	"bytes"
	"encoding/json"
	apiv1 "github.com/octacian/backlot/api/v1"
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"strings"
	"testing"
)

func TestBuildVersion(t *testing.T) {
	for _, test := range []struct {
		name, module, revision, modified, want string
	}{
		{name: "absent", want: "dev"},
		{name: "installed tag", module: "v0.1.0", want: "v0.1.0"},
		{name: "installed revision", module: "v0.0.0-20261023120000-abcdef012345", want: "v0.0.0-20261023120000-abcdef012345"},
		{name: "checkout", module: "v0.1.0", revision: "abcdef", want: "dev"},
		{name: "dirty checkout", module: "v0.1.0+dirty", revision: "abcdef", modified: "true", want: "dev-dirty"},
	} {
		t.Run(test.name, func(t *testing.T) {
			info := &debug.BuildInfo{Main: debug.Module{Version: test.module}}
			if test.revision != "" {
				info.Settings = append(info.Settings, debug.BuildSetting{Key: "vcs.revision", Value: test.revision})
			}
			info.Settings = append(info.Settings, debug.BuildSetting{Key: "vcs.modified", Value: test.modified})
			got := buildVersion(info)
			if got.Version != test.want || got.Commit != test.revision || got.BuildTime != "" {
				t.Fatalf("unexpected identity: %+v", got)
			}
		})
	}
	if got := buildVersion(nil); got.Version != "dev" || got.Commit != "" {
		t.Fatal(got)
	}
	version, commit, buildTime = "v0.1.0", "full-sha", "2026-10-23T12:00:00Z"
	t.Cleanup(func() { version, commit, buildTime = "dev", "", "" })
	got := buildVersion(&debug.BuildInfo{Main: debug.Module{Version: "v0.0.0"}})
	if got.Version != version || got.Commit != commit || got.BuildTime != buildTime {
		t.Fatal(got)
	}
}

func TestExecutableMachineFailuresAndTextExceptions(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "backlot")
	if out, err := exec.Command("go", "build", "-o", binary, ".").CombinedOutput(); err != nil {
		t.Fatal(err, string(out))
	}
	for _, args := range [][]string{{"--json", "restart", "--project", "private-value-canary", "id"}, {"--json", "export"}, {"--json", "scene", "unknown"}, {"--json", "version", "extra"}, {"--json", "status", "--interval", "private-value-canary"}} {
		var stdout, stderr bytes.Buffer
		command := exec.Command(binary, args...)
		command.Stdout, command.Stderr = &stdout, &stderr
		err := command.Run()
		var response apiv1.ErrorResponse
		if err == nil || json.Unmarshal(stdout.Bytes(), &response) != nil || response.Error.Code != "invalid_arguments" || strings.Count(stderr.String(), "\n") != 1 || strings.Contains(stdout.String()+stderr.String(), "private-value-canary") {
			t.Fatal(args, err, stdout.String(), stderr.String())
		}
	}
	for _, args := range [][]string{{"--json", "licenses"}, {"--json", "run", "--help"}, {"--json", "daemon"}} {
		data, err := exec.Command(binary, args...).Output()
		if err != nil || len(data) == 0 || json.Valid(data) {
			t.Fatal(args, err, string(data))
		}
	}
}
