//go:build darwin || linux

package cli

import (
	"context"
	"errors"
	"io"
	"os"
	"os/signal"
	"syscall"

	v1 "github.com/octacian/backlot/api/v1"
	"golang.org/x/sys/unix"
	"golang.org/x/term"
)

// terminalInput observes cancellation while waiting for keystrokes without an
// unjoined input goroutine or closing the caller-owned stdin descriptor.
type terminalInput struct {
	ctx context.Context
	fd  int
}

func (r terminalInput) Read(data []byte) (int, error) {
	for {
		if err := r.ctx.Err(); err != nil {
			return 0, err
		}
		ready, err := unix.Poll([]unix.PollFd{{Fd: int32(r.fd), Events: unix.POLLIN}}, 50)
		if errors.Is(err, unix.EINTR) {
			continue
		}
		if err != nil {
			return 0, err
		}
		if ready == 0 {
			continue
		}
		n, err := unix.Read(r.fd, data)
		if errors.Is(err, unix.EINTR) || errors.Is(err, unix.EAGAIN) {
			continue
		}
		if n == 0 && err == nil {
			return 0, io.EOF
		}
		return n, err
	}
}

func setupTerminal(ctx context.Context, writer io.Writer) (*term.Terminal, func() error, error) {
	fd := int(os.Stdin.Fd())
	inputCtx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	state, err := term.MakeRaw(fd)
	if err != nil {
		stop()
		return nil, nil, &v1.PlanError{Code: "setup_terminal", Message: "cannot enter private terminal input mode"}
	}
	terminal := term.NewTerminal(struct {
		io.Reader
		io.Writer
	}{terminalInput{ctx: inputCtx, fd: fd}, writer}, "")
	restore := func() error {
		stop()
		if err := term.Restore(fd, state); err != nil {
			return &v1.PlanError{Code: "setup_terminal", Message: "cannot restore terminal state; active configuration unchanged"}
		}
		return nil
	}
	return terminal, restore, nil
}
