//go:build darwin || linux

package native

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	v1 "github.com/octacian/backlot/api/v1"
)

// TestSessionFixture is a generic application which creates another group in
// its guardian's session. Every fixture self-expires, including failure paths.
func TestSessionFixture(t *testing.T) {
	if os.Getenv("BACKLOT_SESSION_FIXTURE") != "1" {
		return
	}
	time.AfterFunc(15*time.Second, func() {
		if path := os.Getenv("BACKLOT_SESSION_EXPIRED"); path != "" {
			_ = os.WriteFile(path, []byte("backup lifetime expired"), 0600)
		}
		os.Exit(33)
	})
	if os.Getenv("BACKLOT_SESSION_OMIT_SCAN") == "1" && os.Getenv("BACKLOT_SESSION_CHILD") != "1" {
		anchor := exec.Command("/bin/sleep", "15")
		configureAnchor(anchor)
		if err := anchor.Start(); err != nil {
			t.Fatal(err)
		}
		anchorDone := make(chan struct{})
		go func() { _ = anchor.Wait(); close(anchorDone) }()
		defer fixtureJoin(t, anchor, anchorDone)
		child := exec.Command(os.Args[0], "-test.run=^TestSessionFixture$")
		child.Env = append(os.Environ(), "BACKLOT_SESSION_CHILD=1")
		child.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		if err := child.Start(); err != nil {
			t.Fatal(err)
		}
		childDone := make(chan struct{})
		go func() { _ = child.Wait(); close(childDone) }()
		defer fixtureJoin(t, child, childDone)
		until := time.Now().Add(time.Second)
		for time.Now().Before(until) {
			if _, err := os.Stat(os.Getenv("BACKLOT_SESSION_READY")); err == nil {
				break
			}
			time.Sleep(time.Millisecond)
		}
		calls := 0
		w := &ownedWorkload{anchor: anchor, scanSession: func(context.Context) ([]int, error) {
			calls++
			if calls == 1 {
				return []int{child.Process.Pid}, nil
			}
			return nil, syscall.EPERM
		}}
		if err := w.stopSession(time.Second); !errors.Is(err, syscall.EPERM) {
			t.Fatal("denied scan", err)
		}
		if len(w.sessionState.members) != 1 {
			t.Fatal("known member discarded")
		}
		w.scanSession = func(context.Context) ([]int, error) { return nil, nil }
		if err := w.stopSession(0); err != nil {
			t.Fatal("retry", err)
		}
		select {
		case <-childDone:
		case <-time.After(time.Second):
			t.Fatal("child did not join")
		}
		state, ok := child.ProcessState.Sys().(syscall.WaitStatus)
		if !ok || !state.Signaled() || state.Signal() != syscall.SIGKILL {
			t.Fatal("child was not reached by identity KILL")
		}
		return
	}

	if os.Getenv("BACKLOT_SESSION_LATE_SCAN") == "1" {
		anchor := exec.Command("/bin/sleep", "15")
		configureAnchor(anchor)
		if err := anchor.Start(); err != nil {
			t.Fatal(err)
		}
		done := make(chan struct{})
		defer fixtureJoin(t, anchor, done)
		go func() { _ = anchor.Wait(); close(done) }()
		calls := 0
		w := &ownedWorkload{anchor: anchor, scanSession: func(ctx context.Context) ([]int, error) {
			calls++
			if calls == 2 {
				<-ctx.Done()
			}
			return nil, nil
		}}
		err := w.stopSession(0)
		if err == nil {
			t.Fatal("late empty second observation released anchor")
		}
		if err := anchor.Process.Signal(syscall.Signal(0)); err != nil {
			t.Fatal("late observation killed retained anchor", err)
		}
		return
	}
	if os.Getenv("BACKLOT_SESSION_SCAN_FAILURE") == "1" {
		anchor := exec.Command("/bin/sleep", "15")
		configureAnchor(anchor)
		if anchor.Start() != nil {
			os.Exit(30)
		}
		w := &ownedWorkload{anchor: anchor, scanSession: func(context.Context) ([]int, error) { return nil, syscall.EPERM }}
		err := w.stopSession(0)
		alive := anchor.Process.Signal(syscall.Signal(0)) == nil
		_ = anchor.Process.Kill()
		done := make(chan struct{})
		go func() { _ = anchor.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(time.Second):
			os.Exit(31)
		}
		if !errors.Is(err, syscall.EPERM) || !alive {
			os.Exit(32)
		}
		os.Exit(0)
	}
	if os.Getenv("BACKLOT_SESSION_CHILD") == "1" {
		signal.Ignore(syscall.SIGTERM)
		_ = os.WriteFile(os.Getenv("BACKLOT_SESSION_READY"), []byte(strconv.Itoa(os.Getpid())), 0600)
		select {}
	}
	child := exec.Command(os.Args[0], "-test.run=^TestSessionFixture$")
	child.Env = append(os.Environ(), "BACKLOT_SESSION_CHILD=1")
	child.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := child.Start(); err != nil {
		os.Exit(20)
	}
	// Retain a sole Wait owner. Under session cleanup the guardian may terminate
	// root first; child self-expiration remains a backup, never success evidence.
	if err := child.Wait(); err != nil {
		os.Exit(21)
	}
	os.Exit(0)
}

