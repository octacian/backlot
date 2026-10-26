//go:build darwin || linux

package daemon

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/containerd/errdefs"
	"github.com/moby/moby/client"
	v1 "github.com/octacian/backlot/api/v1"
	"github.com/octacian/backlot/internal/manager"
)

// Engine access is trusted fixture administration, never an application value.
// Read only the exact server in this private fixture and never print its env.
func (f *sharedMariaDBFixture) liveAdminRecord(t *testing.T) sharedServiceRecord {
	t.Helper()
	id, port, running := f.server(t)
	if !running {
		t.Fatal("owned SQL fixture is stopped")
	}
	out, err := f.d.ContainerInspect(context.Background(), id, client.ContainerInspectOptions{})
	if err != nil || out.Container.Config == nil {
		t.Fatal("owned administration unavailable", err)
	}
	password := ""
	for _, value := range out.Container.Config.Env {
		prefix := "MARIADB_ROOT_PASSWORD="
		if f.manager == "postgresql" {
			prefix = "POSTGRES_PASSWORD="
		}
		if s, ok := strings.CutPrefix(value, prefix); ok {
			password = s
		}
	}
	if !validID(password) {
		t.Fatal("private administration contract incomplete")
	}
	owner := out.Container.Config.Labels["io.backlot.owner"]
	if !validID(owner) {
		t.Fatal("private fixture server authority incomplete")
	}
	return sharedServiceRecord{Declaration: v1.SharedService{Manager: f.manager, Port: f.internalPort}, AdminPassword: password, Port: port, Server: ownedResource{ID: id, Token: owner}}
}
func (f *sharedMariaDBFixture) liveAdmin(t *testing.T) *sql.DB {
	return sharedAdmin(t, f.liveAdminRecord(t))
}

func (f *sharedMariaDBFixture) requireAbsent(t *testing.T, c sharedFixtureCredential) {
	t.Helper()
	db := f.liveAdmin(t)
	defer func() { _ = db.Close() }()
	if f.manager == "postgresql" {
		var count int
		if err := db.QueryRow("SELECT (SELECT count(*) FROM pg_database WHERE datname=$1)+(SELECT count(*) FROM pg_roles WHERE rolname IN ($1,$2))", c.database, c.user).Scan(&count); err != nil || count != 0 {
			t.Fatal("PostgreSQL three-target absence unverified", err)
		}
		return
	}
	databases, users := sharedSQLCounts(t, db, v1.ManagerRequest{Database: c.database, Username: c.user})
	if databases != 0 || users != 0 {
		t.Fatal("automatic cleanup left actual SQL database/user", databases, users)
	}
}

