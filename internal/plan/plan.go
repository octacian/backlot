package plan

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	v1 "github.com/octacian/backlot/api/v1"
	"github.com/octacian/backlot/internal/manifest"
)

// Resolve reads and validates a scene, checking required files and executables.
// No application command or provider is executed; values requiring allocation stay symbolic.
func Resolve(request v1.PlanRequest) (v1.PlanResponse, error) {
	source, err := Locate(request.ProjectPath)
	if err != nil {
		return v1.PlanResponse{}, err
	}
	return resolve(request, nil, source)
}

func resolve(request v1.PlanRequest, snapshot *Snapshot, source Source) (v1.PlanResponse, error) {
	var result v1.PlanResponse
	file, project := source.ManifestPath, source.ProjectPath

	layers, err := loadLayers(source, request.ConfigPath, false, false)
	if snapshot != nil {
		snapshot.ConfigProvenance = &ConfigProvenance{Path: layers.machinePath, Present: layers.machinePresent}
		provenance := layers.private.Provenance
		snapshot.PrivateProvenance = &provenance
		snapshot.PrivateSourceState = layers.private.State
	}
	if err != nil {
		return result, err
	}
	m, config := layers.manifest, layers.effective

	scene, ok := m.Scenes[request.Scene]
	if !ok {
		return result, problem("unknown_scene", "scene", "scene is not declared; choose a name from scenes")
	}
	if len(request.TerminalArgs) > 0 && scene.TerminalJob == "" {
		return result, problem("invalid_arguments", "terminal_args", "arguments after -- require a disposable scene with a terminal job")
	}
	for _, arg := range request.TerminalArgs {
		if strings.ContainsRune(arg, 0) {
			return result, problem("invalid_arguments", "terminal_args", "NUL is not permitted")
		}
	}
	result = v1.PlanResponse{APIVersion: v1.Version, Project: m.Project, Checkout: source.CheckoutPath, ManifestPath: file, ManifestDigest: layers.manifestDigest, ConfigDigest: layers.machineDigest, Scene: request.Scene, Lifetime: scene.Lifetime, StartupDetails: scene.StartupDetails, TerminalJob: scene.TerminalJob, Resources: map[string]v1.Resource{}, Outputs: map[string]v1.Output{}, Publish: scene.Publish}
	for _, name := range scene.Resources {
		result.Resources[name] = m.Resources[name]
		if r := m.Resources[name]; r.Kind == "allocation" {
			if result.SharedServices == nil {
				result.SharedServices = map[string]v1.SharedService{}
			}
			result.SharedServices[r.SharedService] = m.SharedServices[r.SharedService]
		}
	}
	if snapshot == nil && scene.Publish != nil && config.Caddy == nil {
		return v1.PlanResponse{}, problem("missing_provider", "config.caddy", "published scene requires Caddy settings; supply --config PATH")
	}
	if snapshot == nil && len(result.SharedServices) > 0 && config.Docker == nil {
		return v1.PlanResponse{}, problem("missing_provider", "config.docker", "shared allocations require explicit local Docker configuration")
	}
	order, err := manifest.Order(m, scene)
	if err != nil {
		return v1.PlanResponse{}, err
	}
	selectedOutputs := map[string]bool{}
	for _, name := range order {
		for _, output := range m.Components[name].Outputs {
			selectedOutputs[output] = true
		}
	}
	if err := validateOutputs(project, m.Outputs, selectedOutputs); err != nil {
		return v1.PlanResponse{}, err
	}
	for _, name := range order {
		c := m.Components[name]
		if len(c.HostMappings) > 0 && (config.Caddy == nil || config.Caddy.Mode != "managed") {
			return v1.PlanResponse{}, problem("invalid_contract", "components."+name+".host_mappings", "origin host mappings require explicit managed Caddy configuration")
		}
		// Check declared assignments, including selected reusable sets, before
		// layering can hide a reference behind a later explicit override.
		for _, mount := range c.Mounts {
			if mount.OriginCACert != "" && (config.Caddy == nil || config.Caddy.Mode != "managed") {
				return v1.PlanResponse{}, problem("invalid_contract", "components."+name+".mounts", "origin_ca_cert requires explicit managed Caddy configuration")
			}
		}
		environments := []v1.Environment{c.Environment}
		for _, set := range c.EnvironmentSets {
			environments = append(environments, m.Environments[set])
		}
		for _, environment := range environments {
			for _, value := range environment.Assign {
				if value.Ref != nil && value.Ref.Kind == "resource" && value.Ref.Field == "ca_cert_path" && (config.Caddy == nil || config.Caddy.Mode != "managed") {
					return v1.PlanResponse{}, problem("invalid_contract", "components."+name+".environment", "ca_cert_path requires explicit managed Caddy configuration")
				}
			}
		}
		if snapshot == nil && c.Runtime == v1.Container && config.Docker == nil {
			return v1.PlanResponse{}, problem("missing_provider", "config.docker", "selected container work requires local Docker settings; supply --config PATH")
		}
		planned := v1.PlannedComponent{Artifacts: c.Artifacts, Name: name, Kind: c.Kind, Runtime: c.Runtime, Command: c.Command, Args: slices.Clone(c.Args), Policy: c.Policy, Initializes: c.Initializes, DependsOn: c.DependsOn, Resources: c.Resources, Outputs: c.Outputs, Ports: c.Ports, Mounts: c.Mounts, HostMappings: c.HostMappings}
		if c.Command != nil {
			executable, err := tool(layers, c.Command.Tool, project, name, "command", snapshot)
			if err != nil {
				return v1.PlanResponse{}, err
			}
			planned.Executable = executable
			command := *c.Command
			command.Args = slices.Clone(command.Args)
			planned.Command = &command
		}
		if name == scene.TerminalJob {
			if planned.Command != nil {
				planned.Command.Args = append(planned.Command.Args, request.TerminalArgs...)
			} else {
				planned.Args = append(planned.Args, request.TerminalArgs...)
			}
		}
		for _, o := range c.Outputs {
			result.Outputs[o] = m.Outputs[o]
		}
		env, err := resolveEnvironmentValues(m, config, c, project, result)
		if err != nil {
			return v1.PlanResponse{}, err
		}
		planned.Environment = map[string]v1.PlannedValue{}
		for key, value := range env {
			planned.Environment[key] = value.wire()
			if snapshot != nil && value.secret {
				privateKey := name + "/environment/" + key
				snapshot.Secrets[privateKey] = value.private()
				if value.implicitBaseline {
					snapshot.ImplicitBaseline[privateKey] = true
				}
			}
		}
		if len(c.Fixtures) > 0 {
			planned.Fixtures = map[string]v1.PlannedFixture{}
		}
		for key, fixture := range c.Fixtures {
			value, err := resolveValue(m, config, fixture.Value, project, result)
			if err != nil {
				return v1.PlanResponse{}, err
			}
			planned.Fixtures[key] = v1.PlannedFixture{Description: fixture.Description, Value: value.wire()}
			if snapshot != nil && value.secret {
				snapshot.Secrets[name+"/fixture/"+key] = value.private()
			}
		}
		if c.Image != nil {
			if r := c.Image.Ref; r != nil && r.Kind == "build_image" {
				if result.BuildImages == nil {
					result.BuildImages = map[string]v1.BuildImage{}
				}
				result.BuildImages[r.Name] = m.BuildImages[r.Name]
			}
			image, err := resolveValue(m, config, *c.Image, project, result)
			if err != nil {
				return v1.PlanResponse{}, err
			}
			if image.literal != nil && *image.literal == "" {
				return v1.PlanResponse{}, problem("missing_input", "components."+name+".image", "image must not be empty")
			}
			if snapshot != nil && image.secret {
				snapshot.Secrets[name+"/image"] = image.private()
			}
			wire := image.wire()
			planned.Image = &wire
		}
		if c.Readiness != nil {
			p := c.Readiness
			planned.Readiness = &v1.PlannedProbe{Kind: p.Kind, Command: p.Command, Timeout: p.Timeout}
			if p.Command != nil {
				executable, err := tool(layers, p.Command.Tool, project, name, "readiness", snapshot)
				if err != nil {
					return v1.PlanResponse{}, err
				}
				planned.Readiness.Executable = executable
			}
			if p.Target != nil {
				target, err := resolveValue(m, config, *p.Target, project, result)
				if err != nil {
					return v1.PlanResponse{}, err
				}
				if snapshot != nil && target.secret {
					snapshot.Secrets[name+"/readiness"] = target.private()
				}
				wire := target.wire()
				planned.Readiness.Target = &wire
			}
		}
		result.Components = append(result.Components, planned)
	}
	if layers.private.Provenance.Present {
		// Selected bare PATH tools contribute their actual resolved executables.
		// Unselected tools retain syntax-only validation and never require availability.
		effective := config
		effective.Tools = maps.Clone(config.Tools)
		if effective.Tools == nil {
			effective.Tools = map[string]string{}
		}
		for _, component := range result.Components {
			if component.Command != nil {
				effective.Tools[component.Command.Tool] = component.Executable
			}
			if component.Readiness != nil && component.Readiness.Command != nil {
				effective.Tools[component.Readiness.Command.Tool] = component.Readiness.Executable
			}
		}
		result.EffectiveConfigDigest = effectiveDigest(m, effective, project)
	}
	if snapshot != nil {
		snapshot.Docker = config.Docker
		snapshot.Caddy = config.Caddy
		snapshot.Manifest = m
		snapshot.Plan = result
	}
	return result, nil
}

