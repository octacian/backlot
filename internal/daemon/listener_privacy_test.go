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
			done := make(chan error, 1)
			joined = false
			go func() {
				conn, err := listener.AcceptUnix()
				if err != nil {
					done <- err
					return
				}
				defer func() { _ = conn.Close(); done <- err }()
				_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
				if mode == "write-reset" {
					var b [1]byte
					_, err = conn.Read(b[:])
				} else {
					var payload json.RawMessage
					err = json.NewDecoder(conn).Decode(&payload)
					if err == nil {
						_, err = io.Copy(io.Discard, conn)
						if errors.Is(err, io.EOF) {
							err = nil
						}
					}
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
			identity := native.Identity{Protocol: 1, PID: 42, Purpose: "application", Control: socket, Token: strings.Repeat("a", 64), Birth: "private-birth", Component: "private-component", Attempt: strings.Repeat("c", 64)}
			if mode == "write-reset" {
				identity.Birth = strings.Repeat("private-birth", 1<<18)
			}
			group := &native.Group{Identity: identity}
			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel()
			ready, err := networkProbeWith(ctx, "tcp", "12345", group.ListenerEndpoint)
			if err == nil || ready {
				t.Fatal("membership transport failed open", ready, err)
			}
			var transport *net.OpError
			if !errors.As(err, &transport) || mode == "deadline" && !transport.Timeout() {
				t.Fatal("transport classification lost", err)
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
