package manager

import (
	"errors"
	"io"
	"net"
	"os"
	"testing"
	"time"
)

func postgresPipe(t *testing.T) (*postgresDeadlineConn, net.Conn, func(func()) <-chan struct{}) {
	t.Helper()
	a, b := net.Pipe()
	var owners []chan struct{}
	t.Cleanup(func() {
		_ = a.Close()
		_ = b.Close()
		for _, done := range owners {
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Error("owned pipe operation did not join after close")
			}
		}
	})
	return &postgresDeadlineConn{Conn: a}, b, func(work func()) <-chan struct{} {
		done := make(chan struct{})
		owners = append(owners, done)
		go func() { defer close(done); work() }()
		return done
	}
}

func awaitPostgresPipe(t *testing.T, done <-chan struct{}, budget time.Duration) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(budget):
		t.Fatal("original pipe operation exceeded finite bound")
	}
}

func requirePostgresTimeout(t *testing.T, err error) {
	t.Helper()
	var timeout net.Error
	if !errors.As(err, &timeout) || !timeout.Timeout() {
		t.Fatal("original socket did not time out", err)
	}
}

func TestPostgreSQLTransportDeadline(t *testing.T) {
	for _, scenario := range []string{"startup", "clear-read", "blocked-write", "idle-renewal", "trickled-read"} {
		t.Run(scenario, func(t *testing.T) {
			t.Parallel()
			conn, peer, run := postgresPipe(t)
			if err := conn.SetDeadline(time.Now().Add(100 * time.Millisecond)); err != nil {
				t.Fatal(err)
			}
			if scenario != "startup" {
				if err := conn.SetDeadline(time.Time{}); err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "idle-renewal" {
				time.Sleep(postgresResponseTimeout + 20*time.Millisecond)
			}
			if scenario != "blocked-write" {
				run(func() {
					var request [1]byte
					if _, err := io.ReadFull(peer, request[:]); err != nil {
						return
					}
					if scenario == "idle-renewal" {
						_, _ = peer.Write([]byte{1})
					}
					if scenario == "trickled-read" {
						ticker := time.NewTicker(50 * time.Millisecond)
						defer ticker.Stop()
						for {
							if _, err := peer.Write([]byte{1}); err != nil {
								return
							}
							<-ticker.C
						}
					}
				})
			}
			started := time.Now()
			var operationErr error
			var received int
			done := run(func() {
				_, operationErr = conn.Write([]byte{1})
				if operationErr != nil || scenario == "blocked-write" {
					return
				}
				var response [1]byte
				for {
					_, operationErr = conn.Read(response[:])
					if operationErr != nil {
						return
					}
					received++
					if scenario == "idle-renewal" {
						return
					}
				}
			})
			budget := postgresResponseTimeout + 3*time.Second
			if scenario == "startup" {
				budget = time.Second
			}
			awaitPostgresPipe(t, done, budget)
			if scenario == "idle-renewal" {
				if operationErr != nil || received != 1 {
					t.Fatal("expired idle socket did not renew on request", operationErr)
				}
				return
			}
			requirePostgresTimeout(t, operationErr)
			if scenario != "startup" && time.Since(started) < postgresResponseTimeout/2 {
				t.Fatal("startup clear did not restore full response budget")
			}
			if scenario == "trickled-read" && received < 2 {
				t.Fatal("no real partial response exercised")
			}
		})
	}
}

type postgresDeadlineFailure struct {
	net.Conn
	wrote bool
}

func (*postgresDeadlineFailure) SetDeadline(time.Time) error { return os.ErrInvalid }
func (c *postgresDeadlineFailure) Write([]byte) (int, error) {
	c.wrote = true
	return 0, net.ErrClosed
}

func TestPostgreSQLDeadlineFailureAbortsWrite(t *testing.T) {
	conn, _, _ := postgresPipe(t)
	failed := &postgresDeadlineFailure{Conn: conn.Conn}
	conn.Conn = failed
	if _, err := conn.Write([]byte{1}); !errors.Is(err, os.ErrInvalid) || failed.wrote {
		t.Fatal("deadline error allowed unbounded write", err)
	}
}
