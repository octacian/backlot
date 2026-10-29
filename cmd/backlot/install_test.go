//go:build darwin || linux

package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/gofrs/flock"
	v1 "github.com/octacian/backlot/api/v1"
	"github.com/octacian/backlot/internal/daemon"
	"go.yaml.in/yaml/v3"
)

type installFixture struct {
	t                                *testing.T
	root, script, old, target, asset string
	env                              []string
	retain                           bool
}

func installPut(t *testing.T, path string, data []byte, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, mode); err != nil {
		t.Fatal(err)
	}
}
func installArchive(t *testing.T, binary []byte, attack string) []byte {
	t.Helper()
	var data bytes.Buffer
	compressed := gzip.NewWriter(&data)
	archive := tar.NewWriter(compressed)
	for _, name := range []string{"LICENSE", "THIRD_PARTY_NOTICES.md", "backlot"} {
		body := []byte("fixture notices")
		if name == "backlot" {
			body = binary
		}
		header := &tar.Header{Name: name, Mode: 0755, Size: int64(len(body)), Typeflag: tar.TypeReg}
		if name == "backlot" {
			switch attack {
			case "traversal":
				header.Name = "../backlot"
			case "absolute":
				header.Name = "/backlot"
			case "symlink":
				header.Typeflag = tar.TypeSymlink
				header.Linkname = "/bin/sh"
				header.Size = 0
				body = nil
			case "hardlink":
				header.Typeflag = tar.TypeLink
				header.Linkname = "LICENSE"
				header.Size = 0
				body = nil
			case "directory":
				header.Typeflag = tar.TypeDir
				header.Size = 0
				body = nil
			case "missing":
				continue
			}
		}
		if err := archive.WriteHeader(header); err != nil {
			t.Fatal(err)
		}
		if _, err := archive.Write(body); err != nil {
			t.Fatal(err)
		}
		if attack == "duplicate" && name == "backlot" {
			if err := archive.WriteHeader(header); err != nil {
				t.Fatal(err)
			}
			if _, err := archive.Write(body); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	if err := compressed.Close(); err != nil {
		t.Fatal(err)
	}
	return data.Bytes()
}
func newInstallFixture(t *testing.T, old, target string, asset []byte) *installFixture {
	t.Helper()
	root, err := os.MkdirTemp("/tmp", "bl-install-")
	if err != nil {
		t.Fatal(err)
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	var f *installFixture
	t.Cleanup(func() {
		if f != nil && f.retain {
			t.Log("preserved uncertain fixture", root)
			return
		}
		if err := os.RemoveAll(root); err != nil {
			t.Error(err)
		}
	})
	script, err := filepath.Abs("../../install.sh")
	if err != nil {
		t.Fatal(err)
	}
	f = &installFixture{t: t, root: root, script: script, old: old, target: target, asset: "backlot_v0.4.0_" + runtime.GOOS + "_" + runtime.GOARCH + ".tar.gz"}
	f.putAsset(asset)
	f.write("tools/curl", `#!/bin/sh
set -eu
printf '%s\n' "$*" >> "$FIXTURE_ROOT/calls"
out= url=
while [ "$#" -gt 0 ]; do
 case $1 in
 --proto|--connect-timeout|--max-time|-w) shift 2;;
 -o) out=$2; shift 2;;
 --*) shift;;
 *) url=$1; shift;;
 esac
done
case $url in
 */latest) printf '%s' "https://github.com/octacian/backlot/releases/tag/${FIXTURE_LATEST:-v0.4.0}";;
 *)
 if [ "${FIXTURE_FAILURE:-}" = interrupt ]; then touch "$FIXTURE_ROOT/interrupted"; sleep 0.2; exit 1; fi
 [ "${FIXTURE_FAILURE:-}" != download ] || exit 22
 cp "$FIXTURE_ROOT/assets/${url##*/}" "$out";;
esac
`)
	f.write("tools/uname", fmt.Sprintf("#!/bin/sh\nif [ -n \"${FIXTURE_UNSUPPORTED:-}\" ]; then echo unsupported; elif [ \"$1\" = -s ]; then echo %s; else echo %s; fi\n", map[string]string{"darwin": "Darwin", "linux": "Linux"}[runtime.GOOS], map[string]string{"arm64": "arm64", "amd64": "x86_64"}[runtime.GOARCH]))
	f.env = append(os.Environ(), "HOME="+root+"/home", "XDG_CONFIG_HOME="+root+"/config", "TMPDIR="+root+"/temp", "FIXTURE_ROOT="+root, "PATH="+root+"/tools:"+os.Getenv("PATH"))
	for _, dir := range []string{"home", "config", "temp", "bin"} {
		if err := os.Mkdir(filepath.Join(root, dir), 0700); err != nil {
			t.Fatal(err)
		}
	}
	return f
}
func (f *installFixture) write(name, body string) {
	f.t.Helper()
	installPut(f.t, filepath.Join(f.root, name), []byte(body), 0700)
}
func (f *installFixture) putAsset(data []byte) {
	f.t.Helper()
	installPut(f.t, filepath.Join(f.root, "assets", f.asset), data, 0600)
	f.write("assets/backlot_v0.4.0_checksums.txt", fmt.Sprintf("%x  %s\n", sha256.Sum256(data), f.asset))
}
func (f *installFixture) args(extra ...string) []string {
	return append([]string{f.script, "--destination", f.root + "/bin/backlot", "--state-dir", f.root + "/state"}, extra...)
}
func (f *installFixture) run(ok bool, extra ...string) string {
	f.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "sh", f.args(extra...)...)
	command.Env = f.env
	command.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	var output []byte
	var err error
	if strings.Contains(strings.Join(f.env, "\n"), "FIXTURE_FAILURE=interrupt") {
		var buffer bytes.Buffer
		command.Stdout = &buffer
		command.Stderr = &buffer
		if err = command.Start(); err != nil {
			f.t.Fatal(err)
		}
		deadline := time.Now().Add(5 * time.Second)
		for {
			if _, err := os.Stat(f.root + "/interrupted"); err == nil {
				break
			}
			if time.Now().After(deadline) {
				_ = command.Process.Kill()
				_ = command.Wait()
				f.t.Fatal("interruption fixture never reached download")
			}
			time.Sleep(10 * time.Millisecond)
		}
		if err := command.Process.Signal(syscall.SIGTERM); err != nil {
			f.t.Fatal(err)
		}
		err = command.Wait()
		output = buffer.Bytes()
	} else {
		output, err = command.CombinedOutput()
	}
	if (err == nil) != ok {
		f.t.Fatalf("installer error=%v expected success=%t: %s", err, ok, output)
	}
	for _, pattern := range []string{f.root + "/temp/backlot-install.*", f.root + "/bin/.backlot-install.*"} {
		matches, err := filepath.Glob(pattern)
		if err != nil || len(matches) != 0 {
			f.t.Fatalf("temporary files retained: %v %v", matches, err)
		}
	}
	return string(output)
}
func (f *installFixture) installOld() {
	f.t.Helper()
	data, err := os.ReadFile(f.old)
	if err != nil {
		f.t.Fatal(err)
	}
	installPut(f.t, f.root+"/bin/backlot", data, 0755)
}
func (f *installFixture) preserved() {
	f.t.Helper()
	a, err := os.ReadFile(f.old)
	if err != nil {
		f.t.Fatal(err)
	}
	b, err := os.ReadFile(f.root + "/bin/backlot")
	if err != nil || !bytes.Equal(a, b) {
		f.t.Fatal("old executable not preserved", err)
	}
}
func TestInstaller(t *testing.T) {
	t.Setenv("GORACE", "atexit_sleep_ms=0")
	binaries := t.TempDir()
	paths := []string{}
	for _, version := range []string{"v0.3.0", "v0.4.0"} {
		path := filepath.Join(binaries, version)
		command := exec.Command("go", "build", "-race", "-buildvcs=false", "-ldflags=-X main.version="+version, "-o", path, ".")
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("build: %v %s", err, output)
		}
		paths = append(paths, path)
	}
	data, err := os.ReadFile(paths[1])
	if err != nil {
		t.Fatal(err)
	}
	asset := installArchive(t, data, "")
	fixture := func(t *testing.T) *installFixture { return newInstallFixture(t, paths[0], paths[1], asset) }
	t.Run("defaults", func(t *testing.T) {
		f := fixture(t)
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		command := exec.CommandContext(ctx, "sh", f.script)
		command.Env = f.env
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatal(err, string(output))
		}
		if !bytes.Equal(mustInstallRead(t, f.root+"/home/.local/bin/backlot"), data) {
			t.Fatal("default destination mismatch")
		}
	})

	t.Run("fresh-update-repeat-pins-PATH", func(t *testing.T) {
		f := fixture(t)
		if out := f.run(true, "--version", "v0.4.0"); !strings.Contains(out, "PATH selects") {
			t.Fatal(out)
		}
		f.run(true)
		if out := f.run(false, "--version", "v0.4.0"); !strings.Contains(out, "fresh-install only") {
			t.Fatal(out)
		}
		f.installOld()
		f.run(true)
		actual, err := os.ReadFile(f.root + "/bin/backlot")
		if err != nil || !bytes.Equal(actual, data) {
			t.Fatal("update did not replace", err)
		}
	})
	for _, mode := range []string{"download", "interrupt", "corrupt", "missing-checksum", "duplicate-checksum", "bad-checksum", "extra-checksum-field", "missing-asset", "unsupported", "invalid-latest", "invalid-tag", "newline-tag", "duplicate-option", "relative-destination", "symlink-destination", "unsafe-parent", "unknown-existing", "traversal", "absolute", "symlink", "hardlink", "directory", "missing", "duplicate", "helperless", "identity-mismatch"} {
		t.Run(mode, func(t *testing.T) {
			f := fixture(t)
			f.installOld()
			extra := []string{}
			switch mode {
			case "download", "interrupt":
				f.env = append(f.env, "FIXTURE_FAILURE="+mode)
			case "corrupt":
				installPut(t, f.root+"/assets/"+f.asset, []byte("corrupt"), 0600)
			case "missing-checksum":
				f.write("assets/backlot_v0.4.0_checksums.txt", "")
			case "duplicate-checksum":
				body, _ := os.ReadFile(f.root + "/assets/backlot_v0.4.0_checksums.txt")
				f.write("assets/backlot_v0.4.0_checksums.txt", string(body)+string(body))
			case "bad-checksum":
				f.write("assets/backlot_v0.4.0_checksums.txt", strings.Repeat("z", 64)+"  "+f.asset+"\n")
			case "extra-checksum-field":
				f.write("assets/backlot_v0.4.0_checksums.txt", fmt.Sprintf("%x  %s extra\n", sha256.Sum256(asset), f.asset))
			case "missing-asset":
				if err := os.Remove(f.root + "/assets/" + f.asset); err != nil {
					t.Fatal(err)
				}
			case "unsupported":
				f.env = append(f.env, "FIXTURE_UNSUPPORTED=1")
			case "invalid-latest":
				f.env = append(f.env, "FIXTURE_LATEST=v0.4.0-rc1")
			case "invalid-tag", "newline-tag":
				extra = []string{"--version", "v0.04.0"}
				if mode == "newline-tag" {
					extra[1] = "v0.4.0\nv0.5.0"
				}
				if err := os.Remove(f.root + "/bin/backlot"); err != nil {
					t.Fatal(err)
				}
			case "duplicate-option":
				extra = []string{"--state-dir", f.root + "/state"}
			case "relative-destination":
				command := exec.Command("sh", f.script, "--destination", "relative")
				command.Env = f.env
				output, err := command.CombinedOutput()
				if err == nil || !bytes.Contains(output, []byte("destination must be absolute")) {
					t.Fatal(err, string(output))
				}
				f.preserved()
				return
			case "symlink-destination":
				if err := os.Rename(f.root+"/bin/backlot", f.root+"/saved"); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(f.root+"/saved", f.root+"/bin/backlot"); err != nil {
					t.Fatal(err)
				}
			case "unsafe-parent":
				if err := os.Chmod(f.root+"/bin", 0777); err != nil {
					t.Fatal(err)
				}
			case "unknown-existing":
				f.write("bin/backlot", "#!/bin/sh\ntouch '"+f.root+"/executed'\n")
			case "identity-mismatch":
				f.putAsset(installArchive(t, mustInstallRead(t, f.old), ""))
			case "helperless":
				f.putAsset(installArchive(t, []byte("#!/bin/sh\nexit 1\n"), ""))
			default:
				f.putAsset(installArchive(t, []byte("#!/bin/sh\ntouch '"+f.root+"/asset-executed'\nexit 1\n"), mode))
			}
			f.run(false, extra...)
			if _, err := os.Stat(f.root + "/asset-executed"); !os.IsNotExist(err) {
				t.Fatal("unsafe archive executable ran before rejection")
			}
			switch mode {
			case "invalid-tag", "newline-tag":
				if _, err := os.Stat(f.root + "/state"); !os.IsNotExist(err) {
					t.Fatal("created state on invalid argument")
				}
			case "unknown-existing":
				if _, err := os.Stat(f.root + "/executed"); !os.IsNotExist(err) {
					t.Fatal("unknown executable ran")
				}
			default:
				f.preserved()
			}
		})
	}
	t.Run("handoff", func(t *testing.T) { testInstallHandoff(t, fixture) })
	t.Run("terminal", func(t *testing.T) { testInstallTerminal(t, fixture) })
}

