package tlsverify

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"time"
)

// Helper handles only the private same-executable protocol. Main and package
// test entry points call it before their ordinary argument parsing.
func Helper() bool {
	if len(os.Args) != 2 || os.Args[1] != "__backlot-tls-verify" {
		return false
	}
	var request Request
	decoder := json.NewDecoder(io.LimitReader(os.Stdin, 1<<20))
	decoder.DisallowUnknownFields()
	result := Result{Failure: "unavailable"}
	if decoder.Decode(&request) == nil {
		var extra any
		if decoder.Decode(&extra) == io.EOF {
			result = verify(request)
		}
	}
	_ = json.NewEncoder(os.Stdout).Encode(result)
	return true
}

func verify(r Request) Result {
	ctx, cancel := context.WithTimeout(context.Background(), 2500*time.Millisecond)
	defer cancel()
	if r.RootPEM != "" {
		block, rest := pem.Decode([]byte(r.RootPEM))
		if block == nil || block.Type != "CERTIFICATE" || len(bytes.TrimSpace(rest)) != 0 {
			return Result{Failure: "unavailable"}
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil || !cert.IsCA || cert.CheckSignatureFrom(cert) != nil {
			return Result{Failure: "unavailable"}
		}
		chains, err := cert.Verify(x509.VerifyOptions{KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}})
		result := verification(err)
		// invariant: an alternate chain cannot establish trust in this exact root.
		if err == nil {
			result.Verified = false
			for _, chain := range chains {
				if len(chain) == 1 && bytes.Equal(chain[0].Raw, cert.Raw) {
					result.Verified = true
				}
			}
			if !result.Verified {
				result.Failure = "rejected"
			}
		}
		return result
	}
	u, err := url.Parse(r.URL)
	if err != nil || u.Hostname() == "" || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil {
		return Result{Failure: "unavailable"}
	}
	dial := (&net.Dialer{}).DialContext
	if r.Pinned != "" {
		host, _, err := net.SplitHostPort(r.Pinned)
		if err != nil || net.ParseIP(host) == nil {
			return Result{Failure: "unavailable"}
		}
		dial = func(ctx context.Context, network, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, network, r.Pinned)
		}
	}
	config := &tls.Config{MinVersion: tls.VersionTLS12, ServerName: u.Hostname()}
	if r.TLSOnly {
		address := u.Host
		if u.Port() == "" {
			address = net.JoinHostPort(u.Hostname(), "443")
		}
		raw, err := dial(ctx, "tcp", address)
		if err != nil {
			return verification(err)
		}
		defer func() { _ = raw.Close() }()
		conn := tls.Client(raw, config)
		return verification(conn.HandshakeContext(ctx))
	}
	transport := &http.Transport{DialContext: dial, TLSClientConfig: config}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, r.URL, nil)
	if err != nil {
		return verification(err)
	}
	response, err := client.Do(request)
	if err != nil {
		return verification(err)
	}
	_ = response.Body.Close()
	return Result{Verified: true, StatusCode: response.StatusCode, Marker: response.Header.Get("Backlot-Instance"), Location: response.Header.Get("Location")}
}

func verification(err error) Result {
	if err == nil {
		return Result{Verified: true}
	}
	var unknown x509.UnknownAuthorityError
	var invalid x509.CertificateInvalidError
	var host x509.HostnameError
	if errors.As(err, &unknown) || errors.As(err, &invalid) || errors.As(err, &host) {
		return Result{Failure: "rejected"}
	}
	return Result{Failure: "unavailable"}
}
