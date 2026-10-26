//go:build darwin || linux

package daemon

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/containerd/errdefs"
	"github.com/lib/pq"
	"github.com/moby/moby/client"
	v1 "github.com/octacian/backlot/api/v1"
	"github.com/octacian/backlot/internal/manager"
)

// The early slice must establish real CLI/native/bridge SQL before broad matrices.
func TestSharedPostgreSQLEarlyFlow(t *testing.T) {
	f := newSharedSQLFixture(t, true, "postgresql")
	a, b := f.run("host", f.h.project), f.run("bridge", f.linked)
	ca, cb := f.credential(t, a.Instance.ID, "native"), f.credential(t, b.Instance.ID, "container")
	if ca.database == cb.database || ca.user == cb.user || ca.password == cb.password {
		t.Fatal("allocation collision")
	}
	if status := f.shared(t); status.Allocations != 2 {
		t.Fatal("linked worktrees did not share one server")
	}
	appA, appB := f.db(t, ca), f.db(t, cb)
	wa, wb := independentSharedWitness(t, appA), independentSharedWitness(t, appB)
	for _, pair := range [][2]sharedFixtureCredential{{ca, cb}, {cb, ca}} {
		peer := pair[0]
		peer.database = pair[1].database
		db := f.db(t, peer)
		var denied *pq.Error
		err := db.PingContext(context.Background())
		_ = db.Close()
		if !errors.As(err, &denied) || denied.Code != "42501" {
			t.Fatal("peer CONNECT did not prove denial")
		}
	}
	f.cli("stop", "--state-dir", f.h.dir, "--json", a.Instance.ID)
	f.run("host", f.h.project)
	verifySharedWitness(t, appA, wa)
	verifySharedWitness(t, appB, wb)
	if f.credential(t, a.Instance.ID, "native") != ca {
		t.Fatal("persistent credentials rotated")
	}
	// Credential drift is refused before any database drop; restore only via trusted fixture admin.
	admin := f.liveAdmin(t)
	if _, err := admin.Exec("ALTER ROLE " + pq.QuoteIdentifier(ca.user) + " PASSWORD 'changed-fixture-password'"); err != nil {
		t.Fatal(err)
	}
	restored := false
	defer func() {
		if !restored {
			_, _ = admin.Exec("ALTER ROLE " + pq.QuoteIdentifier(ca.user) + " PASSWORD " + pq.QuoteLiteral(ca.password))
		}
	}()
	_, err := f.h.cli.Destroy(context.Background(), a.Instance.ID)
	if err == nil {
		t.Fatal("default release ignored credential drift")
	}
	var exists bool
	if err = admin.QueryRow("SELECT EXISTS(SELECT FROM pg_database WHERE datname=$1)", ca.database).Scan(&exists); err != nil || !exists {
		t.Fatal("drift deleted database", err)
	}
	verifySharedWitness(t, appB, wb)
	if _, err = admin.Exec("ALTER ROLE " + pq.QuoteIdentifier(ca.user) + " PASSWORD " + pq.QuoteLiteral(ca.password)); err != nil {
		t.Fatal(err)
	}
	restored = true
	_ = appA.Close()
	f.cli("destroy", "--state-dir", f.h.dir, "--json", a.Instance.ID)
	f.requireAbsent(t, ca)
	verifySharedWitness(t, appB, wb)
	_ = appB.Close()
	f.cli("destroy", "--state-dir", f.h.dir, "--json", b.Instance.ID)
	f.requireAbsent(t, cb)
	// Driver cancellation must return within the API expectation with safe manager diagnostics.
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err = admin.ExecContext(ctx, "SELECT pg_sleep(30)")
	if err == nil || time.Since(start) > 5*time.Second {
		t.Fatal("PostgreSQL cancellation exceeded bound")
	}
	_ = admin.Close()
	server, _, _ := f.server(t)
	inspected, err := f.d.ContainerInspect(context.Background(), server, client.ContainerInspectOptions{})
	if err != nil || len(inspected.Container.Mounts) != 1 || inspected.Container.NetworkSettings == nil {
		t.Fatal("exact cleanup targets unavailable", err)
	}
	targets := []ownedResource{{Kind: "container", ID: server}, {Kind: "volume", ID: inspected.Container.Mounts[0].Name}}
	for _, net := range inspected.Container.NetworkSettings.Networks {
		targets = append(targets, ownedResource{Kind: "network", ID: net.NetworkID})
	}
	for _, target := range targets {
		t.Log("exact owned pre-disposal target", target.Kind, target.ID)
	}
	f.cli("shared", "destroy", "--state-dir", f.h.dir, "--json", f.serviceID)
	for _, target := range targets {
		if observed := verifyDockerResource(context.Background(), f.d, target); !errdefs.IsNotFound(observed) {
			t.Fatal("exact owned target absence unverified", target.Kind, target.ID)
		}
	}

	t.Log("real native/bridge schema/trusted-extension DDL/DML, wrong-auth and peer/admin denial; parent persistence; credential drift preservation/retry; SQL absence; bounded cancellation")
}

