//go:build darwin || linux

// Package procidentity acquires ephemeral kernel process identities. It does not
// authenticate historical ownership or prove session absence. Callers must hold
// independent live session authority before using a matching session as ownership.
package procidentity

import (
	"errors"
	"fmt"
	"sync"
	"syscall"

	"golang.org/x/sys/unix"
)

// Handle owns one acquired identity. It must be closed; methods serialize with
// Close. A handle cannot be persisted or retargeted to another PID.
type Handle struct {
	mu       sync.Mutex
	pid      int
	closed   bool
	identity platformIdentity
}

// Acquire obtains a kernel identity before any membership observation. PID alone
// is not ownership. Failure never falls back to numeric PID signaling.
func Acquire(pid int) (*Handle, error) {
	if pid <= 0 || int(int32(pid)) != pid {
		return nil, fmt.Errorf("acquire process identity: %w", syscall.EINVAL)
	}
	identity, err := acquire(pid)
	if err != nil {
		return nil, fmt.Errorf("acquire process %d identity: %w", pid, err)
	}
	return &Handle{pid: pid, identity: identity}, nil
}

// Session reads membership then validates the acquired identity. Exec or exit
// races return an error; callers may reacquire under their own finite budget.
// Membership can change after return under the cooperative process contract.
func (h *Handle) Session() (int, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return 0, syscall.EBADF
	}
	sid, err := unix.Getsid(h.pid)
	if err != nil {
		return 0, fmt.Errorf("observe process session: %w", err)
	}
	if err := h.identity.validate(h.pid); err != nil {
		return 0, fmt.Errorf("validate session observation: %w", err)
	}
	return sid, nil
}

// Validate checks the acquired identity without signaling the numeric PID.
// Linux zombies remain valid until reaped; Darwin may reject a dead task.
func (h *Handle) Validate() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return syscall.EBADF
	}
	return h.identity.validate(h.pid)
}

// Signal sends a positive signal to the acquired kernel identity only. It does
// not assert ownership or session membership; Session must precede actuation.
func (h *Handle) Signal(sig syscall.Signal) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return syscall.EBADF
	}
	if sig <= 0 || sig >= 32 {
		return syscall.EINVAL
	}
	return h.identity.signal(sig)
}

// Close releases acquired resources exactly once. Calls after Close fail closed.
func (h *Handle) Close() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return nil
	}
	h.closed = true
	return h.identity.close()
}

// ErrChanged means the acquired identity changed, including across Darwin exec.
var ErrChanged = errors.New("process identity changed")