func TestSharedMariaDBOwnership(t *testing.T) {
	for _, fault := range []string{"database-comment", "user-authentication", "unexpected-grant", "missing-acknowledged-database"} {
		t.Run(fault, func(t *testing.T) {
			f := newSharedMariaDBFixture(t, true)
			ctx := context.Background()
			a, b := f.run("host", f.h.project), f.run("bridge", f.linked)
			ca, cb := f.credential(t, a.Instance.ID, "native"), f.credential(t, b.Instance.ID, "container")
			wa, wb := independentSharedWitness(t, f.db(t, ca)), independentSharedWitness(t, f.db(t, cb))
			sharedStop(t, f.h)
			record, allocation := f.private(t, a.Instance.ID)
			startSharedRecoveryDaemon(t, f.h, nil)
			if _, err := f.h.cli.Shared(ctx, "start", v1.SharedRequest{APIVersion: v1.Version, ServiceID: record.ID}); err != nil {
				t.Fatal(err)
			}
			admin := f.liveAdmin(t)
			inspect := allocation.Request
			inspect.Operation = "inspect"
			observed, err := manager.MariaDB(ctx, admin, inspect, nil)
			if err != nil || observed.Ownership != "owned" || observed.Effect != "ready" {
				t.Fatal("fault target not verified owned before mutation", err)
			}
			var originalComment string
			if err = admin.QueryRow("SELECT SCHEMA_COMMENT FROM information_schema.SCHEMATA WHERE SCHEMA_NAME=?", ca.database).Scan(&originalComment); err != nil || !validID(originalComment) {
				t.Fatal("owned database marker unavailable", err)
			}
			restoreQuery := ""
			query := ""
			switch fault {
			case "database-comment":
				query = "ALTER DATABASE `" + ca.database + "` COMMENT '" + newID() + "'"
				restoreQuery = "ALTER DATABASE `" + ca.database + "` COMMENT '" + originalComment + "'"
			case "user-authentication":
				query = "ALTER USER '" + ca.user + "'@'%' IDENTIFIED BY '" + newID() + "'"
				restoreQuery = "ALTER USER '" + ca.user + "'@'%' IDENTIFIED BY '" + ca.password + "'"
			case "unexpected-grant":
				query = "GRANT SELECT ON mysql.* TO '" + ca.user + "'@'%'"
				restoreQuery = "REVOKE SELECT ON mysql.* FROM '" + ca.user + "'@'%'"
			case "missing-acknowledged-database":
				query = "DROP DATABASE `" + ca.database + "`"
			}
			repaired := false
			// Parent owns this deliberate fixture mutation; repair only that exact known
			// change before normal cleanup, never adopt/delete a mismatched object.
			repair := func() {
				if repaired {
					return
				}
				if restoreQuery != "" {
					if _, err := admin.Exec(restoreQuery); err != nil {
						t.Error("owned fixture mutation repair failed; preserve state")
						return
					}
				}
				repaired = true
			}
			t.Cleanup(repair)
			if _, err = admin.Exec(query); err != nil {
				t.Fatal("owned SQL fault failed", err)
			}
			r := f.request("host", f.h.project)
			r.InstanceID = a.Instance.ID
			if _, err = f.h.cli.Restart(ctx, r); err != nil {
				t.Fatal(err)
			}
			failed := f.wait(t, a.Instance.ID, v1.Failed)
			if failed.Instance.Execution.FailureDetail == nil || failed.Instance.Execution.FailureDetail.Code != "allocation_missing" {
				t.Fatal("missing/mismatched acknowledged allocation not fenced")
			}
			databases, users := sharedSQLCounts(t, admin, allocation.Request)
			if fault == "missing-acknowledged-database" {
				if databases != 0 || users != 1 {
					t.Fatal("missing acknowledged database was silently recreated")
				}
			} else {
				if databases != 1 || users != 1 {
					t.Fatal("mismatch provisioning changed objects")
				}
				_, err = f.h.cli.Destroy(ctx, a.Instance.ID)
				code(t, err, "cleanup_failed")
				afterDB, afterUser := sharedSQLCounts(t, admin, allocation.Request)
				if afterDB != 1 || afterUser != 1 {
					t.Fatal("cleanup partially deleted before verifying both identities")
				}
				var witness string
				if err = admin.QueryRow("SELECT value FROM `" + ca.database + "`.independent_witness").Scan(&witness); err != nil || witness != wa {
					t.Fatal("uncertain target data was mutated/deleted", err)
				}
				if f.shared(t).PendingReleases != 1 {
					t.Fatal("uncertain release obligation not inspectable")
				}
			}
			verifySharedWitness(t, f.db(t, cb), wb)
			repair()
			if !repaired {
				t.Fatal("fixture repair could not be verified")
			}
			f.destroy(t, a.Instance.ID)
			f.requireAbsent(t, ca)
			verifySharedWitness(t, f.db(t, cb), wb)
			f.destroy(t, b.Instance.ID)
			t.Log("mismatch/missing state preserved without name-only adoption/recreation/deletion; exact fixture repair followed by explicit cleanup", fault)
		})
	}
}

