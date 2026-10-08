package plan

import (
	"crypto/sha256"
	"encoding/hex"
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

	data, err := readFile(file)
	if err != nil {
		return result, err
	}
	var m v1.Manifest
	if err := manifest.Decode(data, filepath.Ext(file), &m); err != nil {
		return result, err
	}
	if err := manifest.Validate(m); err != nil {
		return result, err
	}
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
	var config v1.MachineConfig
	configFile := request.ConfigPath
	if configFile == "" {
		dir, err := os.UserConfigDir()
		candidate := ""
		if err == nil {
			candidate = filepath.Join(dir, "backlot", "config.yaml")
		}
		if candidate != "" {
			if _, err := os.Stat(candidate); err == nil {
				configFile = candidate
			} else if !os.IsNotExist(err) {
				return result, problem("config", "config", "cannot inspect default config; use --config PATH")
			}
		}
	}
	var configDigest string
	if configFile != "" {
		data, err := readFile(configFile)
		if err != nil {
			return result, err
		}
		if err := manifest.Decode(data, filepath.Ext(configFile), &config); err != nil {
			return result, err
		}
		if err := manifest.ValidateMachine(config); err != nil {
			return result, err
		}
		configDigest = digest(data)
	}
	for name := range config.Tools {
		if _, ok := m.Tools[name]; !ok {
			return result, problem("config", "config.tools."+name, "override names an undeclared project tool")
		}
	}
	for name := range config.Inputs {
		if _, ok := m.Inputs[name]; !ok {
			return result, problem("config", "config.inputs."+name, "value names an undeclared project input")
		}
	}
	result = v1.PlanResponse{APIVersion: v1.Version, Project: m.Project, Checkout: source.CheckoutPath, ManifestPath: file, ManifestDigest: digest(data), ConfigDigest: configDigest, Scene: request.Scene, Lifetime: scene.Lifetime, TerminalJob: scene.TerminalJob, Resources: map[string]v1.Resource{}, Outputs: map[string]v1.Output{}, Publish: scene.Publish}
	for _, name := range scene.Resources {
		result.Resources[name] = m.Resources[name]
	}
	if snapshot == nil && scene.Publish != nil && config.Caddy == nil {
		return v1.PlanResponse{}, problem("missing_provider", "config.caddy", "published scene requires Caddy settings; supply --config PATH")
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
		if snapshot == nil && c.Runtime == v1.Container && config.Docker == nil {
			return v1.PlanResponse{}, problem("missing_provider", "config.docker", "selected container work requires local Docker settings; supply --config PATH")
		}
		planned := v1.PlannedComponent{Name: name, Kind: c.Kind, Runtime: c.Runtime, Command: c.Command, Args: slices.Clone(c.Args), Policy: c.Policy, Initializes: c.Initializes, DependsOn: c.DependsOn, Resources: c.Resources, Outputs: c.Outputs, Ports: c.Ports, Mounts: c.Mounts}
		if c.Command != nil {
			executable, err := tool(m, config, c.Command.Tool, project)
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
		if c.Image != nil {
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
				executable, err := tool(m, config, p.Command.Tool, project)
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
	if snapshot != nil {
		snapshot.Docker = config.Docker
		snapshot.Caddy = config.Caddy
		snapshot.Manifest = m
		snapshot.Plan = result
	}
	return result, nil
}

func digest(data []byte) string { sum := sha256.Sum256(data); return hex.EncodeToString(sum[:]) }

func tool(m v1.Manifest, c v1.MachineConfig, name, project string) (string, error) {
	executable := m.Tools[name]
	if override, ok := c.Tools[name]; ok {
		executable = override
	}
	if strings.ContainsAny(executable, "/\\") && !filepath.IsAbs(executable) {
		executable = filepath.Join(project, executable)
	}
	resolved, err := exec.LookPath(executable)
	if err != nil {
		return "", problem("missing_tool", "tools."+name, "executable is unavailable; install it or set config.tools override")
	}
	if !filepath.IsAbs(resolved) {
		resolved, err = filepath.Abs(resolved)
		if err != nil {
			return "", problem("missing_tool", "tools."+name, "cannot resolve executable path")
		}
	}
	info, err := os.Stat(resolved)
	if err != nil || !info.Mode().IsRegular() {
		return "", problem("missing_tool", "tools."+name, "executable must be a regular file")
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

func resolveValue(m v1.Manifest, c v1.MachineConfig, v v1.Value, project string, p v1.PlanResponse) (resolvedValue, error) {
	result := resolvedValue{literal: v.Literal, secret: v.Secret}
	if v.Ref == nil {
		return result, nil
	}
	r := v.Ref
	switch r.Kind {
	case "input":
		literal, ok := c.Inputs[r.Name]
		if !ok || literal == "" {
			return result, problem("missing_input", "inputs."+r.Name, "required value is missing; set config.inputs in --config PATH")
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
		result.secret = result.secret || m.Resources[r.Name].Kind == "secret"
	case "service":
		result.symbolic = r
	case "output":
		literal := filepath.Join(project, filepath.FromSlash(m.Outputs[r.Name].Path))
		result.literal = &literal
	}
	return result, nil
}
