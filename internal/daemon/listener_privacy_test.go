//go:build darwin || linux

package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	v1 "github.com/octacian/backlot/api/v1"
	"github.com/octacian/backlot/internal/native"
)

// Exercise the real membership transport and probe boundary, then persist the
// returned public failure in the settled export fixture. No private authority is
// a runtime resource or serialization input in that fixture.
func TestSessionListenerFailureExecutionExportPrivacy(t *testing.T) {
	for _, mode := range []string{"deadline", "write-reset"} {
		t.Run(mode, func(t *testing.T) {
			svc, instance, request := exportFixture(t)
			root, err := os.MkdirTemp("/tmp", "bl-native-")
			if err != nil {
				t.Fatal(err)
			}
			joined := true
			t.Cleanup(func() {
				if !joined {
					t.Log("preserved unjoined socket fixture", root)
					return
				}
				if err := os.RemoveAll(root); err != nil {
					t.Error(err)
				}
			})
			socket := filepath.Join(root, "private.sock")
			listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: socket, Net: "unix"})
			if err != nil {
				t.Fatal(err)
			}
			identity := native.Identity{Protocol: 1, PID: 42, Purpose: "application", Control: socket, Token: strings.Repeat("a", 64), Birth: "private-birth", Component: "private-component", Attempt: strings.Repeat("c", 64)}
			if mode == "write-reset" {
				// Match the native transport fixture: exceed the socket buffers,
				// preparing the bounded payload before starting the request budget.
				identity.Birth = strings.Repeat("private-birth", 1<<16)
			}
			group := &native.Group{Identity: identity}
			done := make(chan error, 1)
			received := make(chan struct{})
			serverReady := make(chan struct{})
			joined = false
			go func() {
				close(serverReady)
				conn, err := listener.AcceptUnix()
				if err != nil {
					done <- err
					return
				}
				defer func() { _ = conn.Close(); done <- err }()
				_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
				if mode == "write-reset" {
					err = conn.SetReadBuffer(1024)
					if err == nil {
						var b [1]byte
						var n int
						n, err = conn.Read(b[:])
						if err == nil && n != 1 {
							err = io.ErrUnexpectedEOF
						}
					}
				} else {
					var payload json.RawMessage
					err = json.NewDecoder(conn).Decode(&payload)
					close(received)
					if err == nil {
						_, err = io.Copy(io.Discard, conn)
						if errors.Is(err, io.EOF) {
							err = nil
						}
					}
				}
				if mode == "write-reset" && err == nil {
					close(received)
					// Closing with unread request bytes supplies the real reset.
				}
			}()
			t.Cleanup(func() {
				_ = listener.Close()
				select {
				case err := <-done:
					joined = true
					if err != nil {
						t.Error("socket fixture", err)
					}
				case <-time.After(3 * time.Second):
					t.Error("socket fixture not joined")
				}
			})
			select {
			case <-serverReady:
			case <-time.After(time.Second):
				t.Fatal("socket fixture not ready")
			}
			budget := time.Second
			if mode == "deadline" {
				budget = 100 * time.Millisecond
			}
			ctx, cancel := context.WithTimeout(context.Background(), budget)
			defer cancel()
			ready, err := networkProbeWith(ctx, "tcp", "12345", group.ListenerEndpoint)
			if err == nil || ready {
				t.Fatal("membership transport failed open", ready, err)
			}
			var transport *net.OpError
			if !errors.As(err, &transport) || mode == "deadline" && !transport.Timeout() {
				t.Fatal("transport classification lost", err)
			}
			select {
			case <-received:
			default:
				t.Fatal("request did not reach fixture")
			}
			if mode == "write-reset" && (transport.Timeout() || !errors.Is(err, syscall.EPIPE) && !errors.Is(err, syscall.ECONNRESET)) {
				t.Fatal("actual non-timeout reset cause lost", err)
			}
			// Same public error-to-failure assignment used by execute; keep arbitrary
			// application streams out of this diagnostic privacy test.
			instance.Execution.Failure = err.Error()
			if _, err := svc.store.execution(instance.ID, instance.Status, instance.Execution); err != nil {
				t.Fatal(err)
			}
			current, err := svc.store.inspect(instance.ID)
			if err != nil {
				t.Fatal(err)
			}
			public, err := json.Marshal(current.Execution)
			if err != nil {
				t.Fatal(err)
			}
			exported, err := svc.export(context.Background(), request)
			if err != nil || !exported.Published {
				t.Fatal("settled diagnostic export", err)
			}
			response, err := json.Marshal(exported)
			if err != nil {
				t.Fatal(err)
			}
			report, err := os.ReadFile(filepath.Join(request.Output, "report.json"))
			if err != nil {
				t.Fatal(err)
			}
			html, err := os.ReadFile(filepath.Join(request.Output, "index.html"))
			if err != nil {
				t.Fatal(err)
			}
			for _, data := range [][]byte{public, response, report, html} {
				text := string(data)
				if !strings.Contains(text, "session listener") {
					t.Fatal("public diagnostic lost")
				}
				for _, secret := range []string{socket, root, identity.Token, identity.Attempt, "private-birth", "private-component"} {
					if strings.Contains(text, secret) {
						t.Fatal("private authority leaked into execution/export")
					}
				}
			}
			reportResult := readExportReport(t, request.Output).Result
			if reportResult.Failure != current.Execution.Failure || len(reportResult.NativeCleanup) != 0 || reportResult.Status != v1.Failed {
				t.Fatal("export changed original execution result", reportResult)
			}
		})
	}
}
