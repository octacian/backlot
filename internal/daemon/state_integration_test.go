//go:build darwin || linux

package daemon

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	v1 "github.com/octacian/backlot/api/v1"
	"github.com/octacian/backlot/internal/native"
	bolt "go.etcd.io/bbolt"
)

func TestStatePreflightRejectsAuthorityBeforeFence(t *testing.T) {
	for _, version := range []int{9, v1.StateVersion} {
		for _, kind := range []string{"unknown protocol", "unknown nested protocol", "invalid copy", "unknown launch purpose"} {
			t.Run(fmt.Sprintf("v%d/%s", version, kind), func(t *testing.T) {
				dir := copyStateNine(t)
				db, err := bolt.Open(filepath.Join(dir, "state.db"), 0600, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer func() { _ = db.Close() }()
				state := &store{db: db, directory: dir}
				if version == v1.StateVersion {
					if err := db.Update(openStateTransaction); err != nil {
						t.Fatal(err)
					}
				}
				instances, err := state.list(v1.ListRequest{APIVersion: v1.Version})
				if err != nil || len(instances.Instances) == 0 {
					t.Fatal("missing retained instance", err)
				}
				id := instances.Instances[0].ID
				err = db.Update(func(tx *bolt.Tx) error {
					switch kind {
					case "unknown protocol":
						return tx.Bucket([]byte("runtime")).Put([]byte(id), []byte(`{"groups":[{"protocol":2,"purpose":"strict","pid":123,"birth":"x","control":"/must-not-contact","token":"`+strings.Repeat("a", 64)+`"}]}`))
					case "unknown nested protocol":
						var r map[string]any
						if err := json.Unmarshal(tx.Bucket([]byte("instances")).Get([]byte(id)), &r); err != nil {
							return err
						}
						r["native_abandonments"] = []any{map[string]any{"groups": []any{map[string]any{"protocol": 7, "purpose": "application"}}}}
						return put(tx, "instances", id, r)
					case "invalid copy":
						return tx.Bucket([]byte("meta")).Put([]byte("doctor_probe/"+newID()), []byte(`{"resource":{"kind":"container","token":"bad"},"ca":{"ready":true}}`))
					case "unknown launch purpose":
						return put(tx, "runtime", id, runtimeRecord{LaunchPurpose: "unknown"})
					}
					return nil
				})
				if err != nil {
					t.Fatal(err)
				}
				before := stateContents(t, state)
				if err := state.db.Update(openStateTransaction); err == nil {
					t.Fatal("unsafe authority accepted")
				} else if !strings.Contains(err.Error(), "matching binary") {
					t.Fatal("missing preservation/lifecycle guidance", err)
				}
				if !reflect.DeepEqual(before, stateContents(t, state)) {
					t.Fatal("failed validation changed records")
				}
			})
		}
	}
}

func TestStateStartupFailurePreservesCommittedState(t *testing.T) {
	svc, _, _ := exportFixture(t)
	before := stateContents(t, svc.store)
	if err := svc.store.db.Close(); err != nil {
		t.Fatal(err)
	}
	socket := SocketPath(svc.directory)
	if err := os.WriteFile(socket, []byte("retained non-socket obstruction"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := Serve(context.Background(), Options{Directory: svc.directory}); err == nil {
		t.Fatal("startup obstruction ignored")
	}
	s, err := openStore(svc.directory)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.db.Close() }()
	if !reflect.DeepEqual(before, stateContents(t, s)) {
		t.Fatal("postcommit startup failure rewrote records")
	}
	if data, err := os.ReadFile(socket); err != nil || string(data) != "retained non-socket obstruction" {
		t.Fatal("startup obstruction changed", err)
	}
}

func TestPreviousBootReleaseRequiresEveryLaunchObligation(t *testing.T) {
	boot, err := native.CurrentBoot()
	if err != nil {
		t.Fatal(err)
	}
	old := "00000000-0000-0000-0000-000000000001"
	if old == boot {
		old = "00000000-0000-0000-0000-000000000002"
	}
	attempt := newID()
	r := runtimeRecord{Attempt: attempt, Groups: []native.Identity{{Boot: old}, {Boot: old}}, LaunchPending: true, PendingBoot: old}
	if !allNativePreviousBoot(r, attempt) {
		t.Fatal("complete old-boot obligations fenced")
	}
	for _, change := range []func(*runtimeRecord){func(r *runtimeRecord) { r.PendingBoot = "" }, func(r *runtimeRecord) { r.Groups[1].Boot = "" }, func(r *runtimeRecord) { r.Groups[1].Boot = boot }, func(r *runtimeRecord) { r.Attempt = newID() }} {
		next := r
		next.Groups = append([]native.Identity(nil), r.Groups...)
		change(&next)
		if allNativePreviousBoot(next, attempt) {
			t.Fatal("partial/missing/current/mismatched proof released aggregate")
		}
	}
	observation := v1.NativeCleanupResult{Component: "api", Attempt: attempt, Confidence: v1.CleanupAuthorityLost, Reason: "guardian_authority_lost", Detail: "Historical authority lost."}
	result := v1.ExecutionResult{Attempt: attempt, NativeCleanup: []v1.NativeCleanupResult{observation}}
	copy := result
	copy.NativeCleanup = append([]v1.NativeCleanupResult(nil), result.NativeCleanup...)
	copy.NativeCleanup[0].ResourceRelease = "previous_boot"
	if result.NativeCleanup[0] != observation || copy.NativeCleanup[0].Validate() != nil || copy.NativeCleanup[0].FencesResources(nil) {
		t.Fatal("release rewrote history or retained active fence")
	}
	stale := []v1.NativeOwnershipWarning{{Attempt: newID(), RecordedAt: "known"}}
	if !observation.FencesResources(stale) {
		t.Fatal("old waiver released new attempt")
	}
}

func TestRejectedStatePreservesPhysicalBytes(t *testing.T) {
	svc, _, _ := exportFixture(t)
	dir := svc.directory
	if err := svc.store.db.Update(func(tx *bolt.Tx) error { return tx.Bucket([]byte("runtime")).Put([]byte(newID()), []byte(`null`)) }); err != nil {
		t.Fatal(err)
	}
	if err := svc.store.db.Close(); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "state.db")
	before, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := openStore(dir); err == nil {
		t.Fatal("corrupt state accepted")
	}
	after, err := os.ReadFile(p)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("rejected DB changed bytes", err)
	}
}

