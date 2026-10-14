//go:build darwin && arm64

package acceptance

import (
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Keep the original short default: macOS's normal TMPDIR can exceed Unix socket
// limits once public job/proxy paths are appended. An explicit durable override
// must fit the longest fixture path; rejecting it avoids silent durability loss.
func newAcceptanceRoot(prefix string) (string, error) {
	parent := os.Getenv("BACKLOT_ACCEPTANCE_TEMP_ROOT")
	if parent == "" {
		parent = "/tmp"
	}
	if !filepath.IsAbs(parent) {
		return "", errors.New("BACKLOT_ACCEPTANCE_TEMP_ROOT must be absolute")
	}
	longest := filepath.Join(parent, "bl-public-4294967295", "job-loss-4294967295", "engine.sock.proxy")
	if len(longest) > 103 {
		return "", errors.New("BACKLOT_ACCEPTANCE_TEMP_ROOT too long for macOS Unix fixture sockets; choose a short private directory")
	}
	return os.MkdirTemp(parent, prefix)
}

func TestAcceptanceTempRoot(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		name := map[bool]string{false: "short-default-despite-long-system-temp", true: "explicit-durable-short-root"}[explicit]
		t.Run(name, func(t *testing.T) {
			parent := "/tmp"
			if explicit {
				var err error
				parent, err = os.MkdirTemp("/tmp", "bl-tmp-")
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() {
					if err := os.RemoveAll(parent); err != nil {
						t.Error(err)
					}
				})
				t.Setenv("BACKLOT_ACCEPTANCE_TEMP_ROOT", parent)
			} else {
				t.Setenv("BACKLOT_ACCEPTANCE_TEMP_ROOT", "")
			}
			t.Setenv("TMPDIR", filepath.Join(t.TempDir(), strings.Repeat("long", 30)))
			root, err := newAcceptanceRoot("bl-public-")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := os.RemoveAll(root); err != nil {
					t.Error(err)
				}
			})
			if filepath.Dir(root) != parent {
				t.Fatal("explicit/default short root selection changed")
			}
			child := filepath.Join(root, "job-loss-4294967295")
			if err := os.Mkdir(child, 0700); err != nil {
				t.Fatal(err)
			}
			listener, err := net.Listen("unix", filepath.Join(child, "engine.sock.proxy"))
			if err != nil {
				t.Fatal("longest nested fixture socket cannot bind", err)
			}
			t.Cleanup(func() {
				if err := listener.Close(); err != nil {
					t.Error(err)
				}
			})
			t.Log("selected short root supports actual longest nested provider socket despite long system TMPDIR")
		})
	}
	t.Run("overlong-explicit-request-rejected", func(t *testing.T) {
		parent := filepath.Join(t.TempDir(), strings.Repeat("long", 30))
		t.Setenv("BACKLOT_ACCEPTANCE_TEMP_ROOT", parent)
		root, err := newAcceptanceRoot("bl-public-")
		if err == nil || root != "" || !strings.Contains(err.Error(), "too long") {
			t.Fatal("overlong explicit durability request silently redirected", root, err)
		}
		if _, err := os.Stat(parent); !os.IsNotExist(err) {
			t.Fatal("rejected override allocated state", err)
		}
	})
}
