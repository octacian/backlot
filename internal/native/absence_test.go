//go:build darwin || linux

package native

import (
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
