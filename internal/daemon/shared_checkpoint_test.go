//go:build darwin || linux

package daemon

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/octacian/backlot/internal/native"
)

// The helper is compiled only in the test binary. Neither project environment nor
// production daemon options can activate a cross-process checkpoint.
func TestSharedCheckpointDaemon(t *testing.T) {
	if os.Getenv("BACKLOT_SHARED_TEST_DAEMON") != "1" {
		return
	}
	point := os.Getenv("BACKLOT_SHARED_TEST_POINT")
	var reached atomic.Bool
	var notified atomic.Bool
	err := Serve(context.Background(), Options{Directory: os.Getenv("BACKLOT_SHARED_TEST_STATE"), sharedCheckpoint: func(at string) error {
		notify := at == os.Getenv("BACKLOT_SHARED_TEST_NOTIFY") && notified.CompareAndSwap(false, true)
		if !notify && (at != point || !reached.CompareAndSwap(false, true)) {
			return nil
		}
		conn, err := net.DialTimeout("unix", os.Getenv("BACKLOT_SHARED_TEST_GATE"), time.Second)
		if err != nil {
			return err
		}
		defer func() { _ = conn.Close() }()
		if _, err = io.WriteString(conn, at+"\n"); err != nil {
			return err
		}
		if notify {
			return nil
		}
		var release [1]byte
		_, err = conn.Read(release[:])
		if err == nil && release[0] == 1 {
			return nil
		}
		return err
	}})
	if err != nil {
		t.Fatal(err)
	}
}

// Owned barriers are keyed by the harness pointer, used only serially by the parent.
var sharedTestBarriers = map[*nativeHarness]net.Listener{}

func startSharedCheckpointDaemon(t *testing.T, h *nativeHarness, point string, waitAPI ...bool) {
	t.Helper()
	if !h.waited {
		if _, err := h.cli.Stop(context.Background()); err != nil {
			t.Fatal(err)
		}
		if err := joinSharedDaemon(t, h); err != nil {
			t.Fatal(err)
		}
		h.waited = true
	}
	path := filepath.Join(filepath.Dir(h.dir), "shared-gate.sock")
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	sharedTestBarriers[h] = listener
	t.Cleanup(func() { _ = listener.Close(); delete(sharedTestBarriers, h) })
	preserveSharedDaemonLog(t, h, "previous daemon")
	h.daemonLog.Reset()
	h.process = exec.Command(os.Args[0], "-test.run=^TestSharedCheckpointDaemon$")
	h.process.Env = append(os.Environ(), "BACKLOT_SHARED_TEST_DAEMON=1", "BACKLOT_SHARED_TEST_POINT="+point, "BACKLOT_SHARED_TEST_STATE="+h.dir, "BACKLOT_SHARED_TEST_GATE="+path)
	h.process.Stdout = io.Discard
	h.process.Stderr = &h.daemonLog
	if err = h.process.Start(); err != nil {
		t.Fatal(err)
	}
	h.ownerDone = nil
	h.waited = false
	if len(waitAPI) > 0 && !waitAPI[0] {
		return
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if _, err = h.cli.Status(context.Background()); err == nil {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("checkpoint daemon startup failed")
}

func waitSharedCheckpoint(t *testing.T, h *nativeHarness, expected ...string) net.Conn {
	t.Helper()
	listener := sharedTestBarriers[h].(*net.UnixListener)
	if err := listener.SetDeadline(time.Now().Add(45 * time.Second)); err != nil {
		t.Fatal(err)
	}
	conn, err := listener.Accept()
	if err != nil {
		t.Fatal("shared effect checkpoint not reached", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	witness, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil || len(witness) > 128 {
		t.Fatal("invalid owned checkpoint observation", err)
	}
	if len(expected) > 0 && strings.TrimSpace(witness) != expected[0] {
		t.Fatal("unexpected effect checkpoint", strings.TrimSpace(witness))
	}
	return conn
}

// Keep the native harness's five-second API expectation while recording actual
// server progress. Each probe is bounded so transport loss cannot hide the
// observation window. An earlier failure remains undiagnosed, not proof that
// extending a deadline is appropriate.
func startSharedRecoveryDaemon(t *testing.T, h *nativeHarness, serverRunning func() bool) {
	t.Helper()
	preserveSharedDaemonLog(t, h, "previous daemon")
	h.daemonLog.Reset()
	h.process = exec.Command(h.binary, "daemon", "serve", "--state-dir", h.dir, "--lease-duration", h.lease)
	h.process.Stdout = &h.daemonLog
	h.process.Stderr = &h.daemonLog
	if err := h.process.Start(); err != nil {
		t.Fatal(err)
	}
	h.ownerDone = nil
	h.waited = false
	started := time.Now()
	observedRunning := false
	if serverRunning != nil {
		observedRunning = serverRunning()
	}
	t.Logf("recovery starts with retained server running=%v", observedRunning)
	deadline := started.Add(5 * time.Second)
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		_, err := h.cli.Status(ctx)
		cancel()
		if observedRunning && serverRunning != nil && !serverRunning() {
			t.Logf("observed retained server transition running->stopped at %s during recovery before API ready=%v", time.Since(started), err == nil)
			observedRunning = false
		}
		if err == nil {
			t.Logf("shared recovery API available after %s (bounded consumer/server recovery before listener)", time.Since(started))
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	_ = h.process.Process.Kill()
	_ = joinSharedDaemon(t, h)
	h.waited = true
	t.Fatal("shared recovery did not expose API within original five-second expectation", h.daemonLog.String())
}

// Reaping precedes this read. Never erase a killed child's race report merely
// because its nonzero exit was expected by a fault fixture.
func preserveSharedDaemonLog(t *testing.T, h *nativeHarness, phase string) {
	t.Helper()
	value := h.daemonLog.String()
	if value != "" {
		t.Logf("%s diagnostics:\n%s", phase, value)
	}
	if err := sharedDaemonDiagnostics(value); err != nil {
		t.Fatal(err)
	}
}

func sharedDaemonDiagnostics(value string) error {
	if strings.Contains(value, "DATA RACE") {
		return errors.New("race-instrumented daemon reported a data race")
	}
	return nil
}

func TestSharedDaemonDiagnostics(t *testing.T) {
	if sharedDaemonDiagnostics("WARNING: DATA RACE\nfixture") == nil {
		t.Fatal("faulted-child race diagnostic suppressed")
	}
	if err := sharedDaemonDiagnostics("ordinary interrupted fixture"); err != nil {
		t.Fatal(err)
	}
}

// Match the production executable's private supervisor/anchor entry points, so
// the test-only checkpoint daemon can exercise real native consumers too.
func TestMain(m *testing.M) {
	if len(os.Args) == 2 && os.Args[1] == "__native-supervisor" {
		if native.Supervise() != nil {
			os.Exit(1)
		}
		os.Exit(0)
	}
	if len(os.Args) == 2 && os.Args[1] == "__native-anchor" {
		if native.Anchor() != nil {
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}
