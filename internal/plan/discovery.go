package plan

import (
	"maps"
	"path/filepath"
	"slices"

	v1 "github.com/octacian/backlot/api/v1"
	"github.com/octacian/backlot/internal/manifest"
)

func declarations(project string) (Source, v1.Manifest, error) {
	source, err := Locate(project)
	var m v1.Manifest
	if err != nil {
		return source, m, err
	}
	data, err := readFile(source.ManifestPath)
	if err == nil {
		err = manifest.Decode(data, filepath.Ext(source.ManifestPath), &m)
	}
	if err == nil {
		err = manifest.Validate(m)
	}
	return source, m, manifest.SourceError(err, "manifest", source.ManifestPath)
}

func sceneSummary(name string, s v1.Scene) v1.SceneSummary {
	return v1.SceneSummary{Name: name, Lifetime: s.Lifetime, ComponentCount: len(s.Components), TerminalJob: s.TerminalJob, Publication: s.Publish != nil}
}

// ListScenes validates declarations without loading configuration or prerequisites.
func ListScenes(project string) (v1.SceneListResponse, error) {
	source, m, err := declarations(project)
	r := v1.SceneListResponse{APIVersion: v1.Version, Project: m.Project, ManifestPath: source.ManifestPath, CheckoutPath: source.CheckoutPath, Scenes: []v1.SceneSummary{}}
	if err != nil {
		return r, err
	}
	for _, name := range slices.Sorted(maps.Keys(m.Scenes)) {
		r.Scenes = append(r.Scenes, sceneSummary(name, m.Scenes[name]))
	}
	return r, nil
}

// DescribeScene projects the selected declared graph without hydrating any values.
func DescribeScene(project, name string) (v1.SceneDescriptionResponse, error) {
	source, m, err := declarations(project)
	r := v1.SceneDescriptionResponse{APIVersion: v1.Version, Project: m.Project, ManifestPath: source.ManifestPath, CheckoutPath: source.CheckoutPath, Components: []v1.DeclaredComponent{}, Resources: map[string]v1.Resource{}, Outputs: map[string]v1.Output{}, BuildImages: map[string]v1.BuildImage{}, SharedServices: map[string]v1.DeclaredSharedService{}}
	if err != nil {
		return r, err
	}
	scene, ok := m.Scenes[name]
	if !ok {
		return r, problem("unknown_scene", "scene", "scene is not declared")
	}
	r.Scene = sceneSummary(name, scene)
	tools, inputs, seeds, required, pass := map[string]bool{}, map[string]bool{}, map[string]bool{}, map[string]bool{}, map[string]bool{}
	value := func(v v1.Value) {
		if v.Ref == nil {
			return
		}
		if v.Ref.Kind == "input" {
			inputs[v.Ref.Name] = true
		}
		if v.Ref.Kind == "build_image" {
			r.BuildImages[v.Ref.Name] = m.BuildImages[v.Ref.Name]
		}
	}
	environment := func(e v1.Environment) {
		for _, n := range e.Seeds {
			seeds[n] = true
		}
		for _, n := range e.Required {
			required[n] = true
		}
		for _, n := range e.PassThrough {
			pass[n] = true
		}
		for _, v := range e.Assign {
			value(v)
		}
	}
	probe := func(p *v1.Probe) {
		if p == nil {
			return
		}
		if p.Command != nil {
			tools[p.Command.Tool] = true
		}
		if p.Target != nil {
			value(*p.Target)
		}
	}
	for _, n := range scene.Resources {
		resource := m.Resources[n]
		r.Resources[n] = resource
		if resource.Kind == "volume" || resource.Kind == "network" {
			r.Prerequisites.Docker = true
		}
		if resource.SharedService != "" {
			s := m.SharedServices[resource.SharedService]
			r.SharedServices[resource.SharedService] = v1.DeclaredSharedService{Manager: s.Manager, Storage: s.Storage, Admin: s.Admin, Port: s.Port}
			r.Prerequisites.Docker = true
		}
	}
	order, err := manifest.Order(m, scene)
	if err != nil {
		return r, err
	}
	for _, n := range order {
		c := m.Components[n]
		d := v1.DeclaredComponent{Name: n, Kind: c.Kind, Runtime: c.Runtime, Policy: c.Policy, DependsOn: c.DependsOn, Resources: c.Resources, Outputs: c.Outputs, Ports: c.Ports, Mounts: c.Mounts, HostMappings: c.HostMappings, Artifacts: c.Artifacts}
		if c.Readiness != nil {
			d.Readiness = c.Readiness.Kind
		}
		r.Components = append(r.Components, d)
		if c.Runtime == v1.Container {
			r.Prerequisites.Docker = true
		}
		if c.Command != nil {
			tools[c.Command.Tool] = true
		}
		if c.Image != nil {
			value(*c.Image)
		}
		for _, f := range c.Fixtures {
			value(f.Value)
		}
		for _, n := range c.Outputs {
			r.Outputs[n] = m.Outputs[n]
		}
		for _, n := range c.EnvironmentSets {
			environment(m.Environments[n])
		}
		environment(c.Environment)
		probe(c.Readiness)
	}
	if p := scene.Publish; p != nil {
		r.Publication = &v1.DeclaredPublication{Resource: p.Resource, Routes: p.Routes, ProbeKind: p.Probe.Kind}
		r.Prerequisites.Caddy = true
		probe(&p.Probe)
	}
	r.Prerequisites.Tools = slices.Sorted(maps.Keys(tools))
	r.Prerequisites.Seeds = slices.Sorted(maps.Keys(seeds))
	r.Prerequisites.Required = slices.Sorted(maps.Keys(required))
	r.Prerequisites.PassThrough = slices.Sorted(maps.Keys(pass))
	r.Prerequisites.Inputs = []v1.DeclaredInput{}
	for _, n := range slices.Sorted(maps.Keys(inputs)) {
		d := m.Inputs[n]
		r.Prerequisites.Inputs = append(r.Prerequisites.Inputs, v1.DeclaredInput{Name: n, Description: d.Description, Secret: d.Secret, Generate: d.Generate})
	}
	return r, nil
}