// Manager validation must precede any connection even for transient force.
func TestPostgreSQLRoleCollision(t *testing.T) {
	request := v1.ManagerRequest{Version: v1.ManagerProtocolVersion, Operation: "release", Force: true, AllocationID: newID(), Ownership: newID(), Database: "bl" + newID()[:32], Password: newID()}
	request.Username = request.Database
	if _, err := manager.PostgreSQL(context.Background(), nil, "127.0.0.1", 5432, newID(), request, nil); err == nil {
		t.Fatal("colliding names accepted")
	}
}

func TestSharedPostgreSQLUnsupportedDataDirectory(t *testing.T) {
	f := newSharedSQLFixture(t, true, "postgresql")
	declaration := f.h.manifest.SharedServices["sql"]
	// The retained Docker env/mount/entrypoint still match; the declared command
	// starts an authenticated cluster elsewhere in the container writable layer.
	declaration.Args = []string{"sh", "-ec", "export PGDATA=/tmp/unsupported-postgres; exec docker-entrypoint.sh postgres"}
	f.h.manifest.SharedServices["sql"] = declaration
	f.writeBoth(t)
	accepted, err := f.h.cli.Run(context.Background(), f.request("host", f.h.project))
	if err != nil {
		t.Fatal(err)
	}
	failed := f.wait(t, accepted.Instance.ID, v1.Failed, v1.RuntimeReady)
	if failed.Instance.Execution == nil || !strings.Contains(failed.Instance.Execution.Failure, "effective data directory is unsupported") || failed.Instance.Status == v1.RuntimeReady {
		t.Fatal("unsupported storage layout was not preserved before readiness")
	}
	admin := f.liveAdmin(t)
	var directory, marker string
	var roles int
	if err = admin.QueryRow("SELECT current_setting('data_directory'),shobj_description(oid,'pg_database') FROM pg_database WHERE datname='postgres'").Scan(&directory, &marker); err != nil {
		t.Fatal(err)
	}
	if directory != "/tmp/unsupported-postgres" || marker != "default administrative connection database" {
		t.Fatal("case did not discriminate effective directory before bootstrap")
	}
	if err = admin.QueryRow("SELECT count(*) FROM pg_roles WHERE rolname LIKE 'bl%'").Scan(&roles); err != nil || roles != 0 {
		t.Fatal("unsupported layout exposed allocation roles", err)
	}
	if status := f.shared(t); status.Allocations != 0 {
		t.Fatal("unsupported layout retained an allocation intent")
	}
}

func TestSharedPostgreSQLUnsupportedSecurity(t *testing.T) {
	f := newSharedSQLFixture(t, true, "postgresql")
	declaration := f.h.manifest.SharedServices["sql"]
	declaration.Args = []string{"postgres", "-c", "password_encryption=md5"}
	f.h.manifest.SharedServices["sql"] = declaration
	f.writeBoth(t)
	accepted := f.accept(t, f.request("host", f.h.project))
	failed := f.wait(t, accepted.Instance.ID, v1.Failed, v1.RuntimeReady)
	if failed.Instance.Status != v1.Failed || failed.Instance.Execution == nil || failed.Instance.Execution.FailureDetail == nil || failed.Instance.Execution.FailureDetail.Code != "shared_unsupported" {
		t.Fatal("unsupported security was not refused before readiness")
	}
	admin := f.liveAdmin(t)
	var setting, marker string
	var roles, databases int
	if err := admin.QueryRow("SELECT current_setting('password_encryption'),shobj_description(oid,'pg_database') FROM pg_database WHERE datname='postgres'").Scan(&setting, &marker); err != nil {
		t.Fatal(err)
	}
	if setting != "md5" || marker != "default administrative connection database" {
		t.Fatal("unsupported effective security changed or maintenance bootstrapped")
	}
	if err := admin.QueryRow("SELECT (SELECT count(*) FROM pg_roles WHERE rolname LIKE 'bl%'),(SELECT count(*) FROM pg_database)").Scan(&roles, &databases); err != nil || roles != 0 || databases != 3 {
		t.Fatal("unsupported security exposed allocation roles/databases", err)
	}
	if f.shared(t).Allocations != 0 {
		t.Fatal("unsupported security retained allocation intent")
	}
	var unchanged string
	if err := admin.QueryRow("SHOW password_encryption").Scan(&unchanged); err != nil || unchanged != setting {
		t.Fatal("unsupported security was repaired", err)
	}
}

