//go:build darwin || linux

package procidentity

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestOwnedChild(t *testing.T) {
	if os.Getenv("BACKLOT_IDENTITY_CHILD") != "1" {
		return
	}
	// Expiration is established before announcing readiness or accepting faults.
	go func() { time.Sleep(15 * time.Second); os.Exit(90) }()
	fmt.Println("ready")
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		switch scanner.Text() {
		case "exit":
			os.Exit(0)
		case "exec":
			if err := syscall.Exec("/bin/sleep", []string{"sleep", "15"}, os.Environ()); err != nil {
				os.Exit(91)
			}
		}
	}
	os.Exit(0)
}

type ownedChild struct {
	cmd    *exec.Cmd
	input  io.WriteCloser
	reaped bool
}

func startChild(t *testing.T) *ownedChild {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(exe, "-test.run=^TestOwnedChild$")
	cmd.Env = append(os.Environ(), "BACKLOT_IDENTITY_CHILD=1")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	input, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	output, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	child := &ownedChild{cmd: cmd, input: input}
	t.Cleanup(func() {
		_ = input.Close()
		if !child.reaped {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
			child.reaped = true
		}
	})
	ready := make(chan string, 1)
	go func() { scanner := bufio.NewScanner(output); scanner.Scan(); ready <- scanner.Text() }()
	select {
	case line := <-ready:
		if line != "ready" {
			t.Fatalf("child readiness %q", line)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("child readiness expired")
	}
	return child
}
func (c *ownedChild) command(t *testing.T, s string) {
	t.Helper()
	if _, err := fmt.Fprintln(c.input, s); err != nil {
		t.Fatal(err)
	}
}
func (c *ownedChild) wait(t *testing.T, signal syscall.Signal) {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- c.cmd.Wait() }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		_ = c.cmd.Process.Kill()
		<-done
		c.reaped = true
		t.Fatal("owned child exit expired")
	}
	c.reaped = true
	status, ok := c.cmd.ProcessState.Sys().(syscall.WaitStatus)
	if !ok || (signal != 0 && (!status.Signaled() || status.Signal() != signal)) || (signal == 0 && status.ExitStatus() != 0) {
		t.Fatalf("unexpected child exit: %v", c.cmd.ProcessState)
	}
}

func mustAcquire(t *testing.T, pid int) *Handle {
	t.Helper()
	h, err := Acquire(pid)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := h.Close(); err != nil {
			t.Error(err)
		}
	})
	return h
}
func eventually(t *testing.T, check func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if check() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("bounded observation expired")
}

func TestIdentitySessionAndExit(t *testing.T) {
	child := startChild(t)
	sentinel := startChild(t)
	h := mustAcquire(t, child.cmd.Process.Pid)
	sid, err := h.Session()
	if err != nil || sid != child.cmd.Process.Pid {
		t.Fatalf("session = %d, %v", sid, err)
	}
	other := mustAcquire(t, sentinel.cmd.Process.Pid)
	otherSID, err := other.Session()
	if err != nil || otherSID == sid {
		t.Fatalf("sentinel session = %d, %v", otherSID, err)
	}
	for _, sig := range []syscall.Signal{0, -1, 32} {
		if err := h.Signal(sig); !errors.Is(err, syscall.EINVAL) {
			t.Fatalf("invalid signal %d: %v", sig, err)
		}
	}
	if err := h.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	child.wait(t, syscall.SIGTERM)
	if err := h.Validate(); err == nil {
		t.Fatal("reaped identity validated")
	}
	if _, err := h.Session(); err == nil {
		t.Fatal("reaped membership validated")
	}
	if err := h.Signal(syscall.SIGTERM); err == nil {
		t.Fatal("reaped identity signaled")
	}
	if err := other.Validate(); err != nil {
		t.Fatalf("unrelated sentinel: %v", err)
	}
	if err := h.Close(); err != nil {
		t.Fatal(err)
	}
	if err := h.Validate(); !errors.Is(err, syscall.EBADF) {
		t.Fatalf("closed validation: %v", err)
	}
	if _, err := h.Session(); !errors.Is(err, syscall.EBADF) {
		t.Fatalf("closed session: %v", err)
	}
	if err := h.Signal(syscall.SIGTERM); !errors.Is(err, syscall.EBADF) {
		t.Fatalf("closed signal: %v", err)
	}
}

func TestIdentityExecAndChurn(t *testing.T) {
	for range 8 {
		child := startChild(t)
		h := mustAcquire(t, child.cmd.Process.Pid)
		child.command(t, "exec")
		if runtime.GOOS == "darwin" {
			eventually(t, func() bool { return errors.Is(h.Validate(), ErrChanged) })
		} else {
			eventually(t, func() bool {
				b, err := os.ReadFile(fmt.Sprintf("/proc/%d/comm", child.cmd.Process.Pid))
				return err == nil && strings.TrimSpace(string(b)) == "sleep"
			})
			if err := h.Validate(); err != nil {
				t.Fatal(err)
			}
		}
		// Reacquisition is explicitly finite and always precedes membership validation.
		fresh := mustAcquire(t, child.cmd.Process.Pid)
		sid, err := fresh.Session()
		if err != nil || sid != child.cmd.Process.Pid {
			t.Fatalf("post-exec session: %d %v", sid, err)
		}
		if err := fresh.Signal(syscall.SIGTERM); err != nil {
			t.Fatal(err)
		}
		child.wait(t, syscall.SIGTERM)
		if err := fresh.Validate(); err == nil {
			t.Fatal("stale churn identity validated")
		}
		if err := fresh.Close(); err != nil {
			t.Fatal(err)
		}
		if err := h.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestIdentityZombie(t *testing.T) {
	child := startChild(t)
	h := mustAcquire(t, child.cmd.Process.Pid)
	child.command(t, "exit")
	// ps is observation only; no PID obtained from it authorizes actuation.
	eventually(t, func() bool {
		b, err := exec.Command("ps", "-o", "stat=", "-p", fmt.Sprint(child.cmd.Process.Pid)).Output()
		return err == nil && strings.HasPrefix(strings.TrimSpace(string(b)), "Z")
	})
	err := h.Validate()
	if runtime.GOOS == "linux" && err != nil {
		t.Fatalf("Linux unreaped zombie pidfd: %v", err)
	}
	t.Logf("%s zombie validation: %v (does not prove live workload)", runtime.GOOS, err)
	child.wait(t, 0)
	if err := h.Validate(); err == nil {
		t.Fatal("reaped zombie validated")
	}
}

func TestIdentityInvalidAndResources(t *testing.T) {
	for _, pid := range []int{0, -1, 1 << 32} {
		if h, err := Acquire(pid); err == nil {
			_ = h.Close()
			t.Fatalf("accepted PID %d", pid)
		}
	}
	child := startChild(t)
	for range 256 {
		h, err := Acquire(child.cmd.Process.Pid)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := h.Session(); err != nil {
			t.Fatal(err)
		}
		if err := h.Close(); err != nil {
			t.Fatal(err)
		}
	}
	h := mustAcquire(t, child.cmd.Process.Pid)
	if err := h.Validate(); err != nil {
		t.Fatal(err)
	}
}
