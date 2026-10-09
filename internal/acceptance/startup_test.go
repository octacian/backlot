//go:build darwin && arm64

package acceptance

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestOwnedDaemonStartupHelper is a direct owned child, without subprocesses.
// The wrapper execs this test binary, so the retained handle remains authoritative.
func TestOwnedDaemonStartupHelper(t *testing.T) {
	root := os.Getenv("BACKLOT_STARTUP_HELPER_ROOT")
	if root == "" {
		return
	}
	marker := ""
	for _, arg := range os.Args {
		switch arg {
		case "serve":
			marker = "serve"
		case "status":
			marker = "status"
		case "stop":
			marker = "stop"
		}
	}
	if marker == "" {
		os.Exit(2)
	}
	response, err := http.Get(os.Getenv("BACKLOT_STARTUP_HELPER_FORWARDER"))
	if err != nil {
		os.Exit(3)
	}
	_, _ = io.Copy(io.Discard, response.Body)
	_ = response.Body.Close()
	if err := os.WriteFile(filepath.Join(root, marker), []byte("witness"), 0600); err != nil {
		os.Exit(4)
	}
	if marker != "serve" {
		os.Exit(1)
	}
	for {
		time.Sleep(time.Second)
	}
}

func TestOwnedDaemonStartupFailureCleanup(t *testing.T) {
	root, err := os.MkdirTemp("/tmp", "bl-startup-")
	if err != nil {
		t.Fatal(err)
	}
	var retained []*harness
	logFile, err := os.OpenFile(filepath.Join(root, "daemon.log"), os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	forwarder := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = fmt.Fprint(w, "forwarding-live") }))
	// Dependents are closed only after all retained ownership has been joined.
	defer func() {
		for _, h := range retained {
			if h.daemon != nil {
				t.Error("startup regression residual; forwarding/log/root preserved", root)
				return
			}
		}
		forwarder.Close()
		if err := logFile.Close(); err != nil {
			t.Error(err)
		}
		if err := os.RemoveAll(root); err != nil {
			t.Error(err)
		}
	}()
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("BACKLOT_STARTUP_HELPER_ROOT", root)
	t.Setenv("BACKLOT_STARTUP_HELPER_FORWARDER", forwarder.URL)
	t.Setenv("BACKLOT_STARTUP_HELPER_BINARY", binary)
	wrapper := filepath.Join(root, "helper")
	if err = os.WriteFile(wrapper, []byte("#!/bin/sh\nexec \"$BACKLOT_STARTUP_HELPER_BINARY\" -test.run=^TestOwnedDaemonStartupHelper$ -- \"$@\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Run("Start-failure", func(t *testing.T) {
		h := &harness{t: t, root: root, binary: filepath.Join(root, "absent"), state: filepath.Join(root, "absent-state"), daemonLog: logFile}
		retained = append(retained, h)
		defer h.stop()
		err := h.startDaemon(250 * time.Millisecond)
		if err == nil || !strings.Contains(err.Error(), "Start failed") || h.daemon != nil || h.daemonDone != nil || h.startupResidual {
			t.Fatal("Start failure retained invalid ownership or omitted cause")
		}
		h.stop() // Guarded no-op; no nil Process/channel or duplicate Wait.
	})
	t.Run("post-Start-readiness-failure", func(t *testing.T) {
		h := &harness{t: t, root: root, binary: wrapper, state: filepath.Join(root, "state"), daemonLog: logFile}
		retained = append(retained, h)
		defer h.stop() // Registered before readiness; startup failure has already joined.
		before := time.Now()
		err := h.startDaemon(time.Second)
		if err == nil || !errors.Is(err, context.DeadlineExceeded) || !strings.Contains(err.Error(), "did not become ready") {
			t.Fatalf("controlled readiness failure cause lost: %v", err)
		}
		if time.Since(before) > 7*time.Second || h.daemon != nil || h.startupResidual || h.cleanupUnverified {
			t.Fatal("startup child was not boundedly joined or was silently left residual")
		}
		for _, name := range []string{"serve", "status"} {
			if _, err := os.Stat(filepath.Join(root, name)); err != nil {
				t.Fatalf("live owned helper/%s witness absent", name)
			}
		}
		if _, err := logFile.WriteString("joined-before-log-close\n"); err != nil {
			t.Fatal("dependent log closed before join", err)
		}
		response, err := http.Get(forwarder.URL)
		if err != nil {
			t.Fatal("forwarding closed before join", err)
		}
		_ = response.Body.Close()
		h.stop()
		if _, err := os.Stat(filepath.Join(root, "stop")); !os.IsNotExist(err) {
			t.Fatal("already joined child received duplicate stop")
		}
		select {
		case <-h.daemonDone:
			t.Fatal("more than one Wait result or consumed result left pending")
		default:
		}
		t.Log("live owned serve/status witnesses; readiness deadline failed; retained child killed and joined once before forwarding/log close; guarded repeated stop is a no-op")
	})
}
