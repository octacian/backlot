//go:build darwin || linux

package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/containerd/errdefs"
	bolt "go.etcd.io/bbolt"
	"os"
	"os/exec"
	"testing"
	"time"

	v1 "github.com/octacian/backlot/api/v1"
)

// TestSharedMariaDBForceEarly is the discriminating real CLI/daemon/provider slice.
// Parent-written random data cannot be reconstructed by application initialization.
func TestSharedMariaDBForceEarly(t *testing.T) {
	f := newSharedMariaDBFixture(t, true)
	a, b := f.run("host", f.h.project), f.run("bridge", f.linked)
	ca, cb := f.credential(t, a.Instance.ID, "native"), f.credential(t, b.Instance.ID, "container")
	wa, wb := independentSharedWitness(t, f.db(t, ca)), independentSharedWitness(t, f.db(t, cb))
	admin := f.liveAdmin(t)
	var original string
	if err := admin.QueryRow("SELECT SCHEMA_COMMENT FROM information_schema.SCHEMATA WHERE SCHEMA_NAME=?", ca.database).Scan(&original); err != nil {
		t.Fatal("target ownership observation failed")
	}
	released := false
	// Exact private fixture is parent-owned; restore its deliberate fault on failure.
	t.Cleanup(func() {
		if !released {
			if _, err := admin.Exec("ALTER DATABASE `" + ca.database + "` COMMENT '" + original + "'"); err != nil {
				t.Error("exact fixture fault repair failed")
			}
		}
	})
	if _, err := admin.Exec("ALTER DATABASE `" + ca.database + "` COMMENT '" + newID() + "'"); err != nil {
		t.Fatal("owned marker drift failed")
	}
	out, err := exec.Command(f.binary, "destroy", "--state-dir", f.h.dir, "--json", a.Instance.ID).CombinedOutput()
	if err == nil {
		t.Fatal("default destroy accepted mismatched target")
	}
	var failure struct {
		Error *v1.PlanError `json:"error"`
	}
	if json.Unmarshal(bytes.SplitN(out, []byte("\n"), 2)[0], &failure) != nil || failure.Error == nil || failure.Error.Code != "cleanup_failed" {
		t.Fatalf("default CLI did not report cleanup failure: %s", out)
	}
	databases, users := sharedSQLCounts(t, admin, v1.ManagerRequest{Database: ca.database, Username: ca.user})
	if databases != 1 || users != 1 {
		t.Fatal("default deletion mutated uncertain SQL")
	}
	verifySharedWitness(t, f.db(t, ca), wa)
	if f.shared(t).PendingReleases != 1 {
		t.Fatal("default failure lost durable obligation")
	}
	var destroyed v1.InstanceResponse
	out = f.cli("destroy", "--force", "--state-dir", f.h.dir, "--json", a.Instance.ID)
	if json.Unmarshal(out, &destroyed) != nil || destroyed.Instance.Status != v1.Destroyed {
		t.Fatal("force CLI did not report actual destruction")
	}
	released = true
	for _, secret := range []string{ca.password, cb.password, original, f.liveAdminRecord(t).AdminPassword} {
		if bytes.Contains(out, []byte(secret)) {
			t.Fatal("force CLI leaked private provisioning value")
		}
	}
	f.requireAbsent(t, ca)
	verifySharedWitness(t, f.db(t, cb), wb)
	if got := f.credential(t, b.Instance.ID, "container"); got != cb {
		t.Fatal("force changed peer credentials")
	}
	status := f.shared(t)
	if status.Allocations != 1 || status.PendingReleases != 0 {
		t.Fatal("force did not settle only selected obligation")
	}
	// Repeated explicit cleanup has no new generation or effects.
	f.cli("destroy", "--force", "--state-dir", f.h.dir, "--json", a.Instance.ID)
	verifySharedWitness(t, f.db(t, cb), wb)
	f.destroy(t, b.Instance.ID)
	f.requireAbsent(t, cb)
	sharedStop(t, f.h)
	_, allocation := f.private(t, a.Instance.ID)
	if allocation.Request.AllocationID != "" {
		t.Fatal("settled target allocation remains in durable state")
	}
	t.Log("real CLI default preserved mismatched SQL; force deleted exact database/user and durable obligation; peer random witness/credentials survived")
}