// Configuration inspects the same layers as setup, without seed reads or tool lookup.
func Configuration(project, config string) (v1.ConfigurationResponse, error) {
	source, err := Locate(project)
	if err != nil {
		return v1.ConfigurationResponse{}, err
	}
	l, err := loadLayers(source, config, false, false)
	if err != nil {
		return v1.ConfigurationResponse{}, err
	}
	setup := (&Setup{source: source, layers: l}).Inspect()
	defaults, err := DefaultConfigPath()
	if err != nil {
		return v1.ConfigurationResponse{}, err
	}
	r := v1.ConfigurationResponse{APIVersion: v1.Version, Context: "caller configuration; tools are candidates, storage is declared intent", ManifestPath: source.ManifestPath, MachinePath: l.machinePath, MachinePresent: l.machinePresent, DefaultMachinePath: defaults, PrivatePath: setup.PrivatePath, PrivatePresent: setup.Registered, Association: setup.Association, Inputs: setup.Inputs, Tools: []v1.ToolCandidate{}, Docker: l.machine.Docker, Caddy: l.machine.Caddy, DeclaredStorage: l.machine.Storage}
	if r.Docker != nil {
		d := *r.Docker
		if d.PublishAddress == "" {
			d.PublishAddress = "127.0.0.1"
		}
		r.Docker = &d
	}
	for _, name := range slices.Sorted(maps.Keys(l.manifest.Tools)) {
		t := toolCandidate(l, name)
		r.Tools = append(r.Tools, v1.ToolCandidate{Name: name, Candidate: t.Candidate, SourceLayer: t.SourceLayer, SourcePath: t.SourcePath})
	}
	return r, nil
}
