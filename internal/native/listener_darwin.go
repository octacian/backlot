package native

import (
	"context"
	"encoding/binary"
	"errors"
	"unsafe"

	"golang.org/x/sys/unix"
)

// Darwin's public proc_pidfdinfo(PROC_PIDFDSOCKETINFO) ABI reports the
// socket's INI_IPV4/INI_IPV6 capability bits, independent of accept(). These
// offsets and size are from sys/proc_info.h (socket_fdinfo, LP64 ABI).
func wildcardServesIPv4(ctx context.Context, pid, fd, port int) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	data, err := darwinSocketInfo(pid, fd)
	if err != nil {
		return false, err
	}
	return darwinSocketIPv4(data, port)
}

func darwinSocketInfo(pid, fd int) ([]byte, error) {
	data := make([]byte, 792)
	//nolint:staticcheck // SA1019: x/sys has no proc_pidfdinfo libSystem wrapper; this single read-only pure-Go boundary uses the public SDK ABI, validates the full result, and fails closed. No cgo or extra privilege is required.
	n, _, errno := unix.Syscall6(unix.SYS_PROC_INFO, 3, uintptr(pid), 3, uintptr(fd), uintptr(unsafe.Pointer(&data[0])), uintptr(len(data)))
	if errno != 0 {
		return nil, errno
	}
	if n != uintptr(len(data)) {
		return nil, errors.New("incompatible Darwin socket-info ABI")
	}
	return data, nil
}

func darwinSocketIPv4(data []byte, port int) (bool, error) {
	if len(data) != 792 || binary.NativeEndian.Uint32(data[256:260]) != 2 || binary.NativeEndian.Uint32(data[184:188]) != unix.AF_INET6 || binary.NativeEndian.Uint32(data[176:180]) != unix.SOCK_STREAM || binary.NativeEndian.Uint32(data[180:184]) != unix.IPPROTO_TCP || binary.BigEndian.Uint16(data[268:270]) != uint16(port) || binary.NativeEndian.Uint32(data[344:348]) != 1 {
		return false, errors.New("listener socket changed or socket-family inspection unavailable")
	}
	for _, b := range data[312:328] {
		if b != 0 {
			return false, errors.New("listener is no longer IPv6 wildcard")
		}
	}
	if data[288] != 2 && data[288] != 3 {
		return false, errors.New("unknown Darwin socket-family capability")
	}
	return data[288]&1 != 0, nil
}
