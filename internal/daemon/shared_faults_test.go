//go:build darwin || linux

package daemon

import (
	"context"
	"database/sql"
	"errors"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/containerd/errdefs"
	"github.com/go-sql-driver/mysql"
	"github.com/moby/moby/client"
	v1 "github.com/octacian/backlot/api/v1"
	"github.com/octacian/backlot/internal/manager"
)

func sharedCrash(t *testing.T, h *nativeHarness) {
	t.Helper()
	if err := h.process.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	if err := h.process.Wait(); err == nil {
		t.Fatal("retained daemon did not fault")
	}
	h.waited = true
	preserveSharedDaemonLog(t, h, "faulted checkpoint daemon")
}
func sharedStop(t *testing.T, h *nativeHarness) {
	t.Helper()
	if h.waited {
		return
	}
	if _, err := h.cli.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := h.process.Wait(); err != nil {
		t.Fatal(err)
	}
	h.waited = true
	preserveSharedDaemonLog(t, h, "stopped fixture daemon")
}
func (f *sharedMariaDBFixture) private(t *testing.T, id string) (sharedServiceRecord, sharedAllocation) {
	t.Helper()
	if !f.h.waited {
		t.Fatal("private state inspection requires reaped daemon")
	}
	state, err := openStore(f.h.dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = state.db.Close() }()
	ids, err := state.sharedIDs()
	if err != nil || len(ids) != 1 {
		t.Fatal("shared journal unavailable", err)
	}
	record, err := state.sharedService(ids[0])
	if err != nil {
		t.Fatal(err)
	}
	allocations, err := state.sharedAllocations(id)
	if err != nil {
		t.Fatal(err)
	}
	return record, allocations["database"]
}
func sharedSQLCounts(t *testing.T, db *sql.DB, request v1.ManagerRequest) (int, int) {
	t.Helper()
	var databases, users int
	if err := db.QueryRow("SELECT COUNT(*) FROM information_schema.SCHEMATA WHERE SCHEMA_NAME=?", request.Database).Scan(&databases); err != nil {
		t.Fatal("cannot observe SQL database effect", err)
	}
	if err := db.QueryRow("SELECT COUNT(*) FROM mysql.user WHERE User=? AND Host='%'", request.Username).Scan(&users); err != nil {
		t.Fatal("cannot observe SQL user effect", err)
	}
	return databases, users
}
func sharedAdmin(t *testing.T, record sharedServiceRecord) *sql.DB {
	t.Helper()
	db, err := sharedDB(record)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}
func (f *sharedMariaDBFixture) explicitRestart(t *testing.T, id, scene, path string) {
	t.Helper()
	r := f.request(scene, path)
	r.InstanceID = id
	if _, err := f.h.cli.Restart(context.Background(), r); err != nil {
		t.Fatal("explicit recovery restart failed", err)
	}
	f.wait(t, id, v1.RuntimeReady)
}
func pendingSharedDestroy(t *testing.T, f *sharedMariaDBFixture, id string, shared bool) func() error {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	done := make(chan error, 1)
	joined := false
	t.Cleanup(func() {
		cancel()
		if !joined {
			<-done
		}
	})
	go func() {
		var err error
		if shared {
			_, err = f.h.cli.Shared(ctx, "destroy", v1.SharedRequest{APIVersion: v1.Version, ServiceID: id})
		} else {
			_, err = f.h.cli.Destroy(ctx, id)
		}
		done <- err
	}()
	return func() error { joined = true; return <-done }
}

