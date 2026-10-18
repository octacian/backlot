//go:build darwin && arm64

package acceptance

import (
	"context"
	"encoding/json"
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
	instance := v1.Instance{ID: "owned", Status: v1.Destroyed}
	if os.Getenv("BACKLOT_RECOVERY_COLLECTION") != "" {
		exit := 137
		instance.Execution = &v1.ExecutionResult{CollectionFailure: "container output collection failed", TerminalExitCode: &exit}
	}
	if args[0] == "inspect" {
		_ = json.NewEncoder(os.Stdout).Encode(v1.InstanceResponse{APIVersion: v1.Version, Instance: instance})
		os.Exit(0)
	}
	if args[0] == "destroy" {
		status, err := unixCall(os.Getenv("BACKLOT_RECOVERY_PROVIDER"), http.MethodDelete, "/owned")
		if err != nil || status != http.StatusNoContent {
			os.Exit(1)
		}
		_ = json.NewEncoder(os.Stdout).Encode(v1.InstanceResponse{APIVersion: v1.Version, Instance: instance})
		os.Exit(0)
	}
	if len(args) < 2 || state == "" {
		os.Exit(2)
	}
	path := filepath.Join(state, "control.address")
	collection := os.Getenv("BACKLOT_RECOVERY_COLLECTION") != ""
	initial := filepath.Join(state, "initial-collection")
	switch args[1] {
	case "status":
		status, err := modelControlCall(path, http.MethodGet, "/_ping")
		if err != nil || status != 200 {
			os.Exit(1)
		}
		if os.Getenv("BACKLOT_RECOVERY_BLOCK_READINESS") != "" {
			// A real model response is witnessed before deliberately withholding
			// CLI readiness, independently of race instrumentation or exit delay.
			if err := publishReadinessWitness(filepath.Join(state, "readiness-blocked"), nil); err != nil {
				os.Exit(6)
			}
			os.Exit(1)
		}
		os.Exit(0)
	case "stop":
		status, err := modelControlCall(path, http.MethodPost, "/stop")
		if collection && status == http.StatusInternalServerError {
			_ = json.NewEncoder(os.Stdout).Encode(v1.ErrorResponse{APIVersion: v1.Version, Error: v1.PlanError{Code: "collection_failed", Message: "container output collection failed"}})
			os.Exit(1)
		}
		if err != nil || status != http.StatusOK {
			os.Exit(1)
		}
		os.Exit(0)
	case "serve":
		if err := os.MkdirAll(state, 0700); err != nil {
			os.Exit(3)
		}
		original := false
		if collection {
			file, err := os.OpenFile(initial, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
			if err == nil {
				_ = file.Close()
				original = true
			}
		}
		// A killed direct helper releases this kernel-owned loopback listener.
		// Each fresh owner writes a fresh address before readiness; no stale Unix
		// socket can block recovery after an authoritative startup-abort join.
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			os.Exit(4)
		}
		if err := os.WriteFile(path, []byte(listener.Addr().String()), 0600); err != nil {
			_ = listener.Close()
			os.Exit(5)
		}
		halt := make(chan struct{})
		var once sync.Once
		server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/stop" && original {
				w.WriteHeader(http.StatusInternalServerError)
			} else {
				w.WriteHeader(http.StatusOK)
			}
			if r.URL.Path == "/stop" {
				once.Do(func() { close(halt) })
			}
		})}
		done := make(chan error, 1)
		go func() { done <- server.Serve(listener) }()
		<-halt
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		_ = server.Shutdown(ctx)
		cancel()
		<-done
		if original {
			os.Exit(1)
		}
	default:
		os.Exit(2)
	}
}

const readinessWitness = "live model responded before explicit readiness failure"

