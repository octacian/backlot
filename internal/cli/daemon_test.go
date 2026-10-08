//go:build !windows

package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	v1 "github.com/octacian/backlot/api/v1"
	"github.com/octacian/backlot/internal/client"
	"github.com/octacian/backlot/internal/daemon"
)

func TestDaemonCLIUsesDurableSocketOperations(t *testing.T) {
	root, err := os.MkdirTemp("/tmp", "bl-cli-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(root); err != nil {
			t.Error(err)
		}
	})
	t.Setenv("HOME", root)
	t.Setenv("XDG_CONFIG_HOME", root)
	state := filepath.Join(root, "state")
	project := filepath.Join(root, "project")
	if err := os.Mkdir(project, 0700); err != nil {
		t.Fatal(err)
	}
	manifest := `{"version":"backlot/v1","project":"fixture","tools":{"go":"go"},"components":{"test":{"kind":"job","runtime":"native","command":{"tool":"go"},"policy":"each-start"}},"scenes":{"test":{"lifetime":"disposable","components":["test"],"terminal_job":"test"}}}`
	if err := os.WriteFile(filepath.Join(project, "backlot.json"), []byte(manifest), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	exited := make(chan error, 1)
	go func() { exited <- daemon.Serve(ctx, daemon.Options{Directory: state}) }()
	t.Cleanup(func() {
		cancel()
		if err := <-exited; err != nil {
			t.Error(err)
		}
	})
	cli := client.New(daemon.SocketPath(state))
	defer cli.Close()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := cli.Status(context.Background()); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("daemon unavailable")
		}
		time.Sleep(10 * time.Millisecond)
	}
	run := func(args ...string) ([]byte, error) {
		var stdout, stderr bytes.Buffer
		err := NewCommand(v1.VersionResponse{}, &stdout, &stderr).Run(context.Background(), append([]string{"backlot"}, args...))
		return stdout.Bytes(), err
	}
	prepared, err := run("prepare", "--state-dir", state, "--project", project, "--json", "test")
	if err != nil {
		t.Fatal(err)
	}
	var response v1.InstanceResponse
	decoder := json.NewDecoder(bytes.NewReader(prepared))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&response); err != nil {
		t.Fatal(err)
	}
	if err := decoder.Decode(&response); !errors.Is(err, io.EOF) {
		t.Fatal("expected one finite result")
	}
	if response.Instance.Status != v1.Prepared || response.LeaseToken == "" {
		t.Fatal("invalid prepared CLI result")
	}
	renewed, err := run("renew", "--state-dir", state, "--lease-token", response.LeaseToken, "--json", response.Instance.ID)
	if err != nil {
		t.Fatalf("renew: %s %v", renewed, err)
	}
	human, err := run("inspect", "--state-dir", state, response.Instance.ID)
	if err != nil || !strings.Contains(string(human), "no application work executed or readiness observed") {
		t.Fatalf("misleading output: %s %v", human, err)
	}
	cancelled, err := run("cancel", "--state-dir", state, "--json", response.Instance.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(cancelled, &response); err != nil || response.Instance.Status != v1.Cancelled {
		t.Fatal("cancellation not reflected")
	}
	doctor, err := run("doctor", "--state-dir", state, "--json")
	if err != nil {
		t.Fatal(err)
	}
	var diagnostic v1.DoctorResponse
	if err := json.Unmarshal(doctor, &diagnostic); err != nil || !diagnostic.Healthy {
		t.Fatal("doctor failed")
	}
	invalid, err := run("inspect", "--state-dir", state, "--json", "invalid")
	if err == nil {
		t.Fatal("invalid target succeeded")
	}
	var envelope v1.ErrorResponse
	if err := json.Unmarshal(invalid, &envelope); err != nil || envelope.Error.Code != "invalid_request" {
		t.Fatalf("invalid CLI envelope: %s", invalid)
	}
}