func TestSharedMariaDBSQLRecovery(t *testing.T) {
	for _, point := range []string{"sql-allocate:database", "sql-allocate:user", "sql-allocate:grant", "sql-release:user", "sql-release:database"} {
		t.Run(point, func(t *testing.T) {
			f := newSharedMariaDBFixture(t, true)
			ctx := context.Background()
			peer := f.run("bridge", f.linked)
			peerCredential := f.credential(t, peer.Instance.ID, "container")
			peerWitness := independentSharedWitness(t, f.db(t, peerCredential))
			var target v1.InstanceResponse
			var before sharedFixtureCredential
			releasing := strings.HasPrefix(point, "sql-release:")
			if releasing {
				target = f.run("host", f.h.project)
				before = f.credential(t, target.Instance.ID, "native")
				independentSharedWitness(t, f.db(t, before))
			}
			startSharedCheckpointDaemon(t, f.h, point)
			var finish func() error
			if releasing {
				finish = pendingSharedDestroy(t, f, target.Instance.ID, false)
			} else {
				target = f.accept(t, f.request("host", f.h.project))
			}
			waitSharedCheckpoint(t, f.h, point)
			sharedCrash(t, f.h)
			if finish != nil && finish() == nil {
				t.Fatal("crashed release reported success")
			}
			record, allocation := f.private(t, target.Instance.ID)
			if allocation.Released || allocation.Request.AllocationID == "" {
				t.Fatal("intent/effect acknowledgment gap lost")
			}
			admin := sharedAdmin(t, record)
			databases, users := sharedSQLCounts(t, admin, allocation.Request)
			wantDB, wantUser := 1, 1
			if point == "sql-allocate:database" || point == "sql-release:user" {
				wantUser = 0
			}
			if point == "sql-release:database" {
				wantDB, wantUser = 0, 0
			}
			if databases != wantDB || users != wantUser {
				t.Fatal("checkpoint did not establish exact partial SQL effect", point, databases, users)
			}
			inspect := allocation.Request
			inspect.Operation = "inspect"
			observed, err := manager.MariaDB(ctx, admin, inspect, nil)
			if err != nil {
				t.Fatal("owned SQL effect unverifiable", err)
			}
			wantEffect := "partial"
			if point == "sql-allocate:grant" {
				wantEffect = "ready"
			}
			if point == "sql-release:database" {
				wantEffect = "released"
			}
			if observed.Effect != wantEffect {
				t.Fatal("partial/ready/absence distinction lost", point, observed.Effect)
			}
			if releasing && allocation.Request.Operation != "release" {
				t.Fatal("destructive intent was not durable")
			}
			if !releasing && allocation.Result.Effect == "ready" {
				t.Fatal("SQL ready acknowledgment was not actually lost")
			}
			_ = admin.Close()
			startSharedRecoveryDaemon(t, f.h, nil)
			if releasing {
				// Explicit ordinary execution cannot convert release intent back into allocate.
				out, err := f.h.cli.Run(ctx, f.request("host", f.h.project))
				if err != nil {
					t.Fatal(err)
				}
				final := f.wait(t, out.Instance.ID, v1.Failed)
				if final.Instance.Execution.FailureDetail == nil || final.Instance.Execution.FailureDetail.Code != "allocation_conflict" {
					t.Fatal("release obligation reopened")
				}
				f.destroy(t, target.Instance.ID)
				f.destroy(t, target.Instance.ID)
			} else {
				f.explicitRestart(t, target.Instance.ID, "host", f.h.project)
				actual := f.credential(t, target.Instance.ID, "native")
				if actual.database != allocation.Request.Database || actual.user != allocation.Request.Username || actual.password != allocation.Request.Password {
					t.Fatal("partial provisioning rotated identity/paired credentials")
				}
				independentSharedWitness(t, f.db(t, actual))
				f.destroy(t, target.Instance.ID)
			}
			f.explicitRestart(t, peer.Instance.ID, "bridge", f.linked)
			if f.credential(t, peer.Instance.ID, "container") != peerCredential {
				t.Fatal("peer credentials changed during SQL recovery")
			}
			verifySharedWitness(t, f.db(t, peerCredential), peerWitness)
			f.destroy(t, peer.Instance.ID)
			t.Log("verified real partial SQL effect, retained-child lost acknowledgment, exact-authority explicit reconciliation, peer witness and cleanup", point)
		})
	}
}

func TestSharedMariaDBDockerRecovery(t *testing.T) {
	for _, point := range []string{"create-effect:volume", "create-effect:network", "create-effect:container", "remove-effect:container", "remove-effect:volume", "remove-effect:network"} {
		t.Run(point, func(t *testing.T) {
			f := newSharedMariaDBFixture(t, true)
			ctx := context.Background()
			removing := strings.HasPrefix(point, "remove-")
			var target v1.InstanceResponse
			if removing {
				target = f.run("host", f.h.project)
				f.destroy(t, target.Instance.ID)
			}
			startSharedCheckpointDaemon(t, f.h, point)
			var finish func() error
			if removing {
				finish = pendingSharedDestroy(t, f, f.shared(t).ID, true)
			} else {
				target = f.accept(t, f.request("host", f.h.project))
			}
			waitSharedCheckpoint(t, f.h, point)
			sharedCrash(t, f.h)
			if finish != nil && finish() == nil {
				t.Fatal("crashed Docker removal reported success")
			}
			record, _ := f.private(t, target.Instance.ID)
			var resource ownedResource
			switch strings.Split(point, ":")[1] {
			case "volume":
				resource = record.Volume
			case "network":
				resource = record.Network
			case "container":
				resource = record.Server
			}
			recoveredID := ""
			if removing {
				if !record.Destroying || resource.Removed {
					t.Fatal("destructive progress or actual lost acknowledgment missing")
				}
				if err := verifyDockerResource(ctx, f.d, resource); !errdefs.IsNotFound(err) {
					t.Fatal("Docker removal effect not observed", err)
				}
			} else {
				if resource.ID != "" {
					t.Fatal("Docker create acknowledgment was not lost")
				}
				if err := verifyDockerResource(ctx, f.d, resource); err != nil {
					t.Fatal("unacknowledged effect lacks exact capability", err)
				}
				var err error
				if resource.Kind == "container" {
					out, inspectErr := f.d.ContainerInspect(ctx, resourceHandle(resource), client.ContainerInspectOptions{})
					err = inspectErr
					recoveredID = out.Container.ID
				} else {
					recoveredID, err = recoverResourceEffect(ctx, f.d, resource)
				}
				if err != nil || recoveredID == "" {
					t.Fatal("owned create effect identity unavailable", err)
				}
			}
			startSharedRecoveryDaemon(t, f.h, nil)
			if removing {
				_, err := f.h.cli.Shared(ctx, "start", v1.SharedRequest{APIVersion: v1.Version, ServiceID: record.ID})
				code(t, err, "shared_cleanup_pending")
				if _, err = f.h.cli.Shared(ctx, "destroy", v1.SharedRequest{APIVersion: v1.Version, ServiceID: record.ID}); err != nil {
					t.Fatal(err)
				}
				_, err = f.h.cli.Shared(ctx, "inspect", v1.SharedRequest{APIVersion: v1.Version, ServiceID: record.ID})
				code(t, err, "not_found")
			} else {
				f.explicitRestart(t, target.Instance.ID, "host", f.h.project)
				credential := f.credential(t, target.Instance.ID, "native")
				independentSharedWitness(t, f.db(t, credential))
				sharedStop(t, f.h)
				after, _ := f.private(t, target.Instance.ID)
				var actual string
				switch resource.Kind {
				case "volume":
					actual = after.Volume.ID
				case "network":
					actual = after.Network.ID
				case "container":
					actual = after.Server.ID
				}
				if actual != recoveredID {
					t.Fatal("verified effect duplicated/replaced during recovery")
				}
				startSharedRecoveryDaemon(t, f.h, nil)
				f.destroy(t, target.Instance.ID)
			}
			t.Log("verified actual Docker effect, retained-child lost acknowledgment, no recreation during destructive progress and exact identity reconciliation", point)
		})
	}
}