func TestSharedMariaDBPreexistingDatabase(t *testing.T) {
	f := newSharedMariaDBFixture(t, true)
	ctx := context.Background()
	startSharedCheckpointDaemon(t, f.h, "allocation-intent")
	target := f.accept(t, f.request("host", f.h.project))
	waitSharedCheckpoint(t, f.h, "allocation-intent")
	sharedCrash(t, f.h)
	record, allocation := f.private(t, target.Instance.ID)
	admin := sharedAdmin(t, record)
	foreignMarker := newID()
	foreignPending := true
	// Registered before creation. This is separately test-owned, with its own
	// marker; Backlot's allocation authority does not authorize deleting it.
	cleanupForeign := func() {
		if !foreignPending {
			return
		}
		var got string
		err := admin.QueryRow("SELECT SCHEMA_COMMENT FROM information_schema.SCHEMATA WHERE SCHEMA_NAME=?", allocation.Request.Database).Scan(&got)
		if errors.Is(err, sql.ErrNoRows) {
			foreignPending = false
			return
		}
		if err != nil || got != foreignMarker {
			t.Error("foreign fixture identity uncertain; preserved")
			return
		}
		if _, err = admin.Exec("DROP DATABASE `" + allocation.Request.Database + "`"); err != nil {
			t.Error("foreign fixture cleanup failed; preserved")
			return
		}
		foreignPending = false
	}
	t.Cleanup(cleanupForeign)
	if _, err := admin.Exec("CREATE DATABASE `" + allocation.Request.Database + "` COMMENT '" + foreignMarker + "'"); err != nil {
		t.Fatal(err)
	}
	startSharedRecoveryDaemon(t, f.h, nil)
	r := f.request("host", f.h.project)
	r.InstanceID = target.Instance.ID
	if _, err := f.h.cli.Restart(ctx, r); err != nil {
		t.Fatal(err)
	}
	f.wait(t, target.Instance.ID, v1.Failed)
	_, err := f.h.cli.Destroy(ctx, target.Instance.ID)
	code(t, err, "cleanup_failed")
	if f.shared(t).PendingReleases != 1 {
		t.Fatal("foreign object release obligation forgotten")
	}
	// Restore root connection to the actual current binding after server recovery.
	_ = admin.Close()
	admin = f.liveAdmin(t)
	var marker string
	if err = admin.QueryRow("SELECT SCHEMA_COMMENT FROM information_schema.SCHEMATA WHERE SCHEMA_NAME=?", allocation.Request.Database).Scan(&marker); err != nil || marker != foreignMarker {
		t.Fatal("preexisting similarly named database was adopted/deleted", err)
	}
	databases, users := sharedSQLCounts(t, admin, allocation.Request)
	if databases != 1 || users != 0 {
		t.Fatal("uncertain preexisting database provisioning advanced")
	}
	cleanupForeign()
	if foreignPending {
		t.Fatal("test-owned foreign cleanup uncertain")
	}
	f.destroy(t, target.Instance.ID)
	t.Log("preexisting similarly named SQL database preserved, no user/grant adopted, durable obligation visible, separate marker-authorized fixture cleanup verified")
}

func TestSharedMariaDBPreexistingVolume(t *testing.T) {
	f := newSharedMariaDBFixture(t, true)
	ctx := context.Background()
	startSharedCheckpointDaemon(t, f.h, "server-intent")
	target := f.accept(t, f.request("host", f.h.project))
	waitSharedCheckpoint(t, f.h, "server-intent")
	sharedCrash(t, f.h)
	record, _ := f.private(t, target.Instance.ID)
	foreign := record.Volume
	foreign.Token = newID()
	foreign.ID = foreign.Name
	pending := true
	cleanup := func() {
		if !pending {
			return
		}
		if err := removeDockerResource(ctx, f.d, foreign); err != nil {
			t.Error("separately owned foreign volume cleanup uncertain")
			return
		}
		if err := verifyDockerResource(ctx, f.d, foreign); !errdefs.IsNotFound(err) {
			t.Error("foreign fixture absence unverified")
			return
		}
		pending = false
	}
	t.Cleanup(cleanup)
	if _, err := createDockerResource(ctx, f.d, foreign); err != nil {
		t.Fatal(err)
	}
	startSharedRecoveryDaemon(t, f.h, nil)
	r := f.request("host", f.h.project)
	r.InstanceID = target.Instance.ID
	if _, err := f.h.cli.Restart(ctx, r); err != nil {
		t.Fatal(err)
	}
	f.wait(t, target.Instance.ID, v1.Failed)
	if err := verifyDockerResource(ctx, f.d, foreign); err != nil {
		t.Fatal("foreign volume mutated/adopted", err)
	}
	f.destroy(t, target.Instance.ID)
	_, err := f.h.cli.Shared(ctx, "destroy", v1.SharedRequest{APIVersion: v1.Version, ServiceID: record.ID})
	if err == nil {
		t.Fatal("shared destroy deleted foreign volume")
	}
	if err = verifyDockerResource(ctx, f.d, foreign); err != nil {
		t.Fatal("shared cleanup changed foreign ownership", err)
	}
	cleanup()
	if pending {
		t.Fatal("foreign owned fixture cleanup incomplete")
	}
	if _, err = f.h.cli.Shared(ctx, "destroy", v1.SharedRequest{APIVersion: v1.Version, ServiceID: record.ID}); err != nil {
		t.Fatal(err)
	}
	t.Log("Docker name collision with different authority preserved through provisioning and destruction; exact foreign fixture cleanup and retry verified")
}

