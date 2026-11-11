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
func TestInstallActivityPagination(t *testing.T) {
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
	entry := func(id string) v1.ActivityEntry {
		return v1.ActivityEntry{Key: "workload/" + id, ID: id, Project: "fixture", Checkout: "/tmp/fixture", Scene: "test", Status: "interrupted", Owner: "absent", Settlement: &v1.ConsumerSettlement{InstanceID: id, State: v1.ConsumerUnknown, Cause: v1.SettlementUnproven, Blockers: []v1.ConsumerBlocker{{Code: "metadata_unproven", InstanceID: id}}}, Blockers: []v1.ActivityBlocker{}}
	}
	first := v1.ActivityResponse{APIVersion: v1.Version, DaemonGeneration: idA, GatewayStatus: "unconfigured", Entries: []v1.ActivityEntry{entry(idA)}, NextAfter: "workload/" + idA}
	last := v1.ActivityResponse{APIVersion: v1.Version, DaemonGeneration: idA, GatewayStatus: "unconfigured", Entries: []v1.ActivityEntry{entry(idB)}, Complete: true}
	script := "#!/bin/sh\ncd '" + root + "'\n[ \"$1\" = activity ] || exit 1\nif [ \"$7\" = \"\" ]; then cat first; else cat last; fi\n"
	if err := os.WriteFile(binary, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"success", "repeated-id", "bad-cursor", "generation", "unknown-status", "missing-settlement", "partial-page", "page-limit", "empty-page"} {
		t.Run(mode, func(t *testing.T) {
			a, b := first, last
			a.Entries = append([]v1.ActivityEntry(nil), first.Entries...)
			b.Entries = append([]v1.ActivityEntry(nil), last.Entries...)
			switch mode {
			case "repeated-id":
				b.Entries[0] = entry(idA)
			case "bad-cursor":
				a.NextAfter = "workload/" + idB
			case "generation":
				b.DaemonGeneration = idB
			case "unknown-status":
				b.Entries[0].Status = "invented"
			case "missing-settlement":
				b.Entries[0].Settlement = nil
			case "partial-page":
				b.Entries[0].Project = ""
			case "page-limit":
				a.NextAfter = "workload/" + idA
				b.Complete = false
				b.NextAfter = "workload/" + idB
			case "empty-page":
				a.Entries = []v1.ActivityEntry{}
			}
			write("first", a)
			write("last", b)
			got, err := installActivity(context.Background(), binary, "/tmp/state")
			ok := mode == "success" || mode == "empty-page"
			if (err == nil) != ok || ok && (len(got) != 2 || got[1].Entries[0].ID != idB) {
				t.Fatalf("%s: %+v %v", mode, got, err)
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
