//go:build darwin || linux

package daemon

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// The caller owns the directory lifetime, including retention on uncertainty.
// Subtests share their owning test's race-instrumented binary.
func buildFixtureBinary(t *testing.T, directory, source string) string {
	t.Helper()
	binary := filepath.Join(directory, filepath.Base(source))
	if output, err := exec.Command("go", "build", "-race", "-o", binary, source).CombinedOutput(); err != nil {
		t.Fatalf("build %s: %v %s", source, err, output)
	}
	return binary
}

// Call once per started child and retain the channel across cleanup attempts.
func waitFixtureChild(command *exec.Cmd) chan error {
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	return done
}

// fixtureOwnerJoin retains the sole Wait owner and its terminal observation.
// A timed-out owner is never waited again by registered cleanup. killErr stays
// private to the fixture; it is not included in the bounded failure message.
type fixtureOwnerJoin struct {
	done                      <-chan error
	attempted, joined, killed bool
	waitErr, killErr, failure error
}

func (j *fixtureOwnerJoin) join(budget, killBudget time.Duration, kill func() error) error {
	if j.attempted {
		return j.failure
	}
	j.attempted = true
	timer := time.NewTimer(budget)
	defer timer.Stop()
	select {
	case j.waitErr = <-j.done:
		j.joined = true
		return nil
	case <-timer.C:
	}
	j.killed = true
	j.killErr = kill() // Only the retained, unreaped direct child authorizes this.
	timer.Reset(killBudget)
	select {
	case j.waitErr = <-j.done:
		j.joined = true
		j.failure = errors.New("daemon shutdown exceeded finite join budget; direct-child Wait owner joined after kill")
	case <-timer.C:
		j.failure = errors.New("daemon shutdown exceeded finite join budget; direct-child Wait owner unfinished; preserving owned fixture state")
	}
	return j.failure
}

// Register before startup or faults; uncertainty keeps private state available.
func (h *nativeHarness) registerStateCleanup(root string, retain bool) {
	h.t.Helper()
	h.t.Cleanup(func() {
		if retain || h.cleanupUnverified || h.preserveFailureEvidence {
			h.t.Log("preserved owned fixture state", root, "daemon joined", h.waited)
			return
		}
		if err := os.RemoveAll(root); err != nil {
			h.t.Error(err)
		}
	})
}

type nativeHarnessShutdown struct {
	stopErr, ownerErr error
	joined, killed    bool
}

// The retained Wait owner is reused on a later close after timeout. No second
// Wait, unbounded receive, or observed-PID actuation is permitted here.
func finishNativeHarnessOwner(stop func(context.Context) error, done <-chan error, kill func() error, stopBudget, joinBudget, killBudget time.Duration) nativeHarnessShutdown {
	ctx, cancel := context.WithTimeout(context.Background(), stopBudget)
	result := nativeHarnessShutdown{stopErr: stop(ctx)}
	cancel()
	// Native cleanup may retry a timed-out channel; Doctor retains this join
	// observation instead so registered cleanup cannot relabel uncertainty.
	owner := &fixtureOwnerJoin{done: done}
	failure := owner.join(joinBudget, killBudget, kill)
	result.joined, result.killed = owner.joined, owner.killed
	result.ownerErr = owner.waitErr
	if failure != nil {
		result.ownerErr = errors.Join(failure, owner.killErr, owner.waitErr)
	}
	return result
}
