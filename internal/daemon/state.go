package daemon

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"time"

	v1 "github.com/octacian/backlot/api/v1"
	"github.com/octacian/backlot/internal/localipc"
	"github.com/octacian/backlot/internal/plan"
	bolt "go.etcd.io/bbolt"
)

var bucketNames = []string{"meta", "instances", "checkouts", "paths", "persistent", "snapshots", "secrets", "runtime", "resources"}

type record struct {
	Instance  v1.Instance `json:"instance"`
	LeaseHash string      `json:"lease_hash,omitempty"`
}

type store struct {
	db        *bolt.DB
	directory string
}

func openStore(directory string) (*store, error) {
	path := filepath.Join(directory, "state.db")
	if _, err := os.Lstat(path); err == nil {
		if err := localipc.File(path, false); err != nil {
			return nil, err
		}
	} else if !os.IsNotExist(err) {
		return nil, problem("state_unavailable", "cannot inspect state database")
	}
	db, err := bolt.Open(path, 0600, &bolt.Options{Timeout: time.Second})
	if err != nil {
		return nil, problem("state_unavailable", "cannot open state database; check permissions or another daemon")
	}
	s := &store{db: db, directory: directory}
	err = db.Update(func(tx *bolt.Tx) error {
		meta := tx.Bucket([]byte("meta"))
		if meta != nil {
			version := meta.Get([]byte("version"))
			if string(version) != strconv.Itoa(v1.StateVersion) {
				return problem("state_version", "unsupported state format; use a compatible binary; state was not modified")
			}
		} else {
			// Existing buckets without a version are not safe to adopt.
			if err := tx.ForEach(func(_ []byte, _ *bolt.Bucket) error {
				return problem("state_version", "state format is missing; state was not modified")
			}); err != nil {
				return err
			}
		}
		for _, name := range bucketNames {
			if _, err := tx.CreateBucketIfNotExists([]byte(name)); err != nil {
				return err
			}
		}
		return tx.Bucket([]byte("meta")).Put([]byte("version"), []byte(strconv.Itoa(v1.StateVersion)))
	})
	if err != nil {
		return nil, errors.Join(err, db.Close())
	}
	return s, nil
}

func put(tx *bolt.Tx, bucket, key string, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return tx.Bucket([]byte(bucket)).Put([]byte(key), data)
}
func load(tx *bolt.Tx, id string) (record, error) {
	var r record
	data := tx.Bucket([]byte("instances")).Get([]byte(id))
	if data == nil {
		return r, problem("not_found", "instance not found; use its recorded instance ID")
	}
	if err := decodeRecord(data, id, &r); err != nil {
		return r, err
	}
	return r, nil
}

func decodeRecord(data []byte, id string, r *record) error {
	if err := json.Unmarshal(data, r); err != nil {
		return problem("state_corrupt", "cannot decode instance record; preserve state for diagnosis")
	}
	i := r.Instance
	if i.ID != id || !validID(i.ID) || !validID(i.Operation.ID) || !validID(i.Checkout.ID) || len(i.Operation.Allocations) != 1 || i.Plan.APIVersion != v1.Version || (i.Plan.Lifetime != v1.Persistent && i.Plan.Lifetime != v1.Disposable) || i.Operation.Status != i.Status {
		return problem("state_corrupt", "invalid instance identity or operation record; preserve state for diagnosis")
	}
	switch i.Status {
	case v1.Preparing, v1.Prepared, v1.Cancelled, v1.Interrupted, v1.Starting, v1.RuntimeReady, v1.Stopping, v1.Stopped, v1.Destroyed, v1.Succeeded, v1.Failed:
	default:
		return problem("state_corrupt", "invalid instance status; preserve state for diagnosis")
	}
	allocation := i.Operation.Allocations[0]
	if !validID(allocation.ID) || !validID(allocation.OwnershipToken) || allocation.Kind != "private_snapshot" || (allocation.Phase != "intent" && allocation.Phase != "effect") {
		return problem("state_corrupt", "invalid snapshot ownership record; preserve state for diagnosis")
	}
	return nil
}

