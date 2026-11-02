package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	v1 "github.com/octacian/backlot/api/v1"
	urfave "github.com/urfave/cli/v3"
)

func TestPublicCommandConfiguration(t *testing.T) {
	root := NewCommand(v1.VersionResponse{}, io.Discard, io.Discard)
	actions, jsonFlags := 0, 0
	var visit func(*urfave.Command)
	visit = func(c *urfave.Command) {
		if c.SkipFlagParsing || c.OnUsageError == nil || c.Usage == "" {
			t.Fatalf("incomplete public command %s", c.Name)
		}
		if len(c.Commands) == 0 {
			actions++
		}
		for _, flag := range c.Flags {
			if flag.Names()[0] == "json" {
				jsonFlags++
				if c != root {
					t.Fatal("local JSON flag", c.Name)
				}
			}
			singleton := true
			switch f := flag.(type) {
			case *urfave.BoolFlag:
				singleton = f.OnlyOnce
			case *urfave.StringFlag:
				singleton = f.OnlyOnce
			case *urfave.IntFlag:
				singleton = f.OnlyOnce
			case *urfave.Int64Flag:
				singleton = f.OnlyOnce
			case *urfave.DurationFlag:
				singleton = f.OnlyOnce
			case *urfave.GenericFlag:
				singleton = f.OnlyOnce
			case *urfave.StringSliceFlag:
				if c.Name != "setup" || f.OnlyOnce {
					t.Fatal("unexpected repeatable flag", c.Name, f.Name)
				}
			default:
				t.Fatalf("unreviewed flag type %T", flag)
			}
			if !singleton {
				t.Fatal("singleton not enforced", c.Name, flag.Names())
			}
		}
		for _, child := range c.Commands {
			visit(child)
		}
	}
	visit(root)
	if actions != 44 || jsonFlags != 1 || root.ExitErrHandler == nil {
		t.Fatal(actions, jsonFlags)
	}
	for _, c := range root.Commands {
		if c.Name != "run" && c.Name != "restart" && c.Name != "reset" {
			continue
		}
		flags := map[string]bool{}
		for _, f := range c.Flags {
			flags[f.Names()[0]] = true
		}
		for name, want := range map[string]bool{"project": c.Name == "run", "attached": c.Name == "run", "keep-on-failure": c.Name == "run", "allow-image-change": c.Name == "restart"} {
			if flags[name] != want {
				t.Fatal(c.Name, name, flags[name])
			}
		}
	}
}

func TestUsageHooksPreserveSafeContextAndOneDocument(t *testing.T) {
	for _, tc := range []struct{ diagnostic, want string }{
		{"flag provided but not defined: -project", "unknown option --project"},
		{"flag needs an argument: config", "option --config requires a value"},
		{`Required flag "output" not set`, "required option --output is missing"},
		{`invalid value "private-canary" for flag -interval: parse error`, "invalid value for --interval"},
		{`invalid value "private-canary" for flag -json: can't duplicate this flag`, "duplicate option --json"},
		{"unrecognized private-canary\nFORGED", "invalid command flags or arguments"},
	} {
		t.Run(tc.want, func(t *testing.T) {
			var out, diag bytes.Buffer
			c := NewCommand(v1.VersionResponse{}, &out, &diag)
			c.Action = func(ctx context.Context, c *urfave.Command) error {
				return c.OnUsageError(ctx, c, errors.New(tc.diagnostic), false)
			}
			err := c.Run(context.Background(), []string{"backlot", "--json"})
			var response v1.ErrorResponse
			if err == nil || json.Unmarshal(out.Bytes(), &response) != nil || response.Error.Code != "invalid_arguments" || !strings.Contains(response.Error.Message, tc.want) || strings.Contains(out.String()+err.Error()+diag.String(), "private-canary") || diag.Len() != 0 {
				t.Fatal(err, out.String(), diag.String())
			}
		})
	}
}

