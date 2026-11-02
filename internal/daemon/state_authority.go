package daemon

import (
	"bytes"
	"encoding/json"
	v1 "github.com/octacian/backlot/api/v1"
	"github.com/octacian/backlot/internal/manifest"
	bolt "go.etcd.io/bbolt"
	"path/filepath"
	"strings"
)

// validateStateAuthority is the bounded pre-effect authority preflight. Domain
// decoders retain historical gaps; this does not validate historical plan graphs,
// current files, providers or project registration. Never resave decoded values.
func validateStateAuthority(tx *bolt.Tx) error {
	if err := validateStateNineAuthority(tx); err != nil {
		return err
	}
	if err := validateSharedAuthority(tx); err != nil {
		return err
	}
	if err := validateHistoryDeletionState(tx); err != nil {
		return err
	}
	if err := validateAttemptState(tx); err != nil {
		return err
	}
	if err := validateBuildImageResults(tx); err != nil {
		return err
	}
	return tx.Bucket([]byte("resources")).ForEach(func(key, data []byte) error {
		g, err := decodeResources(data)
		if err != nil {
			return err
		}
		return validateBuildImagePins(string(key), g)
	})
}

// Version 9 has the same native/resource record shapes, without shared journals.
func validateStateNineAuthority(tx *bolt.Tx) error {
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

// Shared journals carry cleanup authority. Validate stored links without consulting
// current project files or opening another transaction during startup preflight.
func validateSharedAuthority(tx *bolt.Tx) error {
	projects := tx.Bucket([]byte("shared_projects"))
	paths := tx.Bucket([]byte("shared_paths"))
	decodeProject := func(key, data []byte) (sharedProjectIdentity, error) {
		var p sharedProjectIdentity
		if json.Unmarshal(data, &p) != nil || p.ID != string(key) || !validID(p.ID) || !filepath.IsAbs(p.Anchor) || p.Filesystem == "" || p.Relative == "" || filepath.IsAbs(p.Relative) || p.Relative == ".." || strings.HasPrefix(p.Relative, ".."+string(filepath.Separator)) || !manifest.ValidIdentifier(p.Project) || p.ID != hash(p.Filesystem+"/"+p.Relative+"/"+p.Project) {
			return p, problem("state_corrupt", "invalid shared project identity")
		}
		return p, nil
	}
	pathKey := func(p sharedProjectIdentity) []byte { return []byte(p.Anchor + "/" + p.Relative + "/" + p.Project) }
	if err := projects.ForEach(func(key, data []byte) error {
		p, err := decodeProject(key, data)
		if err != nil {
			return err
		}
		if string(paths.Get(pathKey(p))) != p.ID {
			return problem("state_corrupt", "shared project path index missing")
		}
		return nil
	}); err != nil {
		return err
	}
	if err := paths.ForEach(func(key, data []byte) error {
		p, err := decodeProject(data, projects.Get(data))
		if err != nil {
			return err
		}
		if !bytes.Equal(key, pathKey(p)) {
			return problem("state_corrupt", "shared project path index disagrees")
		}
		return nil
	}); err != nil {
		return err
	}
	services := tx.Bucket([]byte("shared_services"))
	if err := services.ForEach(func(key, data []byte) error {
		r, err := decodeSharedService(data, string(key))
		if err != nil {
			return err
		}
		p, err := decodeProject([]byte(r.Project.ID), projects.Get([]byte(r.Project.ID)))
		if err != nil {
			return err
		}
		validManager := (r.Declaration.Manager == "mariadb" && r.Declaration.Port == 3306) || (r.Declaration.Manager == "postgresql" && r.Declaration.Port == 5432)
		if p != r.Project || !manifest.ValidIdentifier(r.Name) || r.ID != hash(p.ID+"/"+r.Name) || r.Endpoint == "" || !validManager || r.Declaration.Image == "" || r.Declaration.Storage != "volume" || r.Declaration.Admin != "generated" || r.Port < 0 || r.Port > 65535 {
			return problem("state_corrupt", "invalid shared server identity or configuration")
		}
		for kind, resource := range map[string]ownedResource{"volume": r.Volume, "network": r.Network, "container": r.Server} {
			if resource.Kind != kind || resource.Name == "" {
				return problem("state_corrupt", "invalid shared resource authority")
			}
		}
		switch r.Status {
		case "intent", "running", "stopped", "destroying", "uncertain":
		default:
			return problem("state_corrupt", "invalid shared service status")
		}
		return nil
	}); err != nil {
		return err
	}
	return tx.Bucket([]byte("shared_allocations")).ForEach(func(key, data []byte) error {
		var parent record
		if err := decodeRecord(tx.Bucket([]byte("instances")).Get(key), string(key), &parent); err != nil {
			return err
		}
		if data == nil {
			return problem("state_corrupt", "invalid shared allocation journal")
		}
		allocations, err := decodeSharedAllocations(data)
		if err != nil {
			return err
		}
		for name, allocation := range allocations {
			resource := parent.Instance.Plan.Resources[name]
			if !manifest.ValidIdentifier(name) || resource.Kind != "allocation" || !manifest.ValidIdentifier(resource.SharedService) {
				return problem("state_corrupt", "shared allocation parent disagrees")
			}
			data := services.Get([]byte(allocation.ServiceID))
			// A validated release acknowledgment can outlive explicit service deletion.
			if data == nil && allocation.Released {
				continue
			}
			if data == nil {
				return problem("state_corrupt", "orphaned shared allocation")
			}
			service, err := decodeSharedService(data, allocation.ServiceID)
			if err != nil {
				return err
			}
			if resource.SharedService != service.Name || parent.Instance.Plan.Project != service.Project.Project {
				return problem("state_corrupt", "shared allocation parent disagrees")
			}
		}
		return nil
	})
}

// New result contracts are checked only after the full historical migration path.
func validateBuildImageResults(tx *bolt.Tx) error {
	check := func(id string, result *v1.ExecutionResult) error {
		if result != nil && v1.ValidateBuildImages(id, result.BuildImages, result.ImageChangeWarnings) != nil {
			return problem("state_corrupt", "invalid retained build-image execution provenance; preserved")
		}
		return nil
	}
	if err := tx.Bucket([]byte("instances")).ForEach(func(key, data []byte) error {
		var r record
		if err := decodeRecord(data, string(key), &r); err != nil {
			return err
		}
		if err := check(string(key), r.Instance.Execution); err != nil {
			return err
		}
		for _, history := range r.NativeAbandonments {
			if err := check(string(key), history.Result); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return err
	}
	if data := tx.Bucket([]byte("meta")).Get([]byte("managed_gateway")); data != nil {
		gateway, err := decodeManagedRecord(data)
		if err != nil {
			return err
		}
		for _, history := range gateway.NativeAbandonments {
			if err := check("", history.Result); err != nil {
				return err
			}
		}
	}
	return nil
}