// The fixture owns only this local proxy. Turning off forwarding never stops or
// mutates the operator's Docker engine or any unrelated resource.
func sharedUnavailableProxy(t *testing.T) (string, *atomic.Bool) {
	t.Helper()
	target, err := url.Parse(os.Getenv("BACKLOT_DOCKER_TEST_ENDPOINT"))
	if err != nil || target.Scheme != "unix" {
		t.Fatal("explicit local provider required")
	}
	path := filepath.Join(shortTemp(t), "engine.sock")
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", target.Path)
	}}
	proxy := &httputil.ReverseProxy{Rewrite: func(r *httputil.ProxyRequest) {
		r.Out.URL.Scheme = "http"
		r.Out.URL.Host = "docker"
		r.Out.Host = "docker"
	}, Transport: transport}
	unavailable := &atomic.Bool{}
	server := &http.Server{ReadHeaderTimeout: time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if unavailable.Load() {
			http.Error(w, "owned fixture provider unavailable", http.StatusServiceUnavailable)
			return
		}
		proxy.ServeHTTP(w, r)
	})}
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	t.Cleanup(func() { _ = server.Close(); _ = listener.Close(); <-done; transport.CloseIdleConnections() })
	return "unix://" + path, unavailable
}
func TestSharedMariaDBUnavailableProvider(t *testing.T) {
	if os.Getenv("BACKLOT_DOCKER_TEST_ENDPOINT") == "" {
		t.Skip("requires explicit owned provider fixture")
	}
	endpoint, unavailable := sharedUnavailableProxy(t)
	f := newSharedMariaDBFixture(t, true, endpoint)
	ctx := context.Background()
	// Restore before journal cleanup on every exit, including failed assertions.
	t.Cleanup(func() { unavailable.Store(false) })
	a, b := f.run("host", f.h.project), f.run("bridge", f.linked)
	ca, cb := f.credential(t, a.Instance.ID, "native"), f.credential(t, b.Instance.ID, "container")
	dbB := f.db(t, cb)
	w := independentSharedWitness(t, dbB)
	unavailable.Store(true)
	_, err := f.h.cli.Destroy(ctx, a.Instance.ID)
	code(t, err, "cleanup_failed")
	view := f.shared(t)
	if view.Allocations != 2 || view.PendingReleases != 1 {
		t.Fatal("provider outage forgot durable cleanup/inspect reason")
	}
	verifySharedWitness(t, dbB, w)
	_, err = f.h.cli.Shared(ctx, "destroy", v1.SharedRequest{APIVersion: v1.Version, ServiceID: view.ID})
	code(t, err, "shared_in_use")
	unavailable.Store(false)
	f.destroy(t, a.Instance.ID)
	f.destroy(t, a.Instance.ID)
	verifySharedWitness(t, dbB, w)
	if f.credential(t, b.Instance.ID, "container") != cb {
		t.Fatal("provider recovery changed peer credentials")
	}
	old := f.db(t, ca)
	oldErr := old.Ping()
	var auth *mysql.MySQLError
	if !errors.As(oldErr, &auth) || auth.Number != 1045 {
		t.Fatal("restored provider did not prove target authentication denial", oldErr)
	}
	f.destroy(t, b.Instance.ID)
	t.Log("actual unavailable selected provider retains inspectable cleanup obligations; restoration and repeated explicit cleanup preserve peer data")
}
