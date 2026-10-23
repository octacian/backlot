package procidentity

import (
	"golang.org/x/sys/unix"
	"syscall"
)

type platformIdentity struct{ fd int }

func acquire(pid int) (platformIdentity, error) {
	fd, err := unix.PidfdOpen(pid, 0)
	return platformIdentity{fd: fd}, err
}
func (p platformIdentity) validate(_ int) error { return unix.PidfdSendSignal(p.fd, 0, nil, 0) }
func (p platformIdentity) signal(sig syscall.Signal) error {
	return unix.PidfdSendSignal(p.fd, sig, nil, 0)
}
func (p platformIdentity) close() error { return unix.Close(p.fd) }
