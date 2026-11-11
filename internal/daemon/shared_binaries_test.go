//go:build darwin || linux

package daemon

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

// Only immutable executables are process-owned; scenarios own all runtime state.
var sharedSQLBinaries sharedSQLBinaryBundle

type sharedSQLBinaryBundle struct {
	once                                  sync.Once
	directory, cli, native, linuxConsumer string
	err                                   error
}

func (b *sharedSQLBinaryBundle) load() error {
	b.once.Do(func() {
		b.directory, b.err = os.MkdirTemp("", "backlot-shared-sql-")
		if b.err != nil {
			return
		}
		b.cli = filepath.Join(b.directory, "backlot")
		b.native = filepath.Join(b.directory, "consumer")
		b.linuxConsumer = filepath.Join(b.directory, "consumer-linux")
		builds := []*exec.Cmd{
			exec.Command("go", "build", "-race", "-o", b.cli, "../../cmd/backlot"),
			exec.Command("go", "build", "-o", b.native, "./testdata/shared-consumer"),
			exec.Command("go", "build", "-o", b.linuxConsumer, "./testdata/shared-consumer"),
		}
		builds[2].Env = append(os.Environ(), "GOOS=linux", "CGO_ENABLED=0")
		for _, build := range builds {
			if output, err := build.CombinedOutput(); err != nil {
				b.err = fmt.Errorf("shared SQL fixture build %s: %w: %s", build.Args, err, output)
				return
			}
		}
	})
	return b.err
}

// TestMain calls this after every scenario cleanup, without loading an unused bundle.
func (b *sharedSQLBinaryBundle) close(failed bool) error {
	if b.directory == "" {
		return nil
	}
	if failed || b.err != nil {
		fmt.Fprintln(os.Stderr, "preserved shared SQL build artifacts", b.directory)
		return nil
	}
	return os.RemoveAll(b.directory)
}

func TestSharedSQLBinaryBundle(t *testing.T) {
	// A local stand-in checks build settings and failure retention without compiling.
	tools := t.TempDir()
	log := filepath.Join(tools, "builds")
	script := `#!/bin/sh
printf '%s|%s|%s\n' "$*" "$GOOS" "$CGO_ENABLED" >> "$BACKLOT_SQL_BUILD_LOG"
if [ "$GOOS" = linux ] && [ "$CGO_ENABLED" = 0 ] && [ "$BACKLOT_SQL_BUILD_FAIL" = 1 ]; then
  echo fixture-build-failure >&2
  exit 1
fi
while [ "$1" != -o ]; do shift; done
shift
printf fixture > "$1"
`
	if err := os.WriteFile(filepath.Join(tools, "go"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", tools)
	t.Setenv("GOOS", runtime.GOOS)
	t.Setenv("CGO_ENABLED", "1")
	t.Setenv("BACKLOT_SQL_BUILD_LOG", log)
	t.Setenv("BACKLOT_SQL_BUILD_FAIL", "0")
	var unused sharedSQLBinaryBundle
	if err := unused.close(false); err != nil || unused.directory != "" {
		t.Fatal("cleanup initialized unused bundle", err)
	}
	if _, err := os.Stat(log); !os.IsNotExist(err) {
		t.Fatal("unused bundle invoked build", err)
	}
	var b sharedSQLBinaryBundle
	t.Cleanup(func() { _ = os.RemoveAll(b.directory) })
	for range 2 {
		t.Run("scenario", func(t *testing.T) {
			if err := b.load(); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				for _, path := range []string{b.cli, b.native, b.linuxConsumer} {
					if _, err := os.Stat(path); err != nil {
						t.Fatal("binary unavailable during scenario cleanup", err)
					}
				}
			})
		})
	}
	builds, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	want := fmt.Sprintf("build -race -o %s ../../cmd/backlot|%s|1\nbuild -o %s ./testdata/shared-consumer|%s|1\nbuild -o %s ./testdata/shared-consumer|linux|0\n", b.cli, runtime.GOOS, b.native, runtime.GOOS, b.linuxConsumer)
	if string(builds) != want {
		t.Fatalf("builds/settings changed or repeated: %s", builds)
	}
	if err := b.close(true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(b.cli); err != nil {
		t.Fatal("failed suite lost build diagnostics", err)
	}
	if err := b.close(false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(b.directory); !os.IsNotExist(err) {
		t.Fatal("successful suite retained bundle", err)
	}

	t.Setenv("BACKLOT_SQL_BUILD_FAIL", "1")
	var failed sharedSQLBinaryBundle
	t.Cleanup(func() { _ = os.RemoveAll(failed.directory) })
	first := failed.load()
	if first == nil || !strings.Contains(first.Error(), "fixture-build-failure") || failed.load() != first {
		t.Fatal("build failure lost diagnostics or retried", first)
	}
	builds, err = os.ReadFile(log)
	if err != nil || strings.Count(string(builds), "\n") != 6 {
		t.Fatal("failed bundle rebuilt", err)
	}
	if err := failed.close(false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(failed.cli); err != nil {
		t.Fatal("partial failed build not retained", err)
	}
}
