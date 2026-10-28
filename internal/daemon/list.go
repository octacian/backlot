package daemon

import (
	"bytes"
	"os"
	"path/filepath"

	v1 "github.com/octacian/backlot/api/v1"
	"github.com/octacian/backlot/internal/plan"
	bolt "go.etcd.io/bbolt"
)

func (s *store) list(request v1.ListRequest) (v1.ListResponse, error) {
	return s.filteredList(v1.FilteredListRequest{APIVersion: v1.Version, AfterID: request.AfterID, Limit: request.Limit})
}

func (s *store) filteredList(request v1.FilteredListRequest) (v1.ListResponse, error) {
	result := v1.ListResponse{APIVersion: v1.Version, Instances: []v1.InstanceSummary{}}
	if err := request.Validate(); err != nil {
		return result, err
	}
	var checkout *v1.CheckoutIdentity
	project := ""
	if request.ProjectPath != "" || request.CheckoutPath != "" {
		path := request.CheckoutPath
		if request.ProjectPath != "" {
			var err error
			project, err = sceneDirectory(request.ProjectPath)
			if err != nil {
				return result, err
			}
			path = plan.CheckoutRoot(project)
		} else {
			if info, err := os.Stat(path); err != nil || !info.IsDir() {
				return result, problem("checkout_identity", "checkout location must be an existing directory")
			}
			var err error
			path, err = sceneDirectory(path)
			if err != nil {
				return result, err
			}
		}
		current, err := identify(path)
		if err != nil {
			return result, err
		}
		checkout = &current
	}
	limit := request.Limit
	if limit == 0 {
		limit = 100
	}
	err := s.db.View(func(tx *bolt.Tx) error {
		if checkout != nil {
			if err := verifyRecordedCheckout(tx, *checkout); err != nil {
				return err
			}
			if _, err := storedSceneInstances(tx); err != nil {
				return err
			}
		}
		cursor := tx.Bucket([]byte("instances")).Cursor()
		key, data := cursor.First()
		if request.AfterID != "" {
			key, data = cursor.Seek([]byte(request.AfterID))
			if bytes.Equal(key, []byte(request.AfterID)) {
				key, data = cursor.Next()
			}
		}
		for ; key != nil; key, data = cursor.Next() {
			var r record
			if err := decodeRecord(data, string(key), &r); err != nil {
				return err
			}
			i := r.Instance
			if checkout != nil {
				if err := validateSceneRecord(i); err != nil {
					return err
				}
				if i.Checkout.Path != checkout.Path {
					continue
				}
				if !sameCheckout(&i.Checkout, checkout) {
					return problem("checkout_changed", "recorded checkout identity changed; use explicit recorded ID")
				}
				if project != "" && filepath.Dir(i.Plan.ManifestPath) != project {
					continue
				}
			}
			if request.ProjectID != "" && request.ProjectID != i.Plan.Project || request.Scene != "" && request.Scene != i.Plan.Scene || request.Lifetime != "" && request.Lifetime != i.Plan.Lifetime || request.State != "" && request.State != i.Status {
				continue
			}
			if len(result.Instances) == limit {
				result.NextAfterID = result.Instances[len(result.Instances)-1].ID
				break
			}
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
