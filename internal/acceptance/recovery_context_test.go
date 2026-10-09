//go:build darwin && arm64

package acceptance

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	v1 "github.com/octacian/backlot/api/v1"
)

// A CLI-shaped native seam exercises the real late-recovery callback. It is not
// Backlot runtime acceptance; the separate real public selection supplies that.
func TestProviderRecoveryCLIHelper(t *testing.T) {
	if os.Getenv("BACKLOT_RECOVERY_PROVIDER") == "" {
		return
	}
	var args []string
	for n, arg := range os.Args {
		if arg == "--" {
			args = os.Args[n+1:]
			break
		}
	}
	if len(args) == 0 {
		os.Exit(2)
	}
	state := ""
	for n, arg := range args {
		if arg == "--state-dir" && n+1 < len(args) {
			state = args[n+1]
		}
	}
	if args[0] == "destroy" {
		status, err := unixCall(os.Getenv("BACKLOT_RECOVERY_PROVIDER"), http.MethodDelete, "/owned")
		if err != nil || status != http.StatusNoContent {
			os.Exit(1)
		}
		_ = json.NewEncoder(os.Stdout).Encode(v1.InstanceResponse{APIVersion: v1.Version, Instance: v1.Instance{ID: "owned", Status: v1.Destroyed}})
		os.Exit(0)
	}
	if len(args) < 2 || state == "" {
		os.Exit(2)
	}
	path := filepath.Join(state, "control.sock")
	switch args[1] {
	case "status":
		status, err := unixCall(path, http.MethodGet, "/_ping")
		if err != nil || status != 200 {
			os.Exit(1)
		}
		os.Exit(0)
	case "stop":
		status, err := unixCall(path, http.MethodPost, "/stop")
		if err != nil || status != 200 {
			os.Exit(1)
		}
		os.Exit(0)
	case "serve":
		if err := os.MkdirAll(state, 0700); err != nil {
			os.Exit(3)
		}
		listener, err := net.Listen("unix", path)
		if err != nil {
			os.Exit(4)
		}
		halt := make(chan struct{})
		var once sync.Once
		server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(200)
			if r.URL.Path == "/stop" {
				once.Do(func() { close(halt) })
			}
		})}
		done := make(chan error, 1)
		go func() { done <- server.Serve(listener) }()
		<-halt
		_ = server.Close()
		<-done
	default:
		os.Exit(2)
	}
}
func TestLateProviderRecoveryContext(t *testing.T) {
	root, logFile, start := ownerFixture(t)
	parent := &harness{t: t}
	var owner *harness
	t.Run("finished-original-owner", func(child *testing.T) {
		owner = &harness{t: child}
		waited := start(owner, nil)
		_ = owner.daemon.Process.Kill()
		if !owner.consumeOwner(time.Second).joined {
			child.Fatal("initial owner unjoined")
		}
		<-waited
	})
	upstreamPath := filepath.Join(root, "recovery-upstream.sock")
	listener, err := net.Listen("unix", upstreamPath)
	if err != nil {
		t.Fatal(err)
	}
	var fail atomic.Bool
	fail.Store(true)
	upstream := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			if fail.Load() {
				w.WriteHeader(http.StatusConflict)
			} else {
				w.WriteHeader(http.StatusNoContent)
			}
			return
		}
		w.WriteHeader(200)
	})}
	upstreamDone := make(chan error, 1)
	go func() { upstreamDone <- upstream.Serve(listener) }()
	defer func() { _ = upstream.Close(); <-upstreamDone }()
	endpoint := filepath.Join(root, "late.sock")
	proxyListener, path, err := listenProvider(endpoint, upstreamPath)
	if err != nil {
		t.Fatal(err)
	}
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", upstreamPath)
	}}
	target, _ := url.Parse("http://provider")
	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.Transport = transport
	server := &http.Server{Handler: proxy}
	done := make(chan error, 1)
	go func() { done <- server.Serve(proxyListener) }()
	dependency := parent.dependency(owner, path, server, done, transport)
	dependency.track("owned")
	wrapper := filepath.Join(root, "recovery-cli")
	if err := os.WriteFile(wrapper, []byte("#!/bin/sh\nexec \"$BACKLOT_STARTUP_HELPER_BINARY\" -test.run=^TestProviderRecoveryCLIHelper$ -- \"$@\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BACKLOT_RECOVERY_PROVIDER", endpoint)
	owner.binary = wrapper
	owner.state = filepath.Join(root, "recovery-state")
	owner.daemonLog = logFile
	original := dependency.recover
	dependency.recover = func() error {
		if owner.t != t {
			return errors.New("late cleanup retained completed subtest context")
		}
		return original()
	}
	if err := parent.cleanupDependencies(); err == nil || !strings.Contains(err.Error(), "usable provider handoff") {
		t.Fatalf("late failure context/handoff lost: %v", err)
	}
	if owner.daemon != nil || dependency.released {
		t.Fatal("failed late cleanup inferred destruction from join")
	}
	fail.Store(false)
	if err := parent.cleanupDependencies(); err != nil {
		t.Fatal(err)
	}
	if owner.daemon != nil || !dependency.released || !owner.logClosed {
		t.Fatal("actual recovery callback did not verify destruction, join and log release")
	}
	t.Log("completed child context replaced by active parent during real cleanup callback; CLI-shaped destroy failure handed off provider path, next owner destroyed/joined/released")
}
