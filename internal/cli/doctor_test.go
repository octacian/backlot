package cli

import (
	"bytes"
	"context"
	v1 "github.com/octacian/backlot/api/v1"
	"strings"
	"testing"
)

func TestDoctorRejectsAmbiguousTargetsBeforeDaemon(t *testing.T) {
	for _, args := range [][]string{{"--instance", strings.Repeat("a", 64), "--scene", "dev"}, {"--config", "/missing"}, {"--project", "/missing"}, {"--container-probe-image", "local:probe"}, {"--instance", strings.Repeat("a", 64), "--probe-public-ca"}, {"argument"}} {
		t.Run(strings.Join(args, "_"), func(t *testing.T) {
			var out bytes.Buffer
			command := NewCommand(v1.VersionResponse{}, &out, &out)
			err := command.Run(context.Background(), append([]string{"backlot", "doctor", "--json", "--state-dir", "/nonexistent-doctor-state"}, args...))
			if err == nil || strings.Contains(out.String(), "daemon_unavailable") {
				t.Fatal("ambiguous target reached daemon", err, out.String())
			}
		})
	}
}
