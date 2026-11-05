package plan

import (
	"strings"

	v1 "github.com/octacian/backlot/api/v1"
)

// TaskCapture is private accepted launch material and the parent contracts it uses.
// Allocation values stay symbolic until invocation against the bound parent.
type TaskCapture struct {
	// Component retains the resolved native command and redacted environment.
	Component v1.PlannedComponent `json:"component"`
	// Secrets supplies only this task’s private captured values.
	Secrets map[string]v1.PlannedValue `json:"secrets"`
	// Outputs declares task-owned mutable paths and groups.
	Outputs map[string]v1.Output `json:"outputs"`
	// Paths preserves canonical output reservations at acceptance.
	Paths map[string]string `json:"paths"`
	// Resources records parent resource contracts consumed by the task.
	Resources map[string]v1.Resource `json:"resources"`
	// Services records referenced parent service and dependency declarations.
	Services map[string]v1.Component `json:"services"`
	// SharedServices records the consumed allocation provider contracts.
	SharedServices map[string]v1.SharedService `json:"shared_services,omitempty"`
	// Docker retains relevant native endpoint provider settings.
	Docker *v1.DockerConfig `json:"docker,omitempty"`
	// Caddy retains relevant publication provider settings.
	Caddy *v1.CaddyConfig `json:"caddy,omitempty"`
	// Publish records a consumed parent publication contract.
	Publish *v1.Publication `json:"publish,omitempty"`
}

// PrepareTask resolves only the requested enabled task, without requiring tools,
// seeds or private inputs used exclusively by unrelated work.
func PrepareTask(source Source, request v1.PlanRequest, name string) (Snapshot, error) {
	snap := Snapshot{Secrets: map[string]v1.PlannedValue{}, ImplicitBaseline: map[string]bool{}}
	_, err := resolveSelected(request, &snap, source, name)
	return snap, err
}

func captureTask(m v1.Manifest, config resolvedConfig, project string, c v1.PlannedComponent, secrets map[string]v1.PlannedValue, scene v1.Scene) (TaskCapture, error) {
	out := TaskCapture{Component: c, Secrets: map[string]v1.PlannedValue{}, Outputs: map[string]v1.Output{}, Paths: map[string]string{}, Resources: map[string]v1.Resource{}, Services: map[string]v1.Component{}, SharedServices: map[string]v1.SharedService{}}
	for key, value := range secrets {
		if strings.HasPrefix(key, c.Name+"/") {
			out.Secrets[key] = value
		}
	}
	for _, name := range c.Outputs {
		out.Outputs[name] = m.Outputs[name]
		path, err := OutputPath(project, m.Outputs[name].Path)
		if err != nil {
			return out, err
		}
		out.Paths[name] = path
	}
	for _, name := range c.Resources {
		out.Resources[name] = m.Resources[name]
	}
	for _, d := range c.DependsOn {
		out.Services[d.Component] = m.Components[d.Component]
	}
	visit := func(v v1.Value) error {
		if v.Ref == nil {
			return nil
		}
		r := v.Ref
		if r.Kind == "service" {
			out.Services[r.Name] = m.Components[r.Name]
			if m.Components[r.Name].Runtime == v1.Container {
				out.Docker = config.Docker
			}
		}
		if r.Kind == "resource" {
			out.Resources[r.Name] = m.Resources[r.Name]
		}
		return nil
	}
	values := []v1.PlannedValue{}
	for _, value := range c.Environment {
		values = append(values, value)
	}
	for _, value := range c.Fixtures {
		values = append(values, value.Value)
	}
	for _, value := range out.Secrets {
		values = append(values, value)
	}
	for _, value := range values {
		if value.Symbolic != nil {
			_ = visit(v1.Value{Ref: value.Symbolic})
		}
		if value.Composition != nil {
			_ = value.Composition.Walk(visit)
		}
	}
	if c.Command != nil {
		for _, arg := range c.Command.Args {
			_ = arg.Walk(visit)
		}
	}
	for _, resource := range out.Resources {
		if resource.Kind == "allocation" {
			out.SharedServices[resource.SharedService] = m.SharedServices[resource.SharedService]
			out.Docker = config.Docker
		}
		if resource.Kind == "origin" {
			out.Publish = scene.Publish
			out.Caddy = config.Caddy
		}
	}
	return out, nil
}
