//go:build darwin || linux

package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/octacian/backlot/internal/client"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gofrs/flock"
	v1 "github.com/octacian/backlot/api/v1"
	bolt "go.etcd.io/bbolt"
)

func TestInfraSelectionAndStatus(t *testing.T) {
	for _, mode := range []string{"missing", "none", "external", "invalid", "managed-failure"} {
		t.Run(mode, func(t *testing.T) {
			s, _, _ := sceneFixture(t)
			s.directory = s.store.directory
			s.generation = newID()
			path := filepath.Join(t.TempDir(), "machine.json")
			config := `{"version":"backlot/v1"}`
			switch mode {
			case "external":
				config = `{"version":"backlot/v1","caddy":{"mode":"external","endpoint":"http://127.0.0.1:2020","scope":"fixture","domain_suffix":"example.test","host_address":"127.0.0.1"}}`
			case "invalid":
				config = `{"version":"wrong"}`
			case "managed-failure":
				config = `{"version":"backlot/v1","caddy":{"mode":"managed","executable":"/does/not/exist"}}`
			}
			if mode != "missing" {
				if err := os.WriteFile(path, []byte(config), 0600); err != nil {
					t.Fatal(err)
				}
			}
			q := v1.InfraRequest{APIVersion: v1.Version, ConfigPath: path}
			if mode == "missing" {
				q.ConfigPath = ""
				q.DefaultConfigPath = path
			}
			out := s.infra(context.Background(), "start", q)
			good := mode == "missing" || mode == "none" || mode == "external"
			if out.Ready != good || (out.Error == nil) != good || out.Daemon != "running" || out.Directory != s.directory || out.Activity == nil {
				t.Fatal(out)
			}
			if mode == "external" && out.Gateway.Mode != "external" {
				t.Fatal(out)
			}
			retained, err := s.store.managed()
			if err != nil || retained.Identity != nil || retained.Pending {
				t.Fatal("unexpected provider allocation", retained, err)
			}
		})
	}
}

func TestInfraOneCoordinatorAndIndependentPhases(t *testing.T) {
	s, _, _ := sceneFixture(t)
	s.generation = newID()
	// Corrupt gateway state is a real phase failure; independent record cleanup
	// must still run and every observer must consume the identical terminal result.
	if err := s.store.db.Update(func(tx *bolt.Tx) error {
		return tx.Bucket([]byte("meta")).Put([]byte("managed_gateway"), []byte("bad"))
	}); err != nil {
		t.Fatal(err)
	}
	var workers sync.WaitGroup
	operations := make(chan *shutdownOperation, 8)
	for range 8 {
		workers.Go(func() { operations <- s.startShutdown() })
	}
	workers.Wait()
	close(operations)
	var first *shutdownOperation
	for o := range operations {
		if first == nil {
			first = o
		}
		if first != o {
			t.Fatal("duplicate coordinator")
		}
	}
	<-first.done
	if first.err == nil || s.stop() != first.err {
		t.Fatal("terminal result replaced", first.err)
	}
	if _, _, err := s.beginMutation(context.Background()); err == nil {
		t.Fatal("admission reopened")
	}
	phases := map[string]string{}
	cleanup := 0
	for _, r := range first.records {
		if err := r.Validate(); err != nil {
			t.Fatal(r, err)
		}
		if r.Type == "phase" {
			phases[r.Phase] = r.State
		}
		if r.Type == "cleanup" {
			cleanup++
			if r.Error == nil {
				t.Fatal("cleanup failure hidden")
			}
		}
	}
	if cleanup != 1 || phases["gateway"] != "failed" || phases["records"] == "" || !s.ownersJoined() {
		t.Fatal(cleanup, phases)
	}
}

func TestInfraObserverDisconnectDoesNotCancel(t *testing.T) {
	for _, disconnect := range []bool{false, true} {
		t.Run(map[bool]string{false: "slow", true: "disconnected"}[disconnect], func(t *testing.T) {
			s, _, _ := sceneFixture(t)
			s.generation = newID()
			_, finish, err := s.beginMutation(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			var once sync.Once
			defer once.Do(finish)
			server := httptest.NewServer(handler(s, v1.DaemonStatusResponse{}, func() {}))
			defer server.Close()
			raw, _ := json.Marshal(v1.InfraRequest{APIVersion: v1.Version, Contract: v1.InfraStopContract, DaemonGeneration: s.generation})
			req, _ := http.NewRequest(http.MethodPost, server.URL+"/v1/infra/stop", bytes.NewReader(raw))
			req.Header.Set("X-Backlot-API-Version", v1.Version)
			req.Header.Set("Content-Type", "application/json")
			response, err := server.Client().Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = response.Body.Close() }()
			var accepted v1.InfraStopRecord
			if json.NewDecoder(response.Body).Decode(&accepted) != nil || accepted.Type != "accepted" {
				t.Fatal(accepted)
			}
			if disconnect {
				_ = response.Body.Close()
			}
			once.Do(finish)
			select {
			case <-s.startShutdown().done:
			case <-time.After(2 * time.Second):
				t.Fatal("observer blocked cleanup")
			}
			if s.shutdown.err != nil {
				t.Fatal(s.shutdown.err)
			}
		})
	}
}

