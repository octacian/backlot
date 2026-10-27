//go:build darwin || linux

package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	v1 "github.com/octacian/backlot/api/v1"
)

func recipeCancellation(t *testing.T, h *nativeHarness, docker *dockerHarness, scene string, args []string, sig os.Signal) {
	t.Helper()
	job := h.manifest.Components["tests"]
	if job.Environment.Assign == nil {
		job.Environment.Assign = map[string]v1.Value{}
	}
	job.Environment.Assign["RUN_ID"] = fixtureRef("instance", "", "id")
	h.manifest.Components["tests"] = job
	h.write()
	argv := []string{"run", scene, "--project", h.project, "--state-dir", h.dir, "--json", "--stop-grace", "2s", "--startup-timeout", "90s"}
	if docker != nil {
		argv = append(argv, "--config", docker.config)
	}
	argv = append(append(argv, "--"), args...)
	command := exec.Command(h.binary, argv...)
	var stdout, stderr bytes.Buffer
	command.Stdout, command.Stderr = &stdout, &stderr
	var owner *fixtureOwnerJoin
	t.Cleanup(func() {
		if owner != nil && !owner.attempted {
			_ = command.Process.Signal(os.Interrupt)
			if err := owner.join(harnessShutdownBudget, 2*time.Second, command.Process.Kill); err != nil {
				h.cleanupUnverified = true
				t.Error(err)
			}
		}
	})
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	owner = &fixtureOwnerJoin{done: waitFixtureChild(command)}
	var holding struct {
		Instance    string `json:"instance"`
		PID         int    `json:"pid"`
		ServerPID   int    `json:"server_pid"`
		BrowserPIDs []int  `json:"browser_pids"`
	}
	deadline := time.Now().Add(90 * time.Second)
	for {
		data, err := os.ReadFile(filepath.Join(h.project, "reports/holding.json"))
		if err == nil {
			if err := json.Unmarshal(data, &holding); err != nil {
				t.Fatal(err)
			}
			break
		}
		if !os.IsNotExist(err) || time.Now().After(deadline) {
			t.Fatal("held test marker unavailable", err)
		}
		time.Sleep(25 * time.Millisecond)
	}
	if holding.Instance == "" || holding.PID <= 0 {
		t.Fatal("empty held-test identity")
	}
	pids := append([]int{holding.PID}, holding.BrowserPIDs...)
	if scene == "browser" {
		if holding.ServerPID <= 0 || len(holding.BrowserPIDs) == 0 {
			t.Fatal("missing server/browser witnesses")
		}
		pids = append(pids, holding.ServerPID)
	}
	for _, pid := range pids {
		if pid <= 0 || syscall.Kill(pid, 0) != nil {
			t.Fatal("fixture witness not observed live")
		}
	}
	pgid, err := syscall.Getpgid(holding.PID)
	if err != nil || pgid <= 1 {
		t.Fatal("supervised fixture group unavailable", err)
	}
	path := filepath.Join(h.project, "cancellation.pids")
	var witness bytes.Buffer
	for _, pid := range append(pids, pgid) {
		fmt.Fprintln(&witness, pid)
	}
	if err := os.WriteFile(path, witness.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	h.extraPIDFiles = append(h.extraPIDFiles, path)
	var absent func() error
	if docker != nil {
		absent = docker.observeDisposableDockerObjects(holding.Instance, "")
	}
	// Only this retained unreaped CLI child authorizes actuation; witnesses are read-only.
	if err := command.Process.Signal(sig); err != nil {
		t.Fatal(err)
	}
	if err := owner.join(harnessShutdownBudget, 2*time.Second, command.Process.Kill); err != nil {
		h.cleanupUnverified = true
		t.Fatal(err)
	}
	want := 130
	if sig == syscall.SIGTERM {
		want = 143
	}
	var exit *exec.ExitError
	if !errors.As(owner.waitErr, &exit) || exit.ExitCode() != want {
		t.Fatal("CLI cancellation exit", owner.waitErr)
	}
	var response v1.InstanceResponse
	if err := json.Unmarshal(stdout.Bytes(), &response); err != nil {
		t.Fatal(err, stderr.String())
	}
	instance := response.Instance
	result := instance.Execution
	if instance.ID != holding.Instance || instance.Status != v1.Cancelled || result == nil || !result.Cancelled || result.Failure != "" || result.CollectionFailure != "" || result.CleanupFailure != "" {
		t.Fatal("cancellation facts", result)
	}
	if _, err := h.cli.Destroy(context.Background(), instance.ID); err != nil {
		t.Fatal(err)
	}
	if absent != nil {
		if err := absent(); err != nil {
			h.cleanupUnverified = true
			t.Fatal(err)
		}
	}
	if data, err := os.ReadFile(path); err != nil || !bytes.Equal(data, witness.Bytes()) {
		h.cleanupUnverified = true
		t.Fatal("process witnesses missing or changed", err)
	}
	h.absent()
	if h.cleanupUnverified {
		t.Fatal("observed process cleanup incomplete")
	}
	exportRecipe(t, h, instance, args, -1)
	native := "unknown"
	if result.TerminalExitCode != nil {
		native = fmt.Sprint(*result.TerminalExitCode)
	}
	t.Logf("cancelled %s: CLI=%d native=%s witnessed=%d; PID reuse can fail conservatively, unseen descendants are not ruled out", scene, want, native, len(pids))
}