func TestSharedMariaDBAllocationCancellation(t *testing.T) {
	for _, keep := range []bool{false, true} {
		t.Run(fmt.Sprint(keep), func(t *testing.T) {
			f := newSharedMariaDBFixture(t, true)
			ctx := context.Background()
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
			r := f.request("allocation-cancel", f.h.project)
			r.Options.KeepOnFailure = keep
			target := f.accept(t, r)
			stopLease := f.keepLease(t, target)
			checkpoint := waitSharedCheckpoint(t, f.h, "sql-allocate:database")
			admin := f.liveAdmin(t)
			var databases, users int
			if err := admin.QueryRow("SELECT COUNT(*) FROM information_schema.SCHEMATA WHERE SCHEMA_NAME LIKE 'bl%'").Scan(&databases); err != nil {
				t.Fatal(err)
			}
			if err := admin.QueryRow("SELECT COUNT(*) FROM mysql.user WHERE User LIKE 'bl%'").Scan(&users); err != nil {
				t.Fatal(err)
			}
			if databases != 1 || users != 0 {
				t.Fatal("provisioning cancellation did not target real partial database-only effect")
			}
			stopLease()
			done := make(chan error, 1)
			go func() { _, err := f.h.cli.Cancel(ctx, target.Instance.ID); done <- err }()
			// Observe cancellation in the real owner before releasing the SQL barrier.
			waitSharedCheckpoint(t, f.h, "cancel-effect")
			if _, err := checkpoint.Write([]byte{1}); err != nil {
				t.Fatal(err)
			}
			if err := <-done; err != nil {
				t.Fatal(err)
			}
			final := f.wait(t, target.Instance.ID, v1.Cancelled)
			if final.Instance.Execution.Kept != keep {
				t.Fatal("partial-provisioning keep policy differs")
			}
			view := f.shared(t)
			if keep && view.Allocations != 1 || !keep && view.Allocations != 0 {
				t.Fatal("partial cleanup/retention count differs")
			}
			if !keep {
				if err := admin.QueryRow("SELECT COUNT(*) FROM information_schema.SCHEMATA WHERE SCHEMA_NAME LIKE 'bl%'").Scan(&databases); err != nil || databases != 0 {
					t.Fatal("default canceled provisioning left database", err)
				}
			}
			f.destroy(t, target.Instance.ID)
			t.Log("actual partial provisioning cancellation is joined, cleaned or kept explicitly, and destroy is verified", keep)
		})
	}
}

func TestSharedMariaDBConsumerBeforeRelease(t *testing.T) {
	for _, component := range []string{"live-native", "live-container"} {
		t.Run(component, func(t *testing.T) {
			f := newSharedMariaDBFixture(t, true)
			ctx := context.Background()
			proof := f.addLive(t)
			startSharedCheckpointDaemon(t, f.h, "release-intent")
			target := f.accept(t, f.request(component, f.h.project))
			f.wait(t, target.Instance.ID, v1.RuntimeReady)
			credential := f.credential(t, target.Instance.ID, component)
			witness := independentSharedWitness(t, f.db(t, credential))
			address := f.http(t, target.Instance.ID)
			finish := pendingSharedDestroy(t, f, target.Instance.ID, false)
			checkpoint := waitSharedCheckpoint(t, f.h, "release-intent")
			requireSharedHTTPAbsent(t, address)
			if component == "live-native" {
				verifySharedStopProof(t, proof, credential.database)
			}
			verifySharedWitness(t, f.db(t, credential), witness)
			if component == "live-container" {
				state, err := f.h.cli.Inspect(ctx, target.Instance.ID)
				if err != nil {
					t.Fatal(err)
				}
				for _, entry := range state.Instance.Execution.Components {
					if entry.Name == component && entry.Status != "stopped" {
						t.Fatal("container was not joined before SQL release")
					}
				}
			}
			if _, err := checkpoint.Write([]byte{1}); err != nil {
				t.Fatal(err)
			}
			if err := finish(); err != nil {
				t.Fatal(err)
			}
			f.requireAbsent(t, credential)
			t.Log("actual long-running consumer absent/joined while independent SQL witness still present before release", component)
		})
	}
}

