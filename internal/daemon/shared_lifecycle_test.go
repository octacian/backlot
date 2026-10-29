//go:build darwin || linux

package daemon

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/lib/pq"
	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/client"
	v1 "github.com/octacian/backlot/api/v1"
	"github.com/octacian/backlot/internal/manager"
)

type sharedFixtureCredential struct{ database, user, password string }

func (f *sharedMariaDBFixture) credential(t *testing.T, id, component string) sharedFixtureCredential {
	t.Helper()
	values, err := f.h.cli.Fixtures(context.Background(), v1.FixturesRequest{APIVersion: v1.Version, InstanceID: id, Component: component})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, v := range values.Fixtures {
		if v.Sensitive && v.Value != "" {
			t.Fatal("ordinary fixture leaked secret")
		}
		got[v.Name] = v.Value
	}
	secret, err := f.h.cli.FixtureSecret(context.Background(), v1.FixturesRequest{APIVersion: v1.Version, InstanceID: id, Component: component, SecretName: "password"})
	if err != nil || len(secret.Fixtures) != 1 {
		t.Fatal("credential unavailable", err)
	}
	return sharedFixtureCredential{got["database"], got["username"], secret.Fixtures[0].Value}
}
func (f *sharedMariaDBFixture) shared(t *testing.T) v1.SharedStatus {
	t.Helper()
	result, err := f.h.cli.Shared(context.Background(), "list", v1.SharedRequest{APIVersion: v1.Version})
	if err != nil || len(result.Services) != 1 {
		t.Fatal("expected one retained shared server", err)
	}
	if result.Services[0].ID != f.serviceID {
		t.Fatal("retained service identity differs from immutable fixture project")
	}
	return result.Services[0]
}
func (f *sharedMariaDBFixture) server(t *testing.T) (string, int, bool) {
	t.Helper()
	// Expected ID is recorded from the fixture identity before effects; do not
	// wait on a daemon service lock held deliberately by an effect checkpoint.
	id := f.serviceID
	list, err := f.d.ContainerList(context.Background(), client.ContainerListOptions{All: true, Filters: client.Filters{}.Add("label", "io.backlot.shared-service="+id)})
	if err != nil || len(list.Items) != 1 {
		t.Fatal("expected one actual owned server", err)
	}
	out, err := f.d.ContainerInspect(context.Background(), list.Items[0].ID, client.ContainerInspectOptions{})
	if err != nil || out.Container.NetworkSettings == nil || out.Container.State == nil {
		t.Fatal("server observation unavailable", err)
	}
	port, _ := network.ParsePort(strconv.Itoa(f.internalPort) + "/tcp")
	bindings := out.Container.NetworkSettings.Ports[port]
	// Docker does not expose active bindings while a retained server is stopped.
	published := 0
	if len(bindings) == 1 {
		published, err = strconv.Atoi(bindings[0].HostPort)
		if err != nil {
			t.Fatal(err)
		}
	}
	return out.Container.ID, published, out.Container.State.Running
}
func (f *sharedMariaDBFixture) db(t *testing.T, c sharedFixtureCredential) *sql.DB {
	t.Helper()
	_, port, running := f.server(t)
	if !running || port == 0 {
		t.Fatal("application requires running retained server")
	}
	if f.manager == "postgresql" {
		db, err := manager.PostgreSQLDB("127.0.0.1", port, c.database, c.user, c.password)
		if db != nil {
			db.SetMaxIdleConns(0)
		}
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = db.Close() })
		return db
	}
	cfg := mysql.NewConfig()
	cfg.User = c.user
	cfg.Passwd = c.password
	cfg.DBName = c.database
	cfg.Net = "tcp"
	cfg.Addr = net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	cfg.Timeout = time.Second
	cfg.ReadTimeout = time.Second
	cfg.WriteTimeout = time.Second
	connector, err := mysql.NewConnector(cfg)
	if err != nil {
		t.Fatal(err)
	}
	db := sql.OpenDB(connector)
	t.Cleanup(func() { _ = db.Close() })
	return db
}
func independentSharedWitness(t *testing.T, db *sql.DB) string {
	t.Helper()
	value := newID()
	table, placeholder := "independent_witness", "?"
	if _, postgres := db.Driver().(*pq.Driver); postgres {
		table, placeholder = "public.independent_witness", "$1"
	}
	if _, err := db.Exec("CREATE TABLE " + table + " (value VARCHAR(64) NOT NULL)"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("INSERT INTO "+table+" VALUES ("+placeholder+")", value); err != nil {
		t.Fatal(err)
	}
	return value
}
func verifySharedWitness(t *testing.T, db *sql.DB, want string) {
	t.Helper()
	table := "independent_witness"
	if _, postgres := db.Driver().(*pq.Driver); postgres {
		table = "public.independent_witness"
	}
	var got string
	if err := db.QueryRow("SELECT value FROM " + table).Scan(&got); err != nil || got != want {
		t.Fatal("independent allocation witness changed/lost", err)
	}
}
func (f *sharedMariaDBFixture) request(scene, path string) v1.RunRequest {
	return v1.RunRequest{APIVersion: v1.Version, Plan: v1.PlanRequest{ProjectPath: path, ConfigPath: f.configuration, Scene: scene}, Options: v1.ExecutionOptions{StartupTimeout: "90s", JobTimeout: "30s", StopGrace: "1s"}}
}
func (f *sharedMariaDBFixture) accept(t *testing.T, r v1.RunRequest) v1.InstanceResponse {
	t.Helper()
	out, err := f.h.cli.Run(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	return out
}
func (f *sharedMariaDBFixture) wait(t *testing.T, id string, wants ...v1.InstanceStatus) v1.InstanceResponse {
	t.Helper()
	deadline := time.Now().Add(90 * time.Second)
	var last v1.InstanceResponse
	for time.Now().Before(deadline) {
		var err error
		last, err = f.h.cli.Inspect(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		for _, want := range wants {
			if last.Instance.Status == want {
				return last
			}
		}
		if last.Instance.Status == v1.Failed || last.Instance.Status == v1.Cancelled || last.Instance.Status == v1.Interrupted || last.Instance.Status == v1.Destroyed {
			b, _ := json.Marshal(last)
			t.Fatalf("unexpected terminal state: %s", b)
		}
		time.Sleep(20 * time.Millisecond)
	}
	b, _ := json.Marshal(last)
	t.Fatalf("lifecycle checkpoint not reached: %s", b)
	return last
}
func (f *sharedMariaDBFixture) writeBoth(t *testing.T) {
	t.Helper()
	f.h.write()
	data, err := os.ReadFile(filepath.Join(f.h.project, "backlot.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(f.linked, "backlot.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
}
func (f *sharedMariaDBFixture) addLive(t *testing.T) string {
	t.Helper()
	proof := filepath.Join(filepath.Dir(f.h.dir), "consumer-stops.jsonl")
	f.h.manifest.Resources["http"] = v1.Resource{Kind: "port"}
	for _, name := range []string{"live-native", "live-container"} {
		base := f.h.manifest.Components["native"]
		if name == "live-container" {
			base = f.h.manifest.Components["container"]
		}
		base.Kind = v1.Service
		base.Policy = ""
		base.Initializes = nil
		base.Resources = []string{"database", "http"}
		base.Environment = cloneFixtureEnvironment(base.Environment)
		base.Environment.Assign["DB_PERSIST"] = fixtureLiteral("1")
		base.Ports = map[string]v1.ServicePort{"http": {Resource: "http"}}
		base.Environment.Assign["DB_LISTEN"] = fixtureRef("resource", "http", "port")
		base.Environment.Assign["DB_CHECK_PORT"] = fixtureRef("resource", "http", "port")
		if name == "live-container" {
			base.Ports["http"] = v1.ServicePort{Resource: "http", ContainerPort: 8080}
			base.Environment.Assign["DB_LISTEN"] = fixtureLiteral("8080")
		} else {
			base.Environment.Assign["DB_STOP_FILE"] = fixtureLiteral(proof)
		}
		// Forwarded TCP can accept before application SQL initialization and HTTP serving.
		base.Readiness = &v1.Probe{Kind: "command", Command: &v1.Command{Tool: "consumer", Args: []string{"check-http"}}, Timeout: "30s"}
		f.h.manifest.Components[name] = base
		f.h.manifest.Scenes[name] = v1.Scene{Lifetime: v1.Persistent, Components: []string{name}, Resources: []string{"database", "http"}}
	}
	f.writeBoth(t)
	return proof
}
func (f *sharedMariaDBFixture) http(t *testing.T, id string) string {
	t.Helper()
	out, err := f.h.cli.Inspect(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("http://127.0.0.1:%d", out.Instance.Execution.Ports["http"]), nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal("actual live application unavailable", err)
	}
	defer func() { _ = response.Body.Close() }()
	data, err := io.ReadAll(response.Body)
	if err != nil || response.StatusCode != 200 || string(data) != "updated" {
		t.Fatal("live application did not read its allocation", err)
	}
	return request.URL.String()
}
func requireSharedHTTPAbsent(t *testing.T, url string) {
	t.Helper()
	c := &http.Client{Timeout: time.Second}
	response, err := c.Get(url)
	if err == nil {
		_ = response.Body.Close()
		t.Fatal("stopped consumer remains reachable")
	}
}
func verifySharedStopProof(t *testing.T, path, database string, offsets ...int) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal("no consumer-before-database stop proof", err)
	}
	if len(offsets) == 1 {
		if offsets[0] > len(data) {
			t.Fatal("stop proof was truncated")
		}
		data = data[offsets[0]:]
	}
	found := false
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var proof struct {
			Database string
			Alive    bool
		}
		if err = json.Unmarshal([]byte(line), &proof); err != nil {
			t.Fatal(err)
		}
		if proof.Database == database {
			if !proof.Alive {
				t.Fatal("database unavailable before consumer stop")
			}
			found = true
		}
	}
	if !found {
		t.Fatal("target consumer stop not observed while its database remained available")
	}
}
func (f *sharedMariaDBFixture) destroy(t *testing.T, id string) {
	t.Helper()
	if _, err := f.h.cli.Destroy(context.Background(), id); err != nil {
		t.Fatal(err)
	}
}
func (f *sharedMariaDBFixture) addDisposable(t *testing.T, name, action string) (string, string) {
	t.Helper()
	gate := filepath.Join(filepath.Dir(f.h.dir), name+"-gate")
	started := gate + "-started"
	job := f.h.manifest.Components["native"]
	job.Policy = v1.EachStart
	job.Initializes = nil
	job.Environment = cloneFixtureEnvironment(job.Environment)
	job.Environment.Assign["DB_PERSIST"] = fixtureLiteral("1")
	job.Environment.Assign["DB_ACTION"] = fixtureLiteral(action)
	job.Environment.Assign["DB_GATE"] = fixtureLiteral(gate)
	job.Environment.Assign["DB_STARTED"] = fixtureLiteral(started)
	job.DependsOn = []v1.Dependency{{Component: "live-native", Condition: v1.Ready}}
	f.h.manifest.Components[name] = job
	f.h.manifest.Scenes[name] = v1.Scene{Lifetime: v1.Disposable, Components: []string{"live-native", name}, Resources: []string{"database", "http"}, TerminalJob: name}
	f.writeBoth(t)
	return gate, started
}
func (f *sharedMariaDBFixture) awaitJob(t *testing.T, response v1.InstanceResponse, path string) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		if response.LeaseToken != "" {
			if _, err := f.h.cli.Renew(context.Background(), response.Instance.ID, response.LeaseToken); err != nil {
				ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				current, inspectErr := f.h.cli.Inspect(ctx, response.Instance.ID)
				data, _ := json.Marshal(current)
				t.Logf("job barrier inspect: %s (error: %v)", data, inspectErr)
				logs, logsErr := f.h.cli.Logs(ctx, v1.LogsRequest{APIVersion: v1.Version, InstanceID: response.Instance.ID})
				t.Logf("job barrier logs: %+v (error: %v)", logs, logsErr)
				cancel()
				t.Fatal(err)
			}
		}
		current, err := f.h.cli.Inspect(context.Background(), response.Instance.ID)
		if err != nil {
			t.Fatal(err)
		}
		if current.Instance.Status == v1.Failed || current.Instance.Status == v1.Cancelled {
			b, _ := json.Marshal(current)
			t.Fatalf("job failed before barrier: %s", b)
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("terminal job did not reach owned barrier")
}

func TestSharedConsumerHTTPReadiness(t *testing.T) {
	binary := buildFixtureBinary(t, t.TempDir(), "./testdata/shared-consumer")
	for _, mode := range []string{"tcp-only", "unavailable", "wrong-body", "ready"} {
		t.Run(mode, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if mode == "tcp-only" {
					connection, _, err := w.(http.Hijacker).Hijack()
					if err == nil {
						_ = connection.Close()
					}
					return
				}
				if mode == "unavailable" {
					w.WriteHeader(http.StatusServiceUnavailable)
				}
				if mode == "wrong-body" {
					_, _ = io.WriteString(w, "uninitialized")
					return
				}
				_, _ = io.WriteString(w, "updated")
			}))
			t.Cleanup(server.Close)
			connection, err := net.DialTimeout("tcp", server.Listener.Addr().String(), time.Second)
			if err != nil {
				t.Fatal("TCP positive control failed", err)
			}
			_ = connection.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, binary, "check-http")
			command.Env = []string{"DB_CHECK_PORT=" + strconv.Itoa(server.Listener.Addr().(*net.TCPAddr).Port)}
			if output, err := command.CombinedOutput(); ctx.Err() != nil || (err == nil) != (mode == "ready") {
				t.Fatalf("HTTP readiness: error=%v context=%v output=%s", err, ctx.Err(), output)
			}
		})
	}
}

