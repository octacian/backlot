package daemon

import (
	"sort"

	v1 "github.com/octacian/backlot/api/v1"
	"github.com/octacian/backlot/internal/plan"
	bolt "go.etcd.io/bbolt"
)

func (s *service) captureFixtures(id string, snapshot plan.Snapshot, result *v1.ExecutionResult, entry *execution) error {
	for _, component := range snapshot.Plan.Components {
		for name, fixture := range component.Fixtures {
			key := component.Name + "/fixture/" + name
			text, err := resolvedValue(fixture.Value, key, id, snapshot, result, entry, v1.Native)
			if err != nil {
				return problem("fixture_failed", "declared fixture could not be resolved")
			}
			record := v1.FixtureRecord{Component: component.Name, Name: name, Description: fixture.Description, Sensitive: fixture.Value.Redacted}
			if record.Sensitive {
				snapshot.Secrets[key+"/resolved"] = v1.PlannedValue{Literal: &text}
			} else {
				record.Value = text
			}
			result.Fixtures = append(result.Fixtures, record)
		}
	}
	sort.Slice(result.Fixtures, func(i, j int) bool {
		a, b := result.Fixtures[i], result.Fixtures[j]
		return a.Component+"/"+a.Name < b.Component+"/"+b.Name
	})
	return s.store.db.Update(func(tx *bolt.Tx) error { return put(tx, "secrets", id, snapshot.Secrets) })
}
func (s *service) fixtures(request v1.FixturesRequest, secret bool) (v1.FixturesResponse, error) {
	response := v1.FixturesResponse{APIVersion: v1.Version, Fixtures: []v1.FixtureRecord{}}
	if !validID(request.InstanceID) || secret && (request.Component == "" || request.SecretName == "") || !secret && request.SecretName != "" {
		return response, problem("invalid_request", "fixture discovery requires an instance; secret access requires a component and fixture name")
	}
	instance, err := s.store.inspect(request.InstanceID)
	if err != nil {
		return response, err
	}
	if instance.Execution == nil {
		return response, nil
	}
	for _, fixture := range instance.Execution.Fixtures {
		if request.Component != "" && fixture.Component != request.Component {
			continue
		}
		if secret {
			if fixture.Name != request.SecretName || !fixture.Sensitive {
				continue
			}
			snapshot, err := s.store.snapshot(request.InstanceID)
			if err != nil {
				return response, err
			}
			value := snapshot.Secrets[fixture.Component+"/fixture/"+fixture.Name+"/resolved"]
			if value.Literal == nil {
				return response, problem("fixture_unavailable", "sensitive fixture value is unavailable")
			}
			fixture.Value = *value.Literal
		}
		response.Fixtures = append(response.Fixtures, fixture)
	}
	if secret && len(response.Fixtures) != 1 {
		return response, problem("fixture_unavailable", "named sensitive fixture is unavailable")
	}
	return response, nil
}
