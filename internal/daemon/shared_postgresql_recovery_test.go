//go:build darwin || linux

package daemon

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/lib/pq"
	v1 "github.com/octacian/backlot/api/v1"
	"github.com/octacian/backlot/internal/manager"
)

func TestSharedPostgreSQLBootstrapRecovery(t *testing.T) {
	for _, point := range []string{"sql-bootstrap:before-commit", "sql-bootstrap:commit"} {
		t.Run(point, func(t *testing.T) {
			f := newSharedSQLFixture(t, true, "postgresql")
			startSharedCheckpointDaemon(t, f.h, point)
			target := f.accept(t, f.request("host", f.h.project))
			waitSharedCheckpoint(t, f.h, point)
			sharedCrash(t, f.h)
			record, allocation := f.private(t, target.Instance.ID)
			if allocation.Request.AllocationID != "" {
				t.Fatal("allocation preceded maintenance initialization")
			}
			admin := sharedAdmin(t, record)
			var marker string
			var public int
			if err := admin.QueryRow("SELECT shobj_description(oid,'pg_database'),(SELECT count(*) FROM aclexplode(COALESCE(datacl,acldefault('d',datdba))) WHERE grantee=0) FROM pg_database WHERE datname='postgres'").Scan(&marker, &public); err != nil {
				t.Fatal(err)
			}
			if point == "sql-bootstrap:before-commit" {
				if marker != "default administrative connection database" || public != 2 {
					t.Fatal("maintenance transaction did not roll back")
				}
			} else if marker != postgresBootstrapMarker(record) || public != 0 {
				t.Fatal("lost acknowledgment lost atomic witness/restrictions")
			}
			_ = admin.Close()
			startSharedRecoveryDaemon(t, f.h, nil)
			f.explicitRestart(t, target.Instance.ID, "host", f.h.project)
			actual := f.credential(t, target.Instance.ID, "native")
			value := independentSharedWitness(t, f.db(t, actual))
			f.cli("stop", "--state-dir", f.h.dir, "--json", target.Instance.ID)
			f.run("host", f.h.project)
			verifySharedWitness(t, f.db(t, actual), value)
			f.destroy(t, target.Instance.ID)
			f.requireAbsent(t, actual)
		})
	}
}