func testInstallHandoff(t *testing.T, fixture func(*testing.T) *installFixture) {
	for _, mode := range []string{"success", "cancel", "status-invalid", "stop-failed", "stop-lost", "lock-retained", "stage-changed", "destination-changed", "rename-denied", "downgrade", "context-cancel", "no-socket", "socket-without-lock", "lock-changed"} {
		t.Run(mode, func(t *testing.T) {
			f := fixture(t)
			f.installOld()
			stage := f.root + "/bin/.stage/backlot"
			data, err := os.ReadFile(f.target)
			if err != nil {
				t.Fatal(err)
			}
			installPut(t, stage, data, 0755)
			if mode == "downgrade" {
				data, err = os.ReadFile(f.old)
				if err != nil {
					t.Fatal(err)
				}
				installPut(t, stage, data, 0755)
				installPut(t, f.root+"/bin/backlot", mustInstallRead(t, f.target), 0755)
			}
			state := f.root + "/state"
			if err := os.Mkdir(state, 0700); err != nil {
				t.Fatal(err)
			}
			lock := flock.New(state+"/daemon.lock", flock.SetPermissions(0600))
			held, err := lock.TryLock()
			if err != nil || !held {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := lock.Close(); err != nil {
					t.Error(err)
				}
			})
			listener, err := net.Listen("unix", daemon.SocketPath(state))
			if err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(daemon.SocketPath(state), 0600); err != nil {
				t.Fatal(err)
			}
			stop := make(chan struct{}, 1)
			server := &http.Server{ReadHeaderTimeout: time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("X-Backlot-API-Version", v1.Version)
				status := v1.DaemonStatusResponse{APIVersion: v1.Version, StateVersion: v1.StateVersion, Status: "running", PID: 42, LeaseDuration: "30s"}
				var result any = status
				switch r.URL.Path {
				case "/v1/status":
					if mode == "status-invalid" {
						status.PID = 0
						result = status
					}
				case "/v1/instances":
					result = v1.ListResponse{APIVersion: v1.Version, Instances: []v1.InstanceSummary{}}
				case "/v1/stop":
					switch mode {
					case "stop-failed":
						w.WriteHeader(500)
						result = v1.ErrorResponse{APIVersion: v1.Version, Error: v1.PlanError{Code: "cleanup_failed", Message: "owned fixture refusal"}}
					case "stop-lost":
						connection, _, err := w.(http.Hijacker).Hijack()
						if err == nil {
							_ = connection.Close()
						}
						return
					default:
						status.Status = "stopping"
						result = status
						if mode != "lock-retained" {
							defer func() { stop <- struct{}{} }()
						}
					}
				default:
					t.Errorf("unexpected endpoint %s", r.URL.Path)
					w.WriteHeader(404)
				}
				body, err := json.Marshal(result)
				if err != nil {
					t.Error(err)
					return
				}
				w.Header().Set("Content-Length", fmt.Sprint(len(body)))
				if _, err := w.Write(body); err != nil {
					t.Error(err)
				}
			})}
			joined := make(chan struct{})
			go func() { defer close(joined); _ = server.Serve(listener) }()
			ownerJoined := make(chan struct{})
			done := make(chan struct{})
			go func() {
				defer close(ownerJoined)
				select {
				case <-stop:
					time.Sleep(20 * time.Millisecond)
					_ = server.Close()
					_ = lock.Close()
				case <-done:
				}
			}()
			t.Cleanup(func() { close(done); _ = server.Close(); <-joined; <-ownerJoined })
			budget := 10 * time.Second
			if mode == "lock-retained" {
				budget = 2 * time.Second
			}
			ctx, cancel := context.WithTimeout(context.Background(), budget)
			defer cancel()
			if mode == "no-socket" {
				_ = server.Close()
			}
			if mode == "socket-without-lock" {
				_ = lock.Close()
			}
			consented := false
			version := "v0.4.0"
			if mode == "downgrade" {
				version = "v0.3.0"
			}
			err = daemon.Install(ctx, daemon.InstallOptions{Directory: state, Stage: stage, Destination: f.root + "/bin/backlot", Version: version, Consent: func([]v1.Instance) error {
				consented = true
				contender := flock.New(state + "/daemon.lock")
				acquired, err := contender.TryLock()
				closeErr := contender.Close()
				if err != nil || closeErr != nil || acquired {
					t.Error("concurrent state ownership escaped", err, closeErr)
				}
				f.preserved()
				switch mode {
				case "context-cancel":
					cancel()
				case "lock-changed":
					if err := os.Rename(state+"/daemon.lock", state+"/saved.lock"); err != nil {
						t.Fatal(err)
					}
					installPut(t, state+"/daemon.lock", nil, 0600)
				case "cancel":
					return fmt.Errorf("cancelled")
				case "stage-changed":
					installPut(t, stage, []byte("changed"), 0755)
				case "destination-changed":
					installPut(t, f.root+"/bin/backlot", []byte("changed"), 0755)
				case "rename-denied":
					if err := os.Chmod(f.root+"/bin", 0500); err != nil {
						t.Fatal(err)
					}
					defer t.Cleanup(func() { _ = os.Chmod(f.root+"/bin", 0700) })
				}
				return nil
			}})
			if (err == nil) != (mode == "success") {
				t.Fatalf("handoff %s: %v", mode, err)
			}
			if mode == "status-invalid" || mode == "downgrade" || mode == "no-socket" || mode == "socket-without-lock" {
				if consented {
					t.Fatal("consent reached before valid identity/status")
				}
			}
			if mode == "success" {
				if !bytes.Equal(mustInstallRead(t, f.root+"/bin/backlot"), mustInstallRead(t, f.target)) {
					t.Fatal("target not replaced")
				}
			} else if mode != "destination-changed" && mode != "downgrade" {
				f.preserved()
			}
		})
	}
}
func mustInstallRead(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func testInstallTerminal(t *testing.T, fixture func(*testing.T) *installFixture) {
	for _, answer := range []string{"n\n", "l\nn\n", "l\ny\n", "pipe", "directTERM", "groupCtrlC"} {
		t.Run(strings.ReplaceAll(answer, "\n", "-"), func(t *testing.T) {
			f := fixture(t)
			f.installOld()
			if err := os.Mkdir(f.root+"/state", 0700); err != nil {
				t.Fatal(err)
			}
			var daemonLog bytes.Buffer
			child := exec.Command(f.root+"/bin/backlot", "daemon", "serve", "--state-dir", f.root+"/state")
			child.Env = f.env
			child.Stdout = &daemonLog
			child.Stderr = &daemonLog
			if err := child.Start(); err != nil {
				t.Fatal(err)
			}
			joined := make(chan error, 1)
			go func() { joined <- child.Wait() }()
			t.Cleanup(func() {
				stop := exec.Command(f.old, "daemon", "stop", "--state-dir", f.root+"/state", "--json")
				stop.Env = f.env
				_ = stop.Run()
				select {
				case <-joined:
				case <-time.After(5 * time.Second):
					_ = child.Process.Kill()
					<-joined
					t.Error("daemon fixture required retained-child kill")
				}
			})
			deadline := time.Now().Add(5 * time.Second)
			for {
				if _, err := os.Stat(daemon.SocketPath(f.root + "/state")); err == nil {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("daemon startup timeout")
				}
				time.Sleep(20 * time.Millisecond)
			}
			if answer == "pipe" {
				if out := f.run(false); !strings.Contains(out, "controlling terminal") {
					t.Fatal(out)
				}
				f.preserved()
				return
			}
			master, slave := installPTY(t)
			defer func() { _ = master.Close() }()
			defer func() { _ = slave.Close() }()
			command := exec.Command("sh", f.args()...)
			command.Env = f.env
			command.Stdin = slave
			if answer == "l\ny\n" {
				command = exec.Command("sh", append([]string{"-s", "--"}, f.args()[1:]...)...)
				command.Env = f.env
				command.Stdin = strings.NewReader(string(mustInstallRead(t, f.script)))
			}
			command.Stdout = slave
			command.Stderr = slave
			command.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 1}
			if err := command.Start(); err != nil {
				t.Fatal(err)
			}
			commandJoined := make(chan struct{})
			var commandErr error
			go func() { commandErr = command.Wait(); close(commandJoined) }()
			defer func() {
				select {
				case <-commandJoined:
					return
				default:
				}
				_ = command.Process.Signal(syscall.SIGTERM)
				_, _ = master.Write([]byte("n\n"))
				select {
				case <-commandJoined:
				case <-time.After(5 * time.Second):
					f.retain = true
					t.Error("installer did not join; preserve fixture", f.root)
				}
			}()
			output := make(chan string, 1)
			prompt := make(chan struct{})
			go func() {
				var body bytes.Buffer
				chunk := make([]byte, 1024)
				notified := false
				for {
					n, err := master.Read(chunk)
					body.Write(chunk[:n])
					if !notified && strings.Contains(body.String(), "Stop daemon and install?") {
						close(prompt)
						notified = true
					}
					if err != nil {
						output <- body.String()
						return
					}
				}
			}()
			select {
			case <-prompt:
			case <-time.After(10 * time.Second):
				t.Fatal("installer never prompted")
			}
			// Consent still comes from /dev/tty when shell input is piped.
			if answer == "directTERM" {
				if err := command.Process.Signal(syscall.SIGTERM); err != nil {
					t.Fatal(err)
				}
			} else {
				input := answer
				if answer == "groupCtrlC" {
					input = "\x03" // Terminal delivery differs from signalling only the shell.
				}
				if _, err := master.Write([]byte(input)); err != nil {
					t.Fatal(err)
				}
			}
			select {
			case <-commandJoined:
				if (commandErr == nil) != (answer == "l\ny\n") {
					t.Fatal("tty installer", commandErr)
				}
			case <-time.After(20 * time.Second):
				t.Fatal("tty installer timed out")
			}
			_ = slave.Close()
			text := <-output
			if !strings.Contains(text, "0 running/kept instances; 0 unresolved") || strings.HasPrefix(answer, "l\n") && !strings.Contains(text, "[y/n]") {
				t.Fatal(text)
			}
			if answer != "l\ny\n" {
				f.preserved()
			}
		})
	}
}

