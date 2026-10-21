package v1

import (
	"strings"
	"testing"
)

func TestSceneTargetRejectsMixedAndUnboundSelectors(t *testing.T) {
	for _, target := range []SceneTarget{{}, {Scene: "dev"}, {Scene: "dev", CWD: "relative"}, {Scene: "dev", CWD: "/tmp", ProjectPath: "relative"}, {InstanceID: "short"}, {InstanceID: strings.Repeat("x", 64)}, {InstanceID: strings.Repeat("a", 64), Scene: "dev"}, {InstanceID: strings.Repeat("a", 64), CWD: "/tmp"}, {InstanceID: strings.Repeat("a", 64), ProjectPath: "/tmp"}} {
		if err := target.Validate(); err == nil {
			t.Fatal("invalid target accepted", target)
		}
	}
	for _, target := range []SceneTarget{{Scene: "dev", CWD: "/tmp"}, {Scene: "dev", CWD: "/tmp", ProjectPath: "/tmp/project/backlot.json"}, {InstanceID: strings.Repeat("a", 64)}} {
		if err := target.Validate(); err != nil {
			t.Fatal(err)
		}
	}
}
