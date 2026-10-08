// A static, owned fixture server/client built into a unique image by the tests.
package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"time"

	ws "github.com/coder/websocket"
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
		if err := verifyWebSocket(https, origin); err != nil {
			panic(err)
		}
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
			echoWebSocket(w, r)
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

// The maintained protocol implementation validates masks/opcodes/control frames
// and preserves bytes already buffered by HTTP Hijack during the upgrade.
func echoWebSocket(w http.ResponseWriter, r *http.Request) {
	connection, err := ws.Accept(w, r, nil)
	if err != nil {
		return
	}
	defer func() { _ = connection.CloseNow() }()
	connection.SetReadLimit(1024)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for {
		kind, payload, err := connection.Read(ctx)
		if err != nil {
			return
		}
		if err := connection.Write(ctx, kind, payload); err != nil {
			return
		}
	}
}

func verifyWebSocket(client *http.Client, origin string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	connection, _, err := ws.Dial(ctx, origin+"/ws", &ws.DialOptions{HTTPClient: client})
	if err != nil {
		return fmt.Errorf("TLS WebSocket upgrade: %w", err)
	}
	defer func() { _ = connection.CloseNow() }()
	for i := range 3 {
		var nonce [16]byte
		if _, err := rand.Read(nonce[:]); err != nil {
			return err
		}
		payload := []byte(fmt.Sprintf("probe-%x-%d", nonce, i))
		if err := connection.Write(ctx, ws.MessageText, payload); err != nil {
			return err
		}
		kind, echoed, err := connection.Read(ctx)
		if err != nil {
			return err
		}
		if kind != ws.MessageText || !bytes.Equal(echoed, payload) {
			return fmt.Errorf("TLS WebSocket payload changed: got %q want %q", echoed, payload)
		}
	}
	return nil
}
