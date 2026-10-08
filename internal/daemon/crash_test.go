//go:build !windows

package daemon

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"sync"
	"testing"
	"time"

	v1 "github.com/octacian/backlot/api/v1"
	"github.com/octacian/backlot/internal/client"
)

// TestDaemonChild is an isolated subprocess entry point, never the operator daemon.
func TestDaemonChild(t *testing.T) {
	directory := os.Getenv("BACKLOT_TEST_CHILD_DIRECTORY")
	if directory == "" {
		t.Skip("subprocess helper")
	}
	if err := Serve(context.Background(), Options{Directory: directory}); err != nil {
		t.Fatal(err)
	}
}

func TestCrashRecoveryWithOwnedDaemonChild(t *testing.T) {
	root := shortTemp(t)
	config := writeFixture(t, root)
	dir := shortTemp(t)
	command := exec.Command(os.Args[0], "-test.run=^TestDaemonChild$")
	command.Env = append(os.Environ(), "BACKLOT_TEST_CHILD_DIRECTORY="+dir)
	var logs bytes.Buffer
	command.Stdout = &logs
	command.Stderr = &logs
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	var once sync.Once
	reap := func() { once.Do(func() { _ = command.Process.Kill(); _ = command.Wait() }) }
	t.Cleanup(reap)
	cli := client.New(SocketPath(dir))
	defer cli.Close()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := cli.Status(context.Background()); err == nil {
			break
		}
		if time.Now().After(deadline) {
			reap()
			t.Fatalf("owned child startup timeout: %s", logs.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
	disposable := prepareFixture(t, cli, request(root, config, "test"))
	persistent := prepareFixture(t, cli, request(root, config, "dev"))
	reap()
	if _, err := os.Lstat(SocketPath(dir)); err != nil {
		t.Fatal("expected stale socket after SIGKILL")
	}
	recovered, _ := startFixture(t, dir, time.Second)
	interrupted, err := recovered.Inspect(context.Background(), disposable.Instance.ID)
	if err != nil || interrupted.Instance.Status != v1.Interrupted || interrupted.Instance.Operation.ID != disposable.Instance.Operation.ID || interrupted.Instance.Operation.Allocations[0].OwnershipToken != disposable.Instance.Operation.Allocations[0].OwnershipToken {
		t.Fatalf("crash recovery lost durable ownership: %+v %v", interrupted, err)
	}
	stable, err := recovered.Inspect(context.Background(), persistent.Instance.ID)
	if err != nil || stable.Instance.Status != v1.Prepared {
		t.Fatal("completed persistent metadata lost after crash")
	}
}
