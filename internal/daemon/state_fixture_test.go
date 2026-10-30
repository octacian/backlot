//go:build darwin || linux

package daemon

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"testing"

	v1 "github.com/octacian/backlot/api/v1"
	bolt "go.etcd.io/bbolt"
)

func copyStateNine(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile("testdata/state/v9/state.db")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "state.db"), data, 0600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestRetainedStateNine(t *testing.T) { testRetainedState(t, copyStateNine(t), 9) }

func TestRetainedStateTen(t *testing.T) {
	data, err := os.ReadFile("testdata/state/v10/state.db")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err = os.WriteFile(filepath.Join(dir, "state.db"), data, 0600); err != nil {
		t.Fatal(err)
	}
	testRetainedState(t, dir, 10)
}

func TestRetainedStateEleven(t *testing.T) {
	data, err := os.ReadFile("testdata/state/v11/state.db")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "state.db"), data, 0600); err != nil {
		t.Fatal(err)
	}
	testRetainedState(t, dir, 11)
}

func testRetainedState(t *testing.T, dir string, version int) {
	t.Helper()
	db, err := bolt.Open(filepath.Join(dir, "state.db"), 0600, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Update(func(tx *bolt.Tx) error {
		if err := tx.Bucket([]byte("meta")).Put([]byte("unknown-passive"), []byte{0, 255, 1}); err != nil {
			return err
		}
		b, err := tx.CreateBucket([]byte("unrelated-passive"))
		if err != nil {
			return err
		}
		return b.Put([]byte("opaque"), []byte{255, 0, 2})
	}); err != nil {
		t.Fatal(err)
	}
	before := stateContents(t, &store{db: db})
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	wantInstances := 2
	if version == 11 {
		wantInstances = 3
	}
	if before["meta"]["version"] != strconv.Itoa(version) || len(before["instances"]) != wantInstances || len(before["checkouts"]) != 1 || len(before["persistent"]) != 1 || len(before["snapshots"]) != wantInstances || len(before["secrets"]) != wantInstances {
		t.Fatal("fixture lost its historical record classes")
	}
	before["meta"]["version"] = strconv.Itoa(v1.StateVersion)
	for _, name := range sharedBuckets {
		before[name] = map[string]string{}
	}
	var migrated map[string]map[string]string
	for range 2 {
		s, err := openStore(dir)
		if err != nil {
			t.Fatal(err)
		}
		func() {
			defer func() {
				if err := s.db.Close(); err != nil {
					t.Error(err)
				}
			}()
			after := stateContents(t, s)
			if migrated != nil && !reflect.DeepEqual(migrated, after) {
				t.Fatal("reopen changed history")
			}
			migrated = after
			wantAttempts := 1
			if version == 11 {
				wantAttempts = 2
			}
			if len(after["attempts"]) != wantAttempts || len(after["attempt_sequences"]) != wantAttempts || len(after["evidence_deletions"]) != 0 {
				t.Fatal("migration did not seed only known current attempts")
			}
			comparable := stateContents(t, s)
			for _, name := range []string{"attempts", "attempt_sequences", "evidence_deletions"} {
				delete(comparable, name)
			}
			if !reflect.DeepEqual(before, comparable) {
				t.Fatal("open/reopen rewrote retained records")
			}
			list, err := s.list(v1.ListRequest{APIVersion: v1.Version})
			if err != nil || len(list.Instances) != wantInstances {
				t.Fatal("cannot read historical instances", err)
			}
			prepared, completed := false, false
			for _, summary := range list.Instances {
				i, err := s.inspect(summary.ID)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := s.snapshot(i.ID); err != nil {
					t.Fatal(err)
				}
				if i.Plan.Lifetime == v1.Persistent && i.Status == v1.Prepared {
					prepared = true
				}
				if i.Plan.Lifetime == v1.Disposable && i.Status == v1.Succeeded && i.Execution != nil && len(i.Execution.NativeCleanup) == 1 && i.Execution.NativeCleanup[0].Confidence == v1.ObservedEmpty {
					completed = true
				}
			}
			if !prepared || !completed {
				t.Fatal("missing prepared scene or completed native cleanup history")
			}
		}()
	}
}

func TestRetainedStateNineRejectedCopy(t *testing.T) {
	for _, kind := range []string{"unsupported", "corrupt", "pre9", "experimental7", "missing source", "collision"} {
		t.Run(kind, func(t *testing.T) {
			dir := copyStateNine(t)
			path := filepath.Join(dir, "state.db")
			db, err := bolt.Open(path, 0600, nil)
			if err != nil {
				t.Fatal(err)
			}
			err = db.Update(func(tx *bolt.Tx) error {
				if kind == "unsupported" {
					return tx.Bucket([]byte("meta")).Put([]byte("version"), []byte("999999"))
				}
				switch kind {
				case "pre9":
					return tx.Bucket([]byte("meta")).Put([]byte("version"), []byte("8"))
				case "experimental7":
					return tx.Bucket([]byte("meta")).Put([]byte("version"), []byte("7"))
				case "missing source":
					return tx.DeleteBucket([]byte("resources"))
				case "collision":
					_, err := tx.CreateBucket([]byte("shared_allocations"))
					return err
				}
				return tx.Bucket([]byte("runtime")).Put([]byte("invalid-orphan"), []byte(`null`))
			})
			if closeErr := db.Close(); err != nil || closeErr != nil {
				t.Fatal(err, closeErr)
			}
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			sentinel := filepath.Join(dir, "passive-file")
			if err := os.WriteFile(sentinel, []byte("preserve"), 0600); err != nil {
				t.Fatal(err)
			}
			if s, err := openStore(dir); err == nil {
				_ = s.db.Close()
				t.Fatal("rejected fixture accepted")
			}
			after, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(before, after) {
				t.Fatal("rejection changed DB bytes", err)
			}
			if data, err := os.ReadFile(sentinel); err != nil || string(data) != "preserve" {
				t.Fatal("rejection touched passive file", err)
			}
		})
	}
}

func TestRetainedStateTenRejectsInventedPinsUnchanged(t *testing.T) {
	data, err := os.ReadFile("testdata/state/v10/state.db")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "state.db")
	if err = os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	db, err := bolt.Open(path, 0600, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = db.Update(func(tx *bolt.Tx) error {
		key := tx.Bucket([]byte("instances")).Cursor()
		id, _ := key.First()
		return tx.Bucket([]byte("resources")).Put(id, []byte(`{"build_images":{}}`))
	}); err != nil {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if s, err := openStore(dir); err == nil {
		_ = s.db.Close()
		t.Fatal("invented source pins accepted")
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("rejected v10 fixture changed")
	}
}
