package native

import (
	"context"
	"errors"
	"net"
	"os"
	"testing"
)

func TestDarwinSocketInspectionRejectsUncertainState(t *testing.T) {
	listener, err := net.Listen("tcp", "[::]:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close() }()
	raw, err := listener.(*net.TCPListener).SyscallConn()
	if err != nil {
		t.Fatal(err)
	}
	var data []byte
	var inspectErr error
	if err := raw.Control(func(fd uintptr) { data, inspectErr = darwinSocketInfo(os.Getpid(), int(fd)) }); err != nil {
		t.Fatal(err)
	}
	if inspectErr != nil {
		t.Fatal(inspectErr)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if dual, err := darwinSocketIPv4(data, port); err != nil || !dual {
		t.Fatal("real dual-stack ABI not recognized", dual, err)
	}
	for _, offset := range []int{176, 180, 184, 256, 268, 288, 312, 344} {
		changed := append([]byte(nil), data...)
		changed[offset] ^= 0xff
		if dual, err := darwinSocketIPv4(changed, port); err == nil || dual {
			t.Fatalf("uncertain field at%d accepted: %v %v", offset, dual, err)
		}
	}
	if _, err := darwinSocketIPv4(data[:791], port); err == nil {
		t.Fatal("truncated ABI accepted")
	}
	if _, err := darwinSocketIPv4(data, port%65535+1); err == nil {
		t.Fatal("different port accepted")
	}
	if _, err := darwinSocketInfo(os.Getpid(), -1); err == nil {
		t.Fatal("invalid descriptor accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := wildcardServesIPv4(ctx, os.Getpid(), -1, port); !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled inspection performed", err)
	}
	// IPV6_V6ONLY capability remains a valid, explicitly negative result.
	only := append([]byte(nil), data...)
	only[288] = 2
	if dual, err := darwinSocketIPv4(only, port); err != nil || dual {
		t.Fatal("IPv6-only capability accepted", dual, err)
	}
}
