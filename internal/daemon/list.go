package daemon

import (
	"bytes"

	v1 "github.com/octacian/backlot/api/v1"
	bolt "go.etcd.io/bbolt"
)

func (s *store) list(request v1.ListRequest) (v1.ListResponse, error) {
	result := v1.ListResponse{APIVersion: v1.Version, Instances: []v1.InstanceSummary{}}
	if request.AfterID != "" && !validID(request.AfterID) || request.Limit < 0 || request.Limit > 100 {
		return result, problem("invalid_request", "after_id must be a recorded-format ID and limit must be 0 through 100")
	}
	limit := request.Limit
	if limit == 0 {
		limit = 100
	}
	err := s.db.View(func(tx *bolt.Tx) error {
		cursor := tx.Bucket([]byte("instances")).Cursor()
		key, data := cursor.First()
		if request.AfterID != "" {
			key, data = cursor.Seek([]byte(request.AfterID))
			if bytes.Equal(key, []byte(request.AfterID)) {
				key, data = cursor.Next()
			}
		}
		for ; key != nil; key, data = cursor.Next() {
			if len(result.Instances) == limit {
				result.NextAfterID = result.Instances[len(result.Instances)-1].ID
				break
			}
			var r record
			if err := decodeRecord(data, string(key), &r); err != nil {
				return err
			}
			i := r.Instance
			summary := v1.InstanceSummary{ID: i.ID, Project: i.Plan.Project, Scene: i.Plan.Scene, Checkout: i.Checkout.Path, Status: i.Status, Lifetime: i.Plan.Lifetime, CreatedAt: i.CreatedAt}
			if i.Execution != nil {
				summary.Origin = i.Execution.Origin
			}
			result.Instances = append(result.Instances, summary)
		}
		return nil
	})
	return result, err
}

// publicInstance copies the allocation slice so redaction cannot alter durable
// recovery authority or a value still in use by an execution owner.
func publicInstance(instance v1.Instance) v1.Instance {
	instance.Operation.Allocations = append([]v1.AllocationRecord(nil), instance.Operation.Allocations...)
	for i := range instance.Operation.Allocations {
		instance.Operation.Allocations[i].OwnershipToken = ""
	}
	return instance
}
