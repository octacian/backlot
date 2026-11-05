//go:build darwin || linux

package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	v1 "github.com/octacian/backlot/api/v1"
)

func composeFormat(template string, values ...v1.Value) v1.Value {
	return v1.Value{Format: &v1.FormatValue{Template: template, Values: values}}
}
func composeEndpoint(host, port v1.Value) v1.Value {
	return v1.Value{HostPort: &v1.HostPortValue{Host: host, Port: port}}
}

func composeFile(base string, parts ...string) v1.Value {
	values := make([]v1.Value, len(parts))
	for i, part := range parts {
		values[i] = fixtureLiteral(part)
	}
	return v1.Value{FilePath: &v1.FilePathValue{Base: fixtureLiteral(base), Parts: values}}
}
func composeURL(base v1.Value, parts ...string) v1.Value {
	values := make([]v1.Value, len(parts))
	for i, part := range parts {
		values[i] = fixtureLiteral(part)
	}
	return v1.Value{URLPath: &v1.URLPathValue{Base: base, Parts: values}}
}

func composeCLI(t *testing.T, h *nativeHarness, config, scene string, want int) v1.InstanceResponse {
	t.Helper()
	command := exec.Command(h.binary, "run", scene, "--project", h.project, "--config", config, "--state-dir", h.dir, "--startup-timeout", "10s", "--job-timeout", "10s", "--stop-grace", "100ms", "--json", "--", "forward %s $HOME a b")
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	err := command.Run()
	code := 0
	if err != nil {
		if e, ok := err.(*exec.ExitError); ok {
			code = e.ExitCode()
		} else {
			t.Fatal(err)
		}
	}
	var response v1.InstanceResponse
	if json.Unmarshal(stdout.Bytes(), &response) != nil || code != want || response.Instance.Execution == nil || response.Instance.Execution.TerminalExitCode == nil || *response.Instance.Execution.TerminalExitCode != want {
		t.Fatalf("CLI code=%d want=%d err=%v output=%s stderr=%s", code, want, err, stdout.Bytes(), stderr.Bytes())
	}
	if response.Instance.Execution.CleanupFailure != "" {
		t.Fatal("cleanup", response.Instance.Execution.CleanupFailure)
	}
	return response
}

