//go:build darwin || linux

package native

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
)

func TestSessionListenerTransportPrivacy(t *testing.T) {
	for _, mode := range []string{"deadline", "cancel", "read-close", "write-reset"} {
		t.Run(mode, func(t *testing.T) {
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
			id := Identity{Purpose: "application", Control: socket, Token: strings.Repeat("a", 64), Birth: "private-birth", Component: "api", Attempt: strings.Repeat("b", 64)}
			if mode == "write-reset" {
				// Exceed the socket buffers, but prepare the payload before the
				// request budget starts so setup cannot masquerade as a reset.
				id.Birth = strings.Repeat("private-birth", 1<<16)
			}
			group := &Group{Identity: id}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
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
						_, err = conn.Read(b[:])
					}
				} else {
					var request cleanupRequest
					err = json.NewDecoder(conn).Decode(&request)
				}
				close(received)
				if err == nil && (mode == "deadline" || mode == "cancel") {
					if mode == "cancel" {
						cancel()
					}
					var b [1]byte
					_, err = conn.Read(b[:])
					if errors.Is(err, io.EOF) {
						err = nil
					}
				}
			}()
			t.Cleanup(func() {
				cancel()
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
			requestCtx, requestCancel := context.WithTimeout(ctx, budget)
			defer requestCancel()
			owned, conflict, err := group.ListenerEndpoint(requestCtx, "127.0.0.1:12345")
			if err == nil || owned || conflict {
				t.Fatal("transport failure accepted", owned, conflict, err)
			}
			select {
			case <-received:
			default:
				t.Fatal("request did not reach fixture")
			}
			for _, secret := range []string{socket, root, "private-birth", id.Token, id.Attempt} {
				if strings.Contains(err.Error(), secret) {
					t.Fatal("private transport information escaped")
				}
			}
			var transport *net.OpError
			if mode == "read-close" {
				if !errors.Is(err, io.EOF) {
					t.Fatal("private EOF cause lost", err)
				}
			} else if !errors.As(err, &transport) {
				t.Fatal("private transport cause lost", err)
			}
			if mode == "write-reset" && !errors.Is(err, syscall.EPIPE) && !errors.Is(err, syscall.ECONNRESET) {
				t.Fatal("private reset cause lost", err)
			}
			if mode == "deadline" && !transport.Timeout() {
				t.Fatal("deadline classification lost", err)
			}
			if mode == "cancel" && !errors.Is(err, context.Canceled) {
				t.Fatal("cancellation classification lost", err)
			}
			expected := "session listener observation unavailable"
			if mode == "write-reset" {
				expected = "session listener authority unavailable"
			}
			if err.Error() != expected {
				t.Fatal("wrong bounded transport branch", err)
			}
		})
	}
}