func TestSharedMariaDBForceOwnership(t *testing.T) {
	for _, fault := range []string{"user-authentication", "unexpected-grant", "manually-removed"} {
		t.Run(fault, func(t *testing.T) {
			f := newSharedMariaDBFixture(t, true)
			a, b := f.run("host", f.h.project), f.run("bridge", f.linked)
			ca, cb := f.credential(t, a.Instance.ID, "native"), f.credential(t, b.Instance.ID, "container")
			wb := independentSharedWitness(t, f.db(t, cb))
			admin := f.liveAdmin(t)
			query, repair := "", ""
			switch fault {
			case "user-authentication":
				query = "ALTER USER '" + ca.user + "'@'%' IDENTIFIED BY '" + newID() + "'"
				repair = "ALTER USER '" + ca.user + "'@'%' IDENTIFIED BY '" + ca.password + "'"
			case "unexpected-grant":
				query = "GRANT SELECT ON mysql.* TO '" + ca.user + "'@'%'"
				repair = "REVOKE SELECT ON mysql.* FROM '" + ca.user + "'@'%'"
			case "manually-removed":
				query = "DROP DATABASE `" + ca.database + "`"
			}
			released := false
			t.Cleanup(func() {
				if !released && repair != "" {
					if _, err := admin.Exec(repair); err != nil {
						t.Error("owned fault repair failed")
					}
				}
			})
			if _, err := admin.Exec(query); err != nil {
				t.Fatal("owned SQL drift failed")
			}
			if fault != "manually-removed" {
				if _, err := f.h.cli.Destroy(context.Background(), a.Instance.ID); err == nil {
					t.Fatal("default accepted ownership drift")
				}
			}
			f.cli("destroy", "--force", "--state-dir", f.h.dir, "--json", a.Instance.ID)
			released = true
			f.requireAbsent(t, ca)
			verifySharedWitness(t, f.db(t, cb), wb)
			if got := f.credential(t, b.Instance.ID, "container"); got != cb {
				t.Fatal("peer credentials changed")
			}
			f.destroy(t, b.Instance.ID)
		})
	}
}

func TestSharedMariaDBForceLabels(t *testing.T) {
	f := newSharedMariaDBFixture(t, true)
	a, b := f.run("host", f.h.project), f.run("bridge", f.linked)
	ca, cb := f.credential(t, a.Instance.ID, "native"), f.credential(t, b.Instance.ID, "container")
	wb := independentSharedWitness(t, f.db(t, cb))
	sharedStop(t, f.h)
	original, _ := f.private(t, a.Instance.ID)
	// The Engine IDs remain unchanged. Drift private capabilities rather than
	// claiming immutable Engine labels can be edited in place.
	t.Cleanup(func() {
		f.h.close()
		state, err := openStore(f.h.dir)
		if err != nil {
			t.Error(err)
			return
		}
		defer func() { _ = state.db.Close() }()
		record, err := state.sharedService(original.ID)
		var detail *v1.PlanError
		if errors.As(err, &detail) && detail.Code == "not_found" {
			return
		}
		if err != nil {
			t.Error(err)
			return
		}
		record.Server.Token = original.Server.Token
		record.Volume.Token = original.Volume.Token
		record.Network.Token = original.Network.Token
		if err = state.saveShared(record); err != nil {
			t.Error(err)
		}
	})
	state, err := openStore(f.h.dir)
	if err != nil {
		t.Fatal(err)
	}
	drift := original
	drift.Server.Token = newID()
	drift.Volume.Token = newID()
	drift.Network.Token = newID()
	err = state.saveShared(drift)
	_ = state.db.Close()
	if err != nil {
		t.Fatal(err)
	}
	startSharedRecoveryDaemon(t, f.h, nil)
	if _, err = f.h.cli.Destroy(context.Background(), a.Instance.ID); err == nil {
		t.Fatal("default bypassed shared labels")
	}
	f.cli("destroy", "--force", "--state-dir", f.h.dir, "--json", a.Instance.ID)
	f.requireAbsent(t, ca)
	verifySharedWitness(t, f.db(t, cb), wb)
	if _, err = f.h.cli.DestroyShared(context.Background(), v1.SharedDestroyRequest{APIVersion: v1.Version, ServiceID: original.ID, Force: true}); err == nil {
		t.Fatal("force bypassed retained peer fence")
	}
	f.cli("destroy", "--force", "--state-dir", f.h.dir, "--json", b.Instance.ID)
	if _, err = f.h.cli.Shared(context.Background(), "destroy", v1.SharedRequest{APIVersion: v1.Version, ServiceID: original.ID}); err == nil {
		t.Fatal("default shared destroy bypassed label mismatch")
	}
	f.cli("shared", "destroy", "--force", "--state-dir", f.h.dir, "--json", original.ID)
	for _, r := range []ownedResource{original.Server, original.Volume, original.Network} {
		if err = verifyDockerResource(context.Background(), f.d, r); !errdefs.IsNotFound(err) {
			t.Fatal("force shared deletion absence unverified")
		}
	}
	result, err := f.h.cli.Shared(context.Background(), "list", v1.SharedRequest{APIVersion: v1.Version})
	if err != nil || len(result.Services) != 0 {
		t.Fatal("shared obligation retained after verified deletion", err)
	}
	t.Log("actual recorded IDs with mismatched capabilities: SQL force bypassed labels; retained peer fenced shared force; allocation-free force deleted only exact recorded effects")
}

