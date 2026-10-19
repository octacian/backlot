//go:build darwin || linux

package daemon

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/containerd/errdefs"
	"github.com/go-sql-driver/mysql"
	"github.com/moby/moby/client"
	v1 "github.com/octacian/backlot/api/v1"
	"github.com/octacian/backlot/internal/plan"
)

// TestSharedMariaDBGate1 is the early real CLI -> daemon -> MariaDB checkpoint.
// It deliberately precedes the later comprehensive lifecycle/crash matrix.
func TestSharedMariaDBGate1(t *testing.T) { testSharedMariaDB(t, false) }

// TestSharedMariaDBGate2 first proves real persistence and interrupted release.
func TestSharedMariaDBGate2(t *testing.T) { testSharedMariaDB(t, true) }

type sharedMariaDBFixture struct {
	h                                                  *nativeHarness
	d                                                  *dockerEngine
	binary, endpoint, configuration, linked, serviceID string
	cli                                                func(...string) []byte
	run                                                func(string, string) v1.InstanceResponse
}

func newSharedMariaDBFixture(t *testing.T, gate2 bool, endpoints ...string) *sharedMariaDBFixture {
	endpoint := os.Getenv("BACKLOT_DOCKER_TEST_ENDPOINT")
	if len(endpoints) > 0 {
		endpoint = endpoints[0]
	}
	if endpoint == "" {
		t.Skip("requires explicit Docker endpoint and pre-pulled mariadb:11.4/alpine:3.21; skip is not provider evidence")
	}
	binary := filepath.Join(t.TempDir(), "backlot")
	if out, err := exec.Command("go", "build", "-race", "-o", binary, "../../cmd/backlot").CombinedOutput(); err != nil {
		t.Fatalf("build CLI: %v %s", err, out)
	}
	h := newNativeHarness(t, binary, "30s")
	d, err := newDocker(endpoint)
	if err != nil {
		t.Fatal(err)
	}
	var fixtureImage, imageOwner string
	var fixtureImageIDs []string
	buildStarted := false
	buildUncertain := false
	var baseIDs []string
	// Register full private-journal cleanup before image builds or provider effects.
	t.Cleanup(func() {
		h.close()
		state, err := openStore(h.dir)
		if err == nil {
			err = state.recoverRuntime()
			ids, idsErr := fixtureInstanceIDs(state)
			err = errors.Join(err, idsErr)
			for _, id := range ids {
				if err == nil {
					ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
					err = state.removeResources(ctx, id)
					cancel()
				}
			}
			services, listErr := state.sharedIDs()
			err = errors.Join(err, listErr)
			svc := &service{store: state}
			for _, id := range services {
				if err == nil {
					ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
					_, err = svc.sharedControl(ctx, "destroy", v1.SharedRequest{APIVersion: v1.Version, ServiceID: id})
					cancel()
				}
			}
			err = errors.Join(err, state.db.Close())
		}
		if err == nil && imageOwner != "" && buildStarted {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			err = removeSharedFixtureImages(ctx, d, imageOwner, fixtureImageIDs...)
			if err == nil {
				for _, id := range baseIDs {
					if _, checkErr := d.ImageInspect(ctx, id); checkErr != nil {
						err = errors.New("cached base image preservation unverified")
						break
					}
				}
			}
			cancel()
		}

		if buildUncertain {
			err = errors.Join(err, errors.New("fixture build completion/identity uncertain; preserve diagnostics"))
		}
		if err != nil {
			h.cleanupUnverified = true
			t.Error("shared fixture cleanup failed; private state preserved", h.dir, err)
		} else {
			t.Log("verified owned consumers, allocations, shared container/network/volume and fixture image absent; cached base images retained")
		}
		_ = d.Close()
	})
	ctx := context.Background()
	maria, err := d.ImageInspect(ctx, "mariadb:11.4")
	if err != nil {
		t.Fatal("pre-pull mariadb:11.4 explicitly", err)
	}
	base, err := d.ImageInspect(ctx, "alpine:3.21")
	if err != nil {
		t.Fatal("pre-pull alpine:3.21 explicitly", err)
	}
	baseIDs = []string{maria.ID, base.ID}
	t.Log("MariaDB local immutable image", maria.ID, "Alpine local immutable base", base.ID)
	native := filepath.Join(t.TempDir(), "consumer")
	if out, err := exec.Command("go", "build", "-o", native, "./testdata/shared-consumer").CombinedOutput(); err != nil {
		t.Fatalf("build native consumer: %v %s", err, out)
	}
	buildDir := t.TempDir()
	build := exec.Command("go", "build", "-o", filepath.Join(buildDir, "consumer"), "./testdata/shared-consumer")
	build.Env = append(os.Environ(), "GOOS=linux", "CGO_ENABLED=0")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build container consumer: %v %s", err, out)
	}
	imageOwner = newID()
	dockerfile := "FROM " + base.ID + "\nLABEL io.backlot.owner=" + imageOwner + "\nCOPY consumer /consumer\nENTRYPOINT [\"/consumer\"]\n"
	if err = os.WriteFile(filepath.Join(buildDir, "Dockerfile"), []byte(dockerfile), 0600); err != nil {
		t.Fatal(err)
	}
	iid := filepath.Join(buildDir, "iid")
	build = exec.Command("docker", "--host", endpoint, "build", "--pull=false", "--network=none", "--force-rm=true", "--iidfile", iid, buildDir)
	build.Env = append(os.Environ(), "DOCKER_BUILDKIT=0")
	buildStarted = true
	buildOutput, buildErr := build.CombinedOutput()
	// Classic builder emits each exact image commit. Record our verified IDs
	// before fault tests; cleanup must not scan an unrelated global image graph.
	seenImages := map[string]bool{}
	for _, match := range regexp.MustCompile(`(?m)^ ---> ((?:sha256:)?[a-f0-9]{12,64})\s*$`).FindAllSubmatch(buildOutput, -1) {
		image, inspectErr := d.ImageInspect(ctx, string(match[1]))
		if inspectErr != nil {
			if errdefs.IsNotFound(inspectErr) {
				continue
			}
			buildUncertain = true
			t.Fatal("fixture build image identity unavailable; preserved")
		}
		if image.Config != nil && image.Config.Labels["io.backlot.owner"] == imageOwner && !seenImages[image.ID] {
			fixtureImageIDs = append(fixtureImageIDs, image.ID)
			seenImages[image.ID] = true
		}
	}
	if buildErr != nil {
		buildUncertain = true
		h.cleanupUnverified = true
		t.Fatal("owned fixture image build failed; inspect private build directory", buildDir)
	}
	imageData, err := os.ReadFile(iid)
	if err != nil {
		buildUncertain = true
		t.Fatal(err)
	}
	fixtureImage = strings.TrimSpace(string(imageData))
	if !seenImages[fixtureImage] || len(fixtureImageIDs) != 3 {
		buildUncertain = true
		t.Fatal("classic fixture build's three owned image commits were not verified; preserved")
	}
	t.Log("recorded exact owned fixture image commits before runtime faults", fixtureImageIDs)
	t.Log("owned static application image", fixtureImage, "source testdata/shared-consumer/main.go")
	configuration := filepath.Join(filepath.Dir(h.dir), "machine.json")
	data, _ := json.Marshal(v1.MachineConfig{Version: v1.ManifestVersion, Docker: &v1.DockerConfig{Endpoint: endpoint}})
	if err = os.WriteFile(configuration, data, 0600); err != nil {
		t.Fatal(err)
	}
	env := v1.Environment{Assign: map[string]v1.Value{}}
	fixtures := map[string]v1.Fixture{}
	for _, field := range []string{"host", "port", "database", "username", "password"} {
		value := fixtureRef("resource", "database", field)
		env.Assign["DB_"+strings.ToUpper(field)] = value
		fixtures[field] = v1.Fixture{Value: value}
	}
	resources := []string{"database"}
	nativeJob := v1.Component{Kind: v1.Job, Runtime: v1.Native, Policy: v1.EachStart, Command: &v1.Command{Tool: "consumer"}, Resources: resources, Environment: env, Fixtures: fixtures}
	if gate2 {
		nativeJob.Policy = v1.FreshOnly
		nativeJob.Initializes = []string{"database"}
	}
	containerJob := nativeJob
	containerJob.Runtime = v1.Container
	containerJob.Command = nil
	containerJob.Image = ptrValue(fixtureLiteral(fixtureImage))
	h.manifest = v1.Manifest{Version: v1.ManifestVersion, Project: "shared-gate-one", Tools: map[string]string{"consumer": native}, SharedServices: map[string]v1.SharedService{"sql": {Manager: "mariadb", Image: maria.ID, Storage: "volume", Admin: "generated", Port: 3306, ReadinessTimeout: "60s"}}, Resources: map[string]v1.Resource{"database": {Kind: "allocation", SharedService: "sql"}}, Components: map[string]v1.Component{"native": nativeJob, "container": containerJob}, Scenes: map[string]v1.Scene{"host": {Lifetime: v1.Persistent, Components: []string{"native"}, Resources: resources}, "bridge": {Lifetime: v1.Persistent, Components: []string{"container"}, Resources: resources}}}
	h.write()
	git(t, h.project, "init", "-q", "-b", "main")
	git(t, h.project, "add", ".")
	git(t, h.project, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "-qm", "Gate one fixture")
	linked := filepath.Join(filepath.Dir(h.project), "linked")
	git(t, h.project, "worktree", "add", "-q", "-b", "linked", linked)

	cli := func(args ...string) []byte {
		t.Helper()
		out, err := exec.Command(binary, args...).CombinedOutput()
		if err != nil {
			if args[0] == "run" {
				var response v1.InstanceResponse
				if json.Unmarshal(out, &response) == nil && response.Instance.ID != "" {
					logs, _ := exec.Command(binary, "logs", "--state-dir", h.dir, "--json", response.Instance.ID).CombinedOutput()
					t.Log("consumer diagnostics", string(logs))
				}
			}
			t.Fatalf("CLI %s failed: %s", args[0], out)
		}
		return out
	}
	run := func(scene, path string) v1.InstanceResponse {
		t.Helper()
		out := cli("run", scene, "--project", path, "--config", configuration, "--state-dir", h.dir, "--startup-timeout", "90s", "--job-timeout", "30s", "--json")
		var response v1.InstanceResponse
		if err := json.Unmarshal(out, &response); err != nil {
			t.Fatal(err)
		}
		if response.Instance.Status != v1.RuntimeReady {
			t.Fatalf("real consumer failed: %s", out)
		}
		return response
	}
	source, identityErr := plan.Locate(h.project)
	if identityErr != nil {
		t.Fatal(identityErr)
	}
	identity, identityErr := identifyShared(source, h.manifest.Project)
	if identityErr != nil {
		t.Fatal(identityErr)
	}
	return &sharedMariaDBFixture{h: h, d: d, binary: binary, endpoint: endpoint, configuration: configuration, linked: linked, serviceID: hash(identity.ID + "/sql"), cli: cli, run: run}
}