func digest(data []byte) string { sum := sha256.Sum256(data); return hex.EncodeToString(sum[:]) }

func tool(l layers, name, project, component, role string, snapshot *Snapshot) (string, error) {
	selection := toolCandidate(l, name)
	selection.Component, selection.Role = component, role
	executable := selection.Candidate
	if strings.ContainsAny(executable, "/\\") && !filepath.IsAbs(executable) {
		executable = filepath.Join(project, executable)
	}
	lookupContext := "caller/offline"
	if snapshot != nil {
		lookupContext = "daemon/runtime"
	}
	unavailable := func(reason string) error {
		return &v1.PlanError{Code: "missing_tool", Field: "tools." + name, SourceLayer: selection.SourceLayer, SourcePath: selection.SourcePath,
			Message: fmt.Sprintf("%s %s %s: attempted executable %q %s; install it or run backlot project setup --edit to set a tools override", lookupContext, component, role, executable, reason)}
	}
	resolved, err := exec.LookPath(executable)
	if err != nil {
		return "", unavailable("is unavailable")
	}
	if !filepath.IsAbs(resolved) {
		resolved, err = filepath.Abs(resolved)
		if err != nil {
			return "", unavailable("cannot be resolved to an absolute path")
		}
	}
	info, err := os.Stat(resolved)
	if err != nil || !info.Mode().IsRegular() {
		return "", unavailable("must be a regular file")
	}
	if snapshot != nil {
		selection.Executable = resolved
		snapshot.ToolSelections = append(snapshot.ToolSelections, selection)
	}
	return resolved, nil
}

