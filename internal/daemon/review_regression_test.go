//go:build darwin || linux

package daemon

import (
	"context"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"syscall"
	"testing"
	"time"

	v1 "github.com/octacian/backlot/api/v1"
	"github.com/octacian/backlot/internal/native"
	bolt "go.etcd.io/bbolt"
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

func TestExecutionAcceptanceRechecksCancellationAndLease(t *testing.T) {
	for _, action := range []string{"cancel", "expire", "expired-without-reconcile"} {
		t.Run(action, func(t *testing.T) {
			root := shortTemp(t)
			config := writeFixture(t, root)
			store, err := openStore(shortTemp(t))
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = store.db.Close() }()
			reached := make(chan string, 1)
			release := make(chan struct{})
			s := &service{store: store, lease: time.Minute, beforeExecution: func(id string) { reached <- id; <-release }}
			result := make(chan error, 1)
			go func() {
				_, err := s.run(context.Background(), v1.RunRequest{APIVersion: v1.Version, Plan: request(root, config, "test").Plan, Options: v1.ExecutionOptions{StartupTimeout: "0s", JobTimeout: "0s"}})
				result <- err
			}()
			id := <-reached
			if action == "cancel" {
				if _, err := s.cancelRuntime(id); err != nil {
					t.Fatal(err)
				}
			} else {
				err := store.db.Update(func(tx *bolt.Tx) error {
					r, err := load(tx, id)
					if err != nil {
						return err
					}
					r.Instance.LeaseExpiresAt = time.Now().Add(-time.Second).Format(time.RFC3339Nano)
					return put(tx, "instances", id, r)
				})
				if err != nil {
					t.Fatal(err)
				}
				if action == "expire" {
					if err := s.expire(); err != nil {
						t.Fatal(err)
					}
				}
			}
			close(release)
			if err := <-result; err == nil {
				t.Fatal("stale prepared response resurrected rejected work")
			}
			instance, err := store.inspect(id)
			if err != nil {
				t.Fatal(err)
			}
			if instance.Execution != nil || len(s.executions) != 0 {
				t.Fatal("rejected acceptance launched an execution", instance)
			}
			journal, err := store.runtime(id)
			if err != nil || len(journal.Groups) != 0 || journal.LaunchPending {
				t.Fatal("rejected acceptance recorded launch", journal, err)
			}
			if action == "cancel" && instance.Status != v1.Cancelled {
				t.Fatal(instance.Status)
			}
		})
	}
}

func TestCancellationRechecksRegisteredOwner(t *testing.T) {
	root := shortTemp(t)
	config := writeFixture(t, root)
	store, err := openStore(shortTemp(t))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.db.Close() }()
	s := &service{store: store, lease: time.Minute}
	response, err := s.prepare(context.Background(), request(root, config, "test"))
	if err != nil {
		t.Fatal(err)
	}
	id := response.Instance.ID
	// Hold the same instance mutation lock used by run acceptance. Cancellation
	// must examine the execution owner only after acquiring it.
	unlock := s.lockMutation(id)
	done := make(chan struct{})
	cancelled := make(chan struct{})
	entry := &execution{done: done, cancel: func() { close(cancelled); close(done) }}
	result := make(chan error, 1)
	go func() { _, err := s.cancelRuntime(id); result <- err }()
	select {
	case err := <-result:
		unlock()
		t.Fatalf("cancellation bypassed acceptance mutation lock: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	s.mu.Lock()
	s.executions = map[string]*execution{id: entry}
	s.mu.Unlock()
	unlock()
	if err := <-result; err != nil {
		t.Fatal(err)
	}
	select {
	case <-cancelled:
	default:
		t.Fatal("registered owner missed by cancellation")
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
			listener, err := lc.Listen(context.Background(), "tcp6", "[::]:0")
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = listener.Close() }()
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
				other, err := net.Listen("tcp4", target)
				if err != nil {
					t.Fatal(err)
				}
				defer func() { _ = other.Close() }()
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
