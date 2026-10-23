package native

import (
	"context"
	"errors"
	"golang.org/x/sys/unix"
	"os/exec"
	"syscall"
	"testing"
	"time"
)

func TestDarwinProcessCensusBounds(t *testing.T) {
	for _, kind := range []string{"growth", "oversize", "partial", "cancel-size", "cancel-read", "late-empty"} {
		t.Run(kind, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			calls := 0
			_, err := boundedProcessTable(ctx, func(buffer []unix.KinfoProc) (uintptr, error) {
				calls++
				if buffer == nil {
					switch kind {
					case "oversize":
						return uintptr(sessionCandidateLimit+1) * unix.SizeofKinfoProc, nil
					case "partial":
						return 1, nil
					case "cancel-size", "late-empty":
						cancel()
						return 0, nil
					}
					return unix.SizeofKinfoProc, nil
				}
				if kind == "cancel-read" {
					cancel()
					return 0, nil
				}
				return 0, syscall.ENOMEM
			})
			if err == nil {
				t.Fatal("incomplete census accepted")
			}
			if calls > 2*processTableAttempts {
				t.Fatal("growth acquisition unbounded", calls)
			}
			if kind == "growth" && calls != 2*processTableAttempts {
				t.Fatal("growth retry seam", calls)
			}
			if (kind == "oversize" || kind == "partial") && calls != 1 {
				t.Fatal("allocated/read rejected size")
			}
			if (kind == "cancel-size" || kind == "cancel-read" || kind == "late-empty") && !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := boundedProcessTable(ctx, func([]unix.KinfoProc) (uintptr, error) { t.Fatal("cancelled scan contacted kernel"); return 0, nil }); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestDarwinBoundedCensusKernel(t *testing.T) {
	if _, err := darwinProcesses(context.Background(), -1, 0); err == nil {
		t.Fatal("invalid sysctl selector omitted kernel errno")
	}
	all, err := darwinProcesses(context.Background(), 0, 0)
	if err != nil || len(all) == 0 {
		t.Fatal("actual kernel census", len(all), err)
	}
	self, err := identify(syscall.Getpid())
	if err != nil || self.PID != syscall.Getpid() || self.Birth == "" {
		t.Fatal("bounded PID census", self, err)
	}
}

func TestDarwinBoundedCensusDuringOwnedChurn(t *testing.T) {
	for range 32 {
		child := exec.Command("/usr/bin/true")
		done := make(chan struct{})
		t.Cleanup(func() {
			if child.Process != nil {
				select {
				case <-done:
					return
				default:
				}
				_ = child.Process.Kill()
				select {
				case <-done:
				case <-time.After(2 * time.Second):
					t.Error("owned churn child did not join")
				}
			}
		})
		if err := child.Start(); err != nil {
			t.Fatal(err)
		}
		go func() { _ = child.Wait(); close(done) }()
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		_, err := darwinProcesses(ctx, 0, 0)
		cancel()
		if err != nil {
			t.Fatal("bounded census during direct-owned exec/exit", err)
		}
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Fatal("owned churn join exceeded budget")
		}
	}
}
