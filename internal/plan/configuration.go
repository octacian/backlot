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
	manifest                                                 v1.Manifest
	machine                                                  v1.MachineConfig
	effective                                                resolvedConfig
	private                                                  projectconfig.Selection
	manifestDigest, machineDigest, manifestPath, machinePath string
	machinePresent                                           bool
}

func loadLayers(source Source, configPath string, staged, reassociate bool) (l layers, loadErr error) {
	l.manifestPath = source.ManifestPath
	layer, path := "manifest", source.ManifestPath
	defer func() { loadErr = manifest.SourceError(loadErr, layer, path) }()
	// Capture source association independently of successful manifest/scene validation.
	var err error
	l.private, err = projectconfig.Select(source.ProjectPath, source.CheckoutPath, source.ManifestPath, reassociate)
	if err != nil {
		return l, err
	}

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
	layer, path = "machine", configPath
	l.machine.Version = v1.ManifestVersion
	if configPath == "" {
		if root, err := os.UserConfigDir(); err == nil {
			l.machinePath = filepath.Join(root, "backlot", "config.yaml")
			path = l.machinePath
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
	if err := manifest.ValidateMembership(l.manifest, nil, l.machine.Tools); err != nil {
		return l, err
	}
	if l.machine.Caddy != nil {
		value := manifest.CaddyDefaults(*l.machine.Caddy)
		l.machine.Caddy = &value
	}
	layer, path = "registry", ""
	l.private, err = l.private.Read(l.manifest, staged)
	if err != nil {
		return l, err
	}
	l.effective = compose(l.machine, l.private.Config)
	return l, nil
}

// resolvedConfig is a private captured composition, never a machine document.
// Explicit fields keep input ownership independent of the public machine contract.
type resolvedConfig struct {
	Version string            `json:"version"`
	Inputs  map[string]string `json:"inputs,omitempty"`
	Tools   map[string]string `json:"tools,omitempty"`
	Docker  *v1.DockerConfig  `json:"docker,omitempty"`
	Caddy   *v1.CaddyConfig   `json:"caddy,omitempty"`
	Storage *v1.StorageConfig `json:"storage,omitempty"`
}

func compose(machine v1.MachineConfig, private v1.PrivateConfig) resolvedConfig {
	result := resolvedConfig{Version: machine.Version, Inputs: maps.Clone(private.Inputs), Tools: maps.Clone(machine.Tools), Docker: machine.Docker, Caddy: machine.Caddy, Storage: machine.Storage}
	if result.Tools == nil {
		result.Tools = map[string]string{}
	}
	maps.Copy(result.Tools, private.Tools)
	return result
}

func effectiveDigest(m v1.Manifest, c resolvedConfig, project string) string {
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