func TestApplicationSessionAndStrictHelperPolicies(t *testing.T) {
	root := t.TempDir()
	binary := filepath.Join(root, "backlot")
	if data, err := exec.Command("go", "build", "-o", binary, "../../cmd/backlot").CombinedOutput(); err != nil {
		t.Fatal(err, string(data))
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	sentinel := ownedSleep(t)
	log, err := os.Create(filepath.Join(root, "output"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = log.Close() }()
	t.Run("application-reaches-child-group", func(t *testing.T) {
		ready := filepath.Join(root, "ready")
		expired := filepath.Join(root, "expired")
		group, err := StartApplication(binary, log, log, "app", strings.Repeat("a", 64))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := group.Stop(0); err != nil {
				t.Error("retained session cleanup", err)
			}
		})
		activated := time.Now()
		err = group.Activate(Spec{Executable: self, Args: []string{"-test.run=^TestSessionFixture$"}, Environment: append(os.Environ(), "BACKLOT_SESSION_FIXTURE=1", "BACKLOT_SESSION_READY="+ready, "BACKLOT_SESSION_EXPIRED="+expired), Directory: root, Component: "app", Attempt: strings.Repeat("a", 64)})
		if err != nil {
			t.Fatal(err)
		}
		deadline := time.Now().Add(3 * time.Second)
		var child int
		for time.Now().Before(deadline) {
			data, err := os.ReadFile(ready)
			if err == nil {
				child, _ = strconv.Atoi(string(data))
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		if child == 0 {
			t.Fatal("child group not ready")
		}
		pgid, err := syscall.Getpgid(child)
		if err != nil || pgid == group.PGID() {
			t.Fatal("fixture did not create another group", err)
		}
		stopStarted := time.Now()
		if err := group.Stop(20 * time.Millisecond); err != nil {
			t.Fatal(err)
		}
		if time.Since(stopStarted) >= 5*time.Second || time.Since(activated) >= 10*time.Second {
			t.Fatal("session cleanup exceeded actuation budget")
		}
		if _, err := os.Stat(expired); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("backup fixture expiry cannot prove cleanup", err)
		}
		result := group.CleanupResult()
		if result == nil || result.Confidence != v1.ObservedEmpty {
			t.Fatal(result)
		}
		// Read-only execution-state observation complements the bounded observation
		// result; it never authorizes a signal to the observed fixture PID.
		if dead, err := anchorExited(child); err == nil && !dead {
			t.Fatal("known child still executing after stop")
		}
		if err := sentinel.Process.Signal(syscall.Signal(0)); err != nil {
			t.Fatal("unrelated sentinel affected", err)
		}
	})
	t.Run("inactive-application-no-fabricated-scans", func(t *testing.T) {
		group, err := StartApplication(binary, log, log, "app", strings.Repeat("a", 64))
		if err != nil {
			t.Fatal(err)
		}
		if err := group.Stop(0); err != nil {
			t.Fatal(err)
		}
		if group.CleanupResult() != nil {
			t.Fatal("inactive application fabricated observed-empty scans")
		}
	})
	t.Run("strict-helper-retains-strong-proof", func(t *testing.T) {
		group, err := Start(binary, log, log)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := group.Stop(0); err != nil {
				t.Error(err)
			}
		})
		if err := group.Activate(Spec{Executable: "/bin/sleep", Args: []string{"15"}, Directory: root, Environment: os.Environ()}); err != nil {
			t.Fatal(err)
		}
		pgid := group.PGID()
		if err := group.Stop(0); err != nil {
			t.Fatal(err)
		}
		if group.Identity.Purpose != "strict" || group.CleanupResult() != nil {
			t.Fatal("strict helper received session semantics")
		}
		if err := signalGroup(pgid, 0); err != syscall.ESRCH {
			t.Fatal("strict kernel group absence not proved", err)
		}
	})
}