// TestStateFenceInterruptionChild exposes a transaction boundary in an owned test
// process, without adding a production crash hook or migration lifecycle.
func TestStateFenceInterruptionChild(t *testing.T) {
	dir := os.Getenv("BACKLOT_STATE_FENCE_CHILD")
	if dir == "" {
		return
	}
	stage := os.Getenv("BACKLOT_STATE_FENCE_STAGE")
	pause := func() { fmt.Println("boundary"); var b [1]byte; _, _ = os.Stdin.Read(b[:]) }
	db, err := bolt.Open(filepath.Join(dir, "state.db"), 0600, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if err := db.Update(func(tx *bolt.Tx) error {
		if err := openStateTransaction(tx); err != nil {
			return err
		}
		if err := tx.Bucket([]byte("meta")).Put([]byte("interruption"), []byte("committed")); err != nil {
			return err
		}
		if stage == "before_commit" {
			pause()
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if stage == "after_commit" {
		pause()
	}
}

func TestStateFenceInterruptedBeforeAndAfterCommit(t *testing.T) {
	for _, stage := range []string{"before_commit", "after_commit"} {
		t.Run(stage, func(t *testing.T) {
			svc, _, _ := exportFixture(t)
			dir := svc.directory
			before := stateContents(t, svc.store)
			if err := svc.store.db.Close(); err != nil {
				t.Fatal(err)
			}
			physical, err := os.ReadFile(filepath.Join(dir, "state.db"))
			if err != nil {
				t.Fatal(err)
			}
			self, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			child := exec.Command(self, "-test.run=^TestStateFenceInterruptionChild$")
			child.Env = append(os.Environ(), "BACKLOT_STATE_FENCE_CHILD="+dir, "BACKLOT_STATE_FENCE_STAGE="+stage)
			in, err := child.StdinPipe()
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = in.Close() }()
			out, err := child.StdoutPipe()
			if err != nil {
				t.Fatal(err)
			}
			child.Stderr = os.Stderr
			waited := false
			t.Cleanup(func() {
				if !waited && child.Process != nil {
					_ = child.Process.Kill()
					_ = child.Wait()
				}
			})
			if err := child.Start(); err != nil {
				t.Fatal(err)
			}
			boundary := make(chan bool, 1)
			go func() { scanner := bufio.NewScanner(out); boundary <- scanner.Scan() && scanner.Text() == "boundary" }()
			select {
			case ok := <-boundary:
				if !ok {
					t.Fatal("missing controlled child boundary")
				}
			case <-time.After(5 * time.Second):
				t.Fatal("child boundary timed out")
			}
			if err := child.Process.Kill(); err != nil {
				t.Fatal(err)
			}
			if err := child.Wait(); err == nil {
				t.Fatal("fault did not interrupt child")
			}
			waited = true
			if stage == "before_commit" {
				after, err := os.ReadFile(filepath.Join(dir, "state.db"))
				if err != nil || !bytes.Equal(physical, after) {
					t.Fatal("precommit interruption changed database", err)
				}
			} else {
				before["meta"]["interruption"] = "committed"
			}
			db, err := bolt.Open(filepath.Join(dir, "state.db"), 0600, &bolt.Options{ReadOnly: true})
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(before, stateContents(t, &store{db: db})) {
				t.Fatal("interruption left partial fence or changed record")
			}
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestCanceledStartupPreservesState(t *testing.T) {
	svc, _, _ := exportFixture(t)
	dir := svc.directory
	if err := svc.store.db.Close(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "state.db")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := Serve(ctx, Options{Directory: dir}); !errors.Is(err, context.Canceled) {
		t.Fatal("startup context ignored", err)
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("canceled startup changed state", err)
	}
	if _, err := os.Lstat(SocketPath(dir)); !os.IsNotExist(err) {
		t.Fatal("canceled startup allocated listener", err)
	}
}

func TestStateUncertainEffectRemainsDiagnosable(t *testing.T) {
	svc, instance, _ := exportFixture(t)
	result := *instance.Execution
	result.CleanupFailure = "native effect could not be authenticated"
	if _, err := svc.store.execution(instance.ID, v1.Failed, &result); err != nil {
		t.Fatal(err)
	}
	journal := runtimeRecord{Attempt: instance.Execution.Attempt, Groups: []native.Identity{{PID: 42, Control: filepath.Join(svc.directory, "missing.sock"), Token: newID(), Boot: "historically-unverifiable"}}}
	if err := svc.store.saveRuntime(instance.ID, journal); err != nil {
		t.Fatal(err)
	}
	before := stateContents(t, svc.store)
	if err := svc.store.db.Update(openStateTransaction); err != nil {
		t.Fatal("recorded uncertainty blocked state open", err)
	}
	if !reflect.DeepEqual(before, stateContents(t, svc.store)) {
		t.Fatal("uncertain effect was normalized or lost")
	}
	if err := svc.store.recoverExecution(instance.ID); err != nil {
		t.Fatal(err)
	}
	got, err := svc.store.inspect(instance.ID)
	if err != nil || got.Execution.CleanupFailure == "" || got.Execution.CollectionFailure == "" || len(got.Execution.NativeCleanup) != 0 {
		t.Fatal("missing recorded authority fabricated release/collection/session observation", err)
	}
}
