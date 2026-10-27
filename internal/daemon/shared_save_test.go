//go:build !windows

package daemon

import (
	"bytes"
	"testing"

	"github.com/octacian/backlot/internal/plan"
	bolt "go.etcd.io/bbolt"
)

func TestSharedSaveRejectsHandlesBeforeCommit(t *testing.T) {
	directory, root := shortTemp(t), shortTemp(t)
	state, err := openStore(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = state.db.Close() }()
	project, err := identifyShared(plan.Source{ProjectPath: root, CheckoutPath: root}, "fixture")
	if err != nil {
		t.Fatal(err)
	}
	if err = state.db.Update(func(tx *bolt.Tx) error { return registerSharedProject(tx, project) }); err != nil {
		t.Fatal(err)
	}
	record := sharedLockRecord(hash(project.ID + "/sql"))
	record.Project = project
	record.Server = ownedResource{Kind: "container", Name: "fixture-server", Token: newID()}
	record.Network = ownedResource{Kind: "network", Name: "fixture-network", Token: newID()}
	record.Volume = ownedResource{Kind: "volume", Name: "fixture-volume", Token: newID()}
	raw := func() []byte {
		var out []byte
		if err := state.db.View(func(tx *bolt.Tx) error {
			out = bytes.Clone(tx.Bucket([]byte("shared_services")).Get([]byte(record.ID)))
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		return out
	}
	// Empty handles remain pending intents; full provider IDs/coherent volumes work.
	for _, acknowledged := range []bool{false, true} {
		if acknowledged {
			record.Server.ID = newID()
			record.Network.ID = newID()
			record.Volume.ID = record.Volume.Name
		}
		if err = state.saveShared(record); err != nil {
			t.Fatal(err)
		}
		previous := raw()
		for _, kind := range []string{"container", "network", "volume"} {
			changed := record
			switch kind {
			case "container":
				changed.Server.ID = "short-id"
			case "network":
				changed.Network.ID = "alias"
			case "volume":
				changed.Volume.ID = "different-volume"
			}
			code(t, state.saveShared(changed), "state_corrupt")
			if !bytes.Equal(raw(), previous) {
				t.Fatal("rejected save changed valid intent/capability", kind)
			}
		}
		if err = state.db.Close(); err != nil {
			t.Fatal(err)
		}
		state, err = openStore(directory)
		if err != nil {
			t.Fatal("valid previous record cannot reopen after rejected provider response", err)
		}
		if !bytes.Equal(raw(), previous) {
			t.Fatal("reopen changed record")
		}
	}
}
