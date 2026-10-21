package daemon

import (
	"context"
	"errors"

	"github.com/containerd/errdefs"
	"github.com/moby/moby/client"
	v1 "github.com/octacian/backlot/api/v1"
	"github.com/octacian/backlot/internal/manager"
	bolt "go.etcd.io/bbolt"
)

// verifySharedResource waives only labels on an acknowledged recorded handle.
// Unknown container/network identities still require their original capability.
func verifySharedResource(ctx context.Context, d *dockerEngine, r ownedResource, force bool) error {
	if !force || r.ID == "" {
		return verifyDockerResource(ctx, d, r)
	}
	switch r.Kind {
	case "container":
		_, err := d.ContainerInspect(ctx, r.ID, client.ContainerInspectOptions{})
		return err
	case "network":
		_, err := d.NetworkInspect(ctx, r.ID, client.NetworkInspectOptions{})
		return err
	case "volume":
		_, err := d.VolumeInspect(ctx, r.ID, client.VolumeInspectOptions{})
		return err
	}
	return problem("state_corrupt", "invalid shared target kind")
}

// sharedStorageAbsent observes both effects even if old acknowledgments say removed.
// Missing container alone cannot establish absence of SQL data on a retained volume.
func sharedStorageAbsent(ctx context.Context, d *dockerEngine, record sharedServiceRecord) (bool, error) {
	_, serverErr := d.ContainerInspect(ctx, resourceHandle(record.Server), client.ContainerInspectOptions{})
	_, volumeErr := d.VolumeInspect(ctx, resourceHandle(record.Volume), client.VolumeInspectOptions{})
	for _, err := range []error{serverErr, volumeErr} {
		if err != nil && !errdefs.IsNotFound(err) {
			return false, sharedProviderError(err)
		}
	}
	return errdefs.IsNotFound(serverErr) && errdefs.IsNotFound(volumeErr), nil
}

func (s *store) releaseSharedAllocation(ctx context.Context, record *sharedServiceRecord, allocation sharedAllocation, force bool) (v1.ManagerResult, error) {
	request := allocation.Request
	request.Operation = "release"
	request.Force = force // Copy only: retained intent never gains force authority.
	result := v1.ManagerResult{Version: request.Version, AllocationID: request.AllocationID, Ownership: "uncertain", Effect: "partial"}
	if force {
		d, err := newDocker(record.Endpoint)
		if err != nil {
			return result, err
		}
		absent, err := sharedStorageAbsent(ctx, d, *record)
		_ = d.Close()
		if err != nil {
			return result, err
		}
		if absent {
			result.Ownership = "absent"
			result.Effect = "released"
			return result, nil
		}
	}
	if err := s.startSharedMode(ctx, record, force); err != nil {
		return result, err
	}
	db, err := sharedDB(*record)
	if err != nil {
		return result, err
	}
	defer func() { _ = db.Close() }()
	return manager.MariaDB(ctx, db, request, s.sharedPoint)
}

// checkSharedReleaseCollision protects known siblings and peers even when the
// operator waives ownership proof. All allocation-map writers hold this service lock.
func (s *store) checkSharedReleaseCollision(instanceID, name string, target sharedAllocation) error {
	return s.db.View(func(tx *bolt.Tx) error {
		return tx.Bucket([]byte("shared_allocations")).ForEach(func(key, data []byte) error {
			allocations, err := decodeSharedAllocations(data)
			if err != nil {
				return err
			}
			for otherName, a := range allocations {
				if string(key) == instanceID && otherName == name || a.Released || a.ServiceID != target.ServiceID {
					continue
				}
				if a.Request.Database == target.Request.Database || a.Request.Username == target.Request.Username {
					return problem("shared_in_use", "recorded SQL target overlaps another retained allocation; preserved")
				}
			}
			return nil
		})
	})
}

// stopForcedShared targets only the recorded container. It never authenticates
// native processes by PID and never detaches or removes foreign consumers.
func (s *store) stopForcedShared(ctx context.Context, record *sharedServiceRecord) (resultErr error) {
	defer func() { resultErr = s.sharedOperationResult(record, resultErr) }()
	d, err := newDocker(record.Endpoint)
	if err != nil {
		return err
	}
	defer func() { _ = d.Close() }()
	if err = verifySharedResource(ctx, d, record.Server, true); errdefs.IsNotFound(err) {
		return nil
	} else if err != nil {
		return sharedProviderError(err)
	}
	handle := resourceHandle(record.Server)
	// Capability-authenticate an unacknowledged effect before using its immutable ID.
	if record.Server.ID == "" {
		record.Server.ID, err = recoverResourceEffect(ctx, d, record.Server)
		if err != nil {
			return sharedProviderError(err)
		}
		if err = s.saveShared(*record); err != nil {
			return err
		}
		handle = record.Server.ID
	}
	if err = s.sharedPoint("stop-server"); err != nil {
		return err
	}
	seconds := 10
	if _, err = d.ContainerStop(ctx, handle, client.ContainerStopOptions{Timeout: &seconds}); err != nil && !errdefs.IsNotFound(err) {
		return sharedProviderError(err)
	}
	out, err := d.ContainerInspect(ctx, handle, client.ContainerInspectOptions{})
	if errdefs.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return sharedProviderError(err)
	}
	if out.Container.State == nil || out.Container.State.Running {
		return problem("cleanup_failed", "shared server remains live; obligation retained")
	}
	return s.saveShared(*record)
}

// removeForcedSharedResource never forces provider consumer checks or implicitly
// deletes mounts from a drifted server. Only the recorded resource is removed.
func removeForcedSharedResource(ctx context.Context, d *dockerEngine, r ownedResource) error {
	if err := verifySharedResource(ctx, d, r, true); errdefs.IsNotFound(err) {
		return nil
	} else if err != nil {
		return err
	}
	if r.ID == "" {
		var err error
		r.ID, err = recoverResourceEffect(ctx, d, r)
		if err != nil {
			return err
		}
	}
	var err error
	switch r.Kind {
	case "container":
		out, inspectErr := d.ContainerInspect(ctx, r.ID, client.ContainerInspectOptions{})
		if errdefs.IsNotFound(inspectErr) {
			return nil
		}
		if inspectErr != nil {
			return inspectErr
		}
		if out.Container.State == nil || out.Container.State.Running {
			return problem("cleanup_failed", "shared container is not stopped; preserved")
		}
		_, err = d.ContainerRemove(ctx, r.ID, client.ContainerRemoveOptions{})
	case "volume", "network":
		// Includes stopped foreign consumers; only the already removed server is absent.
		if err = d.noConsumers(ctx, resourceGeneration{Resources: map[string]ownedResource{"target": r}}); err != nil {
			return err
		}
		if r.Kind == "volume" {
			_, err = d.VolumeRemove(ctx, r.ID, client.VolumeRemoveOptions{})
		} else {
			_, err = d.NetworkRemove(ctx, r.ID, client.NetworkRemoveOptions{})
		}
	default:
		return errors.New("invalid shared removal target")
	}
	if errdefs.IsNotFound(err) {
		return nil
	}
	return err
}