func TestInfraRejectsReplacementBeforeMutation(t *testing.T) {
	s, _, _ := sceneFixture(t)
	s.generation = newID()
	server := httptest.NewServer(handler(s, v1.DaemonStatusResponse{}, func() {}))
	defer server.Close()
	for _, q := range []v1.InfraRequest{
		{APIVersion: v1.Version, Contract: v1.InfraStopContract, DaemonGeneration: newID()},
		{APIVersion: v1.Version, DaemonGeneration: s.generation},
	} {
		raw, _ := json.Marshal(q)
		req, _ := http.NewRequest(http.MethodPost, server.URL+"/v1/infra/stop", bytes.NewReader(raw))
		req.Header.Set("X-Backlot-API-Version", v1.Version)
		req.Header.Set("Content-Type", "application/json")
		response, err := server.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = response.Body.Close()
		if response.StatusCode == http.StatusOK || s.closing || s.shutdown != nil {
			t.Fatal("replacement/unsupported request mutated daemon")
		}
	}
}

func TestInfrastructureOwnershipIdentity(t *testing.T) {
	for _, mode := range []string{"stable", "lock", "root", "socket", "permissions"} {
		t.Run(mode, func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "state")
			o, err := openStateOwnership(root, true)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = o.lock.Close() }()
			held, err := o.try()
			if err != nil || !held {
				t.Fatal(held, err)
			}
			switch mode {
			case "lock":
				if err := os.Rename(filepath.Join(root, "daemon.lock"), filepath.Join(root, "old.lock")); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(root, "daemon.lock"), nil, 0600); err != nil {
					t.Fatal(err)
				}
			case "root":
				if err := os.Rename(root, root+"-old"); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(root, 0700); err != nil {
					t.Fatal(err)
				}
			case "socket":
				if err := os.WriteFile(SocketPath(root), nil, 0600); err != nil {
					t.Fatal(err)
				}
			case "permissions":
				if err := os.Chmod(root, 0755); err != nil {
					t.Fatal(err)
				}
			}
			if (o.verify() == nil) != (mode == "stable") {
				t.Fatal("identity result", mode)
			}
			if mode == "stable" {
				peer := flock.New(filepath.Join(root, "daemon.lock"))
				defer func() { _ = peer.Close() }()
				held, err := peer.TryLock()
				if err != nil || held {
					t.Fatal("completion callback would lose held lock", held, err)
				}
			}
		})
	}
}

