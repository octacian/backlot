//go:build darwin || linux

package daemon

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/lib/pq"
	"github.com/moby/moby/client"
	v1 "github.com/octacian/backlot/api/v1"
	"github.com/octacian/backlot/internal/manager"
)

func TestSharedPostgreSQLPrivilegedResolution(t *testing.T) {
	f := newSharedSQLFixture(t, true, "postgresql")
	a, b := f.run("host", f.h.project), f.run("bridge", f.linked)
	ca, cb := f.credential(t, a.Instance.ID, "native"), f.credential(t, b.Instance.ID, "container")
	app := f.db(t, ca)
	peer := independentSharedWitness(t, f.db(t, cb))
	if _, err := app.Exec(`CREATE TABLE public.management_probe(actor text);
 CREATE FUNCTION public.has_schema_privilege(text,oid,text) RETURNS boolean
 LANGUAGE plpgsql SECURITY INVOKER AS $$ BEGIN
 INSERT INTO public.management_probe VALUES (current_user); RETURN true; END $$`); err != nil {
		t.Fatal(err)
	}
	var allowed bool
	if err := app.QueryRow("SELECT public.has_schema_privilege($1::text, 'public'::regnamespace::oid, 'USAGE')", ca.user).Scan(&allowed); err != nil || !allowed {
		t.Fatal("application overload control was not callable", err)
	}
	var actor string
	if err := app.QueryRow("SELECT actor FROM public.management_probe").Scan(&actor); err != nil || actor != ca.user {
		t.Fatal("invoker control was not the application", err)
	}
	if _, err := app.Exec("TRUNCATE public.management_probe"); err != nil {
		t.Fatal(err)
	}
	f.cli("stop", "--state-dir", f.h.dir, "--json", a.Instance.ID)
	f.run("host", f.h.project)
	var count int
	if err := app.QueryRow("SELECT count(*) FROM public.management_probe").Scan(&count); err != nil || count != 0 {
		t.Fatal("privileged ready reuse executed application overload", err)
	}
	root := f.liveAdminRecord(t)
	admin, err := manager.PostgreSQLDB("127.0.0.1", root.Port, ca.database, "postgres", root.AdminPassword)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = admin.Close() })
	var path string
	err = admin.QueryRow("SHOW search_path").Scan(&path)
	_ = admin.Close()
	if err != nil || path != "pg_catalog,pg_temp" {
		t.Fatal("fresh allocation administrator path was not fixed", err)
	}
	held, err := app.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = held.Close() })
	if _, err = f.h.cli.Destroy(context.Background(), a.Instance.ID); err == nil || !strings.Contains(err.Error(), "PostgreSQL effect failed") {
		t.Fatal("default release did not reach the held-session DROP fence", err)
	}
	if err = held.QueryRowContext(context.Background(), "SELECT count(*) FROM public.management_probe").Scan(&count); err != nil || count != 0 {
		t.Fatal("privileged default release executed application overload", err)
	}
	_ = held.Close()
	verifySharedWitness(t, f.db(t, cb), peer)
	f.destroy(t, a.Instance.ID)
	f.requireAbsent(t, ca)
	verifySharedWitness(t, f.db(t, cb), peer)
	f.destroy(t, b.Instance.ID)
}

