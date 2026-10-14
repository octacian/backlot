//go:build darwin && arm64

package acceptance

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestOwnedDaemonJoinLifetime(t *testing.T) {
	t.Run("grace-timeout-kill-joins", func(t *testing.T) {
		fixture := ownerFixture(t, nil)
		logFile, start := fixture.logFile, fixture.start
		h := &harness{t: t}
		waited := start(h, nil)
		result := h.joinOwner(10*time.Millisecond, time.Second)
		if !result.joined || !result.forced || result.exit == nil || h.daemon != nil || h.ownerUnresolved {
			t.Fatal("forced authoritative join state incorrect")
		}
		select {
		case <-waited:
		default:
			t.Fatal("owner cleared before sole Wait")
		}
		h.stop()
		if _, err := logFile.WriteString("joined\n"); err != nil {
			t.Fatal(err)
		}
		t.Log("grace budget exhausted; retained direct child killed; sole Wait delivered and ownership cleared")
	})
	t.Run("exhaustion-retains-until-explicit-completion", func(t *testing.T) {
		fixture := ownerFixture(t, nil)
		root, logFile, start := fixture.root, fixture.logFile, fixture.start
		h := &harness{t: t}
		delivery := make(chan struct{})
		var deliver sync.Once
		fixture.repair = func() { deliver.Do(func() { close(delivery) }) }
		waited := start(h, delivery)
		command, done := h.daemon, h.daemonDone
		result := h.joinOwner(10*time.Millisecond, 10*time.Millisecond)
		if result.joined || !result.forced || h.daemon != command || h.daemonDone != done || !h.ownerUnresolved || !h.cleanupUnverified {
			t.Fatal("exhaustion lost retained owner/Wait/residual")
		}
		if err := h.startDaemon(time.Millisecond); err == nil || !strings.Contains(err.Error(), "already retained") {
			t.Fatal("restart overwrote unresolved owner")
		}
		h.stop()
		if h.joinOwner(0, 0).joined {
			t.Fatal("automatic repeat consumed unresolved completion")
		}
		if _, err := os.Stat(filepath.Join(root, "stop")); !os.IsNotExist(err) {
			t.Fatal("unresolved owner received duplicate stop")
		}
		if _, err := logFile.WriteString("residual-log-live\n"); err != nil {
			t.Fatal(err)
		}
		response, err := http.Get(os.Getenv("BACKLOT_STARTUP_HELPER_FORWARDER"))
		if err != nil {
			t.Fatal("exhausted owner lost dependent forwarder", err)
		}
		_ = response.Body.Close()
		select {
		case <-waited:
		case <-time.After(time.Second):
			t.Fatal("controlled child kill did not produce sole Wait")
		}
		fixture.repair()
		eventual := h.consumeOwner(time.Second)
		if !eventual.joined || eventual.exit == nil || h.daemon != nil || h.ownerUnresolved {
			t.Fatal("explicit sole completion did not release retained ownership")
		}
		select {
		case <-done:
			t.Fatal("duplicate Wait result")
		default:
		}
		t.Log("join delivery exhausted under bounded seam; same command/channel retained, restart/automatic stop blocked, log live; eventual explicit sole completion joined")
	})
}

