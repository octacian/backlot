package manifest

import (
	v1 "github.com/octacian/backlot/api/v1"
	"slices"
)

func buildImageRef(v v1.Value) *v1.Reference {
	if v.Ref != nil && v.Ref.Kind == "build_image" {
		return v.Ref
	}
	return nil
}

func validateBuildImages(m v1.Manifest) error {
	for name, image := range m.BuildImages {
		producer, ok := m.Components[image.Producer]
		if !ok || producer.Kind != v1.Job || producer.Runtime != v1.Native || producer.Policy != v1.EachStart {
			return invalid("build_images."+name, "producer must be an existing native each-start job")
		}
	}
	for name, environment := range m.Environments {
		for _, v := range environment.Assign {
			if r := buildImageRef(v); r != nil && r.Field != "tag" {
				return invalid("environments."+name, "build_image environment references require tag")
			}
		}
	}
	for name, c := range m.Components {
		effective := map[string]v1.Value{}
		for _, set := range c.EnvironmentSets {
			for key, v := range m.Environments[set].Assign {
				effective[key] = v
			}
		}
		for key, v := range c.Environment.Assign {
			effective[key] = v
		}
		// Validate every assignment before layering can hide a reference in a wrong owner.
		environments := []v1.Environment{c.Environment}
		for _, set := range c.EnvironmentSets {
			environments = append(environments, m.Environments[set])
		}
		for _, environment := range environments {
			for _, v := range environment.Assign {
				if r := buildImageRef(v); r != nil && (r.Field != "tag" || m.BuildImages[r.Name].Producer != name) {
					return invalid("components."+name+".environment", "build_image tag is only available to its producer")
				}
			}
		}
		for _, fixture := range c.Fixtures {
			if buildImageRef(fixture.Value) != nil {
				return invalid("components."+name+".fixtures", "build_image references are only for producer environment and container image")
			}
		}
		if c.Readiness != nil && c.Readiness.Target != nil && buildImageRef(*c.Readiness.Target) != nil {
			return invalid("components."+name+".readiness", "build_image is not a readiness value")
		}
		for image, declaration := range m.BuildImages {
			if declaration.Producer != name {
				continue
			}
			found := false
			for _, v := range effective {
				if r := buildImageRef(v); r != nil && r.Name == image {
					found = true
				}
			}
			if !found {
				return invalid("build_images."+image, "producer must explicitly assign the tag to its effective environment")
			}
		}
	}
	return nil
}

func selectedBuildImages(m v1.Manifest, scene v1.Scene) error {
	selected := map[string]bool{}
	for _, name := range scene.Components {
		c := m.Components[name]
		if c.Image == nil {
			continue
		}
		r := buildImageRef(*c.Image)
		if r == nil {
			continue
		}
		producer := m.BuildImages[r.Name].Producer
		if !slices.Contains(scene.Components, producer) || !slices.Contains(c.DependsOn, v1.Dependency{Component: producer, Condition: v1.Completed}) {
			return invalid("components."+name+".image", "build_image requires an explicitly selected producer and direct completed dependency")
		}
		selected[r.Name] = true
	}
	for _, name := range scene.Components {
		c := m.Components[name]
		environments := []v1.Environment{c.Environment}
		for _, set := range c.EnvironmentSets {
			environments = append(environments, m.Environments[set])
		}
		for _, environment := range environments {
			for _, v := range environment.Assign {
				if r := buildImageRef(v); r != nil && !selected[r.Name] {
					return invalid("components."+name+".environment", "build_image tag requires a selected image consumer")
				}
			}
		}
	}
	return nil
}
