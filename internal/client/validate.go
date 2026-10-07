package client

import (
	"encoding/hex"
	"path/filepath"
	"time"

	v1 "github.com/octacian/backlot/api/v1"
)

func responseError() error {
	return &v1.PlanError{Code: "invalid_response", Message: "daemon response contains invalid identity, status, lifetime or lease fields"}
}
func validID(id string) bool {
	if len(id) != 64 {
		return false
	}
	_, err := hex.DecodeString(id)
	return err == nil
}
func validateInstance(response v1.InstanceResponse) error {
	instance := response.Instance
	if !validID(instance.ID) || !validID(instance.Checkout.ID) || !validID(instance.Operation.ID) || !filepath.IsAbs(instance.Checkout.Path) || instance.Plan.APIVersion != v1.Version || instance.Plan.Project == "" || instance.Plan.Scene == "" {
		return responseError()
	}
	if _, err := time.Parse(time.RFC3339Nano, instance.CreatedAt); err != nil {
		return responseError()
	}
	switch instance.Status {
	case v1.Preparing, v1.Prepared, v1.Cancelled, v1.Interrupted:
	default:
		return responseError()
	}
	if instance.Operation.Status != instance.Status || len(instance.Operation.Allocations) != 1 {
		return responseError()
	}
	allocation := instance.Operation.Allocations[0]
	if !validID(allocation.ID) || !validID(allocation.OwnershipToken) || allocation.Kind != "private_snapshot" || (allocation.Phase != "intent" && allocation.Phase != "effect") {
		return responseError()
	}
	if instance.Status == v1.Prepared && allocation.Phase != "effect" {
		return responseError()
	}
	if response.LeaseToken != "" && !validID(response.LeaseToken) {
		return responseError()
	}
	switch instance.Plan.Lifetime {
	case v1.Persistent:
		if response.LeaseToken != "" || instance.LeaseExpiresAt != "" {
			return responseError()
		}
	case v1.Disposable:
		active := instance.Status == v1.Prepared || instance.Status == v1.Preparing
		if active {
			if _, err := time.Parse(time.RFC3339Nano, instance.LeaseExpiresAt); err != nil {
				return responseError()
			}
		} else if instance.LeaseExpiresAt != "" || response.LeaseToken != "" {
			return responseError()
		}
	default:
		return responseError()
	}
	return nil
}
