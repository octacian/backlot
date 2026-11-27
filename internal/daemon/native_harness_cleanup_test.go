//go:build darwin || linux

package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
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
		if result.stopErr == nil || !result.joined || time.Since(start) > 10*time.Second {
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
		if result.joined || result.ownerErr == nil || !result.killed || kills != 1 || time.Since(start) > 10*time.Second {
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

func TestNativeMixedRecoveryCleanupAfterFatal(t *testing.T) {
	binary := buildFixtureBinary(t, t.TempDir(), "../../cmd/backlot")
	record := filepath.Join(t.TempDir(), "record")
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, self, "-test.run=^TestNativeMixedRecoveryFailurePath$", "-test.v", "-test.timeout=40s")
	command.Env = append(os.Environ(), "BACKLOT_MIXED_FAILURE_BINARY="+binary, "BACKLOT_MIXED_FAILURE_RECORD="+record)
	output, err := command.CombinedOutput()
	if ctx.Err() != nil || err == nil || !strings.Contains(string(output), "intentional assertion after daemon kill and join") {
		t.Fatalf("intentional subprocess: %v %s", err, output)
	}
	for _, failure := range []string{"DATA RACE", "unverified", "survives acknowledged"} {
		if strings.Contains(string(output), failure) {
			t.Fatalf("cleanup failed: %s", output)
		}
	}
	data, err := os.ReadFile(record)
	if err != nil {
		t.Fatal(err)
	}
	var witnesses struct {
		Root string
		PIDs []int
	}
	if err := json.Unmarshal(data, &witnesses); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(witnesses.Root); !os.IsNotExist(err) {
		t.Fatal("owned state removed before verified cleanup", err, string(output))
	}
	if len(witnesses.PIDs) < 4 || witnesses.Root == "" {
		t.Fatal("missing retained owner/workload witnesses")
	}
	for _, pid := range witnesses.PIDs {
		if syscall.Kill(pid, 0) != syscall.ESRCH {
			t.Fatal("failure path owner/workload remains", pid)
		}
	}
	t.Log("intentional failure ran retained authenticated cleanup and joined all owners before removing state")
}

func TestNativeMixedRecoveryFailurePath(t *testing.T) {
	binary := os.Getenv("BACKLOT_MIXED_FAILURE_BINARY")
	if binary == "" {
		return
	}
	h := newNativeHarness(t, binary)
	r := h.run("dev")
	h.state(r.Instance.ID, v1.RuntimeReady)
	h.retainGuardianCleanup(r.Instance.ID)
	identities := h.guardianCapabilities(r.Instance.ID)
	data, err := os.ReadFile(h.pids)
	if err != nil {
		t.Fatal(err)
	}
	witnesses := struct {
		Root string
		PIDs []int
	}{Root: filepath.Dir(h.dir)}
	for _, identity := range identities {
		witnesses.PIDs = append(witnesses.PIDs, identity.PID)
	}
	for _, field := range strings.Fields(string(data)) {
		var pid int
		if _, err := fmt.Sscan(field, &pid); err != nil {
			t.Fatal(err)
		}
		witnesses.PIDs = append(witnesses.PIDs, pid)
	}
	data, err = json.Marshal(witnesses)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(os.Getenv("BACKLOT_MIXED_FAILURE_RECORD"), data, 0600); err != nil {
		t.Fatal(err)
	}
	if err := h.process.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = h.process.Wait()
	h.waited = true
	// Deliberately before openStore or post-fault journal acquisition.
	t.Fatal("intentional assertion after daemon kill and join")
}
