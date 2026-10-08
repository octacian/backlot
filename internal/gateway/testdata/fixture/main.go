// A static, owned fixture server/client built into a unique image by the tests.
package main

import (
	"bufio"
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"time"
)

func client() *http.Client {
	pool, err := x509.SystemCertPool()
	if err != nil {
		panic(err)
	}
	if path := os.Getenv("CERT"); path != "" {
		pool = x509.NewCertPool()
		cert, err := os.ReadFile(path)
		if err != nil || !pool.AppendCertsFromPEM(cert) {
			panic("missing fixture trust")
		}
	}
	transport := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}}
	if address := os.Getenv("GATEWAY_ADDRESS"); address != "" {
		transport.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, network, address)
		}
	}
	return &http.Client{Timeout: 5 * time.Second, Transport: transport}
}

func main() {
	if os.Getenv("CLIENT") == "1" || len(os.Args) > 1 && os.Args[1] == "client" {
		if host := os.Getenv("BACKEND_HOST"); host != "" {
			response, err := http.Get("http://" + net.JoinHostPort(host, os.Getenv("BACKEND_PORT")) + "/direct")
			if err != nil {
				panic(err)
			}
			body, err := io.ReadAll(response.Body)
			_ = response.Body.Close()
			if err != nil || string(body) != "api:/direct" {
				panic("container-to-native reference failed")
			}
		}
		origin := os.Getenv("ORIGIN")
		https := client()
		response, err := https.Get(origin + "/ssr")
		if err != nil {
			panic(err)
		}
		defer func() { _ = response.Body.Close() }()
		body, err := io.ReadAll(response.Body)
		if err != nil || response.StatusCode != 200 || string(body) != "ssr:api:/value" {
			panic(fmt.Sprintf("HTTPS SSR failed: status=%d body=%s error=%v", response.StatusCode, body, err))
		}
		websocket(https, origin)
		https.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }
		redirect, err := https.Get(origin + "/redirect")
		if err != nil {
			panic(err)
		}
		_ = redirect.Body.Close()
		cookies := redirect.Cookies()
		if redirect.StatusCode != 302 || redirect.Header.Get("Location") != origin+"/ssr" || len(cookies) != 1 || !cookies[0].Secure || !cookies[0].HttpOnly {
			panic("canonical redirect/cookie check failed")
		}
		fmt.Println("container HTTPS TLS+SSR+API+WebSocket+redirect+cookie verified")
		return
	}
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ssr":
			response, err := client().Get(os.Getenv("ORIGIN") + "/api/value")
			if err != nil {
				http.Error(w, "SSR failed", 502)
				return
			}
			defer func() { _ = response.Body.Close() }()
			_, _ = io.WriteString(w, "ssr:")
			_, _ = io.Copy(w, response.Body)
		case "/redirect":
			http.SetCookie(w, &http.Cookie{Name: "fixture", Value: "yes", Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode, Path: "/"})
			http.Redirect(w, r, os.Getenv("ORIGIN")+"/ssr", 302)
		case "/ws":
			conn, _, err := w.(http.Hijacker).Hijack()
			if err != nil {
				return
			}
			defer func() { _ = conn.Close() }()
			_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
			_, _ = io.WriteString(conn, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: s3pPLMBiTxaQ9kYGzzhZRbK+xOo=\r\n\r\n")
			// Receive the known masked 'hello' test frame and return an unmasked frame.
			frame := make([]byte, 11)
			if _, err := io.ReadFull(conn, frame); err == nil {
				_, _ = conn.Write([]byte{0x81, 5, 'h', 'e', 'l', 'l', 'o'})
			}
		default:
			_, _ = fmt.Fprintf(w, "%s:%s", os.Getenv("ROLE"), r.URL.Path)
		}
	})
	address := net.JoinHostPort(os.Getenv("BIND"), os.Getenv("PORT"))
	server := &http.Server{Addr: address, ReadHeaderTimeout: 5 * time.Second}
	if err := server.ListenAndServe(); err != nil {
		panic(err)
	}
}

func websocket(client *http.Client, origin string) {
	u, err := url.Parse(origin)
	if err != nil {
		panic(err)
	}
	address := u.Host
	if u.Port() == "" {
		address = net.JoinHostPort(u.Hostname(), "443")
	}
	if fixtureAddress := os.Getenv("GATEWAY_ADDRESS"); fixtureAddress != "" {
		address = fixtureAddress
	}
	config := client.Transport.(*http.Transport).TLSClientConfig.Clone()
	config.ServerName = u.Hostname()
	connection, err := tls.DialWithDialer(&net.Dialer{Timeout: 5 * time.Second}, "tcp", address, config)
	if err != nil {
		panic(err)
	}
	defer func() { _ = connection.Close() }()
	_ = connection.SetDeadline(time.Now().Add(5 * time.Second))
	_, _ = fmt.Fprintf(connection, "GET /ws HTTP/1.1\r\nHost: %s\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Version: 13\r\nSec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\n\r\n", u.Host)
	reader := bufio.NewReader(connection)
	response, err := http.ReadResponse(reader, nil)
	if err != nil || response.StatusCode != 101 || response.Header.Get("Sec-WebSocket-Accept") != "s3pPLMBiTxaQ9kYGzzhZRbK+xOo=" {
		panic("TLS WebSocket upgrade failed")
	}
	_, _ = connection.Write([]byte{0x81, 0x85, 0, 0, 0, 0, 'h', 'e', 'l', 'l', 'o'})
	frame := make([]byte, 7)
	if _, err := io.ReadFull(reader, frame); err != nil || frame[0] != 0x81 || frame[1] != 5 || string(frame[2:]) != "hello" {
		panic("TLS WebSocket frame roundtrip failed")
	}
}
