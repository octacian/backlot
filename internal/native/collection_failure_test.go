package native

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFailedJoinKeepsCollectionGapAndExitProvenance(t *testing.T) {
	read, write, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = read.Close(); _ = write.Close() })
	g := &Group{control: write, exit: &Exit{Known: true, Code: 17}}
	// Missing authority cannot verify cleanup or overwrite a known original exit.
	if err := g.Stop(0); err == nil {
		t.Fatal("unknown cleanup authority succeeded")
	}
	result := g.Result()
	if result == nil || !result.Known || result.Code != 17 || result.CollectionFailure == "" {
		t.Fatal("collection loss/provenance omitted", result)
	}
	// Late status recovery must not erase the independently recorded gap.
	g.exit = &Exit{Known: true, Code: 23}
	recovered := g.Result()
	if !recovered.Known || recovered.Code != 23 || recovered.CollectionFailure == "" {
		t.Fatal("late result cleared collection loss", recovered)
	}
}

func TestResourceOnlyStopKeepsKnownExitAndStickyGap(t *testing.T) {
	if !supported() {
		t.Skip("native boot identity unsupported")
	}
	read, write, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = read.Close() }()
	defer func() { _ = write.Close() }()
	done, statusDone := make(chan struct{}), make(chan struct{})
	close(done)
	close(statusDone)
	g := &Group{Identity: Identity{PID: 42, Birth: "original", Control: filepath.Join(t.TempDir(), "missing"), Token: strings.Repeat("a", 64), Boot: "00000000-0000-0000-0000-000000000001"}, control: write, done: done, statusDone: statusDone, exit: &Exit{Known: true, Code: 17}}
	for range 2 {
		if err := g.Stop(0); err != nil {
			t.Fatal(err)
		}
		result := g.Result()
		if !result.Known || result.Code != 17 || result.CollectionFailure == "" {
			t.Fatal("resource proof erased evidence gap", result)
		}
		g.mu.Lock()
		g.exit = &Exit{Known: true, Code: 17}
		g.mu.Unlock()
	}
}