func TestSharedMariaDBForceMissingStorage(t *testing.T) {
	f := newSharedMariaDBFixture(t, true)
	a, b := f.run("host", f.h.project), f.run("bridge", f.linked)
	for _, id := range []string{a.Instance.ID, b.Instance.ID} {
		if _, err := f.h.cli.StopExecution(context.Background(), id); err != nil {
			t.Fatal(err)
		}
	}
	sharedStop(t, f.h)
	record, _ := f.private(t, a.Instance.ID)
	// Parent owns exact identities and removes only its own stopped fixture server.
	if err := removeContainer(context.Background(), f.d, record.Server); err != nil {
		t.Fatal(err)
	}
	startSharedRecoveryDaemon(t, f.h, nil)
	if _, err := f.h.cli.DestroyWithRequest(context.Background(), v1.DestroyRequest{APIVersion: v1.Version, InstanceID: a.Instance.ID, Force: true}); err == nil {
		t.Fatal("container absence alone forgot retained SQL storage")
	}
	if f.shared(t).Allocations != 2 {
		t.Fatal("single-effect absence lost obligation")
	}
	// Provider non-forced removal confirms no container consumes this volume.
	if err := removeDockerResource(context.Background(), f.d, record.Volume); err != nil {
		t.Fatal(err)
	}
	if err := verifyDockerResource(context.Background(), f.d, record.Volume); !errdefs.IsNotFound(err) {
		t.Fatal("manual owned volume removal unverified")
	}
	if _, err := f.h.cli.Destroy(context.Background(), a.Instance.ID); err == nil {
		t.Fatal("default accepted missing acknowledged storage")
	}
	f.cli("destroy", "--force", "--state-dir", f.h.dir, "--json", a.Instance.ID)
	if f.shared(t).Allocations != 1 {
		t.Fatal("force settled peer obligation implicitly")
	}
	if _, err := f.h.cli.DestroyShared(context.Background(), v1.SharedDestroyRequest{APIVersion: v1.Version, ServiceID: record.ID, Force: true}); err == nil {
		t.Fatal("shared force cascaded retained peer")
	}
	f.cli("destroy", "--force", "--state-dir", f.h.dir, "--json", b.Instance.ID)
	f.cli("shared", "destroy", "--force", "--state-dir", f.h.dir, "--json", record.ID)
	for _, r := range []ownedResource{record.Server, record.Volume, record.Network} {
		if err := verifyDockerResource(context.Background(), f.d, r); !errdefs.IsNotFound(err) {
			t.Fatal("missing-storage recovery left exact fixture effect")
		}
	}
	t.Log("single missing server retained data obligation; both server and volume absent settled only selected instances and broke circular trap without recreating effects")
}

