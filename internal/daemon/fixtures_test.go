//go:build darwin || linux

package daemon

import (
	"encoding/json"
	"testing"

	v1 "github.com/octacian/backlot/api/v1"
	"github.com/octacian/backlot/internal/plan"
	bolt "go.etcd.io/bbolt"
)

func TestStartupRefreshInvalidatesUnresolvableCapture(t *testing.T) {
	store, err := openStore(shortTemp(t))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := store.db.Close(); err != nil {
			t.Error(err)
		}
	}()
	s := &service{store: store}
	id := newID()
	selection := v1.StartupDetailSelection{Component: "server", Fixture: "port", Label: "Port"}
	snapshot := plan.Snapshot{Plan: v1.PlanResponse{StartupDetails: []v1.StartupDetailSelection{selection}, Components: []v1.PlannedComponent{{Name: "server", Fixtures: map[string]v1.PlannedFixture{"port": {Value: v1.PlannedValue{Redacted: true}}}}}}, Secrets: map[string]v1.PlannedValue{
		"server/fixture/port":          {Symbolic: &v1.Reference{Kind: "resource", Name: "absent", Field: "value"}},
		"server/fixture/port/resolved": {Literal: ptr("stale-private-value")},
	}}
	result := &v1.ExecutionResult{Fixtures: []v1.FixtureRecord{{Component: "server", Name: "port", Sensitive: true}, {Component: "server", Name: "unselected", Value: "keep"}}}
	s.refreshStartupFixtures(id, snapshot, result, &execution{resources: &resourceGeneration{Resources: map[string]ownedResource{}}})
	if len(result.Fixtures) != 1 || result.Fixtures[0].Name != "unselected" || result.Fixtures[0].Value != "keep" {
		t.Fatal("failed selected refresh retained stale capture or changed unselected fixture")
	}
	if err := store.db.View(func(tx *bolt.Tx) error {
		var secrets map[string]v1.PlannedValue
		if err := json.Unmarshal(tx.Bucket([]byte("secrets")).Get([]byte(id)), &secrets); err != nil {
			return err
		}
		if _, ok := secrets["server/fixture/port/resolved"]; ok {
			t.Error("stale private capture retained")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestPinnedFixtureReadsOneAttempt(t *testing.T) {
	s, root, config := sceneFixture(t)
	prepared := scenePrepare(t, s, root, config)
	id := prepared.Instance.ID
	a, b := newID(), newID()
	publish := func(attempt, value string) error {
		return s.store.db.Update(func(tx *bolt.Tx) error {
			record, err := load(tx, id)
			if err != nil {
				return err
			}
			record.Instance.Status = v1.RuntimeReady
			record.Instance.Operation.Status = v1.RuntimeReady
			record.Instance.Execution = &v1.ExecutionResult{Attempt: attempt, Fixtures: []v1.FixtureRecord{{Component: "check", Name: "password", Sensitive: true}}}
			if err := put(tx, "instances", id, record); err != nil {
				return err
			}
			literal := value
			return put(tx, "secrets", id, map[string]v1.PlannedValue{"check/fixture/password/resolved": {Literal: &literal}})
		})
	}
	if err := publish(a, "value-a"); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		for i := 0; i < 100; i++ {
			if err := publish(b, "value-b"); err != nil {
				done <- err
				return
			}
			if err := publish(a, "value-a"); err != nil {
				done <- err
				return
			}
		}
		done <- nil
	}()
	for i := 0; i < 100; i++ {
		for _, expected := range []struct{ attempt, value string }{{a, "value-a"}, {b, "value-b"}} {
			response, err := s.fixtures(v1.FixturesRequest{APIVersion: v1.Version, InstanceID: id, ExpectedAttempt: expected.attempt, Component: "check", SecretName: "password"}, true)
			if err != nil {
				code(t, err, "fixture_unavailable")
				continue
			}
			if response.Attempt != expected.attempt || len(response.Fixtures) != 1 || response.Fixtures[0].Value != expected.value {
				t.Error("mixed fixture metadata and private values across attempts")
			}
		}
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	// The pin enforces current readiness; explicit unpinned historical access remains.
	current, err := s.store.inspect(id)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.store.execution(id, v1.Stopped, current.Execution); err != nil {
		t.Fatal(err)
	}
	request := v1.FixturesRequest{APIVersion: v1.Version, InstanceID: id, ExpectedAttempt: a, Component: "check", SecretName: "password"}
	if _, err := s.fixtures(request, true); err == nil {
		t.Fatal("pinned retrieval accepted stopped attempt")
	}
	request.ExpectedAttempt = ""
	if _, err := s.fixtures(request, true); err != nil {
		t.Fatal("explicit historical retrieval lost", err)
	}
}
