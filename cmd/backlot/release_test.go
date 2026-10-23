package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestReleaseRefusesUnsafePublication(t *testing.T) {
	script, err := filepath.Abs("../../.github/scripts/release.sh")
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct{ name, tag, listing string }{
		{"prerelease", "v0.1.0-rc.1", ""},
		{"leading zero", "v0.01.0", ""},
		{"major one", "v1.0.0", ""},
		{"suffix", "v0.1.0+build", ""},
		{"existing draft", "v0.1.0", "echo 42"},
		{"lookup error", "v0.1.0", "exit 1"},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			write := func(name, body string) {
				t.Helper()
				if err := os.MkdirAll(filepath.Dir(filepath.Join(root, name)), 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0755); err != nil {
					t.Fatal(err)
				}
			}
			// Stub only read boundaries; any attempted publication is a test failure.
			write("bin/git", "#!/bin/sh\ncase $1 in\nrev-parse) echo source;;\nstatus) :;;\nls-remote) printf 'source\\trefs/tags/v0.1.0\\n';;\nesac\n")
			write("bin/gh", "#!/bin/sh\nif [ \"$1\" = api ]; then "+test.listing+"; else touch attempted-publication; exit 99; fi\n")
			for _, name := range []string{"darwin_arm64.tar.gz", "linux_amd64.tar.gz", "checksums.txt"} {
				write("dist/backlot_v0.1.0_"+name, "fixture")
			}
			write("docs/releases/v0.1.0.md", "notes")
			write("bin/sha256sum", "#!/bin/sh\nexit 0\n")
			command := exec.Command("bash", script, "publish")
			command.Dir = root
			command.Env = append(os.Environ(), "PATH="+filepath.Join(root, "bin")+string(os.PathListSeparator)+os.Getenv("PATH"), "RELEASE_TAG="+test.tag, "SOURCE_SHA=source", "GITHUB_REPOSITORY=owner/repo")
			output, err := command.CombinedOutput()
			if err == nil {
				t.Fatal("unsafe publication accepted", string(output))
			}
			if _, err := os.Stat(filepath.Join(root, "attempted-publication")); !os.IsNotExist(err) {
				t.Fatal("publication attempted", string(output))
			}
			if test.listing == "" && !strings.Contains(string(output), "invalid stable release tag") {
				t.Fatal("wrong rejection", string(output))
			}
		})
	}
}
