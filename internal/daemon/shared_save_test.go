//go:build !windows

package daemon

import (
	"bytes"
	"context"
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
	if err = state.saveSharedFixture(record); err != nil {
		t.Fatal(err)
	}
	// Empty handles remain pending intents; full provider IDs/coherent volumes work.
	for _, acknowledged := range []bool{false, true} {
		if acknowledged {
			record.Server.ID = newID()
			record.Network.ID = newID()
			record.Volume.ID = record.Volume.Name
		}
		if err = state.saveShared(&record, "consumer_start"); err != nil {
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
			code(t, state.saveShared(&changed, "consumer_start"), "state_corrupt")
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

// saveSharedFixture seeds legacy observations without inventing transition provenance.
func (s *store) saveSharedFixture(record sharedServiceRecord) error {
	for _, resource := range []ownedResource{record.Server, record.Network, record.Volume} {
		if err := validateSharedResourceHandle(resource); err != nil {
			return err
		}
	}
	return s.db.Update(func(tx *bolt.Tx) error { return put(tx, "shared_services", record.ID, record) })
}

func TestSharedTransitionPersistence(t *testing.T) {
	state, err := openStore(shortTemp(t))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = state.db.Close() }()
	record := sharedLockRecord(newID())
	record.Status = "intent"
	if err := state.saveShared(&record, "consumer_start"); err != nil {
		t.Fatal(err)
	}
	if record.LastTransition == nil || record.LastTransition.From != "" || record.LastTransition.To != "intent" {
		t.Fatal("initial intent not recorded")
	}
	stale := record
	record.Status = "running"
	if err := state.saveShared(&record, "consumer_start"); err != nil {
		t.Fatal(err)
	}
	transition := *record.LastTransition
	stale.Status = "running"
	stale.Failure = "controlled observation"
	if err := state.saveShared(&stale, "allocation_release"); err != nil {
		t.Fatal(err)
	}
	if stale.LastTransition == nil || *stale.LastTransition != transition {
		t.Fatal("nonstatus writer replaced committed transition")
	}
	for _, cause := range []string{"", "private-provider-detail"} {
		invalid := stale
		invalid.Status = "uncertain"
		code(t, state.saveShared(&invalid, cause), "state_corrupt")
	}
	retained, err := state.sharedService(record.ID)
	if err != nil || retained.Status != "running" || retained.LastTransition == nil || *retained.LastTransition != transition {
		t.Fatal("rejected writer mutated retained status", err)
	}
	record.Destroying, record.Status = true, "destroying"
	record.Server.Removed = false
	if err := state.saveShared(&record, "explicit_shared_destroy"); err != nil {
		t.Fatal(err)
	}
	transition = *record.LastTransition
	if err := state.stopSharedService(context.Background(), record.ID, "daemon_recovery"); err == nil {
		t.Fatal("missing fixture provider unexpectedly succeeded")
	}
	retained, err = state.sharedService(record.ID)
	if err != nil || retained.Status != "destroying" || !retained.Destroying || retained.LastTransition == nil || *retained.LastTransition != transition {
		t.Fatal("recovery failure erased destruction intent or created no-op transition", err)
	}
}
