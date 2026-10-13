//go:build darwin && arm64

package acceptance

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

type controlledUpstream struct {
	server *http.Server
	done   <-chan error
	joined bool
}
type controlledFixture struct {
	t         *testing.T
	root      string
	logFile   *os.File
	forwarder *httptest.Server
	parent    *harness
	retained  []*harness
	upstreams []*controlledUpstream
	repair    func()
	cleaned   bool
	report    string
}

// Cleanup is registered as soon as the root is owned, including setup failures.
// The second registration runs before environment restoration and is idempotent.
func ownerFixture(t *testing.T, setup func(*controlledFixture) error) *controlledFixture {
	t.Helper()
	root, err := newAcceptanceRoot("bl-owner-")
	if err != nil {
		t.Fatal(err)
	}
	f := &controlledFixture{t: t, root: root, parent: &harness{t: t}, report: os.Getenv("BACKLOT_UNWIND_REPORT")}
	t.Cleanup(f.cleanup)
	f.logFile, err = os.OpenFile(filepath.Join(root, "log"), os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	f.forwarder = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("BACKLOT_STARTUP_HELPER_ROOT", root)
	t.Setenv("BACKLOT_STARTUP_HELPER_FORWARDER", f.forwarder.URL)
	t.Setenv("BACKLOT_STARTUP_HELPER_BINARY", binary)
	t.Cleanup(f.cleanup)
	if setup != nil {
		if err := setup(f); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "helper"), []byte("#!/bin/sh\nexec \"$BACKLOT_STARTUP_HELPER_BINARY\" -test.run=^TestOwnedDaemonStartupHelper$ -- \"$@\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	return f
}
func (f *controlledFixture) retain(h *harness) {
	for _, known := range f.retained {
		if known == h {
			return
		}
	}
	f.retained = append(f.retained, h)
}
func (f *controlledFixture) start(h *harness, delivery <-chan struct{}) <-chan struct{} {
	f.t.Helper()
	if h.daemon != nil {
		f.t.Fatal("fixture cannot replace retained owner")
	}
	h.binary = filepath.Join(f.root, "helper")
	h.root = f.root
	h.daemonLog = f.logFile
	command := exec.Command(h.binary, "daemon", "serve")
	command.Stdout = f.logFile
	command.Stderr = f.logFile
	f.retain(h)
	if err := command.Start(); err != nil {
		f.t.Fatal(err)
	}
	h.daemon = command
	done := make(chan error, 1)
	h.daemonDone = done
	waited := make(chan struct{})
	go func() {
		err := command.Wait()
		close(waited)
		if delivery != nil {
			<-delivery
		}
		done <- err
	}()
	return waited
}
func (f *controlledFixture) upstream(handler http.Handler) string {
	f.t.Helper()
	path := filepath.Join(f.root, fmt.Sprintf("upstream-%d.sock", len(f.upstreams)))
	listener, err := net.Listen("unix", path)
	if err != nil {
		f.t.Fatal(err)
	}
	u := &controlledUpstream{server: &http.Server{Handler: handler}}
	done := make(chan error, 1)
	u.done = done
	f.upstreams = append(f.upstreams, u)
	go func() { done <- u.server.Serve(listener) }()
	return path
}
func (f *controlledFixture) proxy(owner *harness, upstream string) *providerDependency {
	f.t.Helper()
	endpoint := filepath.Join(f.root, fmt.Sprintf("recorded-%d.sock", len(f.parent.dependencies)))
	listener, path, err := listenProvider(endpoint, upstream)
	if err != nil {
		f.t.Fatal(err)
	}
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", upstream)
	}}
	target, _ := url.Parse("http://provider")
	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.Transport = transport
	server := &http.Server{Handler: proxy}
	done := make(chan error, 1)
	d := f.parent.dependency(owner, path, server, done, transport)
	f.retain(owner)
	go func() { done <- server.Serve(listener) }()
	return d
}
func (f *controlledFixture) cleanup() {
	if f.cleaned {
		return
	}
	f.cleaned = true
	// Controlled faults are disarmed before bounded parent-owned recovery. The
	// test has already failed; recovery cannot turn that original outcome green.
	if f.repair != nil {
		f.repair()
	}
	unresolved := false
	for _, h := range f.retained {
		if h.daemon != nil && h.ownerUnresolved {
			// Repair may deliver an already reaped child's sole completion.
			_ = h.consumeOwner(time.Second)
		}
		if h.daemon != nil && !h.ownerUnresolved {
			_ = h.daemon.Process.Kill()
			if !h.consumeOwner(time.Second).joined {
				h.ownerUnresolved = true
			}
		}
		if h.daemon != nil {
			unresolved = true
		}
	}
	if err := f.parent.cleanupDependencies(); err != nil {
		f.t.Error("controlled dependency recovery incomplete; preserve", f.root, err)
		unresolved = true
	}
	for _, d := range f.parent.dependencies {
		if !d.released {
			unresolved = true
		}
	}
	// An unresolved dependency must keep upstream/log/root usable. Never erase
	// its provider path just because the current direct child has joined.
	if unresolved {
		f.t.Error("controlled owner/provider residual; log/forwarder/upstream/root retained", f.root)
		f.receipt(false)
		return
	}
	for _, u := range f.upstreams {
		if err := u.server.Close(); err != nil {
			f.t.Error(err)
			unresolved = true
		}
		select {
		case err := <-u.done:
			u.joined = true
			if err != nil && !errors.Is(err, http.ErrServerClosed) {
				f.t.Error(err)
				unresolved = true
			}
		case <-time.After(time.Second):
			f.t.Error("controlled upstream unjoined; preserve", f.root)
			unresolved = true
		}
	}
	if unresolved {
		f.receipt(false)
		return
	}
	if f.forwarder != nil {
		f.forwarder.Close()
	}
	if f.logFile != nil {
		// Dependency cleanup may already have closed the shared file.
		closed := false
		for _, h := range f.retained {
			closed = closed || h.logClosed
		}
		if !closed {
			if err := f.logFile.Close(); err != nil {
				f.t.Error(err)
				f.receipt(false)
				return
			}
		}
	}
	if err := os.RemoveAll(f.root); err != nil {
		f.t.Error(err)
		f.receipt(false)
		return
	}
	f.receipt(true)
}
func (f *controlledFixture) receipt(closed bool) {
	if f.report == "" {
		return
	}
	allJoined := true
	for _, h := range f.retained {
		allJoined = allJoined && h.daemon == nil
	}
	released := true
	for _, d := range f.parent.dependencies {
		released = released && d.released
	}
	upstreamJoined := true
	for _, u := range f.upstreams {
		upstreamJoined = upstreamJoined && u.joined
	}
	data, err := json.Marshal(map[string]any{"root": f.root, "closed": closed, "owners_joined": allJoined, "dependencies_released": released, "upstreams_joined": upstreamJoined})
	if err == nil {
		err = os.WriteFile(f.report, data, 0600)
	}
	if err != nil {
		f.t.Error("controlled cleanup receipt", err)
	}
}