func TestSharedMariaDBForceCrash(t *testing.T) {
	f := newSharedMariaDBFixture(t, true)
	a, b := f.run("host", f.h.project), f.run("bridge", f.linked)
	ca, cb := f.credential(t, a.Instance.ID, "native"), f.credential(t, b.Instance.ID, "container")
	wb := independentSharedWitness(t, f.db(t, cb))
	startSharedCheckpointDaemon(t, f.h, "sql-release:user")
	if _, err := f.h.cli.Shared(context.Background(), "start", v1.SharedRequest{APIVersion: v1.Version, ServiceID: f.serviceID}); err != nil {
		t.Fatal(err)
	}
	admin := f.liveAdmin(t)
	var original string
	if err := admin.QueryRow("SELECT SCHEMA_COMMENT FROM information_schema.SCHEMATA WHERE SCHEMA_NAME=?", ca.database).Scan(&original); err != nil {
		t.Fatal(err)
	}
	released := false
	t.Cleanup(func() {
		if !released {
			// Join the current daemon before reading private state. Re-observe current
			// binding on restart; the Engine may assign a different host port.
			f.h.close()
			state, repairErr := openStore(f.h.dir)
			if repairErr != nil {
				t.Error(repairErr)
				return
			}
			defer func() { _ = state.db.Close() }()
			record, repairErr := state.sharedService(f.serviceID)
			if repairErr != nil {
				t.Error(repairErr)
				return
			}
			if repairErr = state.startShared(context.Background(), &record); repairErr != nil {
				t.Error(repairErr)
				return
			}
			db, repairErr := sharedDB(record)
			if repairErr != nil {
				t.Error(repairErr)
				return
			}
			defer func() { _ = db.Close() }()
			if _, repairErr = db.Exec("ALTER DATABASE `" + ca.database + "` COMMENT '" + original + "'"); repairErr != nil {
				t.Error("owned partial-release fixture repair failed")
			}
		}
	})
	if _, err := admin.Exec("ALTER DATABASE `" + ca.database + "` COMMENT '" + newID() + "'"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := f.h.cli.DestroyWithRequest(ctx, v1.DestroyRequest{APIVersion: v1.Version, InstanceID: a.Instance.ID, Force: true})
		done <- err
	}()
	joined := false
	t.Cleanup(func() {
		cancel()
		if !joined {
			<-done
		}
	})
	waitSharedCheckpoint(t, f.h, "sql-release:user")
	if dbs, users := sharedSQLCounts(t, admin, v1.ManagerRequest{Database: ca.database, Username: ca.user}); dbs != 1 || users != 0 {
		t.Fatal("forced partial effect not observed")
	}
	sharedCrash(t, f.h)
	if <-done == nil {
		t.Fatal("crashed forced release reported success")
	}
	joined = true
	_, allocation := f.private(t, a.Instance.ID)
	if allocation.Released || allocation.Request.Operation != "release" || allocation.Request.Force {
		t.Fatal("crash lost ordinary obligation or persisted force authority")
	}
	state, err := openStore(f.h.dir)
	if err != nil {
		t.Fatal(err)
	}
	err = state.db.View(func(tx *bolt.Tx) error {
		raw := tx.Bucket([]byte("shared_allocations")).Get([]byte(a.Instance.ID))
		if bytes.Contains(raw, []byte(`"force"`)) {
			return errors.New("force field persisted")
		}
		return nil
	})
	_ = state.db.Close()
	if err != nil {
		t.Fatal(err)
	}
	startSharedRecoveryDaemon(t, f.h, nil)
	if _, err = f.h.cli.Shared(context.Background(), "start", v1.SharedRequest{APIVersion: v1.Version, ServiceID: f.serviceID}); err != nil {
		t.Fatal(err)
	}
	_ = admin.Close()
	admin = f.liveAdmin(t) // New live binding; never reuse the pre-recovery port.
	if dbs, users := sharedSQLCounts(t, admin, v1.ManagerRequest{Database: ca.database, Username: ca.user}); dbs != 1 || users != 0 {
		t.Fatal("recovery automatically continued forced release")
	}
	if _, err = f.h.cli.Destroy(context.Background(), a.Instance.ID); err == nil {
		t.Fatal("default retry reused force authority")
	}
	f.cli("destroy", "--force", "--state-dir", f.h.dir, "--json", a.Instance.ID)
	released = true
	f.requireAbsent(t, ca)
	verifySharedWitness(t, f.db(t, cb), wb)
	if f.credential(t, b.Instance.ID, "container") != cb {
		t.Fatal("crash/force changed peer credentials")
	}
	f.destroy(t, b.Instance.ID)
	t.Log("real forced user DROP/crash retained mismatched database and ordinary release intent without force JSON; recovery/default did not continue force; explicit repeat verified absence")
}

func TestSharedMariaDBForceUnavailable(t *testing.T) {
	if os.Getenv("BACKLOT_DOCKER_TEST_ENDPOINT") == "" {
		t.Skip("requires explicitly granted provider")
	}
	endpoint, unavailable := sharedUnavailableProxy(t)
	f := newSharedMariaDBFixture(t, true, endpoint)
	t.Cleanup(func() { unavailable.Store(false) })
	a, b := f.run("host", f.h.project), f.run("bridge", f.linked)
	ca, cb := f.credential(t, a.Instance.ID, "native"), f.credential(t, b.Instance.ID, "container")
	peerDB := f.db(t, cb)
	wb := independentSharedWitness(t, peerDB)
	unavailable.Store(true)
	_, err := f.h.cli.DestroyWithRequest(context.Background(), v1.DestroyRequest{APIVersion: v1.Version, InstanceID: a.Instance.ID, Force: true})
	code(t, err, "cleanup_failed")
	if got := f.shared(t); got.Allocations != 2 || got.PendingReleases != 1 {
		t.Fatal("force outage forgot obligation")
	}
	verifySharedWitness(t, peerDB, wb)
	unavailable.Store(false)
	f.cli("destroy", "--force", "--state-dir", f.h.dir, "--json", a.Instance.ID)
	f.requireAbsent(t, ca)
	verifySharedWitness(t, peerDB, wb)
	f.destroy(t, b.Instance.ID)
}