func TestSharedMariaDBLifecycle(t *testing.T)    { testSharedLifecycle(t, "mariadb") }
func TestSharedPostgreSQLLifecycle(t *testing.T) { testSharedLifecycle(t, "postgresql") }
func testSharedLifecycle(t *testing.T, engine string) {
	f := newSharedSQLFixture(t, true, engine)
	ctx := context.Background()
	proof := f.addLive(t)
	// Actual long-running native and container services, in independent worktrees.
	a := f.accept(t, f.request("live-native", f.h.project))
	b := f.accept(t, f.request("live-container", f.linked))
	f.wait(t, a.Instance.ID, v1.RuntimeReady)
	f.wait(t, b.Instance.ID, v1.RuntimeReady)
	ca, cb := f.credential(t, a.Instance.ID, "live-native"), f.credential(t, b.Instance.ID, "live-container")
	wa, wb := independentSharedWitness(t, f.db(t, ca)), independentSharedWitness(t, f.db(t, cb))
	urlA, urlB := f.http(t, a.Instance.ID), f.http(t, b.Instance.ID)
	serverID, _, _ := f.server(t)
	shared := f.shared(t)
	for _, action := range []string{"stop", "restart", "destroy"} {
		_, err := f.h.cli.Shared(ctx, action, v1.SharedRequest{APIVersion: v1.Version, ServiceID: shared.ID})
		code(t, err, "shared_in_use")
	}
	// Stop/start + restart must preserve independently written data and exact credentials.
	if _, err := f.h.cli.StopExecution(ctx, a.Instance.ID); err != nil {
		t.Fatal(err)
	}
	verifySharedStopProof(t, proof, ca.database)
	requireSharedHTTPAbsent(t, urlA)
	f.http(t, b.Instance.ID)
	f.accept(t, f.request("live-native", f.h.project))
	f.wait(t, a.Instance.ID, v1.RuntimeReady)
	if f.credential(t, a.Instance.ID, "live-native") != ca {
		t.Fatal("live persistent credential rotation")
	}
	verifySharedWitness(t, f.db(t, ca), wa)
	verifySharedWitness(t, f.db(t, cb), wb)
	req := f.request("live-native", f.h.project)
	req.InstanceID = a.Instance.ID
	if _, err := f.h.cli.Restart(ctx, req); err != nil {
		t.Fatal(err)
	}
	f.wait(t, a.Instance.ID, v1.RuntimeReady)
	if f.credential(t, a.Instance.ID, "live-native") != ca {
		t.Fatal("restart changed exported paired credentials")
	}
	verifySharedWitness(t, f.db(t, ca), wa)
	verifySharedWitness(t, f.db(t, cb), wb)
	if _, err := f.h.cli.Reset(ctx, req); err != nil {
		t.Fatal(err)
	}
	f.wait(t, a.Instance.ID, v1.RuntimeReady)
	newer := f.credential(t, a.Instance.ID, "live-native")
	if newer.database == ca.database || newer.user == ca.user || newer.password == ca.password {
		t.Fatal("reset retained live target allocation")
	}
	verifySharedStopProof(t, proof, ca.database)
	verifySharedWitness(t, f.db(t, cb), wb)
	f.http(t, b.Instance.ID)
	var count int
	query := "SELECT COUNT(*) FROM information_schema.TABLES WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME='independent_witness'"
	if engine == "postgresql" {
		query = "SELECT COUNT(*) FROM information_schema.TABLES WHERE TABLE_SCHEMA='public' AND TABLE_NAME='independent_witness'"
	}
	if err := f.db(t, newer).QueryRow(query).Scan(&count); err != nil || count != 0 {
		t.Fatal("reset retained independent data", err)
	}
	wa = independentSharedWitness(t, f.db(t, newer))
	ca = newer
	urlA = f.http(t, a.Instance.ID)
	// Additional persistent scene shares the same server, then target-only destroy.
	extra := f.run("host", f.h.project)
	ce := f.credential(t, extra.Instance.ID, "native")
	independentSharedWitness(t, f.db(t, ce))
	f.destroy(t, extra.Instance.ID)
	if got, _, _ := f.server(t); got != serverID {
		t.Fatal("target destroy replaced peer server")
	}
	verifySharedWitness(t, f.db(t, ca), wa)
	verifySharedWitness(t, f.db(t, cb), wb)
	// Simultaneous disposable runs retain independent live services until released.
	var runs []v1.InstanceResponse
	var creds []sharedFixtureCredential
	var witnesses []string
	for i := range 3 {
		name := fmt.Sprintf("simultaneous-%d", i)
		f.addDisposable(t, name, "wait")
	}
	accepted := make(chan v1.InstanceResponse, 3)
	errs := make(chan error, 3)
	barrier := make(chan struct{})
	for i := range 3 {
		go func() {
			<-barrier
			out, err := f.h.cli.Run(ctx, f.request(fmt.Sprintf("simultaneous-%d", i), f.h.project))
			accepted <- out
			errs <- err
		}()
	}
	close(barrier)
	for range 3 {
		out := <-accepted
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
		runs = append(runs, out)
	}
	for _, out := range runs {
		scene := out.Instance.Plan.Scene
		f.awaitJob(t, out, filepath.Join(filepath.Dir(f.h.dir), scene+"-gate-started"))
		c := f.credential(t, out.Instance.ID, "live-native")
		for _, old := range creds {
			if c == old || c.database == old.database || c.password == old.password {
				t.Fatal("concurrent disposable collision")
			}
		}
		creds = append(creds, c)
		witnesses = append(witnesses, independentSharedWitness(t, f.db(t, c)))
	}
	if f.shared(t).Allocations != 5 {
		t.Fatal("persistent and simultaneous disposable allocation count differs")
	}
	for i, out := range runs {
		if err := os.WriteFile(filepath.Join(filepath.Dir(f.h.dir), out.Instance.Plan.Scene+"-gate"), []byte("release"), 0600); err != nil {
			t.Fatal(err)
		}
		final := f.wait(t, out.Instance.ID, v1.Succeeded)
		if final.Instance.Execution.Kept || final.Instance.Execution.CleanupFailure != "" {
			t.Fatal("successful disposable cleanup not complete")
		}
		if f.shared(t).Allocations != 4-i {
			t.Fatal("successful disposable allocation record retained")
		}
		f.requireAbsent(t, creds[i])
		verifySharedStopProof(t, proof, creds[i].database)
		for j := i + 1; j < len(runs); j++ {
			verifySharedWitness(t, f.db(t, creds[j]), witnesses[j])
		}
		verifySharedWitness(t, f.db(t, ca), wa)
		verifySharedWitness(t, f.db(t, cb), wb)
	}
	t.Log("live native/container persistence/reset/destroy, consumer SQL-alive stop proof, simultaneous disposable isolation/cleanup and peer preservation passed")
	// Failure, explicit cancellation and deadline with both keep policies.
	for _, mode := range []string{"failure", "cancel", "deadline"} {
		for _, keep := range []bool{false, true} {
			name := fmt.Sprintf("%s-%v", mode, keep)
			action := "wait"
			if mode == "failure" {
				action = "fail"
			}
			_, started := f.addDisposable(t, name, action)
			r := f.request(name, f.h.project)
			r.Options.KeepOnFailure = keep
			if mode == "deadline" {
				r.Options.JobTimeout = "500ms"
			}
			out := f.accept(t, r)
			if mode == "cancel" {
				f.awaitJob(t, out, started)
				if _, err := f.h.cli.Cancel(ctx, out.Instance.ID); err != nil {
					t.Fatal(err)
				}
			}
			want := v1.Failed
			if mode == "cancel" {
				want = v1.Cancelled
			}
			final := f.wait(t, out.Instance.ID, want)
			if final.Instance.Execution.Kept != keep {
				t.Fatal("keep policy differs", name)
			}
			if _, err := os.Stat(started); err != nil {
				t.Fatal("terminal failure/cancellation/deadline did not reach actual job", err)
			}
			if !keep {
				if f.shared(t).Allocations != 2 {
					t.Fatal("default terminal policy retained allocation")
				}
				f.requireAbsent(t, f.credential(t, out.Instance.ID, "live-native"))
			} else if f.shared(t).Allocations != 3 {
				t.Fatal("kept allocation record missing")
			}
			if mode == "failure" && (final.Instance.Execution.TerminalExitCode == nil || *final.Instance.Execution.TerminalExitCode != 17) {
				t.Fatal("lost terminal failure code")
			}
			if keep {
				c := f.credential(t, out.Instance.ID, "live-native")
				w := independentSharedWitness(t, f.db(t, c))
				f.http(t, out.Instance.ID)
				if mode == "failure" {
					if _, err := f.h.cli.StopExecution(ctx, a.Instance.ID); err != nil {
						t.Fatal(err)
					}
					if _, err := f.h.cli.StopExecution(ctx, b.Instance.ID); err != nil {
						t.Fatal(err)
					}
				}
				for _, action := range []string{"stop", "restart"} {
					_, err := f.h.cli.Shared(ctx, action, v1.SharedRequest{APIVersion: v1.Version, ServiceID: shared.ID})
					code(t, err, "shared_in_use")
				}
				if mode == "failure" {
					if _, err := f.h.cli.StopExecution(ctx, out.Instance.ID); err != nil {
						t.Fatal(err)
					}
					for _, action := range []string{"stop", "restart"} {
						if _, err := f.h.cli.Shared(ctx, action, v1.SharedRequest{APIVersion: v1.Version, ServiceID: shared.ID}); err != nil {
							t.Fatal("stopped consumers incorrectly fence server control", err)
						}
					}
					_, err := f.h.cli.Shared(ctx, "destroy", v1.SharedRequest{APIVersion: v1.Version, ServiceID: shared.ID})
					code(t, err, "shared_in_use")
					f.accept(t, f.request("live-native", f.h.project))
					f.accept(t, f.request("live-container", f.linked))
					f.wait(t, a.Instance.ID, v1.RuntimeReady)
					f.wait(t, b.Instance.ID, v1.RuntimeReady)
				}
				verifySharedWitness(t, f.db(t, c), w)
			}
			f.destroy(t, out.Instance.ID)
			f.destroy(t, out.Instance.ID)
			verifySharedWitness(t, f.db(t, ca), wa)
			verifySharedWitness(t, f.db(t, cb), wb)
		}
	}
	// Graceful shutdown joins live consumers while SQL is still available; data remains.
	_, gracefulStarted := f.addDisposable(t, "shutdown-disposable", "wait")
	graceful := f.accept(t, f.request("shutdown-disposable", f.h.project))
	stopGracefulLease := f.keepLease(t, graceful)
	f.awaitJob(t, graceful, gracefulStarted)
	cg := f.credential(t, graceful.Instance.ID, "live-native")
	wg := independentSharedWitness(t, f.db(t, cg))
	urlG := f.http(t, graceful.Instance.ID)
	proofBeforeShutdown, err := os.ReadFile(proof)
	if err != nil {
		t.Fatal(err)
	}
	stopGracefulLease()
	if _, err := f.h.cli.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	if err := f.h.process.Wait(); err != nil {
		t.Fatal(err)
	}
	f.h.waited = true
	preserveSharedDaemonLog(t, f.h, "graceful live shutdown")
	verifySharedStopProof(t, proof, ca.database, len(proofBeforeShutdown))
	verifySharedStopProof(t, proof, cg.database, len(proofBeforeShutdown))
	requireSharedHTTPAbsent(t, urlA)
	requireSharedHTTPAbsent(t, urlB)
	requireSharedHTTPAbsent(t, urlG)
	startSharedRecoveryDaemon(t, f.h, func() bool {
		out, err := f.d.ContainerInspect(ctx, serverID, client.ContainerInspectOptions{})
		if err != nil {
			t.Fatal(err)
		}
		return out.Container.State.Running
	})
	// No transparent continuation, explicit restart restores data/credentials.
	stateA, _ := f.h.cli.Inspect(ctx, a.Instance.ID)
	if active(stateA.Instance.Status) {
		t.Fatal("shutdown transparently resumed execution")
	}
	f.accept(t, f.request("live-native", f.h.project))
	f.accept(t, f.request("live-container", f.linked))
	f.wait(t, a.Instance.ID, v1.RuntimeReady)
	f.wait(t, b.Instance.ID, v1.RuntimeReady)
	verifySharedWitness(t, f.db(t, ca), wa)
	verifySharedWitness(t, f.db(t, cb), wb)
	gracefulFinal := f.wait(t, graceful.Instance.ID, v1.Cancelled)
	if gracefulFinal.Instance.Execution.Kept || f.credential(t, graceful.Instance.ID, "live-native") != cg {
		t.Fatal("shutdown changed retained disposable credentials or kept live consumers")
	}
	verifySharedWitness(t, f.db(t, cg), wg)
	f.destroy(t, graceful.Instance.ID)
	f.requireAbsent(t, cg)
	// Active crash: pause actual recovery before server stop, after joining consumers.
	_, crashStarted := f.addDisposable(t, "crash-disposable", "wait")
	crashed := f.accept(t, f.request("crash-disposable", f.h.project))
	stopCrashLease := f.keepLease(t, crashed)
	f.awaitJob(t, crashed, crashStarted)
	cc := f.credential(t, crashed.Instance.ID, "live-native")
	wc := independentSharedWitness(t, f.db(t, cc))
	urlC := f.http(t, crashed.Instance.ID)
	stopCrashLease()
	urlA, urlB = f.http(t, a.Instance.ID), f.http(t, b.Instance.ID)
	if err := f.h.process.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	if err := f.h.process.Wait(); err == nil {
		t.Fatal("daemon did not crash")
	}
	f.h.waited = true
	preserveSharedDaemonLog(t, f.h, "active crashed daemon")
	startSharedCheckpointDaemon(t, f.h, "stop-server", false)
	checkpoint := waitSharedCheckpoint(t, f.h, "stop-server")
	requireSharedHTTPAbsent(t, urlA)
	requireSharedHTTPAbsent(t, urlB)
	requireSharedHTTPAbsent(t, urlC)
	serverBeforeStop, err := f.d.ContainerInspect(ctx, serverID, client.ContainerInspectOptions{})
	if err != nil || serverBeforeStop.Container.State == nil || !serverBeforeStop.Container.State.Running {
		t.Fatal("server stopped before observable consumer-join checkpoint", err)
	}
	if _, err = checkpoint.Write([]byte{1}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	ready := false
	for time.Now().Before(deadline) {
		if _, err = f.h.cli.Status(ctx); err == nil {
			ready = true
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !ready {
		t.Fatal("recovered daemon API unavailable within original expectation")
	}
	if _, err = f.h.cli.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	if err = f.h.process.Wait(); err != nil {
		t.Fatal(err)
	}
	f.h.waited = true
	preserveSharedDaemonLog(t, f.h, "observed consumer-before-server recovery")
	startSharedRecoveryDaemon(t, f.h, func() bool {
		out, err := f.d.ContainerInspect(ctx, serverID, client.ContainerInspectOptions{})
		if err != nil {
			t.Fatal(err)
		}
		return out.Container.State.Running
	})
	stateA, _ = f.h.cli.Inspect(ctx, a.Instance.ID)
	stateB, _ := f.h.cli.Inspect(ctx, b.Instance.ID)
	if stateA.Instance.Status != v1.Interrupted || stateB.Instance.Status != v1.Interrupted {
		t.Fatal("active crash did not report interruption")
	}
	requireSharedHTTPAbsent(t, urlA)
	requireSharedHTTPAbsent(t, urlB)
	_, _, running := f.server(t)
	if running {
		t.Fatal("shared server remained running after recovered consumers")
	}
	for _, selection := range []struct{ id, scene, path string }{{a.Instance.ID, "live-native", f.h.project}, {b.Instance.ID, "live-container", f.linked}} {
		r := f.request(selection.scene, selection.path)
		r.InstanceID = selection.id
		if _, err := f.h.cli.Restart(ctx, r); err != nil {
			t.Fatal("explicit restart after crash failed", err)
		}
	}

	f.wait(t, a.Instance.ID, v1.RuntimeReady)
	f.wait(t, b.Instance.ID, v1.RuntimeReady)
	if f.credential(t, a.Instance.ID, "live-native") != ca || f.credential(t, b.Instance.ID, "live-container") != cb {
		t.Fatal("crash rotated paired credentials")
	}
	verifySharedWitness(t, f.db(t, ca), wa)
	verifySharedWitness(t, f.db(t, cb), wb)
	crashFinal := f.wait(t, crashed.Instance.ID, v1.Interrupted)
	if crashFinal.Instance.Execution.Kept || f.credential(t, crashed.Instance.ID, "live-native") != cc {
		t.Fatal("crash changed retained disposable credentials or kept live consumers")
	}
	requireSharedHTTPAbsent(t, urlC)
	verifySharedWitness(t, f.db(t, cc), wc)
	f.destroy(t, crashed.Instance.ID)
	f.requireAbsent(t, cc)
	// A short lease is used only for deliberate client-loss cases, after the
	// shutdown/crash cases have retained their independent live owners.
	if _, err := f.h.cli.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	if err := f.h.process.Wait(); err != nil {
		t.Fatal(err)
	}
	f.h.waited = true
	preserveSharedDaemonLog(t, f.h, "before client-loss lease configuration")
	f.h.lease = "2s"
	startSharedRecoveryDaemon(t, f.h, nil)
	f.accept(t, f.request("live-native", f.h.project))
	f.accept(t, f.request("live-container", f.linked))
	f.wait(t, a.Instance.ID, v1.RuntimeReady)
	f.wait(t, b.Instance.ID, v1.RuntimeReady)
	for _, keep := range []bool{false, true} {
		name := fmt.Sprintf("client-loss-%v", keep)
		_, started := f.addDisposable(t, name, "wait")
		r := f.request(name, f.h.project)
		r.Options.KeepOnFailure = keep
		out := f.accept(t, r)
		f.awaitJob(t, out, started)
		lostCredential := f.credential(t, out.Instance.ID, "live-native")
		lostWitness := independentSharedWitness(t, f.db(t, lostCredential))
		final := f.wait(t, out.Instance.ID, v1.Cancelled)
		if final.Instance.Execution.Kept != keep {
			t.Fatal("client-loss keep policy differs")
		}
		if keep {
			f.http(t, out.Instance.ID)
			verifySharedWitness(t, f.db(t, lostCredential), lostWitness)
		} else {
			f.requireAbsent(t, lostCredential)
			if f.shared(t).Allocations != 2 {
				t.Fatal("client loss retained default allocation")
			}
		}
		f.destroy(t, out.Instance.ID)
		verifySharedWitness(t, f.db(t, ca), wa)
		verifySharedWitness(t, f.db(t, cb), wb)
	}
	f.destroy(t, a.Instance.ID)
	f.destroy(t, b.Instance.ID)
	if f.shared(t).Allocations != 0 {
		t.Fatal("retained allocations after explicit cleanup")
	}
	_, _, running = f.server(t)
	if !running {
		t.Fatal("idle shared server stopped implicitly")
	}
	for _, action := range []string{"stop", "restart", "stop"} {
		if _, err := f.h.cli.Shared(ctx, action, v1.SharedRequest{APIVersion: v1.Version, ServiceID: shared.ID}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.h.cli.Shared(ctx, "destroy", v1.SharedRequest{APIVersion: v1.Version, ServiceID: shared.ID}); err != nil {
		t.Fatal(err)
	}
	t.Log("failed/canceled/deadline/client-loss keep policies, explicit/repeat cleanup, graceful/active crash preservation/no continuation, fences and explicit idle lifecycle passed")
}

// Test API clients must keep a disposable lease while deliberately holding a
// startup barrier. Cancel/lease-loss tests stop this owner explicitly first.
func (f *sharedMariaDBFixture) keepLease(t *testing.T, response v1.InstanceResponse) func() {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		ticker := time.NewTicker(200 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				done <- nil
				return
			case <-ticker.C:
				_, err := f.h.cli.Renew(ctx, response.Instance.ID, response.LeaseToken)
				if err != nil {
					if ctx.Err() != nil {
						done <- nil
					} else {
						done <- err
					}
					return
				}
			}
		}
	}()
	var once sync.Once
	stop := func() {
		once.Do(func() {
			cancel()
			if err := <-done; err != nil {
				t.Error("owned test lease renewal failed", err)
			}
		})
	}
	t.Cleanup(stop)
	return stop
}

// Exercise the joins with main's private composition, startup presentation and
// attached owners through the real CLI/API and shared SQL, without a new harness.
func TestSharedMariaDBMainContracts(t *testing.T)    { testSharedMainContracts(t, "mariadb") }
func TestSharedPostgreSQLMainContracts(t *testing.T) { testSharedMainContracts(t, "postgresql") }
func testSharedMainContracts(t *testing.T, engine string) {
	t.Setenv("BACKLOT_PROJECT_CONFIG_DIR", filepath.Join(shortTemp(t), "registry"))
	f := newSharedSQLFixture(t, false, engine)
	sharedStop(t, f.h)
	f.h.lease = "10s"
	f.h.start()
	proof := f.addLive(t)
	f.h.manifest.Inputs = map[string]v1.Input{"token": {Secret: true}}
	for _, name := range []string{"live-native", "live-container"} {
		component := f.h.manifest.Components[name]
		component.Environment.Assign["PRIVATE_TOKEN"] = fixtureRef("input", "token", "")
		component.Fixtures["input"] = v1.Fixture{Value: fixtureRef("input", "token", "")}
		f.h.manifest.Components[name] = component
		scene := f.h.manifest.Scenes[name]
		scene.StartupDetails = []v1.StartupDetailSelection{{Component: name, Fixture: "password", Label: "Application password"}, {Component: name, Fixture: "input", Label: "Project token"}}
		f.h.manifest.Scenes[name] = scene
	}
	f.writeBoth(t)
	configure := func(value string) {
		t.Helper()
		payload, err := json.Marshal(v1.PrivateConfig{Version: v1.ManifestVersion, Inputs: map[string]string{"token": value}})
		if err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command(f.binary, "project", "setup", "--project", f.h.project, "--config", f.configuration, "--from", "-", "--json")
		cmd.Stdin = bytes.NewReader(payload)
		if _, err := cmd.CombinedOutput(); err != nil {
			t.Fatal("private shared setup failed", err)
		}
	}
	configure("configured-shared-token")
	started := f.run("live-native", f.h.project)
	id := started.Instance.ID
	credential := f.credential(t, id, "live-native")
	if started.Instance.Plan.EffectiveConfigDigest == "" || started.StartupDetails == nil || started.StartupDetails.Warning != nil || len(started.StartupDetails.Details) != 2 || started.StartupDetails.Attempt != started.Instance.Execution.Attempt {
		t.Fatal("shared startup lost captured provenance or selected summary")
	}
	if started.StartupDetails.Details[0].Value != credential.password || !started.StartupDetails.Details[0].Sensitive || started.StartupDetails.Details[1].Value != "configured-shared-token" {
		t.Fatal("selected shared application secrets were not displayed")
	}
	private := f.liveAdminRecord(t)
	inspect, err := f.h.cli.Inspect(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	fixtures, err := f.h.cli.Fixtures(context.Background(), v1.FixturesRequest{APIVersion: v1.Version, InstanceID: id, Component: "live-native"})
	if err != nil {
		t.Fatal(err)
	}
	logs, err := f.h.cli.Logs(context.Background(), v1.LogsRequest{APIVersion: v1.Version, InstanceID: id})
	if err != nil {
		t.Fatal(err)
	}
	for _, view := range []any{inspect, fixtures, logs, f.shared(t)} {
		ordinary, _ := json.Marshal(view)
		for _, secret := range []string{private.AdminPassword, private.Server.Token, credential.password, "configured-shared-token"} {
			if bytes.Contains(ordinary, []byte(secret)) {
				t.Fatal("ordinary shared observation leaked a secret")
			}
		}
	}
	startup, _ := json.Marshal(started)
	for _, secret := range []string{private.AdminPassword, private.Server.Token} {
		if bytes.Contains(startup, []byte(secret)) {
			t.Fatal("administration entered startup presentation")
		}
	}
	url := f.http(t, id)
	configure("changed-shared-token")
	request := f.request("live-native", f.h.project)
	request.InstanceID = id
	_, err = f.h.cli.Restart(context.Background(), request)
	code(t, err, "unsafe_drift")
	f.http(t, id)
	if f.credential(t, id, "live-native") != credential {
		t.Fatal("rejected private drift mutated allocation")
	}
	configure("configured-shared-token")
	if _, err := f.h.cli.StopExecution(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	requireSharedHTTPAbsent(t, url)

	for _, name := range []string{"live-native", "live-container"} {
		path := f.h.project
		if name == "live-container" {
			path = f.linked
		}
		request := f.request(name, path)
		request.Attached = true
		accepted := f.accept(t, request)
		if accepted.Attachment == nil {
			t.Fatal("shared start omitted attached authority")
		}
		f.wait(t, accepted.Instance.ID, v1.RuntimeReady)
		credential := f.credential(t, accepted.Instance.ID, name)
		witness := independentSharedWitness(t, f.db(t, credential))
		url := f.http(t, accepted.Instance.ID)
		owner := v1.AttachedRequest{APIVersion: v1.Version, InstanceID: accepted.Instance.ID, Owner: accepted.Attachment.Owner}
		if name == "live-native" {
			if _, err := f.h.cli.Attached(context.Background(), owner, true); err != nil {
				t.Fatal(err)
			}
		} // No renewal for the container owner: client loss expires its lease.
		f.wait(t, accepted.Instance.ID, v1.Stopped)
		requireSharedHTTPAbsent(t, url)
		if name == "live-native" {
			verifySharedStopProof(t, proof, credential.database)
		}
		if f.credential(t, accepted.Instance.ID, name) != credential {
			t.Fatal("attached cleanup released persistent credentials")
		}
		verifySharedWitness(t, f.db(t, credential), witness)
		f.accept(t, f.request(name, path))
		f.wait(t, accepted.Instance.ID, v1.RuntimeReady)
		_, err := f.h.cli.Attached(context.Background(), owner, true)
		code(t, err, "conflict")
		f.http(t, accepted.Instance.ID)
		verifySharedWitness(t, f.db(t, credential), witness)
		if _, err := f.h.cli.StopExecution(context.Background(), accepted.Instance.ID); err != nil {
			t.Fatal(err)
		}
	}
}