func TestComposeEarlyFlow(t *testing.T) {
	binary := buildFixtureBinary(t, t.TempDir(), "../../cmd/backlot")
	t.Setenv("BACKLOT_PROJECT_CONFIG_DIR", filepath.Join(shortTemp(t), "registry"))
	t.Run("native", func(t *testing.T) {
		h := newNativeHarness(t, binary)
		config := filepath.Join(filepath.Dir(h.project), "machine.json")
		if err := os.WriteFile(config, []byte(`{"version":"backlot/v1"}`), 0600); err != nil {
			t.Fatal(err)
		}
		port := fixtureRef("resource", "port", "port")
		endpoint := composeEndpoint(fixtureLiteral("127.0.0.1"), port)
		target := composeURL(composeFormat("http://%s", endpoint), "private/", "", "/ready")
		path := composeFile("/base", "..", "outside", "space 雪")
		privatePath := path
		privatePath.Secret = true
		server := h.manifest.Components["server"]
		server.Environment.Assign["PORT"] = composeFormat("%s", port)
		privateTarget := target
		privateTarget.Secret = true
		server.Readiness = &v1.Probe{Kind: "http", Target: &privateTarget, Timeout: "5s"}
		h.manifest.Components["server"] = server
		h.manifest.Tools["sh"] = "sh"
		special := "space %s %% $HOME & ? = 雪"
		script := `test "$1" = "$PORT" && test "$2" = "$URL" && test "$3" = "$SPECIAL" && test "$4" = "$FILE" && test "$FILE" = '/outside/space 雪' && test "$PRIVATE" = "$FILE" && test "$5" = 'forward %s $HOME a b' || exit 29; printf 'resolved-port=%s resolved-url=%s special=%s\n' "$1" "$2" "$3"; exit "$EXIT"`
		job := h.manifest.Components["job"]
		job.Command = &v1.Command{Tool: "sh", Args: append(v1.StringArguments("-c", script, "fixture"), v1.Argument{Value: port}, v1.Argument{Value: target}, v1.Argument{Value: composeFormat("%s", fixtureLiteral(special))}, v1.Argument{Value: path})}
		job.Environment.Assign["FILE"] = path
		job.Environment.Assign["PRIVATE"] = composeFormat("%s", privatePath)
		job.Environment.Assign["URL"] = target
		job.Environment.Assign["SPECIAL"] = fixtureLiteral(special)
		job.Environment.Assign["EXIT"] = fixtureLiteral("0")
		h.manifest.Components["job"] = job
		h.write()
		job.Environment.Assign["INVALID"] = composeFile("private-canary\x00", "..", "safe")
		h.manifest.Components["job"] = job
		h.write()
		output, err := exec.Command(h.binary, "run", "test", "--project", h.project, "--config", config, "--state-dir", h.dir, "--json").CombinedOutput()
		if err == nil || strings.Contains(string(output), "private-canary") {
			t.Fatalf("invalid operand not rejected safely: %v %s", err, output)
		}
		listed, err := h.cli.List(context.Background(), v1.ListRequest{APIVersion: v1.Version})
		if err != nil || len(listed.Instances) != 0 {
			t.Fatal("invalid path had instance effects", listed, err)
		}
		delete(job.Environment.Assign, "INVALID")
		h.manifest.Components["job"] = job
		h.write()
		t.Log("NUL in a cleaned-away filesystem operand rejected before instance effects without exposing its canary")
		success := composeCLI(t, h, config, "test", 0)
		if value := success.Instance.Plan.Components[0].Readiness.Target; value == nil || !value.Redacted || value.Literal != nil || value.Composition != nil || value.Symbolic != nil {
			t.Fatal("sensitive readiness exposed", value)
		}
		t.Logf("real native CLI success: port=%d target=http://127.0.0.1:%d/private/ready exact special argv and forwarding verified", success.Instance.Execution.Ports["port"], success.Instance.Execution.Ports["port"])
		job.Environment.Assign["EXIT"] = fixtureLiteral("17")
		h.manifest.Components["job"] = job
		h.write()
		failure := composeCLI(t, h, config, "test", 17)
		t.Logf("real native CLI application failure: original_exit=%d cleanup=%q", *failure.Instance.Execution.TerminalExitCode, failure.Instance.Execution.CleanupFailure)
	})
	t.Run("container", func(t *testing.T) {
		endpoint := os.Getenv("BACKLOT_DOCKER_TEST_ENDPOINT")
		if endpoint == "" {
			t.Skip("requires explicit local Docker endpoint and cached alpine:3.21")
		}
		h := newDockerHarness(t, binary, endpoint)
		// Existing harness registers authenticated journal cleanup before CLI effects.
		port := fixtureRef("service", "server", "port")
		port.Ref.Port = "http"
		host := fixtureRef("service", "server", "host")
		target := composeURL(composeFormat("http://%s", composeEndpoint(host, port)), "private", "ready")
		path := composeFile("/base", "..", "outside", "space 雪")
		privatePath := path
		privatePath.Secret = true
		daemonTarget := composeURL(composeFormat("http://%s", composeEndpoint(fixtureLiteral("127.0.0.1"), fixtureRef("resource", "port", "port"))), "private", "ready")
		server := v1.Component{Kind: v1.Service, Runtime: v1.Container, Image: ptrValue(fixtureLiteral("alpine:3.21")), Args: v1.StringArguments("sh", "-ec", "while true; do printf 'HTTP/1.1 200 OK\\r\\nContent-Length: 6\\r\\nConnection: close\\r\\n\\r\\nready\\n' | nc -l -p 8080; done"), Resources: []string{"port"}, Ports: map[string]v1.ServicePort{"http": {Resource: "port", ContainerPort: 8080}}, Readiness: &v1.Probe{Kind: "http", Target: &daemonTarget, Timeout: "10s"}}
		script := `test "$1" = "$URL" && test "$2" = 'space %s $HOME a b' && test "$3" = "$FILE" && test "$FILE" = '/outside/space 雪' && test "$PRIVATE" = "$FILE" && test "$4" = 'forward %s $HOME a b' || exit 29; test "$(wget -qO- "$1")" = ready || exit 30; printf 'container-url=%s\n' "$1"; exit "$EXIT"`
		job := v1.Component{Kind: v1.Job, Runtime: v1.Container, Image: ptrValue(fixtureLiteral("alpine:3.21")), Policy: v1.EachStart, Args: append(v1.StringArguments("sh", "-ec", script, "fixture"), v1.Argument{Value: target}, v1.Argument{Value: composeFormat("space %%s %s", fixtureLiteral("$HOME a b"))}, v1.Argument{Value: path}), Environment: v1.Environment{Assign: map[string]v1.Value{"URL": target, "EXIT": fixtureLiteral("0"), "FILE": path, "PRIVATE": composeFormat("%s", privatePath)}}, DependsOn: []v1.Dependency{{Component: "server", Condition: v1.Ready}}}
		h.manifest.Components = map[string]v1.Component{"server": server, "job": job}
		h.manifest.Resources = map[string]v1.Resource{"port": {Kind: "port"}}
		h.manifest.Scenes = map[string]v1.Scene{"test": {Lifetime: v1.Disposable, Components: []string{"server", "job"}, Resources: []string{"port"}, TerminalJob: "job"}}
		h.write()
		success := composeCLI(t, h.nativeHarness, h.config, "test", 0)
		t.Logf("real container CLI success: URL=http://server:8080/private/ready mapped_host_port=%s exact argv and forwarding verified", strconv.Itoa(success.Instance.Execution.Ports["port"]))
		job.Environment.Assign["EXIT"] = fixtureLiteral("17")
		h.manifest.Components["job"] = job
		h.write()
		failure := composeCLI(t, h.nativeHarness, h.config, "test", 17)
		t.Logf("real container CLI application failure: original_exit=%d cleanup=%q", *failure.Instance.Execution.TerminalExitCode, failure.Instance.Execution.CleanupFailure)
		// The parent receives private endpoints; its host-native command probe receives host endpoints.
		h.manifest.Tools = map[string]string{"sh": "sh"}
		server.Environment = v1.Environment{Assign: map[string]v1.Value{"URL": target, "PORT": port, "FILE": path, "PRIVATE": composeFormat("%s", privatePath)}}
		server.Readiness = &v1.Probe{Kind: "command", Timeout: "10s", Command: &v1.Command{Tool: "sh", Args: append(v1.StringArguments("-c", `test "$FILE" = "$2" && test "$PRIVATE" = "$FILE" && test "$FILE" = '/outside/space 雪' && test "$PORT" != 8080 && test "$URL" = "$1" && test "$URL" = "http://127.0.0.1:$PORT/private/ready"`, "probe"), v1.Argument{Value: target}, v1.Argument{Value: path})}}
		server.Args = append(v1.StringArguments("sh", "-ec", `test "$FILE" = "$1" && test "$PRIVATE" = "$FILE" || exit 31; while true; do printf 'HTTP/1.1 200 OK\r\nContent-Length: 6\r\nConnection: close\r\n\r\nready\n' | nc -l -p 8080; done`, "server"), v1.Argument{Value: path})
		h.manifest.Components["server"] = server
		job.Environment.Assign["EXIT"] = fixtureLiteral("0")
		h.manifest.Components["job"] = job
		h.write()
		composeCLI(t, h.nativeHarness, h.config, "test", 0)
		t.Log("real container parent host-native probe received host env and argv endpoints; container job retained private endpoints")

	})
}
