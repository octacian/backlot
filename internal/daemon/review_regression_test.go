//go:build darwin || linux

package daemon

import (
	"context"
	"net"
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