func TestSemanticFailuresAndGroupErrorsUseGlobalJSON(t *testing.T) {
	for _, args := range [][]string{
		{"version", "extra"}, {"licenses", "extra"}, {"plan"}, {"project", "setup", "extra"},
		{"run"}, {"restart", "id", "tail"}, {"reset", "id", "tail"}, {"stop"}, {"destroy"}, {"logs"}, {"fixtures"}, {"secret", "id"},
		{"prepare"}, {"inspect"}, {"status"}, {"cancel"}, {"renew"}, {"doctor", "extra"}, {"list", "extra"},
		{"scene", "start"}, {"scene", "status"}, {"scene", "stop"},
		{"daemon", "serve", "extra"}, {"daemon", "start", "extra"}, {"daemon", "stop", "extra"}, {"daemon", "status", "extra"},
		{"gateway", "start", "extra"}, {"gateway", "stop", "extra"}, {"gateway", "restart", "extra"}, {"gateway", "status", "extra"},
		{"shared", "list", "extra"}, {"shared", "inspect"}, {"shared", "start"}, {"shared", "stop"}, {"shared", "restart"}, {"shared", "destroy"},
		{"export"}, {"unknown"}, {"project", "unknown"}, {"scene", "unknown"}, {"daemon", "unknown"}, {"gateway", "unknown"}, {"shared", "unknown"},
	} {
		t.Run(strings.Join(args, "/"), func(t *testing.T) {
			var out, diag bytes.Buffer
			err := NewCommand(v1.VersionResponse{}, &out, &diag).Run(context.Background(), append([]string{"backlot", "--json"}, args...))
			var response v1.ErrorResponse
			if err == nil || json.Unmarshal(out.Bytes(), &response) != nil || response.Error.Code != "invalid_arguments" || strings.Contains(out.String(), "USAGE:") {
				t.Fatal(err, out.String(), diag.String())
			}
		})
	}
}

func TestHumanTextExceptionsAndUsefulHelp(t *testing.T) {
	for _, args := range [][]string{nil, {"project"}, {"scene"}, {"daemon"}, {"gateway"}, {"shared"}, {"run", "--help"}, {"project", "setup", "--help"}, {"licenses"}} {
		var out, diag bytes.Buffer
		if err := NewCommand(v1.VersionResponse{}, &out, &diag).Run(context.Background(), append([]string{"backlot", "--json"}, args...)); err != nil || out.Len() == 0 || json.Valid(out.Bytes()) {
			t.Fatal(args, err, out.String())
		}
		if len(args) > 1 && args[0] == "run" {
			for _, text := range []string{"<scene>", "terminal-job args", "--startup-timeout", "--attached"} {
				if !strings.Contains(out.String(), text) {
					t.Fatal("missing help", text)
				}
			}
		}
	}
}

func TestHumanResultsShareOutcomesAndPropagateOutputFailure(t *testing.T) {
	code := 23
	execution := &v1.ExecutionResult{Attempt: "attempt", TerminalExitCode: &code, Kept: true, EvidenceExpired: true, Components: []v1.ComponentResult{{Name: "test", Status: "failed", ExitCode: &code}}, Failure: "runtime failed", CollectionFailure: "collection failed", CleanupFailure: "cleanup failed", FailureDetail: &v1.PlanError{Code: "failed_detail", Message: "detail"}, CleanupDetails: []v1.PlanError{{Code: "cleanup_detail", Message: "cleanup detail"}}}
	instance := v1.InstanceResponse{APIVersion: v1.Version, Instance: v1.Instance{ID: "instance", Status: v1.Failed, Execution: execution}, ManifestDrift: true}
	for _, value := range []any{instance, v1.SharedResponse{Services: []v1.SharedStatus{{ID: "shared", Name: "db", Status: "uncertain", Failure: "shared failure", PendingReleases: 2}}}} {
		var out bytes.Buffer
		c := &urfave.Command{Writer: &out, ErrWriter: &out}
		if err := output(c, value, nil); err != nil {
			t.Fatal(err)
		}
		want := []string{"shared", "uncertain", "shared failure", "pending releases: 2"}
		if _, ok := value.(v1.InstanceResponse); ok {
			want = []string{"attempt", "Terminal exit: 23", "test: failed (exit 23)", "Runtime failure: runtime failed", "Collection failure: collection failed", "Cleanup failure: cleanup failed", "kept resources: true", "evidence expired: true", "failed_detail", "cleanup_detail", "drift detected", "backlot inspect instance", "backlot logs instance", "backlot export instance", "backlot destroy instance"}
		}
		for _, text := range want {
			if !strings.Contains(out.String(), text) {
				t.Fatal("missing fact", text, out.String())
			}
		}
		if err := output(&urfave.Command{Writer: failingWriter{}, ErrWriter: failingWriter{}}, value, nil); !errors.Is(err, io.ErrClosedPipe) {
			t.Fatal("lost write failure", err)
		}
	}
	var out bytes.Buffer
	if err := outcomeOutput(&out, nil, []v1.ComponentResult{{Name: "pending", Status: "starting"}}, "", "", ""); err != nil || strings.Count(out.String(), "unknown") != 2 {
		t.Fatal(err, out.String())
	}
}

