package gateway

import (
	"context"
	"os/exec"
	"testing"
	"time"
)

// testdata packages are excluded from ./...; keep the real fixture's false-positive
// discriminators and protocol validation in the default repository gate.
func TestWebSocketFixtureContract(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, "go", "test", "-race", "./testdata/fixture", "-count=1", "-v")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("WebSocket fixture contract: %v\n%s", err, output)
	}
}