func (s *store) begin(snapshot plan.Snapshot, checkout v1.CheckoutIdentity, lease time.Duration) (v1.InstanceResponse, bool, error) {
	response := v1.InstanceResponse{APIVersion: v1.Version}
	created := false
	err := s.db.Update(func(tx *bolt.Tx) error {
		paths := tx.Bucket([]byte("paths"))
		if id := paths.Get([]byte(checkout.Path)); id != nil && string(id) != checkout.ID {
			return problem("checkout_replaced", "checkout path now identifies different files; old records preserved; use a separate checkout")
		}
		checkouts := tx.Bucket([]byte("checkouts"))
		if data := checkouts.Get([]byte(checkout.ID)); data != nil {
			var previous v1.CheckoutIdentity
			if err := json.Unmarshal(data, &previous); err != nil {
				return problem("state_corrupt", "cannot decode checkout record")
			}
			if previous.Path != checkout.Path || previous.GitDirectory != checkout.GitDirectory {
				return problem("checkout_moved", "checkout moved; old records preserved; use a separate checkout")
			}
		}
		p := snapshot.Plan
		key := p.Project + "/" + checkout.ID + "/" + p.Scene
		if p.Lifetime == v1.Persistent {
			if id := tx.Bucket([]byte("persistent")).Get([]byte(key)); id != nil {
				r, err := load(tx, string(id))
				if err != nil {
					return err
				}
				response.Instance = r.Instance
				response.ManifestDrift = r.Instance.Plan.ManifestDigest != p.ManifestDigest
				response.ConfigDrift = r.Instance.Plan.ConfigDigest != p.ConfigDigest
				return nil
			}
		}
		now := time.Now().UTC()
		id := newID()
		instance := v1.Instance{ID: id, Checkout: checkout, Status: v1.Preparing, Plan: p, CreatedAt: now.Format(time.RFC3339Nano), Operation: v1.Operation{ID: newID(), Status: v1.Preparing, Allocations: []v1.AllocationRecord{{ID: newID(), Kind: "private_snapshot", OwnershipToken: newID(), Phase: "intent"}}}}
		r := record{Instance: instance}
		if p.Lifetime == v1.Disposable {
			response.LeaseToken = newID()
			r.LeaseHash = hash(response.LeaseToken)
			r.Instance.LeaseExpiresAt = now.Add(lease).Format(time.RFC3339Nano)
		}
		if err := put(tx, "instances", id, r); err != nil {
			return err
		}
		if err := put(tx, "checkouts", checkout.ID, checkout); err != nil {
			return err
		}
		if err := paths.Put([]byte(checkout.Path), []byte(checkout.ID)); err != nil {
			return err
		}
		if p.Lifetime == v1.Persistent {
			if err := tx.Bucket([]byte("persistent")).Put([]byte(key), []byte(id)); err != nil {
				return err
			}
		}
		response.Instance = r.Instance
		created = true
		return nil
	})
	return response, created, err
}

// effect atomically records the private snapshot and its ownership effect. It must
// follow committed intent; completion is separate so both interruption gaps recover.
func (s *store) effect(id string, snapshot plan.Snapshot) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		r, err := load(tx, id)
		if err != nil {
			return err
		}
		if r.Instance.Status != v1.Preparing || tx.Bucket([]byte("snapshots")).Get([]byte(id)) != nil {
			return problem("conflict", "snapshot already recorded or preparation interrupted")
		}
		if err := put(tx, "snapshots", id, snapshot); err != nil {
			return err
		}
		if err := put(tx, "secrets", id, snapshot.Secrets); err != nil {
			return err
		}
		r.Instance.Operation.Allocations[0].Phase = "effect"
		return put(tx, "instances", id, r)
	})
}

