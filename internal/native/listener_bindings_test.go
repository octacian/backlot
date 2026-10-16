//go:build darwin || linux

package native

import (
	"context"
	"net"
	"strconv"
	"syscall"
	"testing"
)

func TestExactListenerBindings(t *testing.T) {
	ipv4, err := net.Listen("tcp4", "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ipv4.Close() })
	port := ipv4.Addr().(*net.TCPAddr).Port
	ipv6, err := net.Listen("tcp6", net.JoinHostPort("::1", strconv.Itoa(port)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ipv6.Close() })
	expected := []string{net.JoinHostPort("0.0.0.0", strconv.Itoa(port)), net.JoinHostPort("::1", strconv.Itoa(port))}
	owned, err := OwnsListenerAddresses(context.Background(), syscall.Getpgrp(), port, expected)
	if err != nil || !owned {
		t.Fatal("exact IPv4 wildcard and IPv6 loopback refused", owned, err)
	}
	legacyPort := []string{net.JoinHostPort("0.0.0.0", "0"+strconv.Itoa(port)), net.JoinHostPort("::1", "0"+strconv.Itoa(port))}
	owned, err = OwnsListenerAddresses(context.Background(), syscall.Getpgrp(), port, legacyPort)
	if err != nil || !owned {
		t.Fatal("legacy numeric port spelling refused", owned, err)
	}
	owned, err = OwnsListenerAddresses(context.Background(), syscall.Getpgrp(), port, expected[:1])
	if err != nil || owned {
		t.Fatal("undeclared extra IPv6 loopback accepted", owned, err)
	}
	// Generic TCP's IPv4 wildcard can be an IPv6 dual-stack wildcard. Inspect the
	// actual listener rather than treating an address string as family evidence.
	generic, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = generic.Close() })
	genericPort := generic.Addr().(*net.TCPAddr).Port
	owned, err = OwnsListenerAddresses(context.Background(), syscall.Getpgrp(), genericPort, []string{net.JoinHostPort("0.0.0.0", strconv.Itoa(genericPort)), net.JoinHostPort("::1", strconv.Itoa(genericPort))})
	if err != nil || owned {
		t.Fatal("generic wildcard accepted as complete IPv4-only/IPv6-loopback policy", owned, err)
	}
}
