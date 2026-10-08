//go:build darwin || linux

package native

import (
	"bufio"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func ownedSleep(t *testing.T) *exec.Cmd {
	t.Helper()
	cmd := exec.Command("/bin/sleep", "60")
	configure(cmd)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	return cmd
}

func TestPinnedAnchorIgnoresReusedMemberObservation(t *testing.T) {
	anchor := ownedSleep(t)
	unrelated := ownedSleep(t)
	original, err := identify(anchor.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	close(done)
	w := &ownedWorkload{anchor: anchor, collected: make(chan error, 2), statusDone: done, publish: func(Exit) {}}
	w.collected <- nil
	w.collected <- nil
	calls := 0
	w.beforeSignal = func() {
		calls++
		if calls == 1 {
			// Force the anchor to exit exactly between observation and actuation. It
			// remains our unreaped child, so its PID/group cannot become unrelated.
			if err := anchor.Process.Kill(); err != nil {
				t.Fatal(err)
			}
			deadline := time.Now().Add(time.Second)
			for {
				dead, err := anchorExited(anchor.Process.Pid)
				if err != nil {
					t.Fatal(err)
				}
				if dead {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("anchor did not exit")
				}
				time.Sleep(time.Millisecond)
			}
		}
		current, err := identify(anchor.Process.Pid)
		if err != nil || current.Birth != original.Birth {
			t.Fatal("anchor released before group actuation", current, err)
		}
	}
	scans := 0
	w.scan = func(pgid int) ([]int, error) {
		scans++
		if scans == 1 {
			// Discriminating stale enumeration: this PID now denotes an unrelated
			// process. An implementation that signals enumerated members kills it.
			return []int{pgid, unrelated.Process.Pid}, nil
		}
		return groupMembers(pgid)
	}
	if err := w.stop(0); err != nil {
		t.Fatal(err)
	}
	if calls < 2 {
		t.Fatal("fault seam did not exercise escalation")
	}
	if err := unrelated.Process.Signal(syscall.Signal(0)); err != nil {
		t.Fatal("unrelated reused member received a signal", err)
	}
	// A retry after anchor release must never target a reusable group ID.
	before := calls
	if err := w.stop(0); err == nil || calls != before {
		t.Fatal("released anchor allowed further signaling", err, calls)
	}
}

func TestLostAuthorityCannotSignalRecordedPID(t *testing.T) {
	unrelated := ownedSleep(t)
	id, err := identify(unrelated.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	id.Control = filepath.Join(t.TempDir(), "missing.sock")
	id.Token = strings.Repeat("a", 64)
	if err := Stop(id, 0); err == nil {
		t.Fatal("absent authority accepted")
	}
	if err := unrelated.Process.Signal(syscall.Signal(0)); err != nil {
		t.Fatal("authority loss signaled an observed PID", err)
	}
}

func TestKernelAbsenceRejectsIncompleteEnumeration(t *testing.T) {
	anchor := exec.Command("/bin/sleep", "60")
	configureAnchor(anchor)
	if err := anchor.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = anchor.Process.Kill(); _ = anchor.Wait() })
	member := exec.Command("/bin/sh", "-c", "trap '' TERM; echo ready; exec /bin/sleep 60")
	configureRoot(member, anchor.Process.Pid)
	ready, err := member.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := member.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = member.Process.Kill(); _ = member.Wait() })
	if line, err := bufio.NewReader(ready).ReadString('\n'); err != nil || line != "ready\n" {
		t.Fatal("fixture not ready", line, err)
	}
	// Force a dead pinned anchor, with an independently live owned member that
	// deliberately survives TERM. Enumeration lies about that member's presence.
	if err := anchor.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for {
		dead, err := anchorExited(anchor.Process.Pid)
		if err != nil {
			t.Fatal(err)
		}
		if dead {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("anchor did not exit")
		}
		time.Sleep(time.Millisecond)
	}
	statusDone := make(chan struct{})
	close(statusDone)
	w := &ownedWorkload{anchor: anchor, statusDone: statusDone, collected: make(chan error, 2), publish: func(Exit) {}}
	w.collected <- nil
	w.collected <- nil
	scans := 0
	w.scan = func(pgid int) ([]int, error) {
		scans++
		if scans == 1 {
			return []int{pgid}, nil
		}
		return nil, nil
	}
	signals := 0
	w.beforeSignal = func() { signals++ }
	if err := w.stop(0); err == nil || !strings.Contains(err.Error(), "kernel group absence") {
		t.Fatal("incomplete enumeration acknowledged success", err)
	}
	if err := member.Process.Signal(syscall.Signal(0)); err != nil {
		t.Fatal("survivor seam failed", err)
	}
	before := signals
	if err := w.stop(0); err == nil || signals != before {
		t.Fatal("kernel proof failure allowed post-release signals", err)
	}
}
