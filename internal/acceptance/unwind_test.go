//go:build darwin && arm64

package acceptance

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The subprocess must fail, while its parent verifies the cleanup that Fatal
// would otherwise hide from assertions in the failing test itself.
func TestControlledFixtureUnwind(t *testing.T) {
	for _, phase := range []string{"setup-after-allocation", "fatal-after-pending-destroy", "startup-abort-after-listen"} {
		t.Run(phase, func(t *testing.T) {
			report := filepath.Join(t.TempDir(), "cleanup.json")
			binary, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, binary, "-test.run=^TestControlledFixtureUnwindHelper$", "-test.v")
			cmd.Env = append(os.Environ(), "BACKLOT_UNWIND_PHASE="+phase, "BACKLOT_UNWIND_REPORT="+report)
			data, err := cmd.CombinedOutput()
			var exit *exec.ExitError
			if !errors.As(err, &exit) || exit.ExitCode() != 1 || ctx.Err() != nil || !strings.Contains(string(data), "injected "+phase) {
				t.Fatalf("expected isolated intentional failure, got %v: %s", err, data)
			}
			raw, err := os.ReadFile(report)
			if err != nil {
				t.Fatal("missing unwind cleanup receipt", err, string(data))
			}
			var receipt struct {
				Root         string `json:"root"`
				Closed       bool   `json:"closed"`
				Owners       bool   `json:"owners_joined"`
				Dependencies bool   `json:"dependencies_released"`
				Upstreams    bool   `json:"upstreams_joined"`
			}
			if json.Unmarshal(raw, &receipt) != nil || !receipt.Closed || !receipt.Owners || !receipt.Dependencies || !receipt.Upstreams {
				t.Fatalf("unwind failed to recover/join/close: %s; %s", raw, data)
			}
			if _, err := os.Stat(receipt.Root); !os.IsNotExist(err) {
				t.Fatal("closed fixture root remains", err)
			}
			t.Log("intentional inner failure retained; bounded registered cleanup joined owners, released dependencies, joined upstream and removed owned root")
		})
	}
}
func TestControlledFixtureUnwindHelper(t *testing.T) {
	switch os.Getenv("BACKLOT_UNWIND_PHASE") {
	case "setup-after-allocation":
		ownerFixture(t, func(*controlledFixture) error { return errors.New("injected setup-after-allocation") })
	case "startup-abort-after-listen":
		f, owner, _, _ := recoveryFixture(t)
		t.Setenv("BACKLOT_RECOVERY_BLOCK_READINESS", "1")
		repair := f.repair
		f.repair = func() {
			repair()
			if err := os.Unsetenv("BACKLOT_RECOVERY_BLOCK_READINESS"); err != nil {
				t.Error(err)
			}
		}
		// Run cleanup before environment restoration; it explicitly disarms the
		// readiness fault before any recovery, even when assertions call Fatal.
		t.Cleanup(f.cleanup)
		err := owner.startDaemon(500 * time.Millisecond)
		if err == nil || !errors.Is(err, context.DeadlineExceeded) || owner.daemon != nil {
			t.Fatal("expected joined startup abort", err)
		}
		if _, err := os.Stat(filepath.Join(owner.state, "control.address")); err != nil {
			t.Fatal("abort occurred before model listener allocation", err)
		}
		witness, err := os.ReadFile(filepath.Join(owner.state, "readiness-blocked"))
		if err != nil || string(witness) != "live model responded before explicit readiness failure" {
			t.Fatal("explicit failure lacked a live-helper witness", err)
		}
		if err := f.logFile.Sync(); err != nil {
			t.Fatal(err)
		}
		t.Fatal("injected startup-abort-after-listen")
	case "fatal-after-pending-destroy":
		f, owner, dependency, _ := recoveryFixture(t)
		if err := owner.startDaemon(5 * time.Second); err != nil {
			t.Fatal(err)
		}
		owner.stop()
		if err := f.parent.cleanupDependencies(); err == nil || dependency.released {
			t.Fatal("pending destruction failure missing")
		}
		t.Fatal("injected fatal-after-pending-destroy")
	}
}