type resolvedValue struct {
	literal          *string
	symbolic         *v1.Reference
	secret           bool
	implicitBaseline bool
}

func (v resolvedValue) wire() v1.PlannedValue {
	if v.secret {
		return v1.PlannedValue{Redacted: true}
	}
	return v1.PlannedValue{Literal: v.literal, Symbolic: v.symbolic}
}

func resolveValue(m v1.Manifest, c resolvedConfig, v v1.Value, project string, p v1.PlanResponse) (resolvedValue, error) {
	result := resolvedValue{literal: v.Literal, secret: v.Secret}
	if v.Ref == nil {
		return result, nil
	}
	r := v.Ref
	switch r.Kind {
	case "input":
		literal, ok := c.Inputs[r.Name]
		if !ok || literal == "" {
			return result, problem("missing_input", "inputs."+r.Name, "required value is missing; run backlot project setup --edit")
		}
		result.literal = &literal
		result.secret = result.secret || m.Inputs[r.Name].Secret
	case "instance":
		var literal string
		switch r.Field {
		case "project":
			literal = p.Project
		case "scene":
			literal = p.Scene
		case "checkout":
			literal = p.Checkout
		default:
			result.symbolic = r
			return result, nil
		}
		result.literal = &literal
	case "resource":
		result.symbolic = r
		result.secret = result.secret || (m.Resources[r.Name].Kind == "secret" || m.Resources[r.Name].Kind == "allocation" && r.Field == "password")
	case "service", "build_image":
		result.symbolic = r
	case "output":
		literal := filepath.Join(project, filepath.FromSlash(m.Outputs[r.Name].Path))
		result.literal = &literal
	}
	return result, nil
}

func toolCandidate(l layers, name string) ToolSelection {
	selection := ToolSelection{Tool: name, Candidate: l.manifest.Tools[name], SourceLayer: "manifest", SourcePath: l.manifestPath}
	if override, ok := l.machine.Tools[name]; ok {
		selection.Candidate, selection.SourceLayer, selection.SourcePath = override, "machine", l.machinePath
	}
	if override, ok := l.private.Config.Tools[name]; ok {
		selection.Candidate, selection.SourceLayer, selection.SourcePath = override, "private", l.private.Provenance.Path
	}
	return selection
}
