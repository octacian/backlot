package plan

import (
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"strings"

	v1 "github.com/octacian/backlot/api/v1"
	"github.com/octacian/backlot/internal/manifest"
	"github.com/octacian/backlot/internal/projectconfig"
)

type layers struct {
	manifest                                   v1.Manifest
	machine                                    v1.MachineConfig
	effective                                  v1.MachineConfig
	private                                    projectconfig.Selection
	manifestDigest, machineDigest, machinePath string
	machinePresent                             bool
}

func loadLayers(source Source, configPath string, staged, reassociate bool) (layers, error) {
	var l layers
	data, err := readFile(source.ManifestPath)
	if err != nil {
		return l, err
	}
	if err := manifest.Decode(data, filepath.Ext(source.ManifestPath), &l.manifest); err != nil {
		return l, err
	}
	if err := manifest.Validate(l.manifest); err != nil {
		return l, err
	}
	l.manifestDigest = digest(data)
	l.machine.Version = v1.ManifestVersion
	if configPath == "" {
		if root, err := os.UserConfigDir(); err == nil {
			l.machinePath = filepath.Join(root, "backlot", "config.yaml")
			if _, err := os.Stat(l.machinePath); err == nil {
				configPath = l.machinePath
			} else if !os.IsNotExist(err) {
				return l, problem("config", "config", "cannot inspect default machine config")
			}
		}
	} else {
		l.machinePath = configPath
	}
	if configPath != "" {
		l.machine = v1.MachineConfig{}
		data, err := readFile(configPath)
		if err != nil {
			return l, err
		}
		if err := manifest.Decode(data, filepath.Ext(configPath), &l.machine); err != nil {
			return l, err
		}
		l.machinePresent = true
		l.machineDigest = digest(data)
	}
	if err := manifest.ValidateMachine(l.machine); err != nil {
		return l, err
	}
	if err := manifest.ValidateMembership(l.manifest, l.machine.Inputs, l.machine.Tools); err != nil {
		return l, err
	}
	if l.machine.Caddy != nil {
		value := manifest.CaddyDefaults(*l.machine.Caddy)
		l.machine.Caddy = &value
	}
	l.private, err = projectconfig.Load(source.ProjectPath, source.CheckoutPath, source.ManifestPath, l.manifest, staged, reassociate)
	if err != nil {
		return l, err
	}
	l.effective = compose(l.machine, l.private.Config)
	return l, nil
}

func compose(machine v1.MachineConfig, private v1.PrivateConfig) v1.MachineConfig {
	result := machine
	result.Inputs = map[string]string{}
	result.Tools = map[string]string{}
	maps.Copy(result.Inputs, machine.Inputs)
	maps.Copy(result.Inputs, private.Inputs)
	maps.Copy(result.Tools, machine.Tools)
	maps.Copy(result.Tools, private.Tools)
	return result
}

func effectiveDigest(m v1.Manifest, c v1.MachineConfig, project string) string {
	tools := maps.Clone(m.Tools)
	maps.Copy(tools, c.Tools)
	for name, path := range tools {
		if strings.ContainsAny(path, "/\\") && !filepath.IsAbs(path) {
			tools[name] = filepath.Join(project, path)
		}
	}
	c.Tools = tools
	data, _ := json.Marshal(c)
	return digest(append([]byte("backlot/effective-config/v1\x00"), data...))
}

// ConfigurationDrift preserves legacy machine-byte drift and compares composed effective values.
func ConfigurationDrift(a, b v1.PlanResponse) bool {
	if a.EffectiveConfigDigest != "" || b.EffectiveConfigDigest != "" {
		return a.EffectiveConfigDigest != b.EffectiveConfigDigest
	}
	return a.ConfigDigest != b.ConfigDigest
}
