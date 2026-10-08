//go:build !windows

package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	v1 "github.com/octacian/backlot/api/v1"
	"github.com/octacian/backlot/internal/client"
	"github.com/octacian/backlot/internal/plan"
	bolt "go.etcd.io/bbolt"
)

const fixtureManifest = `{"version":"backlot/v1","project":"fixture","tools":{"go":"go"},"inputs":{"password":{"secret":true}},"components":{"check":{"kind":"job","runtime":"native","command":{"tool":"go","args":["version"]},"policy":"each-start","environment":{"assign":{"PASSWORD":{"ref":{"kind":"input","name":"password"}},"LITERAL_SECRET":{"literal":"literal-fixture-secret","secret":true}},"seeds":["seed.env"]}}},"scenes":{"dev":{"lifetime":"persistent","components":["check"]},"test":{"lifetime":"disposable","components":["check"],"terminal_job":"check"}}}`

func shortTemp(t *testing.T) string {
	t.Helper()
	path, err := os.MkdirTemp("/tmp", "bl-m2-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(path); err != nil {
			t.Error(err)
		}
	})
	return path
}

func writeFixture(t *testing.T, root string) string {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, "backlot.json"), []byte(fixtureManifest), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "seed.env"), []byte("SEED=seed-fixture-secret\n"), 0600); err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(shortTemp(t), "config.json")
	if err := os.WriteFile(config, []byte(`{"version":"backlot/v1","inputs":{"password":"resolved-fixture-secret"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	return config
}
func request(root, config, scene string) v1.PrepareRequest {
	return v1.PrepareRequest{APIVersion: v1.Version, Plan: v1.PlanRequest{ProjectPath: root, ConfigPath: config, Scene: scene}}
}
func startFixture(t *testing.T, dir string, lease time.Duration) (*client.Client, func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	exited := make(chan error, 1)
	go func() { exited <- Serve(ctx, Options{Directory: dir, LeaseDuration: lease}) }()
	cli := client.New(SocketPath(dir))
	deadline := time.After(5 * time.Second)
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case err := <-exited:
			cancel()
			cli.Close()
			t.Fatalf("daemon startup: %v", err)
		case <-deadline:
			cancel()
			cli.Close()
			t.Fatal("daemon startup timeout")
		case <-tick.C:
			if _, err := cli.Status(context.Background()); err == nil {
				var once sync.Once
				stop := func() {
					once.Do(func() {
						cancel()
						if err := <-exited; err != nil {
							t.Errorf("daemon exit: %v", err)
						}
						cli.Close()
					})
				}
				t.Cleanup(stop)
				return cli, stop
			}
		}
	}
}
func prepareFixture(t *testing.T, cli *client.Client, req v1.PrepareRequest) v1.InstanceResponse {
	t.Helper()
	response, err := cli.Prepare(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	return response
}
func code(t *testing.T, err error, want string) {
	t.Helper()
	var typed *v1.PlanError
	if !errors.As(err, &typed) || typed.Code != want {
		t.Fatalf("error %v, want code %s", err, want)
	}
}
func git(t *testing.T, root string, args ...string) {
	t.Helper()
	command := exec.Command("git", args...)
	command.Dir = root
	command.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v %s", args, err, output)
	}
}

func TestRealWorktreesAliasesConcurrentDuplicatesAndDrift(t *testing.T) {
	repo := shortTemp(t)
	config := writeFixture(t, repo)
	git(t, repo, "init", "-q")
	git(t, repo, "add", ".")
	git(t, repo, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "-qm", "fixture")
	roots := []string{filepath.Join(shortTemp(t), "one"), filepath.Join(shortTemp(t), "two")}
	for _, root := range roots {
		git(t, repo, "worktree", "add", "--detach", root, "HEAD")
	}
	alias := filepath.Join(shortTemp(t), "alias")
	if err := os.Symlink(roots[0], alias); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(shortTemp(t), "state")
	cli, stop := startFixture(t, dir, time.Second)
	responses := make(chan v1.InstanceResponse, 24)
	failures := make(chan error, 24)
	var group sync.WaitGroup
	for i := range 24 {
		group.Go(func() {
			root := roots[0]
			if i%2 == 0 {
				root = alias
			}
			r, err := cli.Prepare(context.Background(), request(root, config, "dev"))
			if err != nil {
				failures <- err
			} else {
				responses <- r
			}
		})
	}
	group.Wait()
	close(responses)
	close(failures)
	for err := range failures {
		t.Error(err)
	}
	id := ""
	checkout := ""
	for r := range responses {
		if id == "" {
			id = r.Instance.ID
			checkout = r.Instance.Checkout.ID
		}
		if r.Instance.ID != id || r.Instance.Status != v1.Prepared {
			t.Fatalf("duplicates diverged: %+v", r)
		}
	}
	if id == "" {
		t.Fatal("no successful preparation")
	}
	other := prepareFixture(t, cli, request(roots[1], config, "dev"))
	if other.Instance.ID == id || other.Instance.Checkout.ID == checkout {
		t.Fatal("real worktrees shared identity")
	}
	if other.Instance.Checkout.GitDirectory == "" || other.Instance.Checkout.GitHead == "" {
		t.Fatal("missing Git provenance")
	}
	disposableIDs := map[string]bool{}
	for range 4 {
		r := prepareFixture(t, cli, request(alias, config, "test"))
		if disposableIDs[r.Instance.ID] || r.LeaseToken == "" {
			t.Fatal("disposable identity or lease missing")
		}
		disposableIDs[r.Instance.ID] = true
	}
	original, err := cli.Inspect(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	changed := strings.Replace(fixtureManifest, `"version"]`, `"env"]`, 1)
	if err := os.WriteFile(filepath.Join(roots[0], "backlot.json"), []byte(changed), 0600); err != nil {
		t.Fatal(err)
	}
	drift := prepareFixture(t, cli, request(alias, config, "dev"))
	if !drift.ManifestDrift || drift.Instance.ID != id || drift.Instance.Plan.ManifestDigest != original.Instance.Plan.ManifestDigest {
		t.Fatal("manifest snapshot overwritten or drift missing")
	}
	stop()
	restarted, _ := startFixture(t, dir, time.Second)
	persisted, err := restarted.Inspect(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if persisted.Instance.Status != v1.Prepared || persisted.Instance.Plan.ManifestDigest != original.Instance.Plan.ManifestDigest {
		t.Fatal("completed persistent metadata did not survive restart")
	}
	for disposable := range disposableIDs {
		r, err := restarted.Inspect(context.Background(), disposable)
		if err != nil || r.Instance.Status != v1.Interrupted {
			t.Fatalf("shutdown disposable: %+v %v", r, err)
		}
	}
}

func TestLeaseRenewalExpiryAndExplicitCancellation(t *testing.T) {
	root := shortTemp(t)
	config := writeFixture(t, root)
	cli, _ := startFixture(t, filepath.Join(shortTemp(t), "state"), 400*time.Millisecond)
	r := prepareFixture(t, cli, request(root, config, "test"))
	_, err := cli.Renew(context.Background(), r.Instance.ID, strings.Repeat("0", 64))
	code(t, err, "invalid_lease")
	time.Sleep(100 * time.Millisecond)
	renewed, err := cli.Renew(context.Background(), r.Instance.ID, r.LeaseToken)
	if err != nil {
		t.Fatal(err)
	}
	if renewed.Instance.LeaseExpiresAt <= r.Instance.LeaseExpiresAt {
		t.Fatal("lease not extended")
	}
	inspected, err := cli.Inspect(context.Background(), r.Instance.ID)
	if err != nil || inspected.LeaseToken != "" {
		t.Fatal("inspection disclosed lease")
	}
	// Client disconnect has no HTTP-session heuristic; it is enforced by the deadline.
	deadline := time.Now().Add(3 * time.Second)
	for {
		current, err := cli.Inspect(context.Background(), r.Instance.ID)
		if err != nil {
			t.Fatal(err)
		}
		if current.Instance.Status == v1.Cancelled {
			if current.Instance.Operation.Reason != "client lease expired" {
				t.Fatal("missing expiry reason")
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("disconnected preparation outlived lease")
		}
		time.Sleep(20 * time.Millisecond)
	}
	_, err = cli.Renew(context.Background(), r.Instance.ID, r.LeaseToken)
	if err == nil {
		t.Fatal("expired lease resurrected")
	}
	second := prepareFixture(t, cli, request(root, config, "test"))
	cancelled, err := cli.Cancel(context.Background(), second.Instance.ID)
	if err != nil || cancelled.Instance.Status != v1.Cancelled {
		t.Fatalf("cancel: %+v %v", cancelled, err)
	}
	again, err := cli.Cancel(context.Background(), second.Instance.ID)
	if err != nil || again.Instance.Operation.ID != second.Instance.Operation.ID {
		t.Fatal("cancel not idempotent")
	}
	persistent := prepareFixture(t, cli, request(root, config, "dev"))
	if persistent.Instance.LeaseExpiresAt != "" || persistent.LeaseToken != "" {
		t.Fatal("persistent preparation leased")
	}
	_, err = cli.Renew(context.Background(), persistent.Instance.ID, second.LeaseToken)
	code(t, err, "invalid_lease")
}

func TestIntentEffectRecoveryAndPrivateSnapshots(t *testing.T) {
	for _, stage := range []string{"intent", "effect"} {
		t.Run(stage, func(t *testing.T) {
			root := shortTemp(t)
			config := writeFixture(t, root)
			dir := shortTemp(t)
			if err := os.Chmod(dir, 0700); err != nil {
				t.Fatal(err)
			}
			state, err := openStore(dir)
			if err != nil {
				t.Fatal(err)
			}
			service := &service{store: state, lease: time.Second, checkpoint: func(at string) error {
				if at == stage {
					return fmt.Errorf("injected interruption")
				}
				return nil
			}}
			interrupted, err := service.prepare(context.Background(), request(root, config, "dev"))
			if err == nil {
				t.Fatal("interruption not injected")
			}
			if err := state.db.Close(); err != nil {
				t.Fatal(err)
			}
			state, err = openStore(dir)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := state.db.Close(); err != nil {
					t.Error(err)
				}
			}()
			if err := state.reconcile("crash recovery", false); err != nil {
				t.Fatal(err)
			}
			recovered, err := state.inspect(interrupted.Instance.ID)
			if err != nil {
				t.Fatal(err)
			}
			if recovered.Status != v1.Interrupted || recovered.Operation.ID != interrupted.Instance.Operation.ID || recovered.Operation.Allocations[0].Phase != stage {
				t.Fatalf("lost interruption ownership: %+v", recovered)
			}
			data, err := json.Marshal(recovered)
			if err != nil {
				t.Fatal(err)
			}
			for _, secret := range []string{"resolved-fixture-secret", "seed-fixture-secret", "literal-fixture-secret"} {
				if strings.Contains(string(data), secret) {
					t.Fatal("public state leaks secret")
				}
			}
			if err := state.db.View(func(tx *bolt.Tx) error {
				snapshot := tx.Bucket([]byte("snapshots")).Get([]byte(recovered.ID))
				secrets := tx.Bucket([]byte("secrets")).Get([]byte(recovered.ID))
				if stage == "intent" {
					if snapshot != nil || secrets != nil {
						t.Fatal("intent falsely reports effect")
					}
					return nil
				}
				var private plan.Snapshot
				if err := json.Unmarshal(snapshot, &private); err != nil {
					return err
				}
				if private.Manifest.Project != "fixture" || private.Manifest.Components["check"].Environment.Assign["LITERAL_SECRET"].Literal == nil {
					t.Fatal("private validated manifest missing")
				}
				for _, secret := range []string{"resolved-fixture-secret", "seed-fixture-secret", "literal-fixture-secret"} {
					if !strings.Contains(string(secrets), secret) {
						t.Fatalf("protected resolved value missing: %s", secret)
					}
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			repeated, created, err := state.begin(plan.Snapshot{Plan: recovered.Plan}, recovered.Checkout, time.Second)
			if err != nil || created || repeated.Instance.ID != recovered.ID {
				t.Fatal("recovery auto-adopted or discarded old record")
			}
		})
	}
}

func TestMovedAndReplacedCheckoutNeverAdopted(t *testing.T) {
	parent := shortTemp(t)
	root := filepath.Join(parent, "checkout")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	config := writeFixture(t, root)
	cli, _ := startFixture(t, filepath.Join(shortTemp(t), "state"), time.Second)
	original := prepareFixture(t, cli, request(root, config, "dev"))
	moved := filepath.Join(parent, "moved")
	if err := os.Rename(root, moved); err != nil {
		t.Fatal(err)
	}
	_, err := cli.Prepare(context.Background(), request(moved, config, "dev"))
	code(t, err, "checkout_moved")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	writeFixture(t, root)
	_, err = cli.Prepare(context.Background(), request(root, config, "dev"))
	code(t, err, "checkout_replaced")
	inspected, err := cli.Inspect(context.Background(), original.Instance.ID)
	if err != nil || inspected.Instance.ID != original.Instance.ID || inspected.Instance.Checkout.Path != original.Instance.Checkout.Path {
		t.Fatal("old record mutated")
	}
}

func TestStateVersionRejectedWithoutMutation(t *testing.T) {
	dir := shortTemp(t)
	state, err := openStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := state.db.Update(func(tx *bolt.Tx) error { return tx.Bucket([]byte("meta")).Put([]byte("version"), []byte("999")) }); err != nil {
		t.Fatal(err)
	}
	if err := state.db.Close(); err != nil {
		t.Fatal(err)
	}
	_, err = openStore(dir)
	code(t, err, "state_version")
	db, err := bolt.Open(filepath.Join(dir, "state.db"), 0600, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	}()
	if err := db.View(func(tx *bolt.Tx) error {
		if string(tx.Bucket([]byte("meta")).Get([]byte("version"))) != "999" {
			t.Fatal("unsupported version mutated")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestSingleDaemonAndStaleSocketSafety(t *testing.T) {
	dir := filepath.Join(shortTemp(t), "state")
	_, stop := startFixture(t, dir, time.Second)
	err := Serve(context.Background(), Options{Directory: dir})
	code(t, err, "already_running")
	stop()
	stale, err := net.ListenUnix("unix", &net.UnixAddr{Name: SocketPath(dir), Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	stale.SetUnlinkOnClose(false)
	if err := os.Chmod(SocketPath(dir), 0600); err != nil {
		t.Fatal(err)
	}
	if err := stale.Close(); err != nil {
		t.Fatal(err)
	}
	_, stop = startFixture(t, dir, time.Second)
	stop()
	live, err := net.ListenUnix("unix", &net.UnixAddr{Name: SocketPath(dir), Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := live.Close(); err != nil {
			t.Error(err)
		}
	}()
	if err := os.Chmod(SocketPath(dir), 0600); err != nil {
		t.Fatal(err)
	}
	err = Serve(context.Background(), Options{Directory: dir})
	code(t, err, "socket_in_use")
	if _, err := os.Lstat(SocketPath(dir)); err != nil {
		t.Fatal("unrelated live listener removed")
	}
}

func TestPermissionsAndDoctorProviderIndependence(t *testing.T) {
	dir := filepath.Join(shortTemp(t), "unsafe")
	if err := os.Mkdir(dir, 0755); err != nil {
		t.Fatal(err)
	}
	code(t, Serve(context.Background(), Options{Directory: dir}), "permissions")
	private := filepath.Join(shortTemp(t), "state")
	cli, stop := startFixture(t, private, time.Second)
	root := shortTemp(t)
	config := writeFixture(t, root)
	prepared := prepareFixture(t, cli, request(root, config, "test"))
	if prepared.Instance.Status != v1.Prepared {
		t.Fatal("preparation failed")
	}
	doctor := Doctor(context.Background(), private)
	if !doctor.Healthy || doctor.Checks[len(doctor.Checks)-1].Status != "deferred" {
		t.Fatalf("doctor required providers: %+v", doctor)
	}
	for _, name := range []string{"state.db", "daemon.lock", "daemon.sock"} {
		info, err := os.Lstat(filepath.Join(private, name))
		if err != nil || info.Mode().Perm()&0077 != 0 {
			t.Fatalf("unsafe %s: %v", name, err)
		}
	}
	stop()
	if err := os.Chmod(filepath.Join(private, "state.db"), 0644); err != nil {
		t.Fatal(err)
	}
	code(t, Serve(context.Background(), Options{Directory: private}), "permissions")
	if err := os.Chmod(filepath.Join(private, "state.db"), 0600); err != nil {
		t.Fatal(err)
	}
	// A foreign regular file or symlink at the socket path is never removed.
	if err := os.WriteFile(SocketPath(private), []byte("preserve"), 0600); err != nil {
		t.Fatal(err)
	}
	code(t, Serve(context.Background(), Options{Directory: private}), "permissions")
	data, err := os.ReadFile(SocketPath(private))
	if err != nil || string(data) != "preserve" {
		t.Fatal("foreign path removed")
	}
}

func TestContainerPublishedPreparationRequiresNoProviders(t *testing.T) {
	root := shortTemp(t)
	config := writeFixture(t, root)
	manifest := `{"version":"backlot/v1","project":"fixture","inputs":{"password":{"secret":true}},"resources":{"port":{"kind":"port"},"origin":{"kind":"origin"}},"components":{"web":{"kind":"service","runtime":"container","image":{"ref":{"kind":"input","name":"password"}},"resources":["port","origin"],"ports":{"http":{"resource":"port","container_port":8080}},"readiness":{"kind":"tcp","target":{"literal":"127.0.0.1:1234","secret":true},"timeout":"1s"}}},"scenes":{"dev":{"lifetime":"persistent","components":["web"],"resources":["port","origin"],"publish":{"resource":"origin","routes":[{"path":"/","service":"web","port":"http","prefix":"preserve"}],"probe":{"kind":"http","target":{"ref":{"kind":"resource","name":"origin","field":"url"}},"timeout":"1s"}}}}}`
	if err := os.WriteFile(filepath.Join(root, "backlot.json"), []byte(manifest), 0600); err != nil {
		t.Fatal(err)
	}
	dir := shortTemp(t)
	cli, stop := startFixture(t, dir, time.Second)
	prepared := prepareFixture(t, cli, request(root, config, "dev"))
	if prepared.Instance.Status != v1.Prepared || prepared.Instance.Plan.Publish == nil || !prepared.Instance.Plan.Components[0].Image.Redacted || !prepared.Instance.Plan.Components[0].Readiness.Target.Redacted {
		t.Fatal("missing redacted metadata plan")
	}
	if !Doctor(context.Background(), dir).Healthy {
		t.Fatal("doctor required absent providers")
	}
	stop()
	state, err := openStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := state.db.Close(); err != nil {
			t.Error(err)
		}
	}()
	if err := state.db.View(func(tx *bolt.Tx) error {
		var secrets map[string]v1.PlannedValue
		if err := json.Unmarshal(tx.Bucket([]byte("secrets")).Get([]byte(prepared.Instance.ID)), &secrets); err != nil {
			return err
		}
		if secrets["web/image"].Literal == nil || *secrets["web/image"].Literal != "resolved-fixture-secret" || secrets["web/readiness"].Literal == nil || *secrets["web/readiness"].Literal != "127.0.0.1:1234" {
			t.Fatal("protected image/probe resolutions were lost")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestInFlightReplacementRejectsSnapshotBeforeDurableAcceptance(t *testing.T) {
	for _, nested := range []bool{false, true} {
		name := "checkout"
		if nested {
			name = "project_inside_git_checkout"
		}
		t.Run(name, func(t *testing.T) {
			checkout := shortTemp(t)
			project := checkout
			if nested {
				git(t, checkout, "init", "-q")
				project = filepath.Join(checkout, "project")
				if err := os.Mkdir(project, 0700); err != nil {
					t.Fatal(err)
				}
			}
			config := writeFixture(t, project)
			alias := filepath.Join(shortTemp(t), "alias")
			if err := os.Symlink(project, alias); err != nil {
				t.Fatal(err)
			}
			state, err := openStore(shortTemp(t))
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := state.db.Close(); err != nil {
					t.Error(err)
				}
			}()
			moved := filepath.Join(shortTemp(t), "original")
			checkpointReached := false
			svc := &service{store: state, lease: time.Second, resolve: func(source plan.Source, req v1.PlanRequest) (plan.Snapshot, error) {
				// Resolve real input bytes, then replace the source before returning control
				// to the coordinator. Capturing identity after resolution must fail this
				// test: it would observe the replacement and accept the original snapshot.
				snapshot, err := plan.Prepare(source, req)
				if err != nil {
					return snapshot, err
				}
				checkpointReached = true
				// A nested project replacement leaves checkout/Git identity unchanged.
				if err := os.Rename(project, moved); err != nil {
					return snapshot, err
				}
				if err := os.Mkdir(project, 0700); err != nil {
					return snapshot, err
				}
				changed := strings.Replace(fixtureManifest, `"version"]`, `"env"]`, 1)
				if err := os.WriteFile(filepath.Join(project, "backlot.json"), []byte(changed), 0600); err != nil {
					return snapshot, err
				}
				if err := os.WriteFile(filepath.Join(project, "seed.env"), []byte("SEED=replacement-fixture-secret\n"), 0600); err != nil {
					return snapshot, err
				}
				return snapshot, nil
			}}
			_, err = svc.prepare(context.Background(), request(alias, config, "dev"))
			code(t, err, "checkout_changed")
			if !checkpointReached {
				t.Fatal("controlled replacement did not run")
			}
			if err := state.db.View(func(tx *bolt.Tx) error {
				for _, name := range []string{"instances", "checkouts", "paths", "persistent", "snapshots", "secrets"} {
					if tx.Bucket([]byte(name)).Stats().KeyN != 0 {
						t.Fatalf("changed source durably accepted into %s", name)
					}
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			// A separate preparation from the now-stable replacement can be recorded,
			// and must contain only the replacement's own resolved snapshot.
			svc.resolve = nil
			prepared, err := svc.prepare(context.Background(), request(alias, config, "dev"))
			if err != nil {
				t.Fatal(err)
			}
			current, err := identify(prepared.Instance.Plan.Checkout)
			if err != nil {
				t.Fatal(err)
			}
			if prepared.Instance.Checkout.ID != current.ID || prepared.Instance.Plan.Components[0].Command.Args[0] != "env" {
				t.Fatal("replacement adopted the original snapshot")
			}
		})
	}
}
