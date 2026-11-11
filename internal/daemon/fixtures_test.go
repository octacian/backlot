//go:build darwin || linux

package daemon

import (
	"encoding/json"
	"strings"
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
		"server/fixture/port":          plannedValue(composeFormat("private-%s", fixtureRef("resource", "absent", "value"))),
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

func TestComposedFixtureHydrationAndBounds(t *testing.T) {
	origin := fixtureRef("resource", "origin", "url")
	password := fixtureRef("resource", "db", "password")
	value := v1.Value{URLPath: &v1.URLPathValue{Base: origin, Parts: []v1.Value{fixtureLiteral("api/dev/direct-login"), composeFormat("%s", password)}}}
	key := "server/fixture/login"
	snapshot := plan.Snapshot{Plan: v1.PlanResponse{Resources: map[string]v1.Resource{"origin": {Kind: "origin"}, "db": {Kind: "allocation"}}, Publish: &v1.Publication{Resource: "origin"}}, Secrets: map[string]v1.PlannedValue{key: plannedValue(value)}}
	entry := &execution{shared: map[string]sharedConnection{"db": {Request: v1.ManagerRequest{Password: "hydrated-private-canary"}}}}
	result := &v1.ExecutionResult{Origin: "https://scene.example.test"}
	fixture := v1.PlannedFixture{Value: v1.PlannedValue{Redacted: true}}
	record, err := resolveFixture("id", "server", "login", fixture, snapshot, result, entry)
	if err != nil || !record.Sensitive || record.Value != "" || snapshot.Secrets[key+"/resolved"].Literal == nil || *snapshot.Secrets[key+"/resolved"].Literal != result.Origin+"/api/dev/direct-login/hydrated-private-canary" {
		t.Fatal("published origin fixture hydration or private capture failed", err)
	}
	for _, n := range []int{(1 << 20) - 1, 1 << 20} {
		entry.shared["db"] = sharedConnection{Request: v1.ManagerRequest{Password: "hydrated-private-canary" + strings.Repeat("x", n-len("hydrated-private-canary"))}}
		snapshot.Secrets[key] = plannedValue(composeFormat("x%s", password))
		_, err := resolveFixture("id", "server", "login", fixture, snapshot, result, entry)
		if (err == nil) != (n < 1<<20) || err != nil && (strings.Contains(err.Error(), "hydrated-private-canary") || err.Error() != "declared fixture could not be resolved") {
			t.Fatal("hydrated fixture bounds or sanitized failure", n, err)
		}
	}
	// Invalid secret-bearing URL parts must also fail without including the value.
	entry.shared["db"] = sharedConnection{Request: v1.ManagerRequest{Password: "hydrated-private-canary/../escape"}}
	snapshot.Secrets[key] = plannedValue(value)
	if _, err := resolveFixture("id", "server", "login", fixture, snapshot, result, entry); err == nil || strings.Contains(err.Error(), "hydrated-private-canary") {
		t.Fatal("invalid private fixture exposed", err)
	}
}