func TestLastObservedStripsDisclosure(t *testing.T) {
	result := lastObserved(v1.InstanceResponse{LeaseToken: "lease-canary", Attachment: &v1.Attachment{}, StartupDetails: &v1.StartupSummary{}, ManifestDrift: true}, fmt.Errorf("transport-private-canary"))
	data, err := json.Marshal(result)
	if err != nil || result.Error == nil || result.Error.Code != "observation_failed" || !result.ManifestDrift || strings.Contains(string(data), "canary") || result.StartupDetails != nil || result.Attachment != nil || result.LeaseToken != "" {
		t.Fatal(err, string(data))
	}
}

func TestUnhealthyDoctorKeepsFindingsInOneResult(t *testing.T) {
	var out, diag bytes.Buffer
	err := NewCommand(v1.VersionResponse{}, &out, &diag).Run(context.Background(), []string{"backlot", "--json", "doctor", "--state-dir", t.TempDir()})
	var response v1.DoctorResponse
	if err == nil || json.Unmarshal(out.Bytes(), &response) != nil || response.Healthy || len(response.Checks) == 0 || len(response.Paths) == 0 {
		t.Fatal("doctor lost unhealthy result", err, out.String())
	}
}

// These assertions cover Backlot registration metadata, not framework help formatting.
func TestPublicHelpMetadata(t *testing.T) {
	var visit func(*urfave.Command, string)
	visit = func(c *urfave.Command, path string) {
		if c.Usage == "" {
			t.Errorf("%s missing usage", path)
		}
		for _, flag := range c.Flags {
			if flag.(urfave.DocGenerationFlag).GetUsage() == "" {
				t.Errorf("%s --%s missing usage", path, flag.Names()[0])
			}
		}
		if len(c.Commands) == 0 {
			if c.ArgsUsage == "" || !strings.Contains(c.Description, "Example") {
				t.Errorf("%s missing target shape or example", path)
			}
		}
		for _, child := range c.Commands {
			visit(child, path+" "+child.Name)
		}
	}
	root := NewCommand(v1.VersionResponse{}, io.Discard, io.Discard)
	visit(root, "backlot")
	for _, tc := range []struct {
		path    []string
		args    string
		phrases []string
	}{
		{[]string{"secret"}, "<instance-id> <fixture-name>", []string{"requires --component", "backlot secret --component db INSTANCE_ID password"}},
		{[]string{"fixtures"}, "<instance-id>", []string{"redacted", "backlot fixtures --component db INSTANCE_ID"}},
		{[]string{"project", "setup"}, " ", []string{"Inspect registered setup", "--edit", "--from private.yaml --reassociate", "does not import old credentials"}},
		{[]string{"scene", "start"}, "<scene>", []string{"--attached owns", "backlot scene start dev"}},
		{[]string{"scene", "status"}, "<scene>", []string{"without lifecycle ownership", "backlot scene status dev"}},
		{[]string{"scene", "stop"}, "<scene>", []string{"retaining data", "backlot scene stop dev"}},
	} {
		c := root
		for _, name := range tc.path {
			for _, child := range c.Commands {
				if child.Name == name {
					c = child
					break
				}
			}
		}
		if c.ArgsUsage != tc.args {
			t.Errorf("%v target = %q", tc.path, c.ArgsUsage)
		}
		for _, phrase := range tc.phrases {
			if !strings.Contains(c.Usage+" "+c.Description, phrase) {
				t.Errorf("%v missing %q", tc.path, phrase)
			}
		}
	}
}
