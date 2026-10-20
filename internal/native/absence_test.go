//go:build darwin || linux

package native

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestBootRecoveryPreservesUnrelatedProcess(t *testing.T) {
	child := ownedSleep(t)
	id, err := identify(child.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	id.Control = filepath.Join(t.TempDir(), "missing.sock")
	id.Token = strings.Repeat("a", 64)
	boot, err := bootIdentity()
	if err != nil || !validBootIdentity(boot) {
		t.Fatal("kernel boot identity unavailable", err)
	}
	id.Boot = boot
	if err := Stop(id, 0); err == nil {
		t.Fatal("same-boot live guardian bypassed")
	}
	id.Boot = "00000000-0000-0000-0000-000000000001"
	if id.Boot == boot {
		id.Boot = "00000000-0000-0000-0000-000000000002"
	}
	if err := Stop(id, 0); err != nil {
		t.Fatal("prior-boot resource absence rejected", err)
	}
	if err := child.Process.Signal(syscall.Signal(0)); err != nil {
		t.Fatal("reused PID received signal", err)
	}
	id.Boot = "corrupted"
	if err := Stop(id, 0); err == nil {
		t.Fatal("malformed boot accepted")
	}
}

func TestLegacyMissingReceiptAfterSessionExit(t *testing.T) {
	child := ownedSleep(t)
	id, err := identify(child.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	id.Control = filepath.Join(t.TempDir(), "missing.sock")
	id.Token = strings.Repeat("a", 64)
	if err := child.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = child.Wait()
	if err := Stop(id, 0); err != nil {
		t.Fatal("absent session could not recover", err)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(id.Control), "receipt.json")); !os.IsNotExist(err) {
		t.Fatal("recovery fabricated a receipt", err)
	}
}

func TestSessionAbsenceRejectsSurvivorsAndRaces(t *testing.T) {
	for _, tc := range []struct {
		name       string
		sid        int
		sessionErr error
		changed    bool
		observeErr error
		wantOK     bool
	}{
		{name: "absent", sid: 44, wantOK: true},
		{name: "surviving-descendant", sid: 42},
		{name: "permission-denied", sessionErr: syscall.EPERM},
		{name: "disappeared-parent", sessionErr: syscall.ESRCH},
		{name: "reused-parent", sid: 44, changed: true},
		{name: "unreadable-birth", sid: 44, observeErr: errors.New("unreadable")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			snapshot := []Identity{{PID: 123, Birth: "original"}}
			err := verifyAbsentSession(42, snapshot, func(int) (int, error) { return tc.sid, tc.sessionErr }, func(int) (Identity, error) {
				id := snapshot[0]
				if tc.changed {
					id.Birth = "reused"
				}
				return id, tc.observeErr
			})
			if (err == nil) != tc.wantOK {
				t.Fatal("incorrect absence decision", err)
			}
		})
	}
}

func TestSessionSnapshotsDetectForkExitAndReuse(t *testing.T) {
	before := []Identity{{PID: 1, Birth: "one"}, {PID: 2, Birth: "two"}}
	for _, tc := range []struct {
		name  string
		after []Identity
		equal bool
	}{
		{name: "reordered", after: []Identity{before[1], before[0]}, equal: true},
		{name: "fork", after: append(append([]Identity{}, before...), Identity{PID: 3, Birth: "new"})},
		{name: "exit", after: before[:1]},
		{name: "reuse", after: []Identity{before[0], {PID: 2, Birth: "new"}}},
		{name: "empty"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if sameProcesses(before, tc.after) != tc.equal {
				t.Fatal("unstable snapshot accepted")
			}
		})
	}
}