func testSharedMariaDB(t *testing.T, gate2 bool) {
	f := newSharedMariaDBFixture(t, gate2)
	h, d, binary, configuration, linked, cli, run := f.h, f.d, f.binary, f.configuration, f.linked, f.cli, f.run
	ctx := context.Background()
	resources := []string{"database"}
	var err error
	var data []byte
	var a, b v1.InstanceResponse
	if gate2 {
		type started struct {
			out   []byte
			err   error
			index int
		}
		results := make(chan started, 2)
		barrier := make(chan struct{})
		for index, selection := range [][2]string{{"host", h.project}, {"bridge", linked}} {
			go func() {
				<-barrier
				out, err := exec.Command(binary, "run", selection[0], "--project", selection[1], "--config", configuration, "--state-dir", h.dir, "--startup-timeout", "90s", "--job-timeout", "30s", "--json").CombinedOutput()
				results <- started{out, err, index}
			}()
		}
		close(barrier)
		for range 2 {
			result := <-results
			if result.err != nil {
				t.Fatalf("concurrent start failed: %s", result.out)
			}
			var response v1.InstanceResponse
			if err := json.Unmarshal(result.out, &response); err != nil || response.Instance.Status != v1.RuntimeReady {
				t.Fatal("concurrent consumer failed", err)
			}
			if result.index == 0 {
				a = response
			} else {
				b = response
			}
		}
	} else {
		a = run("host", h.project)
		b = run("bridge", linked)
	}
	var services v1.SharedResponse
	if err = json.Unmarshal(cli("shared", "list", "--state-dir", h.dir, "--json"), &services); err != nil {
		t.Fatal(err)
	}
	if len(services.Services) != 1 || services.Services[0].Allocations != 2 {
		t.Fatal("worktrees did not share exactly one server", services)
	}
	serviceID := services.Services[0].ID
	actualServers, err := d.ContainerList(ctx, client.ContainerListOptions{All: true, Filters: client.Filters{}.Add("label", "io.backlot.shared-service="+serviceID)})
	if err != nil || len(actualServers.Items) != 1 {
		t.Fatal("did not observe exactly one actual owned shared server", err)
	}
	serverID := actualServers.Items[0].ID
	credential := func(id, component string) (string, string, string, int) {
		t.Helper()
		out, err := h.cli.Fixtures(ctx, v1.FixturesRequest{APIVersion: v1.Version, InstanceID: id, Component: component})
		if err != nil {
			t.Fatal(err)
		}
		values := map[string]string{}
		for _, f := range out.Fixtures {
			if f.Sensitive && f.Value != "" {
				t.Fatal("ordinary fixture exposed password")
			}
			values[f.Name] = f.Value
		}
		secret, err := h.cli.FixtureSecret(ctx, v1.FixturesRequest{APIVersion: v1.Version, InstanceID: id, Component: component, SecretName: "password"})
		if err != nil || len(secret.Fixtures) != 1 {
			t.Fatal("explicit credential unavailable", err)
		}
		var port int
		if _, err = fmt.Sscanf(values["port"], "%d", &port); err != nil {
			t.Fatal(err)
		}
		return values["database"], values["username"], secret.Fixtures[0].Value, port
	}
	adb, auser, apass, port := credential(a.Instance.ID, "native")
	bdb, buser, bpass, _ := credential(b.Instance.ID, "container")
	if adb == bdb || auser == buser || apass == bpass {
		t.Fatal("allocation identity collision")
	}
	applicationDB := func(database, user, password string) *sql.DB {
		config := mysql.NewConfig()
		config.User = user
		config.Passwd = password
		config.Net = "tcp"
		config.Addr = fmt.Sprintf("127.0.0.1:%d", port)
		config.DBName = database
		connector, err := mysql.NewConnector(config)
		if err != nil {
			t.Fatal(err)
		}
		db := sql.OpenDB(connector)
		t.Cleanup(func() { _ = db.Close() })
		return db
	}
	appA := applicationDB(adb, auser, apass)
	appB := applicationDB(bdb, buser, bpass)
	var serverVersion string
	if err = appA.QueryRowContext(ctx, "SELECT VERSION()").Scan(&serverVersion); err != nil || !strings.HasPrefix(serverVersion, "11.4.") {
		t.Fatal("fixture requires MariaDB 11.4", err)
	}
	t.Log("actual owned server version", serverVersion)
	control := func(db *sql.DB) {
		t.Helper()
		var value string
		if err := db.QueryRowContext(ctx, "SELECT value FROM witness WHERE id=1").Scan(&value); err != nil || value != "updated" {
			t.Fatal("same-user application positive control failed", err)
		}
	}
	for _, pair := range []struct {
		db   *sql.DB
		peer string
	}{{appA, bdb}, {appB, adb}} {
		control(pair.db)
		if _, err = pair.db.ExecContext(ctx, "SELECT * FROM `"+pair.peer+"`.witness"); !sqlAccessDenied(err) {
			t.Fatal("peer read did not prove privilege denial")
		}
		if _, err = pair.db.ExecContext(ctx, "INSERT INTO `"+pair.peer+"`.witness VALUES (2,'peer',NULL)"); !sqlAccessDenied(err) {
			t.Fatal("peer mutation did not prove privilege denial")
		}
		control(pair.db)
	}
	t.Log("real native and container consumers passed empty database/DDL/DML/admin checks; distinct credentials; bidirectional peer reads/writes denied")
	if gate2 {
		writeWitness := func(db *sql.DB) string {
			t.Helper()
			value := newID()
			if _, err := db.ExecContext(ctx, "CREATE TABLE independent_witness (value VARCHAR(64) NOT NULL)"); err != nil {
				t.Fatal(err)
			}
			if _, err := db.ExecContext(ctx, "INSERT INTO independent_witness VALUES (?)", value); err != nil {
				t.Fatal(err)
			}
			return value
		}
		witnessA, witnessB := writeWitness(appA), writeWitness(appB)
		verifyWitness := func(db *sql.DB, expected string) {
			t.Helper()
			var value string
			if err := db.QueryRowContext(ctx, "SELECT value FROM independent_witness").Scan(&value); err != nil || value != expected {
				t.Fatal("independent data witness lost or changed", err)
			}
		}
		// Existing application data is read on every restart; credentials must stay paired.
		for _, action := range []string{"stop-start", "restart"} {
			if action == "stop-start" {
				cli("stop", "--state-dir", h.dir, "--json", a.Instance.ID)
				run("host", h.project)
			} else {
				cli("restart", a.Instance.ID, "--state-dir", h.dir, "--startup-timeout", "90s", "--json")
			}
			database, user, password, _ := credential(a.Instance.ID, "native")
			if database != adb || user != auser || password != apass {
				t.Fatal("persistent credentials rotated", action)
			}
			control(appA)
			control(appB)
			verifyWitness(appA, witnessA)
			verifyWitness(appB, witnessB)
		}
		cli("reset", a.Instance.ID, "--state-dir", h.dir, "--startup-timeout", "90s", "--json")
		newDatabase, newUser, newPassword, _ := credential(a.Instance.ID, "native")
		if newDatabase == adb || newUser == auser || newPassword == apass {
			t.Fatal("reset reused old allocation")
		}
		_ = appA.Close()
		appA = applicationDB(newDatabase, newUser, newPassword)
		control(appA)
		control(appB)
		var oldWitnessTables int
		if err := appA.QueryRowContext(ctx, "SELECT COUNT(*) FROM information_schema.TABLES WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME='independent_witness'").Scan(&oldWitnessTables); err != nil || oldWitnessTables != 0 {
			t.Fatal("reset retained independent witness", err)
		}
		verifyWitness(appB, witnessB)
		witnessA = writeWitness(appA)
		verifyWitness(appA, witnessA)
		// A new connection using old credentials must fail authentication after release.
		old := applicationDB(adb, auser, apass)
		if oldErr := old.PingContext(ctx); oldErr == nil {
			t.Fatal("old allocation credentials still usable")
		} else {
			var driver *mysql.MySQLError
			if !errors.As(oldErr, &driver) || driver.Number != 1045 {
				t.Fatalf("old credential rejection did not prove authentication denial: %T %v", oldErr, oldErr)
			}
		}
		_ = old.Close()
		t.Log("early Gate2: simultaneous actual worktrees/one server, stop/start and restart data+credential pairing, fresh target-only reset and peer preservation passed")

		// Register the private barrier and daemon cleanup before the retained-child fault.
		_ = appA.Close()
		_ = appB.Close()
		startSharedCheckpointDaemon(t, h, "release-effect")
		completed := make(chan error, 1)
		go func() {
			_, err := exec.Command(binary, "destroy", "--state-dir", h.dir, "--json", a.Instance.ID).CombinedOutput()
			completed <- err
		}()
		waitSharedCheckpoint(t, h, "release-effect")
		if err := h.process.Process.Kill(); err != nil {
			t.Fatal(err)
		}
		if err := h.process.Wait(); err == nil {
			t.Fatal("retained daemon did not fault")
		}
		h.waited = true
		preserveSharedDaemonLog(t, h, "faulted release daemon")
		if err := <-completed; err == nil {
			t.Fatal("lost acknowledgment falsely reported success")
		}
		// Only the dead daemon's private journal is opened here; no live writer.
		state, err := openStore(h.dir)
		if err != nil {
			t.Fatal(err)
		}
		allocations, readErr := state.sharedAllocations(a.Instance.ID)
		if readErr != nil || allocations["database"].Request.Operation != "release" || allocations["database"].Released {
			t.Fatal("release intent lost", readErr)
		}
		_ = state.db.Close()
		startSharedRecoveryDaemon(t, h, func() bool {
			observed, err := d.ContainerInspect(ctx, serverID, client.ContainerInspectOptions{})
			if err != nil || observed.Container.State == nil {
				t.Fatal("cannot observe retained server during recovery", err)
			}
			return observed.Container.State.Running
		})
		recoveredServer, inspectErr := d.ContainerInspect(ctx, serverID, client.ContainerInspectOptions{})
		if inspectErr != nil || recoveredServer.Container.State == nil || recoveredServer.Container.State.Running {
			t.Fatal("recovery did not stop exact retained server before exposing API", inspectErr)
		}
		t.Log("observed exact retained shared server stopped at recovery API availability; execution requires explicit restart")
		// Ordinary start must not recreate an allocation whose release lost its ack.
		out, runErr := exec.Command(binary, "run", "host", "--project", h.project, "--config", configuration, "--state-dir", h.dir, "--startup-timeout", "90s", "--json").CombinedOutput()
		if runErr == nil || !strings.Contains(string(out), "allocation_conflict") {
			t.Fatalf("release obligation reopened as allocate: %s", out)
		}
		cli("destroy", "--state-dir", h.dir, "--json", a.Instance.ID)
		// Recovery stops consumers/server but does not transparently resume; peer data survives explicit restart.
		run("bridge", linked)
		_, _, _, port = credential(b.Instance.ID, "container")
		appB = applicationDB(bdb, buser, bpass)
		control(appB)
		verifyWitness(appB, witnessB)
		cli("destroy", "--state-dir", h.dir, "--json", b.Instance.ID)
		cli("shared", "destroy", "--state-dir", h.dir, "--json", serviceID)
		t.Log("early Gate2: retained-child release-effect lost acknowledgment, durable release fence, repeat explicit cleanup and peer persistence across recovery passed")
		return
	}
	// Relevant failure/recovery: another branch declares incompatible server args.
	// Rejection must leave the owned server identity and peer data unchanged.
	conflict := h.manifest
	conflict.SharedServices = map[string]v1.SharedService{}
	changed := h.manifest.SharedServices["sql"]
	changed.Args = []string{"--max-connections=42"}
	conflict.SharedServices["sql"] = changed
	data, _ = json.Marshal(conflict)
	if err = os.WriteFile(filepath.Join(linked, "backlot.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	// A distinct scene creates a new instance so retained instance drift cannot hide the shared contract check.
	conflict.Scenes["conflict"] = v1.Scene{Lifetime: v1.Persistent, Components: []string{"native"}, Resources: resources}
	data, _ = json.Marshal(conflict)
	if err = os.WriteFile(filepath.Join(linked, "backlot.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	out, runErr := exec.Command(binary, "run", "conflict", "--project", linked, "--config", configuration, "--state-dir", h.dir, "--startup-timeout", "90s", "--json").CombinedOutput()
	if runErr == nil || !strings.Contains(string(out), "shared_conflict") {
		t.Fatal("conflicting server configuration was not explicitly rejected")
	}
	var witness string
	if err = appA.QueryRowContext(ctx, "SELECT value FROM witness WHERE id=1").Scan(&witness); err != nil || witness != "updated" {
		t.Fatal("conflict mutated peer data", err)
	}
	// Recover the rejected declaration using the exact original server contract.
	data, _ = json.Marshal(h.manifest)
	if err = os.WriteFile(filepath.Join(linked, "backlot.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	var failed v1.InstanceResponse
	if err = json.Unmarshal(out, &failed); err != nil || failed.Instance.ID == "" {
		t.Fatal("failed instance identity unavailable", err)
	}
	recoveredData := cli("reset", failed.Instance.ID, "--state-dir", h.dir, "--config", configuration, "--startup-timeout", "90s", "--json")
	var recovered v1.InstanceResponse
	if err = json.Unmarshal(recoveredData, &recovered); err != nil || recovered.Instance.Status != v1.RuntimeReady {
		t.Fatal("explicit reset did not recover corrected declaration", err)
	}
	cli("destroy", "--state-dir", h.dir, "--json", recovered.Instance.ID)
	actualServers, err = d.ContainerList(ctx, client.ContainerListOptions{All: true, Filters: client.Filters{}.Add("label", "io.backlot.shared-service="+serviceID)})
	if err != nil || len(actualServers.Items) != 1 || actualServers.Items[0].ID != serverID {
		t.Fatal("conflict recovery replaced shared server", err)
	}
	if err = appA.Close(); err != nil {
		t.Fatal(err)
	}
	cli("destroy", "--state-dir", h.dir, "--json", a.Instance.ID)
	if err = appB.QueryRowContext(ctx, "SELECT value FROM witness WHERE id=1").Scan(&witness); err != nil || witness != "updated" {
		t.Fatal("destroy disturbed peer allocation", err)
	}
	if err = appB.Close(); err != nil {
		t.Fatal(err)
	}
	cli("destroy", "--state-dir", h.dir, "--json", b.Instance.ID)
	// Restore exact branch configuration and retry the failed instance; fresh empty
	// allocation succeeds without changing the retained shared-server identity.
	data, _ = json.Marshal(h.manifest)
	if err = os.WriteFile(filepath.Join(linked, "backlot.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	t.Log("retained configuration conflict rejected before peer mutation; peer data survives target destruction; shared service remains independently owned", serviceID)
	_ = cli("shared", "inspect", "--state-dir", h.dir, "--json", serviceID)
}

func sqlAccessDenied(err error) bool {
	var driver *mysql.MySQLError
	return errors.As(err, &driver) && (driver.Number == 1044 || driver.Number == 1142 || driver.Number == 1227)
}

func removeSharedFixtureImages(ctx context.Context, d *dockerEngine, owner string, ids ...string) error {
	if len(ids) == 0 {
		return errors.New("fixture build image identities unavailable; preserved")
	}
	pending := map[string]bool{}
	for _, id := range ids {
		image, err := d.ImageInspect(ctx, id)
		if err != nil || image.Config == nil || image.Config.Labels["io.backlot.owner"] != owner {
			return errors.New("fixture image ownership uncertain; preserved")
		}
		pending[id] = true
	}
	var err error
	for len(pending) > 0 {
		progress := false
		for id := range pending {
			if _, err = d.ImageRemove(ctx, id, client.ImageRemoveOptions{PruneChildren: false}); err != nil {
				continue
			}
			if _, err = d.ImageInspect(ctx, id); !errdefs.IsNotFound(err) {
				return errors.New("fixture image absence unverified")
			}
			delete(pending, id)
			progress = true
		}
		if !progress {
			return errors.New("owned fixture image removal blocked; preserved")
		}
	}
	return nil
}
