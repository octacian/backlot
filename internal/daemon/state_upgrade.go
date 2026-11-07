package daemon

import (
	"encoding/json"
	"fmt"
	v1 "github.com/octacian/backlot/api/v1"

	bolt "go.etcd.io/bbolt"
)

type stateMigration struct {
	from, to int
	apply    func(*bolt.Tx) error
}

// Retain each real step from v9 onward. Source checks belong to the step,
// before conversion; current layout/authority checks run only after the chain.
var stateMigrations = []stateMigration{{9, 10, migrateStateNine}, {10, 11, migrateStateTen}, {11, 12, migrateStateEleven}, {12, 13, migrateStateTwelve}, {13, 14, migrateStateThirteen}, {14, 15, migrateStateFourteen}}

func migrateStateFourteen(tx *bolt.Tx) error {
	if err := validateStateAuthority(tx); err != nil {
		return err
	}
	// Legacy evidence remains unrecorded; enrollment is a separate source-validated mutation.
	for _, name := range bindingBuckets {
		if err := tx.Bucket([]byte(name)).ForEach(func(_, raw []byte) error {
			var b struct {
				Current struct {
					Registration *v1.RegistrationIdentity `json:"registration"`
				} `json:"current"`
			}
			if json.Unmarshal(raw, &b) != nil || b.Current.Registration != nil {
				return problem("state_corrupt", "predecessor cannot contain logical registration mappings")
			}
			return nil
		}); err != nil {
			return err
		}
	}
	return validateBindings(tx)
}

func migrateStateNine(tx *bolt.Tx) error {
	for _, name := range stateNineBuckets {
		if tx.Bucket([]byte(name)) == nil {
			return problem("state_corrupt", "source state bucket missing")
		}
	}
	if err := validateStateNineAuthority(tx); err != nil {
		return err
	}
	for _, name := range sharedBuckets {
		// CreateBucket rejects existing namespaces, including empty buckets.
		if _, err := tx.CreateBucket([]byte(name)); err != nil {
			return problem("state_corrupt", "shared state namespace already exists")
		}
	}
	return nil
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

// migrateStateTen adds the image-pin semantic fence without inventing historical pins.
func migrateStateTen(tx *bolt.Tx) error {
	for _, name := range stateElevenBuckets {
		if tx.Bucket([]byte(name)) == nil {
			return problem("state_corrupt", "source state bucket missing")
		}
	}
	if err := validateStateNineAuthority(tx); err != nil {
		return err
	}
	if err := validateSharedAuthority(tx); err != nil {
		return err
	}
	return tx.Bucket([]byte("resources")).ForEach(func(_, data []byte) error {
		var raw map[string]json.RawMessage
		if err := json.Unmarshal(data, &raw); err != nil {
			return err
		}
		if _, exists := raw["build_images"]; exists {
			return problem("state_corrupt", "source version 10 cannot contain build-image pins")
		}
		return nil
	})
}

// migrateStateThirteen fences dependent consumers; legacy snapshots gain no task captures.
func migrateStateThirteen(tx *bolt.Tx) error {
	if err := validateStateAuthority(tx); err != nil {
		return err
	}
	if err := tx.Bucket([]byte("snapshots")).ForEach(func(_, data []byte) error {
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(data, &fields); err != nil {
			return err
		}
		if _, exists := fields["tasks"]; exists {
			return problem("state_corrupt", "predecessor cannot contain accepted task captures")
		}
		return nil
	}); err != nil {
		return err
	}
	return tx.Bucket([]byte("instances")).ForEach(func(_, data []byte) error {
		var fields struct {
			Instance struct {
				Task json.RawMessage `json:"task"`
			} `json:"instance"`
		}
		if err := json.Unmarshal(data, &fields); err != nil {
			return err
		}
		if len(fields.Instance.Task) > 0 {
			return problem("state_corrupt", "predecessor state cannot contain dependent task authority")
		}
		return nil
	})
}