func TestSessionReceiptRejectsMissingOrWrongPolicy(t *testing.T) {
	sentinel := ownedSleep(t)
	for _, mode := range []string{"old", "wrong-attempt", "strict-session-result"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			id := Identity{Protocol: 1, Purpose: "application", Component: "app", Attempt: strings.Repeat("a", 64), PID: sentinel.Process.Pid, Birth: "wrong", Control: filepath.Join(dir, "missing.sock"), Token: strings.Repeat("b", 64)}
			proof := cleanupProof{Identity: id, Result: sessionResult(id, v1.ObservedEmpty)}
			switch mode {
			case "old":
				proof.Identity.Protocol = 0
			case "wrong-attempt":
				proof.Result.Attempt = strings.Repeat("c", 64)
			case "strict-session-result":
				id.Purpose = "strict"
				id.Component = ""
				id.Attempt = ""
				proof.Identity = id
			}
			data, _ := json.Marshal(proof)
			if err := os.WriteFile(filepath.Join(dir, "receipt.json"), data, 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := StopResult(id, 0); err == nil {
				t.Fatal("incompatible receipt accepted")
			}
			if err := sentinel.Process.Signal(syscall.Signal(0)); err != nil {
				t.Fatal("receipt signaled unrelated PID", err)
			}
		})
	}
	// A cancelled membership observation cannot become an empty observation.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	budget := 3
	if _, err := acquireSession(ctx, sentinel.Process.Pid, &budget); err == nil {
		t.Fatal("cancelled identity acquisition accepted")
	}
}

func TestSessionObservationFailureCannotBecomeEmpty(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, self, "-test.run=^TestSessionFixture$")
	command.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	command.Env = append(os.Environ(), "BACKLOT_SESSION_FIXTURE=1", "BACKLOT_SESSION_SCAN_FAILURE=1")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatal("denied observation did not preserve live anchor", err, string(output))
	}
	budget := 0
	if h, err := acquireSession(context.Background(), os.Getpid(), &budget); err == nil || h != nil {
		t.Fatal("exhausted acquisition budget accepted")
	}
}

func TestSessionRetryRetainsKnownMemberDespiteMissedEnumeration(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, self, "-test.run=^TestSessionFixture$")
	command.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	command.Env = append(os.Environ(), "BACKLOT_SESSION_FIXTURE=1", "BACKLOT_SESSION_OMIT_SCAN=1", "BACKLOT_SESSION_READY="+filepath.Join(t.TempDir(), "ready"))
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatal("failed retry erased known live member", err, string(output))
	}
}

func fixtureJoin(t *testing.T, cmd *exec.Cmd, done <-chan struct{}) {
	t.Helper()
	_ = cmd.Process.Kill()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Error("retained fixture child failed to join; cleanup incomplete")
	}
}

func TestApplicationAuthorityErrorKeepsPrivateKernelCause(t *testing.T) {
	cause := errors.New("private process4182 /proc/4182/stat capability-value")
	id := Identity{Purpose: "application", Component: "app", Attempt: strings.Repeat("a", 64)}
	err := &authorityError{message: sessionResult(id, v1.CleanupAuthorityLost).Detail, cause: cause}
	if strings.Contains(err.Error(), "4182") || strings.Contains(err.Error(), "capability-value") || !errors.Is(err, cause) {
		t.Fatal("private kernel identity leaked or cause lost", err)
	}
}

func TestSessionLateEmptyObservationRetainsAnchor(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, self, "-test.run=^TestSessionFixture$")
	command.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	command.Env = append(os.Environ(), "BACKLOT_SESSION_FIXTURE=1", "BACKLOT_SESSION_LATE_SCAN=1")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatal("late empty observation", err, string(output))
	}
}
