//go:build darwin || linux

package daemon

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	v1 "github.com/octacian/backlot/api/v1"
	"github.com/octacian/backlot/internal/native"
	bolt "go.etcd.io/bbolt"
)

// TestActualPredecessorFence is opt-in evidence using state created by an actual
// retained predecessor executable. It performs the fence separately from recovery
// so record-byte preservation cannot be confused with normal journal updates.
func TestActualPredecessorFence(t *testing.T) {
	dir := os.Getenv("BACKLOT_PREDECESSOR_TEST_STATE")
	if dir == "" {
		t.Skip("requires explicitly owned actual-predecessor fixture state")
	}
	if !filepath.IsAbs(dir) {
		t.Fatal("fixture state must be absolute")
	}
	db, err := bolt.Open(filepath.Join(dir, "state.db"), 0600, &bolt.Options{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	before := stateContents(t, &store{db: db})
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	previous := before["meta"]["version"]
	if previous != "6" && previous != "8" {
		t.Fatal("not an actual predecessor fixture")
	}
	s, err := openStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := s.db.Close(); err != nil {
			t.Error(err)
		}
	}()
	before["meta"]["version"] = "9"
	if !reflect.DeepEqual(before, stateContents(t, s)) {
		t.Fatal("non-fence record bytes changed")
	}
	t.Logf("actual predecessor%s ->9: all non-version bucket keys/record bytes preserved", previous)
	if os.Getenv("BACKLOT_PREDECESSOR_TEST_RECEIPTS") == "1" {
		ids, err := s.list(v1.ListRequest{APIVersion: v1.Version})
		if err != nil {
			t.Fatal(err)
		}
		count := 0
		for _, i := range ids.Instances {
			r, err := s.runtime(i.ID)
			if err != nil {
				t.Fatal(err)
			}
			for _, id := range r.Groups {
				if id.Protocol != 0 {
					t.Fatal("predecessor authority was reinterpreted")
				}
				result, err := native.StopResult(id, 0)
				if err != nil || result != nil {
					t.Fatal("legacy receipt cleanup", err)
				}
				count++
			}
		}
		if count == 0 {
			t.Fatal("no actual strict authorities exercised")
		}
		t.Logf("%d exact legacy strict receipt proofs; no session observation synthesized", count)
	}
}