func TestSharedPostgreSQLMaintenanceDrift(t *testing.T) {
	f := newSharedSQLFixture(t, true, "postgresql")
	a := f.run("host", f.h.project)
	credential := f.credential(t, a.Instance.ID, "native")
	value := independentSharedWitness(t, f.db(t, credential))
	admin := f.liveAdmin(t)
	var marker string
	if err := admin.QueryRow("SELECT shobj_description(oid,'pg_database') FROM pg_database WHERE datname='postgres'").Scan(&marker); err != nil {
		t.Fatal(err)
	}
	restore := func() {
		if _, err := admin.Exec("REVOKE ALL ON DATABASE postgres FROM PUBLIC; COMMENT ON DATABASE postgres IS " + pq.QuoteLiteral(marker)); err != nil {
			t.Error(err)
		}
	}
	t.Cleanup(restore)
	for _, fault := range []string{"mismatch", "missing", "acl"} {
		t.Run(fault, func(t *testing.T) {
			query := "COMMENT ON DATABASE postgres IS 'mismatched'"
			if fault == "missing" {
				query = "COMMENT ON DATABASE postgres IS 'default administrative connection database'"
			}
			if fault == "acl" {
				query = "GRANT CONNECT ON DATABASE postgres TO PUBLIC"
			}
			if _, err := admin.Exec(query); err != nil {
				t.Fatal(err)
			}
			var before string
			var grants int
			if err := admin.QueryRow("SELECT shobj_description(oid,'pg_database'),(SELECT count(*) FROM aclexplode(datacl) WHERE grantee=0) FROM pg_database WHERE datname='postgres'").Scan(&before, &grants); err != nil {
				t.Fatal(err)
			}
			_, err := f.h.cli.Shared(context.Background(), "start", v1.SharedRequest{APIVersion: v1.Version, ServiceID: f.serviceID})
			if err == nil {
				t.Fatal("maintenance drift accepted")
			}
			var after string
			var actual int
			if err = admin.QueryRow("SELECT shobj_description(oid,'pg_database'),(SELECT count(*) FROM aclexplode(datacl) WHERE grantee=0) FROM pg_database WHERE datname='postgres'").Scan(&after, &actual); err != nil || after != before || actual != grants {
				t.Fatal("maintenance drift repaired", err)
			}
			verifySharedWitness(t, f.db(t, credential), value)
			restore()
			if _, err = f.h.cli.Shared(context.Background(), "start", v1.SharedRequest{APIVersion: v1.Version, ServiceID: f.serviceID}); err != nil {
				t.Fatal("uncertain status could not reuse restored witness", err)
			}
		})
	}
	// Force releases recorded allocation names without changing bad maintenance metadata.
	if _, err := admin.Exec("COMMENT ON DATABASE postgres IS 'mismatched'"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.h.cli.DestroyWithRequest(context.Background(), v1.DestroyRequest{APIVersion: v1.Version, InstanceID: a.Instance.ID, Force: true}); err != nil {
		t.Fatal(err)
	}
	var after string
	if err := admin.QueryRow("SELECT shobj_description(oid,'pg_database') FROM pg_database WHERE datname='postgres'").Scan(&after); err != nil || after != "mismatched" {
		t.Fatal("force repaired maintenance", err)
	}
	restore()
	f.requireAbsent(t, credential)
}

func TestSharedPostgreSQLDriftForce(t *testing.T) {
	for _, fault := range []string{"owner-marker", "app-marker", "role-setting", "membership", "database-acl", "password"} {
		t.Run(fault, func(t *testing.T) {
			f := newSharedSQLFixture(t, true, "postgresql")
			a, b := f.run("host", f.h.project), f.run("bridge", f.linked)
			ca, cb := f.credential(t, a.Instance.ID, "native"), f.credential(t, b.Instance.ID, "container")
			wa, wb := independentSharedWitness(t, f.db(t, ca)), independentSharedWitness(t, f.db(t, cb))
			admin := f.liveAdmin(t)
			var ownerMarker, appMarker string
			if err := admin.QueryRow("SELECT (SELECT shobj_description(oid,'pg_authid') FROM pg_roles WHERE rolname=$1),(SELECT shobj_description(oid,'pg_authid') FROM pg_roles WHERE rolname=$2)", ca.database, ca.user).Scan(&ownerMarker, &appMarker); err != nil {
				t.Fatal(err)
			}
			query, repair := "", ""
			switch fault {
			case "owner-marker":
				query = "COMMENT ON ROLE " + pq.QuoteIdentifier(ca.database) + " IS 'changed'"
				repair = "COMMENT ON ROLE " + pq.QuoteIdentifier(ca.database) + " IS " + pq.QuoteLiteral(ownerMarker)
			case "app-marker":
				query = "COMMENT ON ROLE " + pq.QuoteIdentifier(ca.user) + " IS 'changed'"
				repair = "COMMENT ON ROLE " + pq.QuoteIdentifier(ca.user) + " IS " + pq.QuoteLiteral(appMarker)
			case "role-setting":
				query = "ALTER ROLE " + pq.QuoteIdentifier(ca.user) + " SET work_mem='8MB'"
				repair = "ALTER ROLE " + pq.QuoteIdentifier(ca.user) + " RESET ALL"
			case "membership":
				query = "GRANT pg_read_all_data TO " + pq.QuoteIdentifier(ca.user)
				repair = "REVOKE pg_read_all_data FROM " + pq.QuoteIdentifier(ca.user)
			case "database-acl":
				query = "GRANT CONNECT ON DATABASE " + pq.QuoteIdentifier(ca.database) + " TO PUBLIC"
				repair = "REVOKE CONNECT ON DATABASE " + pq.QuoteIdentifier(ca.database) + " FROM PUBLIC"
			case "password":
				query = "ALTER ROLE " + pq.QuoteIdentifier(ca.user) + " PASSWORD 'changed'"
				repair = "ALTER ROLE " + pq.QuoteIdentifier(ca.user) + " PASSWORD " + pq.QuoteLiteral(ca.password)
			}
			settled := false
			t.Cleanup(func() {
				if !settled {
					if _, err := admin.Exec(repair); err != nil {
						t.Error("owned fault restoration failed", err)
					}
				}
			})
			if _, err := admin.Exec(query); err != nil {
				t.Fatal(err)
			}
			if _, err := f.h.cli.Destroy(context.Background(), a.Instance.ID); err == nil {
				t.Fatal("default accepted drift")
			}
			root := f.liveAdminRecord(t)
			target, err := manager.PostgreSQLDB("127.0.0.1", root.Port, ca.database, "postgres", root.AdminPassword)
			if err != nil {
				t.Fatal(err)
			}
			verifySharedWitness(t, target, wa)
			_ = target.Close()
			verifySharedWitness(t, f.db(t, cb), wb)
			if fault == "owner-marker" {
				startSharedCheckpointDaemon(t, f.h, "release-intent")
				done := make(chan error, 1)
				go func() {
					_, err := f.h.cli.DestroyWithRequest(context.Background(), v1.DestroyRequest{APIVersion: v1.Version, InstanceID: a.Instance.ID, Force: true})
					done <- err
				}()
				waitSharedCheckpoint(t, f.h, "release-intent")
				sharedCrash(t, f.h)
				if <-done == nil {
					t.Fatal("faulted force reported success")
				}
				_, allocation := f.private(t, a.Instance.ID)
				if allocation.Request.Force || allocation.Request.Operation != "release" {
					t.Fatal("force was persisted or release intent lost")
				}
				startSharedRecoveryDaemon(t, f.h, nil)
				if _, err = f.h.cli.Destroy(context.Background(), a.Instance.ID); err == nil {
					t.Fatal("force inferred on retry")
				}
			}
			if _, err = f.h.cli.DestroyWithRequest(context.Background(), v1.DestroyRequest{APIVersion: v1.Version, InstanceID: a.Instance.ID, Force: true}); err != nil {
				t.Fatal(err)
			}
			settled = true
			f.requireAbsent(t, ca)
			verifySharedWitness(t, f.db(t, cb), wb)
			if _, err = f.h.cli.DestroyShared(context.Background(), v1.SharedDestroyRequest{APIVersion: v1.Version, ServiceID: f.serviceID, Force: true}); err == nil {
				t.Fatal("service force bypassed peer allocation")
			}
			f.cli("destroy", "--force", "--state-dir", f.h.dir, "--json", a.Instance.ID)
			f.destroy(t, b.Instance.ID)
		})
	}
}

func TestSharedPostgreSQLSessionPreservation(t *testing.T) {
	f := newSharedSQLFixture(t, true, "postgresql")
	a := f.run("host", f.h.project)
	c := f.credential(t, a.Instance.ID, "native")
	app := f.db(t, c)
	value := independentSharedWitness(t, app)
	conn, err := app.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	for _, force := range []bool{false, true} {
		if _, err = f.h.cli.DestroyWithRequest(context.Background(), v1.DestroyRequest{APIVersion: v1.Version, InstanceID: a.Instance.ID, Force: force}); err == nil {
			t.Fatal("live session was terminated/bypassed")
		}
		var got string
		if err = conn.QueryRowContext(context.Background(), "SELECT value FROM independent_witness").Scan(&got); err != nil || got != value {
			t.Fatal("retained session/data changed", err)
		}
	}
	_ = conn.Close()
	f.destroy(t, a.Instance.ID)
	f.requireAbsent(t, c)
}

func TestSharedPostgreSQLAllocationCancellation(t *testing.T) {
	for _, keep := range []bool{false, true} {
		t.Run(fmt.Sprint(keep), func(t *testing.T) {
			f := newSharedSQLFixture(t, true, "postgresql")
			t.Setenv("BACKLOT_SHARED_TEST_NOTIFY", "cancel-effect")
			terminal := f.h.manifest.Components["native"]
			terminal.Policy = v1.EachStart
			terminal.Initializes = nil
			f.h.manifest.Components["native"] = terminal
			scene := f.h.manifest.Scenes["host"]
			scene.Lifetime = v1.Disposable
			scene.TerminalJob = "native"
			f.h.manifest.Scenes["allocation-cancel"] = scene
			f.writeBoth(t)
			startSharedCheckpointDaemon(t, f.h, "sql-allocate:database")
			request := f.request("allocation-cancel", f.h.project)
			request.Options.KeepOnFailure = keep
			accepted := f.accept(t, request)
			stopLease := f.keepLease(t, accepted)
			checkpoint := waitSharedCheckpoint(t, f.h, "sql-allocate:database")
			admin := f.liveAdmin(t)
			var databases, roles int
			if err := admin.QueryRow("SELECT (SELECT count(*) FROM pg_database WHERE datname LIKE 'bl%'),(SELECT count(*) FROM pg_roles WHERE rolname LIKE 'bl%')").Scan(&databases, &roles); err != nil || databases != 1 || roles != 2 {
				t.Fatal("cancellation did not hold real partial SQL effects", err)
			}
			stopLease()
			done := make(chan error, 1)
			go func() { _, err := f.h.cli.Cancel(context.Background(), accepted.Instance.ID); done <- err }()
			waitSharedCheckpoint(t, f.h, "cancel-effect")
			if _, err := checkpoint.Write([]byte{1}); err != nil {
				t.Fatal(err)
			}
			if err := <-done; err != nil {
				t.Fatal(err)
			}
			final := f.wait(t, accepted.Instance.ID, v1.Cancelled)
			if final.Instance.Execution.Kept != keep {
				t.Fatal("partial cancellation keep policy differs")
			}
			for _, private := range []string{f.liveAdminRecord(t).AdminPassword, "SCRAM-SHA-256"} {
				if strings.Contains(final.Instance.Execution.Failure, private) {
					t.Fatal("unsafe manager diagnostic")
				}
			}
			count := f.shared(t).Allocations
			if keep && count != 1 || !keep && count != 0 {
				t.Fatal("API cancellation failed to join/retain cleanup")
			}
			f.destroy(t, accepted.Instance.ID)
		})
	}
}

func TestSharedPostgreSQLMissingReadyAndDependency(t *testing.T) {
	for _, fault := range []string{"missing-ready", "external-dependency"} {
		t.Run(fault, func(t *testing.T) {
			f := newSharedSQLFixture(t, true, "postgresql")
			a, b := f.run("host", f.h.project), f.run("bridge", f.linked)
			ca, cb := f.credential(t, a.Instance.ID, "native"), f.credential(t, b.Instance.ID, "container")
			peer := independentSharedWitness(t, f.db(t, cb))
			admin := f.liveAdmin(t)
			f.cli("stop", "--state-dir", f.h.dir, "--json", a.Instance.ID)
			if fault == "missing-ready" {
				if _, err := admin.Exec("DROP DATABASE " + pq.QuoteIdentifier(ca.database)); err != nil {
					t.Fatal(err)
				}
				request := f.request("host", f.h.project)
				request.InstanceID = a.Instance.ID
				if _, err := f.h.cli.Restart(context.Background(), request); err != nil {
					t.Fatal(err)
				}
				f.wait(t, a.Instance.ID, v1.Failed)
			} else {
				// An owned role's extra object is not authority to cascade/drop it.
				t.Cleanup(func() {
					_, err := admin.Exec("DROP TABLE IF EXISTS public.external_dependency")
					if err != nil {
						t.Error(err)
					}
				})
				if _, err := admin.Exec("CREATE TABLE public.external_dependency(value text); ALTER TABLE public.external_dependency OWNER TO " + pq.QuoteIdentifier(ca.user)); err != nil {
					t.Fatal(err)
				}
				for _, force := range []bool{false, true} {
					if _, err := f.h.cli.DestroyWithRequest(context.Background(), v1.DestroyRequest{APIVersion: v1.Version, InstanceID: a.Instance.ID, Force: force}); err == nil {
						t.Fatal("role dependency was bypassed")
					}
				}
				var exists bool
				if err := admin.QueryRow("SELECT to_regclass('public.external_dependency') IS NOT NULL").Scan(&exists); err != nil || !exists {
					t.Fatal("external dependency deleted", err)
				}
			}
			var databases, roles int
			if err := admin.QueryRow("SELECT (SELECT count(*) FROM pg_database WHERE datname=$1),(SELECT count(*) FROM pg_roles WHERE rolname IN ($1,$2))", ca.database, ca.user).Scan(&databases, &roles); err != nil || databases != 0 || roles != 2 {
				t.Fatal("post-drop role witnesses were lost or database recreated", err)
			}
			verifySharedWitness(t, f.db(t, cb), peer)
			if fault == "external-dependency" {
				if _, err := admin.Exec("DROP TABLE public.external_dependency"); err != nil {
					t.Fatal(err)
				}
			}
			if fault == "missing-ready" {
				if _, err := admin.Exec("ALTER ROLE " + pq.QuoteIdentifier(ca.user) + " PASSWORD 'changed-after-drop'"); err != nil {
					t.Fatal(err)
				}
			}
			// After database absence, witnessed role-only release needs no password proof.
			f.destroy(t, a.Instance.ID)
			f.requireAbsent(t, ca)
			verifySharedWitness(t, f.db(t, cb), peer)
			f.destroy(t, b.Instance.ID)
		})
	}
}
