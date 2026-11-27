//go:build darwin || linux

package daemon

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"syscall"
	"testing"
	"time"

	"github.com/octacian/backlot/internal/native"
)

func TestReadinessDestinationOwnership(t *testing.T) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close() }()
	port := strconv.Itoa(listener.Addr().(*net.TCPAddr).Port)
	for _, kind := range []string{"tcp", "http"} {
		target := "192.0.2.1:" + port
		if kind == "http" {
			target = "http://" + target + "/"
		}
		ready, err := networkProbe(context.Background(), kind, target, syscall.Getpgrp())
		if ready {
			t.Fatal("remote destination accepted", kind)
		}
		code(t, err, "readiness_unowned")
	}
	owned, conflict, err := native.ListenerEndpoint(context.Background(), syscall.Getpgrp(), "[::1]:"+port)
	if err != nil || owned || conflict {
		t.Fatal("different local destination substituted", owned, conflict, err)
	}
	ready, err := networkProbe(context.Background(), "tcp", listener.Addr().String(), syscall.Getpgrp())
	if err != nil || !ready {
		t.Fatal("owned numeric destination rejected", ready, err)
	}
	conn, err := net.Dial("tcp", listener.Addr().String())
	if err != nil {
		t.Fatal("listener disturbed", err)
	}
	_ = conn.Close()
	for _, values := range [][]netip.Addr{{netip.MustParseAddr("192.0.2.1")}, {netip.MustParseAddr("127.0.0.1"), netip.MustParseAddr("192.0.2.1")}, nil} {
		_, err := localProbeAddresses(context.Background(), "fixture.invalid", func(context.Context, string, string) ([]netip.Addr, error) { return values, nil })
		code(t, err, "readiness_unowned")
	}
	addresses, err := localProbeAddresses(context.Background(), "fixture.invalid", func(context.Context, string, string) ([]netip.Addr, error) {
		return []netip.Addr{netip.MustParseAddr("127.0.0.1"), netip.MustParseAddr("::1")}, nil
	})
	if err != nil || len(addresses) != 2 {
		t.Fatal(addresses, err)
	}
}

func TestReadinessIPv6WildcardFamilies(t *testing.T) {
	for _, only := range []int{0, 1} {
		t.Run("v6only="+strconv.Itoa(only), func(t *testing.T) {
			lc := net.ListenConfig{Control: func(_, _ string, raw syscall.RawConn) error {
				var optionErr error
				if err := raw.Control(func(fd uintptr) {
					optionErr = syscall.SetsockoptInt(int(fd), syscall.IPPROTO_IPV6, syscall.IPV6_V6ONLY, only)
				}); err != nil {
					return err
				}
				return optionErr
			}}
			var listener net.Listener
			ipv4 := -1
			t.Cleanup(func() {
				if ipv4 >= 0 {
					_ = syscall.Close(ipv4)
				}
				if listener != nil {
					_ = listener.Close()
				}
			})
			var err error
			// Reserve both TCP families; an IPv6-only bind does not reserve IPv4.
			for attempt := 0; attempt < 8; attempt++ {
				listener, err = lc.Listen(context.Background(), "tcp6", "[::]:0")
				if err != nil {
					t.Fatal(err)
				}
				if only == 0 {
					break
				}
				ipv4, err = syscall.Socket(syscall.AF_INET, syscall.SOCK_STREAM, 0)
				if err != nil {
					t.Fatal(err)
				}
				syscall.CloseOnExec(ipv4)
				err = syscall.Bind(ipv4, &syscall.SockaddrInet4{Port: listener.Addr().(*net.TCPAddr).Port, Addr: [4]byte{127, 0, 0, 1}})
				if err == nil {
					break
				}
				_ = syscall.Close(ipv4)
				ipv4 = -1
				_ = listener.Close()
				listener = nil
				if !errors.Is(err, syscall.EADDRINUSE) {
					t.Fatal(err)
				}
			}
			if listener == nil {
				t.Fatal("could not reserve both fixture TCP families", err)
			}
			port := strconv.Itoa(listener.Addr().(*net.TCPAddr).Port)
			target := "127.0.0.1:" + port
			// A TCP listener need not accept before family/ownership inspection.
			ready, err := networkProbe(context.Background(), "tcp", target, syscall.Getpgrp())
			if err != nil || ready != (only == 0) {
				t.Fatal("TCP served family mismatch", ready, err)
			}
			owned, conflict, err := native.ListenerEndpoint(context.Background(), syscall.Getpgrp(), target)
			if err != nil || owned != (only == 0) || conflict {
				t.Fatal("socket family proof mismatch", owned, conflict, err)
			}
			t.Logf("IPV6_V6ONLY=%d IPv4 endpoint=%s TCP readiness=%t", only, target, ready)
			if only == 1 {
				ready, err = networkProbe(context.Background(), "http", "http://"+target+"/", syscall.Getpgrp())
				if err != nil || ready {
					t.Fatal("IPv6-only HTTP target accepted", ready, err)
				}
				// A distinct IPv4 socket can coexist on this exact port. Deliberately
				// ask for another group so its owner is unrelated; v6-only cannot mask it.
				if err := syscall.Listen(ipv4, 1); err != nil {
					t.Fatal(err)
				}
				owned, conflict, err := native.ListenerEndpoint(context.Background(), syscall.Getpgrp()+1, target)
				if err != nil || owned || !conflict {
					t.Fatal("unrelated IPv4 endpoint accepted", owned, conflict, err)
				}
				_, err = networkProbe(context.Background(), "http", "http://"+target+"/", syscall.Getpgrp()+1)
				code(t, err, "port_conflict")
				// The IPv6-only socket does not conflict with the owned IPv4
				// destination. This must remain ready, not over-reject.
				ready, err = networkProbe(context.Background(), "tcp", target, syscall.Getpgrp())
				if err != nil || !ready {
					t.Fatal("owned IPv4 destination rejected", ready, err)
				}
			} else {
				server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) }), ReadHeaderTimeout: time.Second}
				done := make(chan error, 1)
				go func() { done <- server.Serve(listener) }()
				defer func() { _ = server.Close(); <-done }()
				ready, err := networkProbe(context.Background(), "http", "http://"+target+"/", syscall.Getpgrp())
				if err != nil || !ready {
					t.Fatal("HTTP dual-stack destination rejected", ready, err)
				}
			}
		})
	}
}
