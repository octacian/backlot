//go:build darwin || linux

package daemon

import (
	"context"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	v1 "github.com/octacian/backlot/api/v1"
	"github.com/octacian/backlot/internal/client"
)

func TestNativeHarnessOwnerProcess(t *testing.T) {
	if os.Getenv("BACKLOT_HARNESS_OWNER") != "1" {
		return
	}
	time.Sleep(100 * time.Millisecond)
	os.Exit(23)
}

func TestNativeHarnessShutdownFailureChild(t *testing.T) {
	root := os.Getenv("BACKLOT_HARNESS_FAILURE_ROOT")
	if root == "" {
		return
	}
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	h := &nativeHarness{t: t, dir: root, pids: filepath.Join(root, "never-published")}
	expectedFailure := os.Getenv("BACKLOT_HARNESS_EXPECTED_FAILURE")
	if expectedFailure != "" {
		h.shutdownFailureCode = "cleanup_failed"
		h.shutdownFailureProof = func() error {
			t.Error("proof ran without expected shutdown result")
			return nil
		}
	}
	// Match the real outer harness's retention rule, without allocating runtime.
	t.Cleanup(func() {
		if !h.cleanupUnverified {
			_ = os.RemoveAll(root)
		}
	})
	listener, err := net.Listen("unix", filepath.Join(root, "api.sock"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(root, "api.sock"), 0600); err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Backlot-API-Version", v1.Version)
		if expectedFailure == "missing-error" {
			writeJSON(w, v1.DaemonStatusResponse{APIVersion: v1.Version, StateVersion: v1.StateVersion, PID: os.Getpid(), Status: "stopping", LeaseDuration: "2s"})
			return
		}
		writeError(w, problem("cleanup_failed", "controlled shutdown cleanup failure"))
	}), ReadHeaderTimeout: time.Second}
	served := make(chan error, 1)
	go func() { served <- server.Serve(listener) }()
	defer func() { _ = server.Close(); <-served }()
	h.cli = client.New(filepath.Join(root, "api.sock"))
	h.process = exec.Command(os.Args[0], "-test.run=^TestNativeHarnessOwnerProcess$")
	h.process.Env = os.Environ()
	if expectedFailure == "" {
		h.process.Env = append(h.process.Env, "BACKLOT_HARNESS_OWNER=1")
	}
	if err := h.process.Start(); err != nil {
		t.Fatal(err)
	}
	h.close()
}

func TestNativeHarnessShutdownFailureRetainsNoPIDState(t *testing.T) {
	for _, mode := range []string{"unexpected-owner-exit", "expected-error-owner-exit-zero", "missing-error"} {
		t.Run(mode, func(t *testing.T) { nativeHarnessShutdownFailureRetainsNoPIDState(t, mode) })
	}
}

func nativeHarnessShutdownFailureRetainsNoPIDState(t *testing.T, mode string) {
	t.Helper()
	root := filepath.Join(shortTemp(t), "uncertain")
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	child := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestNativeHarnessShutdownFailureChild$", "-test.v")
	child.Env = append(os.Environ(), "BACKLOT_HARNESS_FAILURE_ROOT="+root)
	if mode != "unexpected-owner-exit" {
		child.Env = append(child.Env, "BACKLOT_HARNESS_EXPECTED_FAILURE="+mode)
	}
	output, err := child.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatal("cleanup failure path did not finish", string(output))
	}
	if err == nil || !strings.Contains(string(output), "unverified daemon shutdown") || strings.Contains(string(output), "proof ran without expected shutdown result") {
		t.Error("shutdown and nonzero daemon failure were suppressed", string(output))
	}
	if mode == "unexpected-owner-exit" && (!strings.Contains(string(output), "controlled shutdown cleanup failure") || !strings.Contains(string(output), "exit status 23")) {
		t.Error("original unexpected owner failure was lost", string(output))
	}
	if _, err := os.Stat(root); err != nil {
		t.Error("uncertain no-PID state deleted", err)
	}
}

func TestNativeHarnessShutdownBudgets(t *testing.T) {
	t.Run("stop-no-response", func(t *testing.T) {

		socket := filepath.Join(shortTemp(t), "no-response.sock")
		listener, err := net.Listen("unix", socket)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(socket, 0600); err != nil {
			t.Fatal(err)
		}
		entered := make(chan struct{}, 1)
		finished := make(chan struct{})
		release := make(chan struct{})
		var once sync.Once
		releaseHandler := func() { once.Do(func() { close(release) }) }
		defer releaseHandler()
		server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			entered <- struct{}{}
			select {
			case <-r.Context().Done():
			case <-release:
			}
			close(finished)
		}), ReadHeaderTimeout: time.Second}
		served := make(chan error, 1)
		go func() { served <- server.Serve(listener) }()
		defer func() { _ = server.Close(); <-served }()
		cli := client.New(socket)
		defer cli.Close()
		done := make(chan error, 1)
		done <- nil
		start := time.Now()
		result := finishNativeHarnessOwner(func(ctx context.Context) error { _, err := cli.Stop(ctx); return err }, done, func() error { t.Error("joined owner killed"); return nil }, 20*time.Millisecond, 20*time.Millisecond, 20*time.Millisecond)
		select {
		case <-entered:
		default:
			t.Fatal("no actual authenticated stop request")
		}
		if result.stopErr == nil || !result.joined || time.Since(start) > time.Second {
			t.Fatal("stop request unbounded", result)
		}
		releaseHandler()
		select {
		case <-finished:
		case <-time.After(time.Second):
			t.Fatal("cancelled stop handler did not join")
		}

	})
	t.Run("unjoined-after-kill", func(t *testing.T) {
		done := make(chan error)
		kills := 0
		start := time.Now()
		result := finishNativeHarnessOwner(func(context.Context) error { return nil }, done, func() error { kills++; return nil }, 20*time.Millisecond, 20*time.Millisecond, 20*time.Millisecond)
		if result.joined || result.ownerErr == nil || !result.killed || kills != 1 || time.Since(start) > time.Second {
			t.Fatal("post-kill join unbounded", result)
		}
	})
	t.Run("nonzero-owner", func(t *testing.T) {
		done := make(chan error, 1)
		cause := context.Canceled
		done <- cause
		result := finishNativeHarnessOwner(func(context.Context) error { return nil }, done, func() error { t.Error("joined owner killed"); return nil }, time.Second, time.Second, time.Second)
		if result.ownerErr != cause || !result.joined {
			t.Fatal("owner exit suppressed", result)
		}
	})
}