func TestSharedPostgreSQLContaminatedTemplate(t *testing.T) {
	f := newSharedSQLFixture(t, true, "postgresql")
	a := f.run("host", f.h.project)
	ca := f.credential(t, a.Instance.ID, "native")
	app := f.db(t, ca)
	witness := independentSharedWitness(t, app)
	admin := f.liveAdmin(t)
	record := f.liveAdminRecord(t)
	seed := newID()
	clone := ""
	// Register restoration before contaminating the fixture's template. Production
	// must preserve this state; only the test administrator removes its own seed.
	t.Cleanup(func() {
		_, openErr := admin.Exec("ALTER DATABASE template0 ALLOW_CONNECTIONS true")
		if openErr != nil {
			t.Error("template restoration unavailable", openErr)
			return
		}
		template, err := manager.PostgreSQLDB("127.0.0.1", record.Port, "template0", "postgres", record.AdminPassword)
		if err == nil {
			_, err = template.Exec("DROP TABLE IF EXISTS public.template_seed")
			_ = template.Close()
		}
		_, sealErr := admin.Exec("ALTER DATABASE template0 ALLOW_CONNECTIONS false")
		if err != nil || sealErr != nil {
			t.Error("template restoration failed", err, sealErr)
		}
		if clone != "" {
			target, err := manager.PostgreSQLDB("127.0.0.1", record.Port, clone, "postgres", record.AdminPassword)
			if err == nil {
				_, err = target.Exec("DROP TABLE IF EXISTS public.template_seed")
				_ = target.Close()
			}
			if err != nil {
				t.Error("owned contaminated clone restoration failed", err)
			}
		}
	})
	if _, err := admin.Exec("ALTER DATABASE template0 ALLOW_CONNECTIONS true"); err != nil {
		t.Fatal(err)
	}
	template, err := manager.PostgreSQLDB("127.0.0.1", record.Port, "template0", "postgres", record.AdminPassword)
	if err != nil {
		t.Fatal(err)
	}
	_, err = template.Exec("CREATE TABLE public.template_seed(value text); INSERT INTO public.template_seed VALUES (" + pq.QuoteLiteral(seed) + "); GRANT SELECT ON public.template_seed TO PUBLIC")
	_ = template.Close()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = admin.Exec("ALTER DATABASE template0 ALLOW_CONNECTIONS false"); err != nil {
		t.Fatal(err)
	}
	// Ready reuse must permit the existing project's schema and parent-written data.
	f.cli("stop", "--state-dir", f.h.dir, "--json", a.Instance.ID)
	f.run("host", f.h.project)
	verifySharedWitness(t, app, witness)
	accepted, err := f.h.cli.Run(context.Background(), f.request("bridge", f.linked))
	if err != nil {
		t.Fatal(err)
	}
	failed := f.wait(t, accepted.Instance.ID, v1.Failed, v1.RuntimeReady)
	if err = admin.QueryRow("SELECT datname FROM pg_database WHERE datname NOT IN ('postgres','template0','template1',$1)", ca.database).Scan(&clone); err != nil {
		t.Fatal(err)
	}
	if failed.Instance.Execution == nil || !strings.Contains(failed.Instance.Execution.Failure, "not empty") || failed.Instance.Status == v1.RuntimeReady {
		t.Fatal("contaminated clone reached ready or was not detected")
	}
	// Only two allocations exist in this exact privately owned fixture cluster.
	// Catalog lookup here observes the clone; recorded instance cleanup owns deletion.
	var login, passwordNull bool
	var limit int
	if err = admin.QueryRow("SELECT r.rolcanlogin,r.rolpassword IS NULL,d.datconnlimit FROM pg_database d JOIN pg_authid r ON r.rolname=d.datname WHERE d.datname=$1", clone).Scan(&login, &passwordNull, &limit); err != nil || login || !passwordNull || limit != 0 {
		t.Fatal("contaminated clone lost initial fence", err)
	}
	// Check the application role as well as the independent NOLOGIN owner witness.
	if err = admin.QueryRow("SELECT count(*)=0 FROM pg_authid WHERE rolname LIKE 'bl%' AND rolname<>$1 AND rolcanlogin", ca.user).Scan(&login); err != nil || !login {
		t.Fatal("contaminated allocation enabled application LOGIN", err)
	}
	target, err := manager.PostgreSQLDB("127.0.0.1", record.Port, clone, "postgres", record.AdminPassword)
	if err != nil {
		t.Fatal(err)
	}
	var retained string
	err = target.QueryRow("SELECT value FROM public.template_seed").Scan(&retained)
	_ = target.Close()
	if err != nil || retained != seed {
		t.Fatal("unexpected clone contents were deleted or changed", err)
	}
	if err = admin.QueryRow("SELECT count(*) FROM pg_database WHERE datname=$1", clone).Scan(&limit); err != nil || limit != 1 {
		t.Fatal("contaminated database was deleted", err)
	}
	verifySharedWitness(t, app, witness)
	t.Log("preserved contaminated clone and initial roles without ready exposure; existing project objects remained usable")
}