func TestSharedUnselectedOfflinePrivacy(t *testing.T) {
	for _, engine := range []string{"mariadb", "postgresql"} {
		t.Run(engine, func(t *testing.T) { testSharedUnselectedOfflinePrivacy(t, engine) })
	}
}
func testSharedUnselectedOfflinePrivacy(t *testing.T, engine string) {
	binary := buildFixtureBinary(t, t.TempDir(), "../../cmd/backlot")
	h := newNativeHarness(t, binary, "30s")
	ctx := context.Background()
	listener, err := net.Listen("unix", filepath.Join(shortTemp(t), "reject.sock"))
	if err != nil {
		t.Fatal(err)
	}
	var contacts atomic.Int64
	reject := &http.Server{ReadHeaderTimeout: time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		contacts.Add(1)
		http.Error(w, "unexpected provider contact", http.StatusServiceUnavailable)
	})}
	done := make(chan error, 1)
	go func() { done <- reject.Serve(listener) }()
	t.Cleanup(func() { _ = reject.Close(); _ = listener.Close(); <-done })
	config := filepath.Join(filepath.Dir(h.dir), "reject.json")
	raw, _ := json.Marshal(v1.MachineConfig{Version: v1.ManifestVersion, Docker: &v1.DockerConfig{Endpoint: "unix://" + listener.Addr().String()}})
	if err = os.WriteFile(config, raw, 0600); err != nil {
		t.Fatal(err)
	}
	h.manifest.Tools["true"] = "/usr/bin/true"
	port := 3306
	if engine == "postgresql" {
		port = 5432
	}
	h.manifest.SharedServices = map[string]v1.SharedService{"sql": {Manager: engine, Image: "unused-sql:fixture", Storage: "volume", Admin: "generated", Port: port, ReadinessTimeout: "60s"}}
	h.manifest.Resources["database"] = v1.Resource{Kind: "allocation", SharedService: "sql"}
	environment := v1.Environment{Assign: map[string]v1.Value{}}
	for _, field := range []string{"host", "port", "database", "username", "password"} {
		environment.Assign["DB_"+strings.ToUpper(field)] = fixtureRef("resource", "database", field)
	}
	h.manifest.Components["shared"] = v1.Component{Kind: v1.Job, Runtime: v1.Native, Policy: v1.EachStart, Resources: []string{"database"}, Command: &v1.Command{Tool: "true"}, Environment: environment}
	container := h.manifest.Components["shared"]
	container.Runtime, container.Command = v1.Container, nil
	container.Image = ptrValue(fixtureLiteral("unused-consumer:fixture"))
	h.manifest.Components["container"] = container
	h.manifest.Scenes["host"] = v1.Scene{Lifetime: v1.Persistent, Components: []string{"shared"}, Resources: []string{"database"}}
	h.manifest.Resources["private"] = v1.Resource{Kind: "secret"}
	h.manifest.Components["private"] = v1.Component{Kind: v1.Job, Runtime: v1.Native, Policy: v1.EachStart, Resources: []string{"private"}, Command: &v1.Command{Tool: "true"}, Environment: v1.Environment{Assign: map[string]v1.Value{"PRIVATE_VALUE": fixtureRef("resource", "private", "value")}}}
	h.manifest.Scenes["private"] = v1.Scene{Lifetime: v1.Persistent, Components: []string{"private"}, Resources: []string{"private"}}
	h.write()
	for _, scene := range []string{"private", "host"} {
		output, err := exec.Command(binary, "plan", scene, "--project", h.project, "--config", config, "--json").CombinedOutput()
		if err != nil {
			t.Fatal("offline plan", err, string(output))
		}
		var planned v1.PlanResponse
		if err = json.Unmarshal(output, &planned); err != nil {
			t.Fatal(err)
		}
		if scene == "host" {
			if len(planned.SharedServices) != 1 {
				t.Fatal("shared offline plan missing symbolic declaration")
			}
			for _, component := range planned.Components {
				for _, value := range component.Environment {
					if value.Symbolic != nil && value.Symbolic.Kind == "resource" && value.Symbolic.Name == "database" && value.Literal != nil {
						t.Fatal("offline allocation value was materialized")
					}
				}
			}
		}
	}
	r := h.request("private")
	r.Plan.ConfigPath = config
	target, err := h.cli.Run(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	h.state(target.Instance.ID, v1.RuntimeReady)
	if _, err := h.cli.Destroy(ctx, target.Instance.ID); err != nil {
		t.Fatal(err)
	}
	if contacts.Load() != 0 {
		t.Fatal("unselected declarations/offline planning contacted Docker")
	}
	t.Log("private native lifecycle and symbolic offline plans make zero rejecting-provider contacts")
}

