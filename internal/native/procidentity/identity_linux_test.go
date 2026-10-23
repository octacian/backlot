package procidentity

import (
	"errors"
	"os"
	"syscall"
	"testing"
)

func TestLinuxDescriptorsBounded(t *testing.T) {
	child := startChild(t)
	count := func() int {
		entries, err := os.ReadDir("/proc/self/fd")
		if err != nil {
			t.Fatal(err)
		}
		return len(entries)
	}
	before := count()
	for range 256 {
		h := mustAcquire(t, child.cmd.Process.Pid)
		if _, err := h.Session(); err != nil {
			t.Fatal(err)
		}
		if err := h.Close(); err != nil {
			t.Fatal(err)
		}
	}
	if after := count(); after != before {
		t.Fatalf("descriptor count grew: %d -> %d", before, after)
	}
	// A denied/unavailable pidfd operation is an error, never liveness success.
	p := platformIdentity{fd: -1}
	if err := p.validate(child.cmd.Process.Pid); !errors.Is(err, syscall.EBADF) {
		t.Fatalf("invalid pidfd validation: %v", err)
	}
	if err := p.signal(syscall.SIGTERM); !errors.Is(err, syscall.EBADF) {
		t.Fatalf("invalid pidfd signal: %v", err)
	}
}
