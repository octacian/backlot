package manifest

import (
	v1 "github.com/octacian/backlot/api/v1"
	"testing"
)

func TestContainerCAMountBoundaries(t *testing.T) {
	for _, name := range []string{"valid", "mount without environment", "writable", "two sources", "native", "unpublished", "unattached", "wrong kind", "relative", "root target", "duplicate origin", "shadow parent", "shadow root", "shadow child", "duplicate target"} {
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
			case "shadow parent", "shadow root", "shadow child", "duplicate target":
				target := "/trust"
				if name == "shadow root" {
					target = "/"
				}
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

func TestOriginHostMappingBoundaries(t *testing.T) {
	for _, name := range []string{"valid", "dns name", "native", "unattached", "unpublished", "duplicate", "numeric", "loopback", "localhost", "container localhost", "scheme", "port", "empty", "wrong resource"} {
		t.Run(name, func(t *testing.T) {
			var m v1.Manifest
			if err := Decode([]byte(caProject), ".yaml", &m); err != nil {
				t.Fatal(err)
			}
			c := m.Components["web"]
			image := "alpine:3.21"
			c.Runtime, c.Command, c.Image = v1.Container, nil, &v1.Value{Literal: &image}
			c.EnvironmentSets = nil
			c.Ports["http"] = v1.ServicePort{Resource: "port", ContainerPort: 8080}
			c.HostMappings = []v1.HostMapping{{Origin: "origin", Host: "host-gateway"}}
			switch name {
			case "dns name":
				c.HostMappings[0].Host = "host.docker.internal"
			case "native":
				c.Runtime = v1.Native
				c.Image = nil
				c.Command = &v1.Command{Tool: "go"}
				c.Ports["http"] = v1.ServicePort{Resource: "port"}
			case "unattached":
				c.Resources = []string{"port"}
			case "unpublished":
				s := m.Scenes["dev"]
				s.Publish = nil
				m.Scenes["dev"] = s
			case "duplicate":
				c.HostMappings = append(c.HostMappings, c.HostMappings[0])
			case "numeric":
				c.HostMappings[0].Host = "192.168.65.254"
			case "loopback":
				c.HostMappings[0].Host = "127.0.0.1"
			case "localhost":
				c.HostMappings[0].Host = "localhost"
			case "container localhost":
				c.HostMappings[0].Host = "other.localhost"
			case "scheme":
				c.HostMappings[0].Host = "https://host.docker.internal"
			case "port":
				c.HostMappings[0].Host = "host.docker.internal:8443"
			case "empty":
				c.HostMappings[0].Host = ""
			case "wrong resource":
				c.HostMappings[0].Origin = "port"
			}
			m.Components["web"] = c
			if err := Validate(m); (err == nil) != (name == "valid") {
				t.Fatal(name, err)
			}
		})
	}
}