func TestSharedMariaDBOutputPrivacy(t *testing.T) {
	f := newSharedMariaDBFixture(t, true)
	ctx := context.Background()
	target := f.run("host", f.h.project)
	credential := f.credential(t, target.Instance.ID, "native")
	private := f.liveAdminRecord(t)
	inspected, err := f.h.cli.Inspect(ctx, target.Instance.ID)
	if err != nil {
		t.Fatal(err)
	}
	fixtures, err := f.h.cli.Fixtures(ctx, v1.FixturesRequest{APIVersion: v1.Version, InstanceID: target.Instance.ID, Component: "native"})
	if err != nil {
		t.Fatal(err)
	}
	logs, err := f.h.cli.Logs(ctx, v1.LogsRequest{APIVersion: v1.Version, InstanceID: target.Instance.ID})
	if err != nil {
		t.Fatal(err)
	}
	for _, view := range []any{inspected, fixtures, logs, f.shared(t)} {
		body, _ := json.Marshal(view)
		for _, secret := range []string{credential.password, private.AdminPassword, private.Server.Token} {
			if strings.Contains(string(body), secret) {
				t.Fatal("ordinary view exposed private application/administrative authority")
			}
		}

	}
	f.destroy(t, target.Instance.ID)
	t.Log("ordinary selected output remains redacted")
}

func TestSharedMariaDBMultipleAllocations(t *testing.T) {
	f := newSharedMariaDBFixture(t, true)
	ctx := context.Background()
	f.h.manifest.Resources["second"] = v1.Resource{Kind: "allocation", SharedService: "sql"}
	second := f.h.manifest.Components["native"]
	second.Environment = cloneFixtureEnvironment(second.Environment)
	for key, value := range second.Environment.Assign {
		if value.Ref != nil {
			ref := *value.Ref
			ref.Name = "second"
			value.Ref = &ref
			second.Environment.Assign[key] = value
		}
	}
	second.Fixtures = map[string]v1.Fixture{}
	for key, value := range f.h.manifest.Components["native"].Fixtures {
		ref := *value.Value.Ref
		ref.Name = "second"
		value.Value.Ref = &ref
		second.Fixtures[key] = value
	}
	second.Resources = []string{"second"}
	second.Initializes = []string{"second"}
	f.h.manifest.Components["second-init"] = second
	f.h.manifest.Scenes["multiple"] = v1.Scene{Lifetime: v1.Persistent, Components: []string{"native", "second-init"}, Resources: []string{"database", "second"}}
	f.writeBoth(t)
	peer := f.run("bridge", f.linked)
	peerCredential := f.credential(t, peer.Instance.ID, "container")
	peerWitness := independentSharedWitness(t, f.db(t, peerCredential))
	target := f.run("multiple", f.h.project)
	first, other := f.credential(t, target.Instance.ID, "native"), f.credential(t, target.Instance.ID, "second-init")
	if first.database == other.database || first.user == other.user || first.password == other.password {
		t.Fatal("same-instance allocations collided")
	}
	firstDB, otherDB := f.db(t, first), f.db(t, other)
	firstWitness, otherWitness := independentSharedWitness(t, firstDB), independentSharedWitness(t, otherDB)
	if _, err := firstDB.Query("SELECT * FROM `" + other.database + "`.independent_witness"); !sqlAccessDenied(err) {
		t.Fatal("same-instance allocation can read sibling data")
	}
	verifySharedWitness(t, firstDB, firstWitness)
	verifySharedWitness(t, otherDB, otherWitness)
	if f.shared(t).Allocations != 3 {
		t.Fatal("distinct same-instance allocations not retained")
	}
	r := f.request("multiple", f.h.project)
	r.InstanceID = target.Instance.ID
	if _, err := f.h.cli.Reset(ctx, r); err != nil {
		t.Fatal(err)
	}
	f.wait(t, target.Instance.ID, v1.RuntimeReady)
	freshFirst, freshOther := f.credential(t, target.Instance.ID, "native"), f.credential(t, target.Instance.ID, "second-init")
	if freshFirst.database == first.database || freshFirst.user == first.user || freshFirst.password == first.password || freshOther.database == other.database || freshOther.user == other.user || freshOther.password == other.password {
		t.Fatal("multiple allocation reset reused identity")
	}
	f.requireAbsent(t, first)
	f.requireAbsent(t, other)
	verifySharedWitness(t, f.db(t, peerCredential), peerWitness)
	f.destroy(t, target.Instance.ID)
	f.requireAbsent(t, freshFirst)
	f.requireAbsent(t, freshOther)
	verifySharedWitness(t, f.db(t, peerCredential), peerWitness)
	f.destroy(t, peer.Instance.ID)
	t.Log("distinct real same-instance allocations, sibling denial, all-member reset/release and independent peer preservation passed")
}