func unixCall(path, method, route string) (int, error) {
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", path)
	}}
	defer transport.CloseIdleConnections()
	request, err := http.NewRequest(method, "http://provider"+route, nil)
	if err != nil {
		return 0, err
	}
	response, err := (&http.Client{Transport: transport, Timeout: time.Second}).Do(request)
	if err != nil {
		return 0, err
	}
	_, _ = io.Copy(io.Discard, response.Body)
	_ = response.Body.Close()
	return response.StatusCode, nil
}
func TestProviderDependencyRecovery(t *testing.T) {
	for _, persistent := range []bool{false, true} {
		name := map[bool]string{false: "first-destroy-failure", true: "persistent-failure-handoff"}[persistent]
		t.Run(name, func(t *testing.T) {
			fixture := ownerFixture(t, nil)
			start := fixture.start
			owner := &harness{t: t}
			var failures atomic.Int32
			failures.Store(1)
			if persistent {
				failures.Store(10)
			}
			var owned atomic.Bool
			owned.Store(true)
			var foreignDeletes atomic.Int32
			upstreamPath := fixture.upstream(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodDelete && r.URL.Path == "/owned" {
					if failures.Add(-1) >= 0 {
						w.WriteHeader(http.StatusConflict)
						return
					}
					owned.Store(false)
					w.WriteHeader(http.StatusNoContent)
					return
				}
				if r.Method == http.MethodDelete {
					foreignDeletes.Add(1)
					w.WriteHeader(http.StatusForbidden)
					return
				}
				w.WriteHeader(http.StatusOK)
			}))
			dependency := fixture.proxy(owner, upstreamPath)
			endpoint, path := dependency.path.endpoint, dependency.path
			fixture.repair = func() { failures.Store(0) }
			attempts := 0
			dependency.recover = func() error {
				attempts++
				waited := start(owner, nil)
				status, err := unixCall(endpoint, http.MethodGet, "/_ping")
				if err != nil || status != http.StatusOK {
					return errors.New("next cleanup owner lost usable recorded endpoint")
				}
				status, err = unixCall(endpoint, http.MethodDelete, "/owned")
				_ = owner.daemon.Process.Kill()
				if !owner.consumeOwner(time.Second).joined {
					return errors.New("recovery owner unjoined")
				}
				<-waited
				if err != nil {
					return err
				}
				if status != http.StatusNoContent {
					return errors.New("injected persistent owned destruction failure")
				}
				delete(dependency.pending, "owned")
				return nil
			}
			dependency.track("owned")
			firstWait := start(owner, nil)
			status, err := unixCall(endpoint, http.MethodDelete, "/owned")
			if err != nil || status != http.StatusConflict || !owned.Load() {
				t.Fatal("first fault did not retain known owned allocation")
			}
			_ = owner.daemon.Process.Kill()
			if !owner.consumeOwner(time.Second).joined {
				t.Fatal("first cleanup owner unjoined")
			}
			<-firstWait
			if err := dependency.finish(); err != nil || dependency.released {
				t.Fatal("joined owner incorrectly released pending allocation dependency")
			}

			err = dependency.cleanup()
			if persistent {
				if err == nil || !strings.Contains(err.Error(), "usable provider handoff") || dependency.released || !owned.Load() {
					t.Fatal("persistent failure not explicitly preserved/reported")
				}
				target, err := os.Readlink(endpoint)
				if err != nil || target != upstreamPath {
					t.Fatal("persistent endpoint handoff not independent of test proxy")
				}
				if status, err := unixCall(endpoint, http.MethodGet, "/_ping"); err != nil || status != http.StatusOK {
					t.Fatal("reported handoff is unusable")
				}
				// Foreign replacement must survive handoff/release attempts; restore only our exact saved link.
				backup := endpoint + ".owned"
				if err := os.Rename(endpoint, backup); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(endpoint, []byte("foreign"), 0600); err != nil {
					t.Fatal(err)
				}
				if err := path.handoff(); err == nil {
					t.Fatal("foreign endpoint falsely reported as usable handoff")
				}
				if err := path.remove(); err == nil {
					t.Fatal("foreign endpoint accepted as owned")
				}
				if data, err := os.ReadFile(endpoint); err != nil || string(data) != "foreign" {
					t.Fatal("foreign endpoint was deleted")
				}
				if err := os.Remove(endpoint); err != nil {
					t.Fatal(err)
				}
				if err := os.Rename(backup, endpoint); err != nil {
					t.Fatal(err)
				}
				failures.Store(0)
				if err := dependency.cleanup(); err != nil {
					t.Fatal(err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			if owned.Load() || foreignDeletes.Load() != 0 || !dependency.released || owner.daemon != nil {
				t.Fatal("verified recovery/release or foreign preservation failed")
			}
			if _, err := unixCall(endpoint, http.MethodGet, "/_ping"); err == nil {
				t.Fatal("endpoint released before/without verified recovery")
			}
			t.Logf("first destruction failure retained known allocation; subsequent owner used recorded endpoint; persistent=%v recovery_owners=%d; release followed verified destroy+join; foreign preserved", persistent, attempts)
		})
	}
}
