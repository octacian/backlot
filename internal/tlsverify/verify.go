// Package tlsverify refreshes Go trust policy in bounded, joined, unprivileged
// invocations of the current executable. It never injects certificate roots.
package tlsverify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"
)

// Request describes one verification; it is private IPC, never a daemon API.
type Request struct {
	// URL selects an HTTP(S) request or TLS handshake destination.
	URL string `json:"url,omitempty"`
	// Pinned supplies an already ownership-validated numeric dial destination.
	Pinned string `json:"pinned,omitempty"`
	// TLSOnly requests only a hostname/date/chain-verified handshake.
	TLSOnly bool `json:"tls_only,omitempty"`
	// RootPEM requests root-specific system verification instead of networking.
	RootPEM string `json:"root_pem,omitempty"`
}

// Result preserves only verification and HTTP readiness inputs, never body data.
type Result struct {
	// Verified reports successful normal Go verification.
	Verified bool `json:"verified"`
	// StatusCode is the HTTP response status without following redirects.
	StatusCode int `json:"status_code,omitempty"`
	// Marker is the public publication marker.
	Marker string `json:"marker,omitempty"`
	// Location is the unvisited redirect location.
	Location string `json:"location,omitempty"`
	// Failure classifies rejection versus unavailable observation.
	Failure string `json:"failure,omitempty"`
}

// Run joins a fresh same-executable verifier with a hard upper bound. osTrust
// removes certificate-file overrides only for independent OS observations.
func Run(ctx context.Context, request Request, osTrust bool) (Result, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	executable, err := os.Executable()
	if err != nil {
		return Result{}, err
	}
	data, err := json.Marshal(request)
	if err != nil {
		return Result{}, err
	}
	command := exec.CommandContext(ctx, executable, "__backlot-tls-verify")
	command.Stdin = bytes.NewReader(data)
	command.Env = os.Environ()
	if osTrust {
		command.Env = nil
		for _, entry := range os.Environ() {
			if !strings.HasPrefix(entry, "SSL_CERT_FILE=") && !strings.HasPrefix(entry, "SSL_CERT_DIR=") {
				command.Env = append(command.Env, entry)
			}
		}
	}
	// Race-instrumented child fixtures must fit the same probe deadline.
	command.Env = append(command.Env, "GORACE="+os.Getenv("GORACE")+" atexit_sleep_ms=0")
	var output bytes.Buffer
	command.Stdout = &output
	command.Stderr = io.Discard
	if err := command.Run(); err != nil {
		return Result{}, errors.Join(ctx.Err(), err)
	}
	var result Result
	if err := json.Unmarshal(output.Bytes(), &result); err != nil {
		return result, err
	}
	if !result.Verified && result.Failure != "rejected" && result.Failure != "unavailable" {
		return result, errors.New("invalid TLS verifier result")
	}
	return result, nil
}
