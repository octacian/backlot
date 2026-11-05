package plan

import (
	"os"
	"path/filepath"
	"testing"

	v1 "github.com/octacian/backlot/api/v1"
	"github.com/octacian/backlot/internal/manifest"
)

func TestDisposableOutputOverlaps(t *testing.T) {
	for _, tc := range []struct {
		name, second            string
		enabled, other, allowed bool
	}{
		{"same-directory", "reports", true, true, true},
		{"mixed", "reports", true, false, false},
		{"reverse-mixed", "reports", false, true, false},
		{"nested", "reports/nested", true, true, false},
		{"reverse-nested", "reports/nested", false, true, false},
		{"ordinary-nested", "reports/nested", false, false, true},
		{"siblings", "other", true, false, true},
		{"canonical-mixed", "alias", true, false, false},
		{"internal-link", "alias", true, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(filepath.Join(root, "reports"), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, "reports", "keep"), []byte("untouched"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink("reports", filepath.Join(root, "alias")); err != nil {
				t.Fatal(err)
			}
			outputs := map[string]v1.Output{"first": {Path: "reports", ConcurrencyGroup: "reports", EmptyBeforeExecution: tc.enabled}, "second": {Path: tc.second, ConcurrencyGroup: "reports", EmptyBeforeExecution: tc.other}}
			err = ValidateOutputs(root, outputs, map[string]bool{"first": true, "second": true})
			if (err == nil) != tc.allowed {
				t.Fatalf("allowed=%v error=%v", tc.allowed, err)
			}
			if _, err := os.Stat(filepath.Join(root, "reports", "keep")); err != nil {
				t.Fatal("offline validation changed source", err)
			}
			// Lexical overlaps are also rejected before filesystem/tool lookup, across scenes.
			if tc.second != "alias" {
				m := v1.Manifest{Version: v1.ManifestVersion, Project: "fixture", Outputs: outputs, Tools: map[string]string{"sh": "sh"}, Components: map[string]v1.Component{"job": {Kind: v1.Job, Runtime: v1.Native, Policy: v1.EachStart, Command: &v1.Command{Tool: "sh"}}}, Scenes: map[string]v1.Scene{"dev": {Lifetime: v1.Persistent, Components: []string{"job"}}}}
				if err := manifest.Validate(m); (err == nil) != tc.allowed {
					t.Fatalf("manifest allowed=%v error=%v", tc.allowed, err)
				}
			}
		})
	}
}
