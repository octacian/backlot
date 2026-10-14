package manifest

import (
	v1 "github.com/octacian/backlot/api/v1"
	"testing"
)

func TestContainerCAMountBoundaries(t *testing.T) {
	for _, name := range []string{"valid", "mount without environment", "writable", "two sources", "native", "unpublished", "unattached", "wrong kind", "relative", "root target", "duplicate origin", "shadow parent", "shadow child", "duplicate target"} {
		t.Run(name, func(t *testing.T) {
			var m v1.Manifest
			if err := Decode([]byte(caProject), ".yaml", &m); err != nil {
				t.Fatal(err)
			}
			c := m.Components["web"]
			image := "alpine:3.21"
			c.Runtime, c.Command, c.Image = v1.Container, nil, &v1.Value{Literal: &image}
			c.Ports["http"] = v1.ServicePort{Resource: "port", ContainerPort: 8080}
			c.Mounts = []v1.Mount{{OriginCACert: "origin", Target: "/trust/root.pem", ReadOnly: true}}
			switch name {
			case "mount without environment":
				c.EnvironmentSets = nil
			case "writable":
				c.Mounts[0].ReadOnly = false
			case "two sources":
				c.Mounts[0].Resource = "origin"
			case "native":
				c.Runtime = v1.Native
				c.Image = nil
				c.Command = &v1.Command{Tool: "go"}
				c.Ports["http"] = v1.ServicePort{Resource: "port"}
			case "unpublished":
				s := m.Scenes["dev"]
				s.Publish = nil
				m.Scenes["dev"] = s
			case "unattached":
				c.Resources = []string{"port"}
			case "wrong kind":
				m.Resources["origin"] = v1.Resource{Kind: "directory"}
			case "relative":
				c.Mounts[0].Target = "trust.pem"
			case "root target":
				c.Mounts[0].Target = "/"
			case "duplicate origin":
				c.Mounts = append(c.Mounts, v1.Mount{OriginCACert: "origin", Target: "/other.pem", ReadOnly: true})
			case "shadow parent", "shadow child", "duplicate target":
				target := "/trust"
				if name == "shadow child" {
					target = "/trust/root.pem/child"
				}
				if name == "duplicate target" {
					target = "/trust/root.pem"
				}
				m.Outputs = map[string]v1.Output{"out": {Path: "out", ConcurrencyGroup: "out"}}
				c.Outputs = []string{"out"}
				c.Mounts = append(c.Mounts, v1.Mount{Output: "out", Target: target})
			}
			m.Components["web"] = c
			valid := name == "valid" || name == "mount without environment"
			if err := Validate(m); (err == nil) != valid {
				t.Fatal(valid, err)
			}
		})
	}
}
