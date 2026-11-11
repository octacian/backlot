//go:build !windows

package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	v1 "github.com/octacian/backlot/api/v1"
	bolt "go.etcd.io/bbolt"
)

func TestBulkHTTPProgressBeforeLaterTargetCompletes(t *testing.T) {
	for _, disconnect := range []bool{false, true} {
		t.Run(map[bool]string{false: "complete", true: "disconnect"}[disconnect], func(t *testing.T) {
			s, q := attachedCoordinator(t)
			q.Attached = false
			r, err := s.run(context.Background(), q)
			if err != nil {
				t.Fatal(err)
			}
			taskAdmissionReady(t, s, r.Instance.ID)
			first := strings.Repeat("0", 64)
			if err := s.store.db.Update(func(tx *bolt.Tx) error {
				clone, err := load(tx, r.Instance.ID)
				if err != nil {
					return err
				}
				clone.Instance.ID, clone.Instance.Execution = first, nil
				clone.Instance.Status, clone.Instance.Operation.Status = v1.Preparing, v1.Preparing
				return put(tx, "instances", first, clone)
			}); err != nil {
				t.Fatal(err)
			}
			lock := s.mutationLock(r.Instance.ID)
			lock.Lock()
			var unlock sync.Once
			defer unlock.Do(lock.Unlock)
			server := httptest.NewServer(handler(s, v1.DaemonStatusResponse{}, func() {}))
			defer server.Close()
			data, _ := json.Marshal(bulkRequest())
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			req, err := http.NewRequestWithContext(ctx, http.MethodPost, server.URL+"/v1/runtime/stop-all", bytes.NewReader(data))
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-Backlot-API-Version", v1.Version)
			response, err := server.Client().Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = response.Body.Close() }()
			decoder := json.NewDecoder(response.Body)
			read := func(kind string) v1.StopAllRecord {
				t.Helper()
				var event v1.StopAllRecord
				if err := decoder.Decode(&event); err != nil || event.Validate() != nil || event.Type != kind {
					t.Fatalf("event %s: %#v %v", kind, event, err)
				}
				return event
			}
			read("accepted")
			event := read("target")
			if event.Target.ID != first || event.Target.Outcome != "stopped" {
				t.Fatal("earlier target outcome delayed", event)
			}
			current, err := s.store.inspect(r.Instance.ID)
			if err != nil || current.Status != v1.RuntimeReady {
				t.Fatal("later target was not still pending", err)
			}
			if disconnect {
				cancel()
				_ = response.Body.Close()
				unlock.Do(lock.Unlock)
				s.waitOwners()
				current, err := s.store.inspect(r.Instance.ID)
				if err != nil || current.Execution.Settlement.State != v1.ConsumerSettled {
					t.Fatal("disconnect cancelled accepted cleanup", err)
				}
				return
			}
			// Idle progress may outlast the five-second slow-write deadline.
			// Waiting on a workload must not consume that transport-only budget.
			delay := time.NewTimer(6 * time.Second)
			defer delay.Stop()
			select {
			case <-delay.C:
			case <-ctx.Done():
				t.Fatal("progress wait exceeded fixture budget")
			}
			unlock.Do(lock.Unlock)
			read("target")
			if summary := read("summary").Summary; summary.Stopped != 2 || summary.Error != nil {
				t.Fatal(summary)
			}
		})
	}
}

type blockedBulkWriter struct {
	*httptest.ResponseRecorder
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func (w *blockedBulkWriter) Write(p []byte) (int, error) {
	w.once.Do(func() { close(w.started); <-w.release })
	return w.ResponseRecorder.Write(p)
}

func TestBulkSlowOutputDoesNotOwnCleanup(t *testing.T) {
	s, q := attachedCoordinator(t)
	q.Attached = false
	r, err := s.run(context.Background(), q)
	if err != nil {
		t.Fatal(err)
	}
	taskAdmissionReady(t, s, r.Instance.ID)
	data, _ := json.Marshal(bulkRequest())
	req := httptest.NewRequest(http.MethodPost, "/v1/runtime/stop-all", bytes.NewReader(data))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Backlot-API-Version", v1.Version)
	writer := &blockedBulkWriter{ResponseRecorder: httptest.NewRecorder(), started: make(chan struct{}), release: make(chan struct{})}
	done := make(chan struct{})
	go func() { defer close(done); handler(s, v1.DaemonStatusResponse{}, func() {}).ServeHTTP(writer, req) }()
	defer func() { close(writer.release); awaitControlled(t, done, "output observer did not join") }()
	awaitControlled(t, writer.started, "observer did not start")
	s.waitOwners()
	final, err := s.store.inspect(r.Instance.ID)
	if err != nil || final.Execution.Settlement.State != v1.ConsumerSettled {
		t.Fatal("slow output blocked cleanup", err)
	}
	s.mu.Lock()
	closed := s.bulkStopping
	s.mu.Unlock()
	if closed {
		t.Fatal("slow output retained admission fence")
	}
}

func TestBulkOldContractRejectedBeforeEffects(t *testing.T) {
	s, _, _ := sceneFixture(t)
	q := bulkRequest()
	q.Contract = ""
	data, _ := json.Marshal(q)
	req := httptest.NewRequest(http.MethodPost, "/v1/runtime/stop-all", bytes.NewReader(data))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Backlot-API-Version", v1.Version)
	response := httptest.NewRecorder()
	handler(s, v1.DaemonStatusResponse{}, func() {}).ServeHTTP(response, req)
	if response.Code == http.StatusOK || s.admissionGeneration != 0 || s.bulkStopping || len(s.mutationOwners) != 0 {
		t.Fatal("incompatible peer caused effects", response.Code)
	}
}