func TestSharedPostgreSQLStalledConnection(t *testing.T) {
	f := newSharedSQLFixture(t, true, "postgresql")
	declaration := f.h.manifest.SharedServices["sql"]
	declaration.ReadinessTimeout = "15s"
	f.h.manifest.SharedServices["sql"] = declaration
	f.writeBoth(t)
	peer := f.run("bridge", f.linked)
	credential := f.credential(t, peer.Instance.ID, "container")
	witness := independentSharedWitness(t, f.db(t, credential))
	startSharedCheckpointDaemon(t, f.h, "sql-allocate:roles")
	request := f.request("host", f.h.project)
	const startupLimit = 30 * time.Second
	request.Options.StartupTimeout = startupLimit.String()
	requested := time.Now()
	target := f.accept(t, request)
	checkpoint := waitSharedCheckpoint(t, f.h, "sql-allocate:roles")
	admin := f.liveAdmin(t)
	pinned, err := admin.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	queryDone := make(chan struct{})
	queryStarted := false
	t.Cleanup(func() {
		if queryStarted {
			select {
			case <-queryDone:
			default:
				t.Error("pinned connection has an unjoined query; preserving uncertainty")
				return
			}
		}
		_ = pinned.Close()
	})
	var roles []string
	var initial bool
	if err = pinned.PingContext(context.Background()); err != nil {
		t.Fatal("original parent connection did not authenticate", err)
	}
	if err = pinned.QueryRowContext(context.Background(), `SELECT array_agg(rolname ORDER BY rolname),count(*)=2 AND bool_and(NOT rolcanlogin AND rolpassword IS NULL)
 FROM pg_authid WHERE rolname LIKE 'bl%' AND rolname NOT IN ($1,$2)`, credential.database, credential.user).Scan(pq.Array(&roles), &initial); err != nil || !initial {
		t.Fatal("checkpoint did not commit a NOLOGIN/password-null role pair", err)
	}
	server, _, _ := f.server(t)
	paused := func() (bool, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		out, err := f.d.ContainerInspect(ctx, server, client.ContainerInspectOptions{})
		if err != nil {
			return false, err
		}
		if out.Container.ID != server || out.Container.State == nil || out.Container.Config == nil || out.Container.Config.Labels["io.backlot.shared-service"] != f.serviceID {
			return false, errors.New("owned fixture pause identity changed")
		}
		return out.Container.State.Paused, nil
	}
	unpause := func() error {
		active, err := paused()
		if err != nil || !active {
			return err
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, err = f.d.ContainerUnpause(ctx, server, client.ContainerUnpauseOptions{})
		return err
	}
	var queryErr error
	// Registration precedes pause; unpause is teardown, never a success witness.
	t.Cleanup(func() {
		if err := unpause(); err != nil {
			t.Error("owned paused server restoration unresolved", err)
		}
		if queryStarted {
			select {
			case <-queryDone:
			case <-time.After(15 * time.Second):
				t.Error("original parent query remains unjoined")
				return
			}
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	_, err = f.d.ContainerPause(ctx, server, client.ContainerPauseOptions{})
	cancel()
	if err != nil {
		t.Fatal(err)
	}
	if active, err := paused(); err != nil || !active {
		t.Fatal("owned server did not stall", err)
	}
	started := time.Now()
	queryStarted = true
	go func() {
		defer close(queryDone)
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()
		_, queryErr = pinned.ExecContext(ctx, "SELECT 1")
	}()
	if _, err = checkpoint.Write([]byte{1}); err != nil {
		t.Fatal(err)
	}
	// The daemon starts its clock after this request timestamp. Its context stays
	// live beyond the entire unchanged owner bound, not merely through setup.
	liveFor := startupLimit - time.Since(requested)
	if liveFor <= 15*time.Second {
		t.Fatal("no proven live daemon context interval at SQL commencement")
	}
	select {
	case <-queryDone:
	case <-time.After(15 * time.Second):
		t.Fatal("authenticated original socket did not interrupt without unpause")
	}
	if queryErr == nil {
		t.Fatal("paused original connection unexpectedly succeeded")
	}
	queryElapsed := time.Since(started)
	final := f.wait(t, target.Instance.ID, v1.Failed)
	ownerElapsed := time.Since(started)
	if ownerElapsed >= 15*time.Second || final.Instance.Execution.CleanupFailure != "" {
		t.Fatal("daemon owner did not finish within existing join budget")
	}
	controlStarted := time.Now()
	ctx, cancel = context.WithTimeout(context.Background(), 19*time.Second)
	_, err = f.h.cli.Shared(ctx, "start", v1.SharedRequest{APIVersion: v1.Version, ServiceID: f.serviceID})
	controlContextErr := ctx.Err()
	cancel()
	var unavailable *v1.PlanError
	if controlContextErr != nil || !errors.As(err, &unavailable) || (unavailable.Code != "shared_unavailable" && unavailable.Code != "internal") || f.shared(t).Status != "uncertain" {
		t.Fatal("second control did not return a server-side unavailable result", err)
	}
	if active, err := paused(); err != nil || !active {
		t.Fatal("completion relied on restoring server responsiveness", err)
	}
	t.Log("completed while server remained paused; live daemon context interval", liveFor, "original query", queryElapsed, "daemon owner", ownerElapsed, "second server control", unavailable.Code, time.Since(controlStarted))
	if err = unpause(); err != nil {
		t.Fatal(err)
	}
	_ = pinned.Close()
	// Join the retained daemon before private intent inspection; SQL effects remain.
	sharedCrash(t, f.h)
	record, allocation := f.private(t, target.Instance.ID)
	samePair := len(roles) == 2 && ((roles[0] == allocation.Request.Database && roles[1] == allocation.Request.Username) || (roles[1] == allocation.Request.Database && roles[0] == allocation.Request.Username))
	if allocation.Released || allocation.Request.Operation != "allocate" || allocation.Result.Effect == "ready" || !samePair {
		t.Fatal("stalled SQL lost retained exact intent/witnesses")
	}
	root := sharedAdmin(t, record)
	inspect := allocation.Request
	inspect.Operation = "inspect"
	observed, err := sharedManager(context.Background(), record, root, inspect, nil)
	_ = root.Close()
	if err != nil || observed.Ownership != "owned" || observed.Effect != "partial" {
		t.Fatal("uncertain SQL effect was not preserved in initial witnessed state", err)
	}
	startSharedRecoveryDaemon(t, f.h, nil)
	f.explicitRestart(t, target.Instance.ID, "host", f.h.project)
	actual := f.credential(t, target.Instance.ID, "native")
	if actual.database != inspect.Database || actual.user != inspect.Username || actual.password != inspect.Password {
		t.Fatal("stalled effect recovery rotated retained credentials")
	}
	f.explicitRestart(t, peer.Instance.ID, "bridge", f.linked)
	if f.credential(t, peer.Instance.ID, "container") != credential {
		t.Fatal("peer credentials changed during stalled recovery")
	}
	verifySharedWitness(t, f.db(t, credential), witness)
	f.destroy(t, target.Instance.ID)
	f.requireAbsent(t, actual)
	f.destroy(t, peer.Instance.ID)
}
