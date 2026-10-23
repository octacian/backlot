//go:build darwin || linux

package daemon

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	bolt "go.etcd.io/bbolt"
)

// Synthetic versions exercise real bucket/value changes, not historical layouts.
func testStateMigrations() []stateMigration {
	return []stateMigration{
		{100, 101, func(tx *bolt.Tx) error {
			b, err := tx.CreateBucket([]byte("transitional"))
			if err != nil {
				return err
			}
			if err := b.Put([]byte("value"), []byte(strings.ToUpper(string(tx.Bucket([]byte("legacy")).Get([]byte("value")))))); err != nil {
				return err
			}
			return tx.DeleteBucket([]byte("legacy"))
		}},
		{101, 102, func(tx *bolt.Tx) error {
			if _, err := tx.CreateBucket([]byte("resources")); err != nil {
				return err
			}
			value := string(tx.Bucket([]byte("transitional")).Get([]byte("value"))) + "/final"
			if err := tx.Bucket([]byte("meta")).Put([]byte("converted"), []byte(value)); err != nil {
				return err
			}
			return tx.DeleteBucket([]byte("transitional"))
		}},
	}
}

func TestStateMigrationPath(t *testing.T) {
	for _, name := range []string{"chain", "later source", "current", "later failure", "canceled", "final authority", "missing", "backward", "overshoot", "newer", "malformed", "missing version"} {
		t.Run(name, func(t *testing.T) {
			db, err := bolt.Open(filepath.Join(t.TempDir(), "state.db"), 0600, nil)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = db.Close() })
			steps := testStateMigrations()
			if err := db.Update(func(tx *bolt.Tx) error {
				if err := openStateTransaction(tx); err != nil {
					return err
				}
				if err := tx.DeleteBucket([]byte("resources")); err != nil {
					return err
				}
				b, err := tx.CreateBucket([]byte("legacy"))
				if err != nil {
					return err
				}
				if err := b.Put([]byte("value"), []byte("retained")); err != nil {
					return err
				}
				version := "100"
				switch name {
				case "later source", "current":
					if err := steps[0].apply(tx); err != nil {
						return err
					}
					version = "101"
					if name == "current" {
						if err := steps[1].apply(tx); err != nil {
							return err
						}
						version = "102"
					}
				case "newer":
					version = "103"
				case "malformed":
					version = "0100"
				case "missing version":
					version = ""
				}
				return tx.Bucket([]byte("meta")).Put([]byte("version"), []byte(version))
			}); err != nil {
				t.Fatal(err)
			}
			before := stateContents(t, &store{db: db})
			injected := errors.New("later step failed")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			last := steps[1].apply
			switch name {
			case "later failure", "canceled", "final authority":
				steps[1].apply = func(tx *bolt.Tx) error {
					if err := last(tx); err != nil {
						return err
					}
					switch name {
					case "later failure":
						return injected
					case "canceled":
						cancel()
					case "final authority":
						return tx.Bucket([]byte("runtime")).Put([]byte("orphan"), []byte(`null`))
					}
					return nil
				}
			case "missing":
				steps = steps[:1]
			case "backward":
				steps[1].to = 100
			case "overshoot":
				steps[1].to = 103
			}
			open := func(tx *bolt.Tx) error {
				if err := openStateVersion(tx, 102, steps); err != nil {
					return err
				}
				return ctx.Err()
			}
			err = db.Update(open)
			switch name {
			case "chain", "later source", "current":
				if err != nil {
					t.Fatal(err)
				}
				after := stateContents(t, &store{db: db})
				if after["meta"]["version"] != "102" || after["meta"]["converted"] != "RETAINED/final" || after["legacy"] != nil || after["transitional"] != nil || after["resources"] == nil {
					t.Fatal("incomplete conversion", after)
				}
				path := db.Path()
				if err := db.Close(); err != nil {
					t.Fatal(err)
				}
				db, err = bolt.Open(path, 0600, nil)
				if err != nil {
					t.Fatal(err)
				}
				if err := db.Update(open); err != nil || !reflect.DeepEqual(after, stateContents(t, &store{db: db})) {
					t.Fatal("current open repeated a step", err)
				}
			default:
				if err == nil || !reflect.DeepEqual(before, stateContents(t, &store{db: db})) {
					t.Fatal("failed chain changed source records/fence", err)
				}
				if name == "canceled" && !errors.Is(err, context.Canceled) {
					t.Fatal(err)
				}
			}
		})
	}
}