func TestInstallerShellExitStatus(t *testing.T) {
	for _, status := range []int{0, 23} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			helper := []byte(fmt.Sprintf("#!/bin/sh\n[ \"$2\" != --help ] || exit 0\nexit %d\n", status))
			f := newInstallFixture(t, "", "", installArchive(t, helper, ""))
			command := exec.Command("sh", f.args()...)
			command.Env = f.env
			output, err := command.CombinedOutput()
			if command.ProcessState == nil || command.ProcessState.ExitCode() != status {
				t.Fatalf("helper status %d lost: %v %s", status, err, output)
			}
		})
	}
}

// The mock holds cancellation open so the parent must join before deleting its files.
func TestInstallerShellCancellation(t *testing.T) {
	for _, signal := range []syscall.Signal{syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP} {
		t.Run(signal.String(), func(t *testing.T) {
			helper := []byte(`#!/bin/sh
set -eu
[ "$2" != --help ] || exit 0
stage=$3
cancelled=0
trap 'cancelled=1; printf cancelled > "$FIXTURE_ROOT/cancelled"' TERM
printf ready > "$FIXTURE_ROOT/ready"
while [ ! -f "$FIXTURE_ROOT/answer" ]; do sleep 0.02; done
if [ "$cancelled" = 1 ]; then
    [ -f "$stage" ] && [ -f "$0" ] || exit 24
    printf joined > "$FIXTURE_ROOT/joined"
    exit 23
fi
if [ "$(cat "$FIXTURE_ROOT/answer")" = y ]; then printf effect > "$FIXTURE_ROOT/effect"; fi
`)
			f := newInstallFixture(t, "", "", installArchive(t, helper, ""))
			master, slave := installPTY(t)
			t.Cleanup(func() { _ = master.Close(); _ = slave.Close() })
			command := exec.Command("sh", f.args()...)
			command.Env = f.env
			command.Stdin, command.Stdout, command.Stderr = slave, slave, slave
			command.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 1}
			if err := command.Start(); err != nil {
				t.Fatal(err)
			}
			joined := make(chan struct{})
			var result error
			go func() { result = command.Wait(); close(joined) }()
			// Release the helper on failure, then join before fixture deletion.
			t.Cleanup(func() {
				f.write("answer", "n")
				select {
				case <-joined:
				case <-time.After(5 * time.Second):
					f.retain = true
					t.Error("installer did not join its helper; preserve fixture", f.root)
				}
			})
			waitFile := func(name string) {
				t.Helper()
				deadline := time.Now().Add(3 * time.Second)
				for {
					if _, err := os.Stat(f.root + "/" + name); err == nil {
						return
					}
					if time.Now().After(deadline) {
						t.Fatal("helper never reached", name)
					}
					time.Sleep(10 * time.Millisecond)
				}
			}
			waitFile("ready")
			if err := command.Process.Signal(signal); err != nil {
				t.Fatal(err)
			}
			waitFile("cancelled")
			select {
			case <-joined:
				t.Fatal("shell exited before helper joined", result)
			case <-time.After(50 * time.Millisecond):
			}
			// A later y releases cancellation, never authorizes an effect.
			f.write("answer", "y")
			select {
			case <-joined:
				if result == nil {
					t.Fatal("cancelled shell succeeded")
				}
			case <-time.After(5 * time.Second):
				t.Fatal("shell did not join cancelled helper")
			}
			waitFile("joined")
			if _, err := os.Stat(f.root + "/effect"); !os.IsNotExist(err) {
				t.Fatal("helper accepted consent after cancellation", err)
			}
			for _, pattern := range []string{"/temp/backlot-install.*", "/bin/.backlot-install.*"} {
				matches, err := filepath.Glob(f.root + pattern)
				if err != nil || len(matches) != 0 {
					t.Fatal("temporary files retained", matches, err)
				}
			}
		})
	}
}

func TestInstallerSelectsRepositoryGates(t *testing.T) {
	data := mustInstallRead(t, "../../.github/workflows/changes.yml")
	var workflow struct {
		Jobs map[string]struct {
			Steps []struct{ With map[string]string }
		}
	}
	if err := yaml.Unmarshal(data, &workflow); err != nil {
		t.Fatal(err)
	}
	for _, step := range workflow.Jobs["paths"].Steps {
		if filters := step.With["filters"]; filters != "" {
			var groups map[string][]string
			if err := yaml.Unmarshal([]byte(filters), &groups); err != nil {
				t.Fatal(err)
			}
			for _, path := range groups["implementation"] {
				if path == "install.sh" {
					return
				}
			}
		}
	}
	t.Fatal("installer changes must select implementation gates on both supported platforms")
}
