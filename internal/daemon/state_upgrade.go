package daemon

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	v1 "github.com/octacian/backlot/api/v1"
	"github.com/octacian/backlot/internal/plan"
	bolt "go.etcd.io/bbolt"
)

type stateMigration struct {
	from, to int
	apply    func(*bolt.Tx) error
}

// Retain each real step from v9 onward. Source checks belong to the step,
// before conversion; current layout/authority checks run only after the chain.
var stateMigrations = []stateMigration{
	{6, 9, validateLegacyState},
	{8, 9, validateLegacyState},
}

func unsupportedStateVersion(target int) error {
	return problem("state_version", fmt.Sprintf("unsupported state format; this binary requires version %d; no supported forward migration", target))
}

func migrateState(tx *bolt.Tx, version, target int, steps []stateMigration) error {
	for version != target {
		if version > target {
			return unsupportedStateVersion(target)
		}
		var next *stateMigration
		for i := range steps {
			if steps[i].from == version {
				next = &steps[i]
				break
			}
		}
		if next == nil || next.to <= version || next.to > target {
			return unsupportedStateVersion(target)
		}
		if err := next.apply(tx); err != nil {
			return err
		}
		version = next.to
	}
	return nil
}

func requireStateBuckets(tx *bolt.Tx) error {
	for _, name := range bucketNames {
		if tx.Bucket([]byte(name)) == nil {
			return problem("state_corrupt", "private state bucket missing")
		}
	}
	return nil
}

func validateLegacyState(tx *bolt.Tx) error {
	if err := requireStateBuckets(tx); err != nil {
		return err
	}
	if err := validateStateAuthority(tx, string(tx.Bucket([]byte("meta")).Get([]byte("version")))); err != nil {
		return err
	}
	return validateStateSix(tx)
}

// validateStateSix reuses the recorded readers and checks only addressing links
// that cleanup/recovery depend on. Historical uncertainty belongs to recovery:
// boot facts, attempt equality and external capabilities are not migration proof.
// No decoded defaults or historical records are written back.
func validateStateSix(tx *bolt.Tx) error {
	corrupt := func() error {
		return problem("state_corrupt", "invalid predecessor record or ownership link; state was not modified")
	}
	instances := tx.Bucket([]byte("instances"))
	checkouts := tx.Bucket([]byte("checkouts"))
	paths := tx.Bucket([]byte("paths"))
	snapshots := tx.Bucket([]byte("snapshots"))
	secrets := tx.Bucket([]byte("secrets"))
	if err := instances.ForEach(func(key, data []byte) error {
		var r record
		if err := decodeRecord(data, string(key), &r); err != nil {
			return err
		}
		var checkout v1.CheckoutIdentity
		if json.Unmarshal(checkouts.Get([]byte(r.Instance.Checkout.ID)), &checkout) != nil || checkout.ID != r.Instance.Checkout.ID || checkout.Path != r.Instance.Checkout.Path || checkout.GitDirectory != r.Instance.Checkout.GitDirectory {
			return corrupt()
		}
		// beginSelected records every live persistent slot atomically. Only destroy
		// removes it; destroyed history may be slotless or retain its crash-gap slot.
		i := r.Instance
		if i.Plan.Lifetime == v1.Persistent && i.Status != v1.Destroyed && string(tx.Bucket([]byte("persistent")).Get([]byte(sceneSlot(i)))) != i.ID {
			return corrupt()
		}
		if r.Instance.Operation.Allocations[0].Phase == "effect" && (snapshots.Get(key) == nil || secrets.Get(key) == nil) {
			return corrupt()
		}
		return nil
	}); err != nil {
		return err
	}
	if err := checkouts.ForEach(func(key, data []byte) error {
		var c v1.CheckoutIdentity
		if !jsonObject(data) || json.Unmarshal(data, &c) != nil || !validID(c.ID) || string(key) != c.ID || !filepath.IsAbs(c.Path) || string(paths.Get([]byte(c.Path))) != c.ID {
			return corrupt()
		}
		return nil
	}); err != nil {
		return err
	}
	if err := paths.ForEach(func(key, data []byte) error {
		var c v1.CheckoutIdentity
		if json.Unmarshal(checkouts.Get(data), &c) != nil || c.ID != string(data) || c.Path != string(key) {
			return corrupt()
		}
		return nil
	}); err != nil {
		return err
	}
	if err := tx.Bucket([]byte("persistent")).ForEach(func(key, data []byte) error {
		var r record
		if err := decodeRecord(instances.Get(data), string(data), &r); err != nil {
			return corrupt()
		}
		i := r.Instance
		if i.Plan.Lifetime != v1.Persistent || string(key) != i.Plan.Project+"/"+i.Checkout.ID+"/"+i.Plan.Scene {
			return corrupt()
		}
		return nil
	}); err != nil {
		return err
	}
	for _, name := range []string{"snapshots", "secrets", "runtime", "resources"} {
		if err := tx.Bucket([]byte(name)).ForEach(func(key, data []byte) error {
			var r record
			if !jsonObject(data) || decodeRecord(instances.Get(key), string(key), &r) != nil {
				return corrupt()
			}
			switch name {
			case "snapshots":
				var snap plan.Snapshot
				if json.Unmarshal(data, &snap) != nil {
					return corrupt()
				}
				a, b := snap.Plan, r.Instance.Plan
				if a.APIVersion != b.APIVersion || a.Project != b.Project || a.Scene != b.Scene || a.Checkout != b.Checkout || a.ManifestPath != b.ManifestPath || a.Lifetime != b.Lifetime || r.Instance.Operation.Allocations[0].Phase != "effect" || secrets.Get(key) == nil {
					return corrupt()
				}
			case "secrets":
				var values map[string]v1.PlannedValue
				if json.Unmarshal(data, &values) != nil || snapshots.Get(key) == nil {
					return corrupt()
				}
			case "runtime":
				if _, err := decodeRuntime(data); err != nil {
					return err
				}
			case "resources":
				if _, err := decodeResources(data); err != nil {
					return err
				}
			}
			return nil
		}); err != nil {
			return err
		}
	}
	return tx.Bucket([]byte("meta")).ForEach(func(key, data []byte) error {
		switch {
		case string(key) == "managed_gateway":
			_, err := decodeManagedRecord(data)
			return err
		case strings.HasPrefix(string(key), "doctor_probe/"):
			_, err := decodeDiagnosticProbe(strings.TrimPrefix(string(key), "doctor_probe/"), data)
			return err
		}
		return nil
	})
}
