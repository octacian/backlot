package daemon

import (
	v1 "github.com/octacian/backlot/api/v1"
	bolt "go.etcd.io/bbolt"
)

// validateStateAuthority is the bounded pre-effect authority preflight. Domain
// decoders retain historical gaps; this does not validate historical plan graphs,
// current files, providers or project registration. Never resave decoded values.
func validateStateAuthority(tx *bolt.Tx) error {
	checkResult := func(result *v1.ExecutionResult) error {
		if result == nil {
			return nil
		}
		for _, r := range result.NativeCleanup {
			if err := r.Validate(); err != nil || r.Attempt != result.Attempt || !hasComponent(result.Components, r.Component) {
				return problem("state_corrupt", "invalid native cleanup observation or release; preserve state with matching binary")
			}
		}
		return nil
	}
	checkHistory := func(history []nativeAbandonment) error {
		for _, a := range history {
			if err := checkResult(a.Result); err != nil {
				return err
			}
		}
		return nil
	}
	if err := tx.Bucket([]byte("instances")).ForEach(func(key, data []byte) error {
		var r record
		if err := decodeRecord(data, string(key), &r); err != nil {
			return err
		}
		if err := checkResult(r.Instance.Execution); err != nil {
			return err
		}
		return checkHistory(r.NativeAbandonments)
	}); err != nil {
		return err
	}
	if err := tx.Bucket([]byte("runtime")).ForEach(func(_, data []byte) error {
		r, err := decodeRuntime(data)
		if err != nil {
			return err
		}
		if r.LaunchPurpose != "" && r.LaunchPurpose != "application" {
			return problem("state_corrupt", "unknown native launch purpose; preserve state with matching binary")
		}
		return nil
	}); err != nil {
		return err
	}
	// Resource ownership is decoded before route/native cleanup can run. Orphaned
	// authority-bearing journals must not silently disappear from recovery selection.
	for _, name := range []string{"runtime", "resources"} {
		if err := tx.Bucket([]byte(name)).ForEach(func(key, data []byte) error {
			var parent record
			if err := decodeRecord(tx.Bucket([]byte("instances")).Get(key), string(key), &parent); err != nil {
				return err
			}
			if name == "resources" {
				_, err := decodeResources(data)
				return err
			}
			return nil
		}); err != nil {
			return err
		}
	}
	return tx.Bucket([]byte("meta")).ForEach(func(key, data []byte) error {
		if string(key) == "managed_gateway" {
			r, err := decodeManagedRecord(data)
			if err != nil {
				return err
			}
			return checkHistory(r.NativeAbandonments)
		}
		const prefix = "doctor_probe/"
		if len(key) >= len(prefix) && string(key[:len(prefix)]) == prefix {
			_, err := decodeDiagnosticProbe(string(key[len(prefix):]), data)
			return err
		}
		// json.Unmarshal above checks nested native identities through their decoder.
		return nil
	})
}
