//go:build darwin || linux

package daemon

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	v1 "github.com/octacian/backlot/api/v1"
	bolt "go.etcd.io/bbolt"
)

func TestCurrentBuildImageResultPreflight(t *testing.T) {
	for _, mode := range []string{"reused source attempt", "optional absence", "invalid image", "invalid warning", "invalid archived image"} {
		t.Run(mode, func(t *testing.T) {
			dir := copyStateNine(t)
			s, err := openStore(dir)
			if err != nil {
				t.Fatal(err)
			}
			closed := false
			t.Cleanup(func() {
				if !closed {
					if err := s.db.Close(); err != nil {
						t.Error(err)
					}
				}
			})
			list, err := s.list(v1.ListRequest{APIVersion: v1.Version})
			if err != nil {
				t.Fatal(err)
			}
			id := list.Instances[0].ID
			currentAttempt, sourceAttempt := strings.Repeat("b", 64), strings.Repeat("c", 64)
			result := &v1.ExecutionResult{Attempt: currentAttempt, Components: []v1.ComponentResult{{Name: "current-build", Status: "completed"}}, BuildImages: []v1.BuildImageResult{{Name: "app", Producer: "original-build", Attempt: sourceAttempt, Tag: buildImageTag(id, sourceAttempt, "app"), ID: "sha256:" + strings.Repeat("a", 64)}}, ImageChangeWarnings: []v1.ImageChangeWarning{{Component: "pending-consumer", Image: "app", PreviousID: "sha256:" + strings.Repeat("b", 64), ID: "sha256:" + strings.Repeat("a", 64), Message: "compatibility risk"}}}
			switch mode {
			case "optional absence":
				result.BuildImages = nil
				result.ImageChangeWarnings = nil
			case "invalid image":
				result.BuildImages[0].Tag = buildImageTag(newID(), sourceAttempt, "app")
			case "invalid warning":
				result.ImageChangeWarnings[0].ID = "sha256:" + strings.Repeat("d", 64)
			}
			if _, err = s.execution(id, v1.Starting, result); err != nil {
				t.Fatal(err)
			}
			if mode == "invalid archived image" {
				if err = s.db.Update(func(tx *bolt.Tx) error {
					r, err := load(tx, id)
					if err != nil {
						return err
					}
					bad := *result
					bad.BuildImages = []v1.BuildImageResult{{}}
					r.NativeAbandonments = []nativeAbandonment{{Result: &bad}}
					return put(tx, "instances", id, r)
				}); err != nil {
					t.Fatal(err)
				}
			}
			// New rules must not become requirements of the historical source reader.
			if err = s.db.View(validateStateNineAuthority); err != nil {
				t.Fatal("v11 validation leaked into historical source checks", err)
			}
			if err = s.db.Close(); err != nil {
				t.Fatal(err)
			}
			closed = true
			path := filepath.Join(dir, "state.db")
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			reopened, err := openStore(dir)
			valid := mode == "reused source attempt" || mode == "optional absence"
			if valid {
				if err != nil {
					t.Fatal(err)
				}
				instance, readErr := reopened.inspect(id)
				closeErr := reopened.db.Close()
				if readErr != nil || closeErr != nil {
					t.Fatal(readErr, closeErr)
				}
				if mode == "reused source attempt" && instance.Execution.BuildImages[0].Attempt != sourceAttempt {
					t.Fatal("lost historical source-attempt provenance")
				}
				return
			}
			if err == nil {
				_ = reopened.db.Close()
				t.Fatal("malformed current provenance survived startup")
			}
			code(t, err, "state_corrupt")
			if !strings.Contains(err.Error(), "state was not modified") || !strings.Contains(err.Error(), "preserve") {
				t.Fatal("missing preservation diagnostic", err)
			}
			after, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(before, after) {
				t.Fatal("rejected current DB was modified")
			}
		})
	}
}