func (s *store) complete(id string) (v1.Instance, error) {
	var instance v1.Instance
	err := s.db.Update(func(tx *bolt.Tx) error {
		r, err := load(tx, id)
		if err != nil {
			return err
		}
		if r.Instance.Status != v1.Preparing || r.Instance.Operation.Allocations[0].Phase != "effect" {
			return problem("conflict", "preparation is not eligible to complete")
		}
		r.Instance.Status = v1.Prepared
		r.Instance.Operation.Status = v1.Prepared
		instance = r.Instance
		return put(tx, "instances", id, r)
	})
	return instance, err
}
func (s *store) inspect(id string) (v1.Instance, error) {
	var instance v1.Instance
	err := s.db.View(func(tx *bolt.Tx) error { r, err := load(tx, id); instance = r.Instance; return err })
	return instance, err
}
func terminal(r *record, status v1.InstanceStatus, reason string) {
	r.Instance.Status = status
	r.Instance.Operation.Status = status
	r.Instance.Operation.Reason = reason
	r.Instance.LeaseExpiresAt = ""
	r.LeaseHash = ""
}
func active(status v1.InstanceStatus) bool {
	return status == v1.Preparing || status == v1.Prepared || status == v1.Starting || status == v1.RuntimeReady || status == v1.Stopping
}

func (s *store) cancel(id string) (v1.Instance, error) {
	var instance v1.Instance
	err := s.db.Update(func(tx *bolt.Tx) error {
		r, err := load(tx, id)
		if err != nil {
			return err
		}
		if active(r.Instance.Status) {
			terminal(&r, v1.Cancelled, "explicit cancellation")
		}
		instance = r.Instance
		return put(tx, "instances", id, r)
	})
	return instance, err
}
func (s *store) renew(id, token string, lease time.Duration) (v1.Instance, error) {
	var instance v1.Instance
	err := s.db.Update(func(tx *bolt.Tx) error {
		r, err := load(tx, id)
		if err != nil {
			return err
		}
		if r.Instance.Plan.Lifetime != v1.Disposable {
			return problem("invalid_lease", "persistent preparation has no client lease")
		}
		if subtle.ConstantTimeCompare([]byte(hash(token)), []byte(r.LeaseHash)) != 1 {
			return problem("invalid_lease", "invalid disposable lease capability")
		}
		now := time.Now().UTC()
		expiry, err := time.Parse(time.RFC3339Nano, r.Instance.LeaseExpiresAt)
		if err != nil || !active(r.Instance.Status) {
			return problem("conflict", "preparation is no longer active")
		}
		if r.Instance.Status == v1.Stopping {
			return problem("conflict", "execution is stopping; its lease cannot be renewed")
		}
		if !expiry.After(now) {
			if r.Instance.Execution != nil {
				return problem("lease_expired", "client lease expired; cancellation is pending verified cleanup")
			}
			terminal(&r, v1.Cancelled, "client lease expired")
			instance = r.Instance
			return put(tx, "instances", id, r)
		}
		r.Instance.LeaseExpiresAt = now.Add(lease).Format(time.RFC3339Nano)
		instance = r.Instance
		return put(tx, "instances", id, r)
	})
	if err == nil && instance.Status == v1.Cancelled {
		return instance, problem("lease_expired", "client lease expired; prepare a new disposable instance")
	}
	return instance, err
}

func (s *store) reconcile(reason string, expiryOnly bool) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		bucket := tx.Bucket([]byte("instances"))
		// Replace values only after traversal; bbolt cursor mutation during iteration can skip keys.
		changes := map[string]record{}
		err := bucket.ForEach(func(key, data []byte) error {
			var r record
			if err := decodeRecord(data, string(key), &r); err != nil {
				return err
			}
			if !active(r.Instance.Status) {
				return nil
			}
			if expiryOnly {
				if r.Instance.Execution != nil {
					return nil
				} // Native leases cancel through the lifecycle owner and join before terminal state.
				if r.Instance.Plan.Lifetime != v1.Disposable {
					return nil
				}
				expiry, err := time.Parse(time.RFC3339Nano, r.Instance.LeaseExpiresAt)
				if err != nil {
					return problem("state_corrupt", "invalid lease deadline")
				}
				if expiry.After(time.Now()) {
					return nil
				}
				terminal(&r, v1.Cancelled, "client lease expired")
			} else {
				if r.Instance.Status == v1.Prepared && r.Instance.Plan.Lifetime == v1.Persistent {
					return nil
				}
				terminal(&r, v1.Interrupted, reason)
			}
			changes[string(key)] = r
			return nil
		})
		if err != nil {
			return err
		}
		for id, r := range changes {
			if err := put(tx, "instances", id, r); err != nil {
				return err
			}
		}
		return nil
	})
}
