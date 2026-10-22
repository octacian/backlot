package daemon

import (
	"encoding/json"
	"slices"
	"sort"

	v1 "github.com/octacian/backlot/api/v1"
	"github.com/octacian/backlot/internal/plan"
	bolt "go.etcd.io/bbolt"
)

func resolveFixture(id, component, name string, fixture v1.PlannedFixture, snapshot plan.Snapshot, result *v1.ExecutionResult, entry *execution) (v1.FixtureRecord, error) {
	key := component + "/fixture/" + name
	text, err := resolvedValue(fixture.Value, key, id, snapshot, result, entry, v1.Native)
	if err != nil {
		return v1.FixtureRecord{}, problem("fixture_failed", "declared fixture could not be resolved")
	}
	record := v1.FixtureRecord{Component: component, Name: name, Description: fixture.Description, Sensitive: fixture.Value.Redacted}
	if record.Sensitive {
		snapshot.Secrets[key+"/resolved"] = v1.PlannedValue{Literal: &text}
	} else {
		record.Value = text
	}
	return record, nil
}

func (s *service) captureFixtures(id string, snapshot plan.Snapshot, result *v1.ExecutionResult, entry *execution) error {
	for _, component := range snapshot.Plan.Components {
		for name, fixture := range component.Fixtures {
			record, err := resolveFixture(id, component.Name, name, fixture, snapshot, result, entry)
			if err != nil {
				return err
			}
			result.Fixtures = append(result.Fixtures, record)
		}
	}
	sortFixtures(result)
	return s.store.db.Update(func(tx *bolt.Tx) error { return put(tx, "secrets", id, snapshot.Secrets) })
}

// refreshStartupFixtures replaces early captures after permitted port retries.
// Missing captures make presentation unavailable without failing ready runtime.
func (s *service) refreshStartupFixtures(id string, snapshot plan.Snapshot, result *v1.ExecutionResult, entry *execution) {
	if len(snapshot.Plan.StartupDetails) == 0 {
		return
	}
	remove := func(detail v1.StartupDetailSelection) {
		result.Fixtures = slices.DeleteFunc(result.Fixtures, func(f v1.FixtureRecord) bool { return f.Component == detail.Component && f.Name == detail.Fixture })
		delete(snapshot.Secrets, detail.Component+"/fixture/"+detail.Fixture+"/resolved")
	}
	for _, detail := range snapshot.Plan.StartupDetails {
		remove(detail)
		for _, component := range snapshot.Plan.Components {
			if component.Name != detail.Component {
				continue
			}
			fixture, ok := component.Fixtures[detail.Fixture]
			if !ok {
				continue
			}
			record, err := resolveFixture(id, component.Name, detail.Fixture, fixture, snapshot, result, entry)
			if err == nil {
				result.Fixtures = append(result.Fixtures, record)
			}
		}
	}
	if err := s.store.db.Update(func(tx *bolt.Tx) error { return put(tx, "secrets", id, snapshot.Secrets) }); err != nil {
		// A failed private write must not pair a fresh public record with old secrets.
		for _, detail := range snapshot.Plan.StartupDetails {
			remove(detail)
		}
	}
	sortFixtures(result)
}

func sortFixtures(result *v1.ExecutionResult) {
	sort.Slice(result.Fixtures, func(i, j int) bool {
		a, b := result.Fixtures[i], result.Fixtures[j]
		return a.Component+"/"+a.Name < b.Component+"/"+b.Name
	})
}

func (s *service) fixtures(request v1.FixturesRequest, secret bool) (v1.FixturesResponse, error) {
	response := v1.FixturesResponse{APIVersion: v1.Version, Fixtures: []v1.FixtureRecord{}}
	if !validID(request.InstanceID) || request.ExpectedAttempt != "" && !validID(request.ExpectedAttempt) || secret && (request.Component == "" || request.SecretName == "") || !secret && request.SecretName != "" {
		return response, problem("invalid_request", "fixture discovery requires an instance; secret access requires a component and fixture name")
	}
	err := s.store.db.View(func(tx *bolt.Tx) error {
		record, err := load(tx, request.InstanceID)
		if err != nil {
			return err
		}
		instance := record.Instance
		if request.ExpectedAttempt != "" {
			if instance.Plan.Lifetime != v1.Persistent || instance.Status != v1.RuntimeReady || instance.Execution == nil || instance.Execution.Attempt != request.ExpectedAttempt {
				return problem("fixture_unavailable", "selected startup attempt is no longer ready")
			}
			response.InstanceID, response.Attempt = instance.ID, instance.Execution.Attempt
		}
		if instance.Execution == nil {
			return nil
		}
		for _, fixture := range instance.Execution.Fixtures {
			if request.Component != "" && fixture.Component != request.Component {
				continue
			}
			if secret {
				if fixture.Name != request.SecretName || !fixture.Sensitive {
					continue
				}
				var secrets map[string]v1.PlannedValue
				if err := json.Unmarshal(tx.Bucket([]byte("secrets")).Get([]byte(request.InstanceID)), &secrets); err != nil {
					return problem("fixture_unavailable", "sensitive fixture value is unavailable")
				}
				value := secrets[fixture.Component+"/fixture/"+fixture.Name+"/resolved"]
				if value.Literal == nil {
					return problem("fixture_unavailable", "sensitive fixture value is unavailable")
				}
				fixture.Value = *value.Literal
			}
			response.Fixtures = append(response.Fixtures, fixture)
		}
		if secret && len(response.Fixtures) != 1 {
			return problem("fixture_unavailable", "named sensitive fixture is unavailable")
		}
		return nil
	})
	if err != nil {
		return v1.FixturesResponse{}, err
	}
	return response, nil
}
