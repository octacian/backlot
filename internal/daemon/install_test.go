//go:build darwin || linux

package daemon

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	v1 "github.com/octacian/backlot/api/v1"
)

func TestInstallVersionOrder(t *testing.T) {
	for _, c := range []struct {
		target, old string
		older       bool
	}{
		{"v0.4.0", "v0.3.9", false}, {"v0.3.9", "v0.4.0", true},
		{"v0.4.0", "v0.4.0", false}, {"v0.3.10", "v0.3.9", false},
		{"v0.99999999999999999999.0", "v0.99999999999999999998.9", false},
	} {
		if got := installOlder(c.target, c.old); got != c.older {
			t.Errorf("%s < %s: %t", c.target, c.old, got)
		}
	}
	for _, tag := range []string{"dev", "v0.03.1", "v1.0.0", "v0.1.0-rc1", "../v0.1.0", "v0.1.0\n"} {
		if installVersion.MatchString(tag) {
			t.Errorf("accepted invalid tag %q", tag)
		}
	}
}
func TestInstallFileRefusesUnknownWithoutExecution(t *testing.T) {
	root := t.TempDir()
	marker := filepath.Join(root, "executed")
	binary := filepath.Join(root, "backlot")
	if err := os.WriteFile(binary, []byte("#!/bin/sh\ntouch '"+marker+"'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := installFile(binary); err == nil {
		t.Fatal("recognized unknown executable")
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("unknown executable ran")
	}
}
func TestInstallUnchanged(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "backlot")
	if err := os.WriteFile(path, []byte("before"), 0700); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	sum, err := installHash(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := installUnchanged(path, info, sum); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("after!"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := installUnchanged(path, info, sum); err == nil {
		t.Fatal("accepted changed bytes")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("missing", path); err != nil {
		t.Fatal(err)
	}
	if err := installUnchanged(path, info, sum); err == nil {
		t.Fatal("accepted replacement symlink")
	}
}
func TestInstallCLIResponseBoundary(t *testing.T) {
	for _, data := range []string{`{"api_version":"v1"} {}`, `{"api_version":"v1","api_version":"v1"}`, `{"api_version":"v1","unknown":true}`, `{"API_VERSION":"v1"}`, `null`, `{"api_version":`, strings.Repeat("x", (1<<20)+1)} {
		t.Run("reject", func(t *testing.T) {
			root := t.TempDir()
			output := filepath.Join(root, "output")
			if err := os.WriteFile(output, []byte(data), 0600); err != nil {
				t.Fatal(err)
			}
			script := filepath.Join(root, "old")
			if err := os.WriteFile(script, []byte("#!/bin/sh\ncat '"+output+"'\n"), 0700); err != nil {
				t.Fatal(err)
			}
			var status v1.DaemonStatusResponse
			if err := installCLI(context.Background(), script, &status, "daemon", "status"); err == nil {
				t.Fatal("accepted malformed/oversized response")
			}
		})
	}
}
func TestInstallDescriptionEscapesTerminal(t *testing.T) {
	i := v1.Instance{ID: strings.Repeat("a", 64), Status: v1.Failed, Plan: v1.PlanResponse{Project: "evil\x1b[2J\n", Scene: strings.Repeat("s", 1000)}, Execution: &v1.ExecutionResult{CleanupFailure: "pending"}}
	text := InstallDescription(i)
	if strings.ContainsAny(text, "\x1b\n") || len(text) > 400 || !strings.Contains(text, "unresolved cleanup") {
		t.Fatalf("unsafe description %q", text)
	}
}

func TestInstallInspectionPagination(t *testing.T) {
	root := t.TempDir()
	binary := filepath.Join(root, "old")
	write := func(name string, value any) {
		t.Helper()
		data, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, name), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	idA, idB := strings.Repeat("a", 64), strings.Repeat("b", 64)
	// Named public snapshots mirror the existing client's minimal persistent fixture.
	instance := func(id string, kept bool) v1.InstanceResponse {
		i := v1.Instance{ID: id, Checkout: v1.CheckoutIdentity{ID: id, Path: "/tmp/fixture"}, Status: v1.Prepared, CreatedAt: "2026-10-07T12:00:00Z", Operation: v1.Operation{ID: id, Status: v1.Prepared, Allocations: []v1.AllocationRecord{{ID: id, Kind: "private_snapshot", Phase: "effect"}}}, Plan: v1.PlanResponse{APIVersion: v1.Version, Project: "fixture", Scene: "test", Lifetime: v1.Persistent, Components: []v1.PlannedComponent{}}}
		if kept {
			i.Status = v1.Failed
			i.Operation.Status = v1.Failed
			i.Execution = &v1.ExecutionResult{Attempt: id, Kept: true, Components: []v1.ComponentResult{}}
		}
		return v1.InstanceResponse{APIVersion: v1.Version, Instance: i}
	}
	first := v1.ListResponse{APIVersion: v1.Version, Instances: []v1.InstanceSummary{{ID: idA, Project: "fixture", Scene: "test", Checkout: "/tmp/fixture", Lifetime: v1.Persistent, Status: v1.Prepared, CreatedAt: "2026-10-07T12:00:00Z"}}, NextAfterID: idA}
	last := v1.ListResponse{APIVersion: v1.Version, Instances: []v1.InstanceSummary{{ID: idB, Project: "fixture", Scene: "test", Checkout: "/tmp/fixture", Lifetime: v1.Persistent, Status: v1.Failed, CreatedAt: "2026-10-07T12:00:00Z"}}}
	write("first", first)
	write("last", last)
	write(idA, instance(idA, false))
	write(idB, instance(idB, true))
	script := "#!/bin/sh\ncd '" + root + "'\ncase $1 in\nlist) if [ \"$7\" = \"\" ]; then cat first; else cat last; fi;;\ninspect) cat \"$4\";;\nesac\n"
	if err := os.WriteFile(binary, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	got, err := installInstances(context.Background(), binary, "/tmp/state")
	if err != nil || len(got) != 1 || got[0].ID != idB {
		t.Fatalf("kept pagination: %+v %v", got, err)
	}
	for _, mode := range []string{"repeated-id", "bad-cursor", "partial-inspect", "wrong-id", "unknown-status", "last-observed", "missing-execution", "partial-page"} {
		t.Run(mode, func(t *testing.T) {
			a, b, i := first, last, instance(idB, true)
			b.Instances = append([]v1.InstanceSummary(nil), last.Instances...)
			switch mode {
			case "partial-page":
				b.Instances[0].Project = ""
			case "repeated-id":
				b.Instances[0].ID = idA
			case "bad-cursor":
				a.NextAfterID = idB
			case "partial-inspect":
				i.Instance.Operation = v1.Operation{}
			case "wrong-id":
				i.Instance.ID = idA
			case "unknown-status":
				i.Instance.Status = "invented"
			case "last-observed":
				i.Error = &v1.PlanError{Code: "observation_failed", Message: "partial"}
			case "missing-execution":
				i.Instance.Status = v1.RuntimeReady
				i.Instance.Operation.Status = v1.RuntimeReady
				i.Instance.Execution = nil
			}
			write("first", a)
			write("last", b)
			write(idB, i)
			if _, err := installInstances(context.Background(), binary, "/tmp/state"); err == nil {
				t.Fatal("accepted uncertain inspection")
			}
		})
	}
}

func TestInstallStatusIsComplete(t *testing.T) {
	base := v1.DaemonStatusResponse{APIVersion: v1.Version, StateVersion: v1.StateVersion, Status: "running", PID: 42, LeaseDuration: "30s"}
	if !installStatus(base, v1.StateVersion, "running") {
		t.Fatal("valid status rejected")
	}
	for _, mode := range []string{"api", "state", "pid", "status", "lease"} {
		s := base
		switch mode {
		case "api":
			s.APIVersion = ""
		case "state":
			s.StateVersion--
		case "pid":
			s.PID = 0
		case "status":
			s.Status = "stopping"
		case "lease":
			s.LeaseDuration = ""
		}
		if installStatus(s, v1.StateVersion, "running") {
			t.Fatalf("accepted incomplete status: %s", mode)
		}
	}
}
