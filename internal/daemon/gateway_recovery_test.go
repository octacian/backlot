//go:build darwin || linux

package daemon

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	v1 "github.com/octacian/backlot/api/v1"
	"github.com/octacian/backlot/internal/gateway"
	"github.com/octacian/backlot/internal/gateway/gatewaytest"
)

func TestShutdownRetainsUnavailableRecoveredRoute(t *testing.T) {
	f := newControlledExpiry(t)
	f.allowError = true // The fixture deliberately retains durable cleanup uncertainty.
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		attempts.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()
	prepared, err := f.s.prepare(context.Background(), request(f.root, f.config, "dev"))
	if err != nil {
		t.Fatal(err)
	}
	intent, err := gateway.NewIntent(v1.CaddyConfig{Endpoint: server.URL, Scope: "backlot", HostAddress: "127.0.0.1"}, "recovered.fixture.test", newID(),
		v1.Publication{Routes: []v1.Route{{Path: "/", Service: "web", Port: "http", Prefix: "preserve"}}},
		[]v1.PlannedComponent{{Name: "web", Ports: map[string]v1.ServicePort{"http": {Resource: "port"}}}}, map[string]int{"port": 1234})
	if err != nil {
		t.Fatal(err)
	}
	result := &v1.ExecutionResult{Attempt: newID(), CleanupFailure: "previous unavailable recovery"}
	id := prepared.Instance.ID
	if _, err := f.s.store.execution(id, v1.Interrupted, result); err != nil {
		t.Fatal(err)
	}
	if err := f.s.store.saveRuntime(id, runtimeRecord{Attempt: result.Attempt, Gateway: &intent}); err != nil {
		t.Fatal(err)
	}
	if err := f.s.stop(); err == nil {
		t.Fatal("unavailable exact-route cleanup reported success")
	}
	journal, err := f.s.store.runtime(id)
	if err != nil || journal.Gateway == nil || journal.Gateway.ID != intent.ID || attempts.Load() == 0 {
		t.Fatal("shutdown skipped durable route retry or discarded authority", err)
	}
	instance, err := f.s.store.inspect(id)
	if err != nil || instance.Status != v1.Interrupted || instance.Execution.CleanupFailure == "" {
		t.Fatal("failed cleanup uncertainty not preserved", err)
	}
}

func TestGatewayJournalRecovery(t *testing.T) {
	f := gatewaytest.Start(t, "fixture.test")
	for _, stage := range []string{"gateway-intent", "gateway-effect", "completed"} {
		t.Run(stage, func(t *testing.T) {
			root, directory := shortTemp(t), shortTemp(t)
			config := writeFixture(t, root)
			state, err := openStore(directory)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = state.db.Close() })
			svc := &service{store: state, directory: directory, lease: time.Minute}
			prepared, err := svc.prepare(context.Background(), request(root, config, "dev"))
			if err != nil {
				t.Fatal(err)
			}
			id := prepared.Instance.ID
			snapshot, err := state.snapshot(id)
			if err != nil {
				t.Fatal(err)
			}
			// This test targets the journal/adapter boundary before any workload
			// activation; the full HTTPS runtime suite exercises real processes.
			snapshot.Caddy = &v1.CaddyConfig{Endpoint: f.Endpoint, Scope: "backlot", DomainSuffix: "fixture.test", HostAddress: "127.0.0.1"}
			snapshot.Plan.Publish = &v1.Publication{Resource: "origin", Routes: []v1.Route{{Path: "/", Service: "web", Port: "http", Prefix: "preserve"}}}
			snapshot.Plan.Components = []v1.PlannedComponent{{Name: "web", Ports: map[string]v1.ServicePort{"http": {Resource: "port"}}}}
			result := &v1.ExecutionResult{Attempt: newID(), Origin: "https://bl-" + id[:40] + ".fixture.test", Components: []v1.ComponentResult{}, Ports: map[string]int{"port": 1234}}
			journal := runtimeRecord{Attempt: result.Attempt}
			if _, err := state.acceptExecution(id, journal, result, false); err != nil {
				t.Fatal(err)
			}
			// Retain cleanup before interruption at either side of the effect.
			t.Cleanup(func() {
				current, err := state.runtime(id)
				if err == nil {
					err = state.removeGateway(context.Background(), id, &current)
				}
				if err != nil {
					t.Error("gateway journal fixture residual", err)
				}
			})
			svc.checkpoint = func(at string) error {
				if at == stage {
					return errors.New("owned fixture interruption")
				}
				return nil
			}
			err = svc.publish(context.Background(), id, snapshot, result, &journal)
			if (err == nil) != (stage == "completed") {
				t.Fatal(stage, err)
			}
			durable, err := state.runtime(id)
			if err != nil || durable.Gateway == nil || durable.Gateway.Applied != (stage == "completed") {
				t.Fatal("intent/completion persistence", stage, err)
			}
			if err := state.db.Close(); err != nil {
				t.Fatal(err)
			}
			state, err = openStore(directory)
			if err != nil {
				t.Fatal(err)
			}
			if err := state.recoverRuntime(); err != nil {
				t.Fatal(err)
			}
			instance, err := state.inspect(id)
			if err != nil || instance.Status != v1.Interrupted || instance.Execution.CleanupFailure != "" {
				t.Fatal("durable publication recovery", stage, err, instance.Execution)
			}
			durable, err = state.runtime(id)
			if err != nil || durable.Gateway != nil {
				t.Fatal("reconciled intent not cleared", stage, err)
			}
		})
	}
}
