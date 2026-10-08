package native

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// Linux SOCK_DIAG emits INET_DIAG_SKV6ONLY for listening IPv6 sockets even
// without privileged extensions. Match the inode retained by the observed fd,
// plus port/address/state; never infer dual-stack capability from family alone.
func wildcardServesIPv4(ctx context.Context, pid, fd, port int) (bool, error) {
	link, err := os.Readlink(fmt.Sprintf("/proc/%d/fd/%d", pid, fd))
	if err != nil {
		return false, err
	}
	if !strings.HasPrefix(link, "socket:[") || !strings.HasSuffix(link, "]") {
		return false, errors.New("listener fd changed")
	}
	inode, err := strconv.ParseUint(strings.TrimSuffix(strings.TrimPrefix(link, "socket:["), "]"), 10, 32)
	if err != nil {
		return false, err
	}
	sock, err := unix.Socket(unix.AF_NETLINK, unix.SOCK_RAW|unix.SOCK_CLOEXEC, unix.NETLINK_SOCK_DIAG)
	if err != nil {
		return false, err
	}
	defer func() { _ = unix.Close(sock) }()
	budget := time.Second
	if deadline, ok := ctx.Deadline(); ok {
		budget = min(budget, time.Until(deadline))
	}
	if budget <= 0 {
		return false, context.DeadlineExceeded
	}
	timeout := unix.NsecToTimeval(budget.Nanoseconds())
	if err := unix.SetsockoptTimeval(sock, unix.SOL_SOCKET, unix.SO_RCVTIMEO, &timeout); err != nil {
		return false, err
	}
	request := make([]byte, 72) // nlmsghdr(16) + inet_diag_req_v2(56)
	binary.NativeEndian.PutUint32(request, uint32(len(request)))
	binary.NativeEndian.PutUint16(request[4:], 20) // SOCK_DIAG_BY_FAMILY
	binary.NativeEndian.PutUint16(request[6:], unix.NLM_F_REQUEST|unix.NLM_F_DUMP)
	binary.NativeEndian.PutUint32(request[8:], 1)
	request[16] = unix.AF_INET6
	request[17] = unix.IPPROTO_TCP
	binary.NativeEndian.PutUint32(request[20:], 1<<10) // TCP_LISTEN
	if err := unix.Sendto(sock, request, 0, &unix.SockaddrNetlink{Family: unix.AF_NETLINK}); err != nil {
		return false, err
	}
	end := time.Now().Add(budget)
	for time.Now().Before(end) {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		data := make([]byte, 65536)
		n, from, err := unix.Recvfrom(sock, data, 0)
		if err != nil {
			return false, err
		}
		source, ok := from.(*unix.SockaddrNetlink)
		if !ok || source.Pid != 0 {
			return false, errors.New("socket diagnosis did not come from kernel")
		}
		messages, err := syscall.ParseNetlinkMessage(data[:n])
		if err != nil {
			return false, err
		}
		for _, message := range messages {
			if message.Header.Seq != 1 {
				return false, errors.New("socket diagnosis sequence mismatch")
			}
			if message.Header.Type == unix.NLMSG_DONE {
				return false, errors.New("listener socket disappeared during family inspection")
			}
			if message.Header.Type == unix.NLMSG_ERROR {
				return false, errors.New("kernel socket-family diagnosis failed")
			}
			if message.Header.Type != 20 {
				continue
			}
			matches, only, err := diagnosticIPv6Only(message.Data, uint32(inode), port)
			if err != nil {
				return false, err
			}
			if matches {
				return !only, nil
			}
		}
	}
	return false, context.DeadlineExceeded
}

func diagnosticIPv6Only(data []byte, inode uint32, port int) (bool, bool, error) {
	if len(data) < 72 {
		return false, false, errors.New("truncated socket diagnosis")
	}
	if data[0] != unix.AF_INET6 || data[1] != 10 || binary.NativeEndian.Uint32(data[68:72]) != inode || binary.BigEndian.Uint16(data[4:6]) != uint16(port) {
		return false, false, nil
	}
	for _, b := range data[8:24] {
		if b != 0 {
			return false, false, errors.New("listener is no longer IPv6 wildcard")
		}
	}
	for attrs := data[72:]; len(attrs) >= 4; {
		size := int(binary.NativeEndian.Uint16(attrs))
		kind := binary.NativeEndian.Uint16(attrs[2:]) & 0x3fff
		if size < 4 || size > len(attrs) {
			return false, false, errors.New("invalid socket diagnosis attribute")
		}
		if kind == 11 { // INET_DIAG_SKV6ONLY, Linux UAPI inet_diag.h
			if size != 5 || attrs[4] > 1 {
				return false, false, errors.New("invalid IPv6-only socket capability")
			}
			return true, attrs[4] == 1, nil
		}
		aligned := (size + 3) &^ 3
		if aligned > len(attrs) {
			return false, false, errors.New("truncated socket diagnosis padding")
		}
		attrs = attrs[aligned:]
	}
	return false, false, errors.New("kernel omitted IPv6-only socket capability")
}
