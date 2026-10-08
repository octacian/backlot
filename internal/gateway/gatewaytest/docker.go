// Package gatewaytest owns opt-in Docker fixture resources by unique labels.
package gatewaytest

import (
	"context"
	"crypto/rand"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// DockerCommand runs an explicit fixture Docker operation.
func DockerCommand(t *testing.T, endpoint string, args ...string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, "docker", append([]string{"--host", endpoint}, args...)...)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("docker %v: %v: %s", args, err, output)
	}
	return strings.TrimSpace(string(output))
}

// FixtureImage builds a unique static Go fixture image with standard public roots.
// Its base must already exist; it never implicitly pulls an image.
func FixtureImage(t *testing.T, endpoint, source string) string {
	t.Helper()
	directory := t.TempDir()
	DockerCommand(t, endpoint, "image", "inspect", "alpine:3.21")
	arch := DockerCommand(t, endpoint, "info", "--format", "{{.Architecture}}")
	switch arch {
	case "aarch64":
		arch = "arm64"
	case "x86_64":
		arch = "amd64"
	}
	command := exec.Command("go", "build", "-o", filepath.Join(directory, "fixture"), source)
	command.Env = append(os.Environ(), "GOOS=linux", "GOARCH="+arch, "CGO_ENABLED=0")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatal(err, string(output))
	}
	if err := os.WriteFile(filepath.Join(directory, "Dockerfile"), []byte("FROM alpine:3.21\nCOPY fixture /fixture\nENTRYPOINT [\"/fixture\"]\n"), 0600); err != nil {
		t.Fatal(err)
	}
	label := fixtureName(t)
	image := label + ":test"
	// Registration precedes build: even a failed build may leave an image effect.
	t.Cleanup(func() {
		command := exec.Command("docker", "--host", endpoint, "image", "inspect", "--format", `{{index .Config.Labels "backlot.fixture"}}`, image)
		output, err := command.CombinedOutput()
		if err != nil {
			if !strings.Contains(string(output), "No such image") && !strings.Contains(string(output), "No such object") {
				t.Error("fixture image absence unverified; preserved", image, string(output))
			}
		} else {
			if strings.TrimSpace(string(output)) != label {
				t.Error("fixture image ownership changed; preserved", image)
				return
			}
			DockerCommand(t, endpoint, "image", "rm", image)
		}
	})
	DockerCommand(t, endpoint, "build", "--pull=false", "--label", "backlot.fixture="+label, "-t", image, directory)
	t.Log("owned static Go fixture image (pre-pulled alpine:3.21 base):", DockerCommand(t, endpoint, "image", "inspect", "--format", "{{.Id}} {{.Architecture}}", image))
	return image
}

// OwnedContainer registers ownership-checked cleanup before creating a container.
func OwnedContainer(t *testing.T, endpoint, image string, args ...string) string {
	t.Helper()
	name := fixtureName(t)
	t.Cleanup(func() {
		CleanupContainer(t, endpoint, name, name)
	})
	create := []string{"create", "--name", name, "--label", "backlot.fixture=" + name}
	create = append(create, args...)
	create = append(create, image)
	return DockerCommand(t, endpoint, create...)
}

func fixtureName(t *testing.T) string {
	t.Helper()
	var token [16]byte
	if _, err := rand.Read(token[:]); err != nil {
		t.Fatal(err)
	}
	return fmt.Sprintf("backlot-https-fixture-%x", token)
}

// CleanupContainer proves exact ownership before removal and reports uncertainty.
// The boolean lets callers retain diagnostic files when cleanup is unverified.
func CleanupContainer(t *testing.T, endpoint, name, owner string) bool {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "docker", "--host", endpoint, "inspect", "--format", `{{index .Config.Labels "backlot.fixture"}}`, name)
	output, err := command.CombinedOutput()
	if err != nil {
		if strings.Contains(string(output), "No such object") || strings.Contains(string(output), "No such container") {
			return true
		}
		t.Error("fixture container absence unverified; preserve diagnostic state", name, string(output))
		return false
	}
	if strings.TrimSpace(string(output)) != owner {
		t.Error("fixture container ownership changed; preserved", name)
		return false
	}
	command = exec.CommandContext(ctx, "docker", "--host", endpoint, "rm", "-f", "-v", name)
	if output, err := command.CombinedOutput(); err != nil {
		t.Error("fixture container cleanup failed; preserve diagnostic state", name, err, string(output))
		return false
	}
	return true
}
