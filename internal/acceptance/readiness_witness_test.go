//go:build darwin && arm64

package acceptance

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestReadinessWitnessInterruptedPublication(t *testing.T) {
	for _, mode := range []string{"truncate-control", "atomic"} {
		t.Run(mode, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "readiness-blocked")
			if err := os.WriteFile(path, []byte(readinessWitness), 0600); err != nil {
				t.Fatal(err)
			}
			binary, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			child := exec.CommandContext(ctx, binary, "-test.run=^TestReadinessWitnessInterruptionHelper$")
			child.Env = append(os.Environ(), "BACKLOT_WITNESS_INTERRUPT="+mode, "BACKLOT_WITNESS_PATH="+path)
			output, err := child.StdoutPipe()
			if err != nil {
				t.Fatal(err)
			}
			input, err := child.StdinPipe()
			if err != nil {
				t.Fatal(err)
			}
			// Retain cleanup before Start and act only through this unreaped direct child.
			joined := false
			t.Cleanup(func() {
				_ = input.Close()
				if child.Process != nil && !joined {
					_ = child.Process.Kill()
					_ = child.Wait()
				}
			})
			if err := child.Start(); err != nil {
				t.Fatal(err)
			}
			gate, err := bufio.NewReader(output).ReadString('\n')
			if err != nil || gate != "allocated-before-write\n" {
				t.Fatalf("child did not reach publication gate: %q %v", gate, err)
			}
			if err := child.Process.Kill(); err != nil {
				t.Fatal(err)
			}
			waitErr := child.Wait()
			joined = true
			var exit *exec.ExitError
			if !errors.As(waitErr, &exit) || ctx.Err() != nil {
				t.Fatal("expected retained child interruption", waitErr, ctx.Err())
			}
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			expected := readinessWitness
			if mode == "truncate-control" {
				expected = ""
			}
			if string(data) != expected {
				t.Fatalf("interrupted %s witness=%q want=%q", mode, data, expected)
			}
			if err := publishReadinessWitness(path, nil); err != nil {
				t.Fatal(err)
			}
			data, err = os.ReadFile(path)
			if err != nil || string(data) != readinessWitness {
				t.Fatalf("complete observation publication=%q err=%v", data, err)
			}
			t.Logf("joined interrupted %s child: witness bytes=%d; complete publication verified", mode, len(expected))
		})
	}
}

func TestReadinessWitnessInterruptionHelper(t *testing.T) {
	mode := os.Getenv("BACKLOT_WITNESS_INTERRUPT")
	if mode == "" {
		return
	}
	path := os.Getenv("BACKLOT_WITNESS_PATH")
	gate := func() {
		if _, err := fmt.Fprintln(os.Stdout, "allocated-before-write"); err != nil {
			os.Exit(3)
		}
		var b [1]byte
		if _, err := os.Stdin.Read(b[:]); err != nil {
			os.Exit(4)
		}
		os.Exit(5) // The parent must kill its retained child while this read is blocked.
	}
	if mode == "truncate-control" {
		file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = file.Close() }()
		gate()
		t.Fatal("interruption control unexpectedly continued")
	}
	if mode != "atomic" {
		t.Fatal("unknown witness interruption mode")
	}
	if err := publishReadinessWitness(path, gate); err != nil {
		t.Fatal(err)
	}
	t.Fatal("interruption helper unexpectedly continued")
}