func TestInfraCancelledObservation(t *testing.T) {
	root := filepath.Join(t.TempDir(), "state")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	owner := flock.New(filepath.Join(root, "daemon.lock"), flock.SetPermissions(0600))
	defer func() { _ = owner.Close() }()
	if held, err := owner.TryLock(); err != nil || !held {
		t.Fatal(err)
	}
	if err := InfraAbsent(root); err == nil {
		t.Fatal("locked scope reported absent")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var final v1.InfraStopRecord
	err := InfraStop(ctx, root, func(r v1.InfraStopRecord) error { final = r; return nil })
	if err == nil || final.State != "unknown" || final.Error == nil {
		t.Fatal(err, final)
	}
	peer := flock.New(filepath.Join(root, "daemon.lock"))
	defer func() { _ = peer.Close() }()
	if held, err := peer.TryLock(); held || err != nil {
		t.Fatal("cancelled observation disturbed owner", held, err)
	}
	// Output errors never convert absent/unknown cleanup into success.
	if err := owner.Close(); err != nil {
		t.Fatal(err)
	}
	sentinel := errors.New("closed output")
	if err := InfraStop(context.Background(), root, func(v1.InfraStopRecord) error { return sentinel }); !errors.Is(err, sentinel) || !strings.Contains(err.Error(), "unknown") {
		t.Fatal(err)
	}
}

func TestInfraCompletionHoldsOwnership(t *testing.T) {
	root, err := os.MkdirTemp("/tmp", "bl-infra-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() { result <- Serve(ctx, Options{Directory: root}) }()
	cli := client.New(SocketPath(root))
	defer cli.Close()
	deadline, stop := context.WithTimeout(context.Background(), 5*time.Second)
	defer stop()
	for {
		if _, err := cli.Status(deadline); err == nil {
			break
		}
		select {
		case <-deadline.Done():
			t.Fatal(deadline.Err())
		case <-time.After(10 * time.Millisecond):
		}
	}
	completed := false
	err = InfraStop(context.Background(), root, func(r v1.InfraStopRecord) error {
		if r.Type != "completion" {
			return nil
		}
		if r.State != "complete" {
			t.Error(r)
			return nil
		}
		contender := flock.New(filepath.Join(root, "daemon.lock"))
		defer func() { _ = contender.Close() }()
		held, err := contender.TryLock()
		if held || err != nil {
			t.Error("completion emitted without held ownership", held, err)
		}
		completed = true
		return nil
	})
	if err != nil || !completed {
		t.Fatal(err, completed)
	}
	if err := <-result; err != nil {
		t.Fatal(err)
	}
}

func TestInfraReportsOnlyObservedReplacement(t *testing.T) {
	root, err := os.MkdirTemp("/tmp", "bl-infra-replace-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	owner, err := openStateOwnership(root, true)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = owner.lock.Close() }()
	if held, err := owner.try(); err != nil || !held {
		t.Fatal(err)
	}
	listener, err := net.Listen("unix", SocketPath(root))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(SocketPath(root), 0600); err != nil {
		t.Fatal(err)
	}
	original, replacement := newID(), newID()
	var generation atomic.Value
	generation.Store(original)
	var stopCalls atomic.Int32
	server := &http.Server{ReadHeaderTimeout: time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Backlot-API-Version", v1.Version)
		if r.URL.Path == "/v1/infra/status" {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(v1.InfraResponse{APIVersion: v1.Version, DaemonGeneration: generation.Load().(string), Directory: root, Daemon: "running", Gateway: &v1.GatewayResponse{APIVersion: v1.Version, Mode: "managed", Status: "unconfigured"}, Activity: &v1.ActivityResponse{APIVersion: v1.Version, DaemonGeneration: generation.Load().(string), GatewayStatus: "unconfigured", Entries: []v1.ActivityEntry{}, Complete: true}, Ready: true, Guidance: "fixture"})
			return
		}
		if r.URL.Path != "/v1/infra/stop" {
			t.Error(r.URL.Path)
			return
		}
		stopCalls.Add(1)
		w.Header().Set("Content-Type", "application/x-ndjson")
		base := v1.InfraStopRecord{APIVersion: v1.Version, DaemonGeneration: original, OperationID: newID(), Type: "accepted"}
		_ = json.NewEncoder(w).Encode(base)
		for _, phase := range []string{"owners", "expiry", "recovery", "gateway", "shared", "records"} {
			event := base
			event.Type, event.Phase, event.State = "phase", phase, "running"
			_ = json.NewEncoder(w).Encode(event)
			event.State = "succeeded"
			_ = json.NewEncoder(w).Encode(event)
		}
		base.Type = "cleanup"
		_ = json.NewEncoder(w).Encode(base)
		// Model the generation visible after the original cleanup result, while a
		// replacement owns this private scope. There is no second stop authorization.
		generation.Store(replacement)
	})}
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	defer func() { _ = server.Close(); <-done }()
	var completion v1.InfraStopRecord
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err = InfraStop(ctx, root, func(r v1.InfraStopRecord) error {
		if r.Type == "completion" {
			completion = r
		}
		return nil
	})
	if err == nil || completion.State != "unknown" || completion.ReplacementGeneration != replacement || stopCalls.Load() != 1 {
		t.Fatal(err, completion, stopCalls.Load())
	}
}

func TestInfraAbsenceReadDoesNotCreateState(t *testing.T) {
	root := filepath.Join(t.TempDir(), "state")
	if err := InfraAbsent(root); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(root); !os.IsNotExist(err) {
		t.Fatal("absence read created state", err)
	}
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := openStateOwnership(root, false); err == nil {
		t.Fatal("read accepted missing lock")
	}
	if _, err := os.Lstat(filepath.Join(root, "daemon.lock")); !os.IsNotExist(err) {
		t.Fatal("read created lock", err)
	}
}