// publishReadinessWitness preserves a completed observation when a later status
// helper is killed by its command deadline. The optional checkpoint lets the
// interruption regression kill its retained child before the write completes.
func publishReadinessWitness(path string, beforeWrite func()) error {
	file, err := os.CreateTemp(filepath.Dir(path), ".readiness-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(file.Name()) }()
	if beforeWrite != nil {
		beforeWrite()
	}
	_, writeErr := file.WriteString(readinessWitness)
	closeErr := file.Close()
	if err := errors.Join(writeErr, closeErr); err != nil {
		return err
	}
	return os.Rename(file.Name(), path)
}

func modelControlCall(path, method, route string) (int, error) {
	address, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	host, _, err := net.SplitHostPort(string(address))
	if err != nil || host != "127.0.0.1" {
		return 0, errors.New("invalid private model control address")
	}
	request, err := http.NewRequest(method, "http://"+string(address)+route, nil)
	if err != nil {
		return 0, err
	}
	response, err := (&http.Client{Timeout: time.Second}).Do(request)
	if err != nil {
		return 0, err
	}
	_, _ = io.Copy(io.Discard, response.Body)
	_ = response.Body.Close()
	return response.StatusCode, nil
}

// recoveryFixture registers upstream/proxy ownership before any failing setup or
// assertion, and keeps the actual shared recovery callback installed.
func recoveryFixture(t *testing.T) (*controlledFixture, *harness, *providerDependency, *atomic.Bool) {
	f := ownerFixture(t, nil)
	owner := &harness{t: t, root: f.root, daemonLog: f.logFile, state: filepath.Join(f.root, "recovery-state")}
	f.retain(owner)
	wrapper := filepath.Join(f.root, "recovery-cli")
	if err := os.WriteFile(wrapper, []byte("#!/bin/sh\nexec \"$BACKLOT_STARTUP_HELPER_BINARY\" -test.run=^TestProviderRecoveryCLIHelper$ -- \"$@\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	owner.binary = wrapper
	var fail atomic.Bool
	fail.Store(true)
	upstream := f.upstream(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			if fail.Load() {
				w.WriteHeader(http.StatusConflict)
			} else {
				w.WriteHeader(http.StatusNoContent)
			}
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	d := f.proxy(owner, upstream)
	d.track("owned")
	f.repair = func() { fail.Store(false) }
	t.Setenv("BACKLOT_RECOVERY_PROVIDER", d.path.endpoint)
	// Runs recovery before these environment variables are restored.
	t.Cleanup(f.cleanup)
	return f, owner, d, &fail
}

func TestLateProviderRecoveryContext(t *testing.T) {
	f, owner, dependency, fail := recoveryFixture(t)
	t.Run("finished-original-owner", func(child *testing.T) { owner.t = child })
	original := dependency.recover
	dependency.recover = func() error {
		if owner.t != t {
			return errors.New("late cleanup retained completed subtest context")
		}
		return original()
	}
	if err := f.parent.cleanupDependencies(); err == nil || !strings.Contains(err.Error(), "usable provider handoff") {
		t.Fatalf("late failure context/handoff lost: %v", err)
	}
	if owner.daemon != nil || dependency.released {
		t.Fatal("failed late cleanup inferred destruction from join")
	}
	fail.Store(false)
	if err := f.parent.cleanupDependencies(); err != nil {
		t.Fatal(err)
	}
	if owner.daemon != nil || !dependency.released || !owner.logClosed {
		t.Fatal("actual recovery callback did not verify destruction, join and log release")
	}
	t.Log("completed child context replaced by active parent during real cleanup callback; first destroy failure handed off provider path, next owner destroyed/joined/released")
}

func TestFreshProviderOwnerShutdown(t *testing.T) {
	f, owner, dependency, fail := recoveryFixture(t)
	t.Setenv("BACKLOT_RECOVERY_COLLECTION", "1")
	t.Cleanup(f.cleanup)
	before := time.Now()
	if err := owner.startDaemon(5 * time.Second); err != nil {
		t.Fatal(err)
	}
	t.Logf("race model initial readiness elapsed=%s", time.Since(before))
	owner.stopDaemonCollection("container output collection failed")
	if owner.daemon != nil || owner.cleanupUnverified {
		t.Fatal("original collector failure owner did not join as expected")
	}
	if err := f.parent.cleanupDependencies(); err == nil || !strings.Contains(err.Error(), "usable provider handoff") {
		t.Fatalf("first destroy failure not preserved: %v", err)
	}
	if owner.daemon != nil || owner.cleanupUnverified || dependency.released {
		t.Fatal("fresh owner did not stop cleanly independently of original collection failure")
	}
	fail.Store(false)
	var recovered v1.Instance
	original := dependency.recover
	dependency.recover = func() error {
		err := original()
		if err == nil {
			recovered, err = owner.instance("inspect", "owned")
		}
		return err
	}
	if err := f.parent.cleanupDependencies(); err != nil {
		t.Fatal(err)
	}
	if !dependency.released || owner.daemon != nil || owner.cleanupUnverified || !owner.logClosed {
		t.Fatal("verified recovery did not destroy/join/release")
	}
	if recovered.Execution == nil || recovered.Execution.CollectionFailure != "container output collection failed" || recovered.Execution.TerminalExitCode == nil || *recovered.Execution.TerminalExitCode != 137 {
		t.Fatal("historical collection failure or actual post-stop exit lost")
	}
	t.Log("original owner expected collection failure; actual shared recovery callback survived first destroy failure and fresh clean shutdown, then verified destruction/join/release with historical result intact")
}
