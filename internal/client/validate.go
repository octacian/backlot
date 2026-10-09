package client

import (
	"encoding/hex"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
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
	case v1.Preparing, v1.Prepared, v1.Cancelled, v1.Interrupted, v1.Starting, v1.RuntimeReady, v1.Stopping, v1.Stopped, v1.Destroyed, v1.Succeeded, v1.Failed:
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
		active := instance.Status == v1.Prepared || instance.Status == v1.Preparing || instance.Status == v1.Starting || instance.Status == v1.RuntimeReady || instance.Status == v1.Stopping
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
	return validateExecution(instance)
}

func validateExecution(instance v1.Instance) error {
	result := instance.Execution
	switch instance.Status {
	case v1.Starting, v1.RuntimeReady, v1.Stopping, v1.Stopped, v1.Destroyed, v1.Succeeded, v1.Failed:
		if result == nil {
			return responseError()
		}
	}
	if result == nil {
		return nil
	}
	if result.TerminalExitCode != nil && (*result.TerminalExitCode < -1 || *result.TerminalExitCode > 255) {
		return responseError()
	}
	for _, timestamp := range []string{result.CompletedAt, result.EvidenceObservedAt} {
		if timestamp != "" {
			if _, err := time.Parse(time.RFC3339Nano, timestamp); err != nil {
				return responseError()
			}
		}
	}
	if !validID(result.Attempt) {
		return responseError()
	}
	if result.Origin != "" {
		origin, err := url.Parse(result.Origin)
		if err != nil || origin.Scheme != "https" || origin.User != nil || origin.Path != "" || origin.RawQuery != "" || origin.ForceQuery || origin.Fragment != "" || !strings.HasPrefix(origin.Hostname(), "bl-"+instance.ID[:40]+".") || origin.Port() == "" && strings.Contains(origin.Host, ":") {
			return responseError()
		}
		if origin.Port() != "" {
			port, err := strconv.Atoi(origin.Port())
			if err != nil || port < 1 || port > 65535 {
				return responseError()
			}
		}
	}
	if instance.Status == v1.RuntimeReady && instance.Plan.Publish != nil && result.Origin == "" {
		return responseError()
	}
	if result.FailureDetail != nil && (result.FailureDetail.Code == "" || result.FailureDetail.Message == "" || result.Failure == "") {
		return responseError()
	}
	for _, detail := range result.CleanupDetails {
		if detail.Code == "" || detail.Message == "" || result.CleanupFailure == "" {
			return responseError()
		}
	}
	seen := map[string]bool{}
	for _, component := range result.Components {
		if component.Name == "" || seen[component.Name] {
			return responseError()
		}
		seen[component.Name] = true
		switch component.Status {
		case "starting", "running", "ready", "completed", "stopped", "unknown", "skipped":
		default:
			return responseError()
		}
		if component.ExitCode != nil && (*component.ExitCode < -1 || *component.ExitCode > 255) {
			return responseError()
		}
	}
	for _, port := range result.Ports {
		if port < 1 || port > 65535 {
			return responseError()
		}
	}
	if instance.Status == v1.Succeeded && (result.Cancelled || result.Failure != "" || result.CleanupFailure != "" || result.CollectionFailure != "") {
		return responseError()
	}
	return nil
}
