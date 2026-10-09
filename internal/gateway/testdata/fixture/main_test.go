package main

import (
	"bufio"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	ws "github.com/coder/websocket"
)

func TestWebSocketEchoDiscriminatesPayload(t *testing.T) {
	good := httptest.NewServer(http.HandlerFunc(echoWebSocket))
	defer good.Close()
	if err := verifyWebSocket(good.Client(), good.URL); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"fixed", "corrupted"} {
		t.Run(mode, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				c, err := ws.Accept(w, r, nil)
				if err != nil {
					return
				}
				defer func() { _ = c.CloseNow() }()
				ctx, cancel := context.WithTimeout(context.Background(), time.Second)
				defer cancel()
				kind, payload, err := c.Read(ctx)
				if err != nil {
					return
				}
				if mode == "fixed" {
					payload = []byte("hello")
				} else {
					payload[0] ^= 1
				}
				_ = c.Write(ctx, kind, payload)
			}))
			defer server.Close()
			if err := verifyWebSocket(server.Client(), server.URL); err == nil || !strings.Contains(err.Error(), "payload changed") {
				t.Fatal("false-positive discriminator failed", err)
			}
		})
	}
}

func TestWebSocketBufferedFrameAndValidation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(echoWebSocket))
	defer server.Close()
	for _, tc := range []struct {
		name      string
		frame     []byte
		expected  string
		closeCode uint16
	}{
		{"masked-buffered-world", maskedText("world"), "world", 0},
		{"masked-buffered-different", maskedText("different application bytes"), "different application bytes", 0},
		{"unmasked-rejected", append([]byte{0x81, 5}, []byte("world")...), "", 1006},
		{"reserved-bit-rejected", append([]byte{0xc1}, maskedText("world")[1:]...), "", 1002},
		{"invalid-opcode-rejected", append([]byte{0x83}, maskedText("world")[1:]...), "", 1002},
	} {
		t.Run(tc.name, func(t *testing.T) {
			conn, err := net.DialTimeout("tcp", strings.TrimPrefix(server.URL, "http://"), time.Second)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = conn.Close() }()
			_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
			// One write pipelines the frame with HTTP headers, exercising Hijack's reader.
			request := fmt.Sprintf("GET /ws HTTP/1.1\r\nHost: %s\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Version: 13\r\nSec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\n\r\n", conn.RemoteAddr())
			if _, err := conn.Write(append([]byte(request), tc.frame...)); err != nil {
				t.Fatal(err)
			}
			reader := bufio.NewReader(conn)
			response, err := http.ReadResponse(reader, nil)
			if err != nil || response.StatusCode != 101 {
				t.Fatal("upgrade", response, err)
			}
			var header [2]byte
			if _, err := io.ReadFull(reader, header[:]); err != nil {
				// The library closes an unmasked client without accepting it or sending data.
				if tc.closeCode == 1006 && errors.Is(err, io.EOF) {
					return
				}
				t.Fatal(err)
			}
			if header[1]&0x80 != 0 || header[1]&0x7f == 126 || header[1]&0x7f == 127 {
				t.Fatal("unexpected server frame", header)
			}
			payload := make([]byte, int(header[1]&0x7f))
			if _, err := io.ReadFull(reader, payload); err != nil {
				t.Fatal(err)
			}
			if tc.closeCode != 0 {
				if header[0] != 0x88 || len(payload) < 2 || binary.BigEndian.Uint16(payload[:2]) != tc.closeCode {
					t.Fatal("invalid frame accepted", header, payload)
				}
			} else if header[0] != 0x81 || string(payload) != tc.expected {
				t.Fatal("actual payload was not echoed", header, string(payload))
			}
		})
	}
}
func maskedText(payload string) []byte {
	mask := []byte{0x13, 0x27, 0x48, 0x9a}
	frame := append([]byte{0x81, 0x80 | byte(len(payload))}, mask...)
	for i, b := range []byte(payload) {
		frame = append(frame, b^mask[i%4])
	}
	return frame
}
