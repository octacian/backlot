package native

import (
	"os"
	"testing"
)

func TestFailedJoinKeepsCollectionGapAndExitProvenance(t *testing.T) {
	for _, known := range []bool{false, true} {
		t.Run(map[bool]string{false: "unknown", true: "known-nonzero"}[known], func(t *testing.T) {
			read, write, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = read.Close(); _ = write.Close() })
			g := &Group{control: write}
			if known {
				g.exit = &Exit{Known: true, Code: 17}
			}
			// Missing authority cannot verify cleanup and must never authorize actuation.
			if err := g.Stop(0); err == nil {
				t.Fatal("unknown cleanup authority succeeded")
			}
			result := g.Result()
			if result == nil || result.Known != known || result.CollectionFailure == "" {
				t.Fatal("collection loss/provenance omitted", result)
			}
			if known && result.Code != 17 {
				t.Fatal("known original nonzero exit overwritten", result)
			}
			// Late status recovery must not erase the independently recorded gap.
			g.mu.Lock()
			g.exit = &Exit{Known: true, Code: 23}
			g.mu.Unlock()
			recovered := g.Result()
			if !recovered.Known || recovered.Code != 23 || recovered.CollectionFailure == "" {
				t.Fatal("late result cleared collection loss", recovered)
			}
		})
	}
}
