//go:build darwin || linux

package daemon

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	v1 "github.com/octacian/backlot/api/v1"
	"github.com/octacian/backlot/internal/client"
	"github.com/octacian/backlot/internal/native"
	bolt "go.etcd.io/bbolt"
)

// historyClient exercises the strict public readers without another recovery
// pass changing the persisted result under test. Shutdown joins the HTTP owner.
func historyClient(t *testing.T, s *service) *client.Client {
	t.Helper()
	socket := filepath.Join(shortTemp(t), "api.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(socket, 0600); err != nil {
		t.Fatal(err)
	}
	s.generation = newID()
	server := &http.Server{Handler: handler(s, v1.DaemonStatusResponse{APIVersion: v1.Version, StateVersion: v1.StateVersion, Status: "running"}, func() {}), ReadHeaderTimeout: time.Second}
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	cli := client.New(socket)
	t.Cleanup(func() {
		cli.Close()
		_ = server.Close()
		if err := <-done; err != http.ErrServerClosed {
			t.Error(err)
		}
	})
	return cli
}

func reopenHistoryStore(t *testing.T, s *service) {
	t.Helper()
	if err := s.store.db.Close(); err != nil {
		t.Fatal(err)
	}
	next, err := openStore(s.directory)
	if err != nil {
		t.Fatal(err)
	}
	s.store.db = next.db
}

func historyRecord(t *testing.T, s *store, id string) record {
	t.Helper()
	var r record
	if err := s.db.View(func(tx *bolt.Tx) error { var err error; r, err = load(tx, id); return err }); err != nil {
		t.Fatal(err)
	}
	return r
}

func TestNativeWaiverRestoredReceiptRetainsStickyHistory(t *testing.T) {
	for _, confidence := range []v1.NativeCleanupConfidence{v1.CleanupIncomplete, v1.CleanupAuthorityLost} {
		t.Run(string(confidence), func(t *testing.T) {
			s, instance, request := exportFixture(t)
			original := *instance.Execution
			if err := s.store.db.Update(func(tx *bolt.Tx) error {
				r, err := load(tx, instance.ID)
				if err != nil {
					return err
				}
				r.Instance.Plan.Lifetime = v1.Persistent
				return put(tx, "instances", instance.ID, r)
			}); err != nil {
				t.Fatal(err)
			}
			original.TerminalExitCode = new(17)
			original.CollectionFailure = "original collection gap"
			original.CleanupFailure = "original session fence"
			original.NativeCleanup = []v1.NativeCleanupResult{{Component: "test", Attempt: original.Attempt, Confidence: confidence, Reason: "cleanup_incomplete", Detail: "Historical session observation."}}
			if confidence == v1.CleanupAuthorityLost {
				original.NativeCleanup[0].Reason = "guardian_authority_lost"
			}
			if _, err := s.store.execution(instance.ID, v1.Failed, &original); err != nil {
				t.Fatal(err)
			}
			// Synthetic receipt for a deliberately different birth of this owned test
			// process: Stop verifies the exact receipt and birth mismatch, never signals.
			dir := shortTemp(t)
			boot, err := native.CurrentBoot()
			if err != nil {
				t.Fatal(err)
			}
			identity := native.Identity{Protocol: 1, Purpose: "application", Component: "test", Attempt: original.Attempt, PID: os.Getpid(), Birth: "synthetic-prior-guardian", Boot: boot, Token: strings.Repeat("a", 64), Control: filepath.Join(dir, "control.sock")}
			observed := v1.NativeCleanupResult{Component: "test", Attempt: original.Attempt, Confidence: v1.ObservedEmpty, Reason: "session_observed_empty", Detail: "Two empty observations with residual risk."}
			receipt, err := json.Marshal(struct {
				Identity native.Identity
				Result   *v1.NativeCleanupResult
				Error    string
			}{identity, &observed, ""})
			if err != nil {
				t.Fatal(err)
			}
			receiptPath := filepath.Join(dir, "receipt.json")
			if err := os.WriteFile(receiptPath, receipt, 0600); err != nil {
				t.Fatal(err)
			}
			if err := native.Stop(identity, 0); err != nil {
				t.Fatal("restored receipt fixture", err)
			}
			if err := s.store.saveRuntime(instance.ID, runtimeRecord{Attempt: original.Attempt, Groups: []native.Identity{identity}}); err != nil {
				t.Fatal(err)
			}
			cli := historyClient(t, s)
			force := v1.StopRequest{APIVersion: v1.Version, InstanceID: instance.ID, Force: true, ExpectedAttempt: original.Attempt}
			forced, err := cli.StopExecutionWithRequest(context.Background(), force)
			if err != nil {
				t.Fatal("force response rejected by strict client", err)
			}
			if len(forced.Instance.NativeOwnershipWarnings) != 1 || forced.Instance.NativeOwnershipWarnings[0].Attempt != original.Attempt {
				t.Fatal("missing exact-attempt acknowledgement")
			}
			if forced.Instance.Execution.CollectionFailure != original.CollectionFailure || !reflect.DeepEqual(forced.Instance.Execution.NativeCleanup, original.NativeCleanup) || *forced.Instance.Execution.TerminalExitCode != 17 {
				t.Fatal("force rewrote original facts")
			}
			audit := historyRecord(t, s.store, instance.ID).NativeAbandonments
			if len(audit) != 1 || audit[0].Result.CleanupFailure == "" || audit[0].Result.CollectionFailure != original.CollectionFailure || *audit[0].Result.TerminalExitCode != 17 || !reflect.DeepEqual(audit[0].Result.NativeCleanup, original.NativeCleanup) || audit[0].Pending || !reflect.DeepEqual(audit[0].Groups, []native.Identity{identity}) {
				t.Fatal("original result or independent receipt proof lost")
			}
			if _, err := cli.StopExecutionWithRequest(context.Background(), force); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(historyRecord(t, s.store, instance.ID).NativeAbandonments, audit) {
				t.Fatal("retry changed audit")
			}
			reopenHistoryStore(t, s)
			if _, err := cli.Inspect(context.Background(), instance.ID); err != nil {
				t.Fatal("reopened status", err)
			}
			request.AllowPartial = true
			exported, err := cli.Export(context.Background(), request)
			if err != nil || exported.Complete || !exported.Published || !reflect.DeepEqual(exported.Result.NativeCleanup, original.NativeCleanup) {
				t.Fatal("partial export", err)
			}
			if err := s.store.guardAbandonedData(instance.ID); err != nil {
				t.Fatal("proven receipt incorrectly requires reboot", err)
			}
			if err := os.Rename(receiptPath, receiptPath+".retained"); err != nil {
				t.Fatal(err)
			}
			code(t, s.store.guardAbandonedData(instance.ID), "native_ownership_relinquished")
			if err := os.Rename(receiptPath+".retained", receiptPath); err != nil {
				t.Fatal(err)
			}
			if err := s.store.removeResources(context.Background(), instance.ID); err != nil {
				t.Fatal("independent data proof", err)
			}
			next := &v1.ExecutionResult{Attempt: newID(), TerminalExitCode: new(0), Components: []v1.ComponentResult{{Name: "test", Status: "completed", ExitCode: new(0)}}}
			if _, err := s.store.acceptExecution(instance.ID, runtimeRecord{Attempt: next.Attempt}, next, true); err != nil {
				t.Fatal("explicit replacement", err)
			}
			if _, err := s.store.execution(instance.ID, v1.Succeeded, next); err != nil {
				t.Fatal(err)
			}
			if _, err := cli.Inspect(context.Background(), instance.ID); err != nil {
				t.Fatal("replacement status", err)
			}
			if !reflect.DeepEqual(historyRecord(t, s.store, instance.ID).NativeAbandonments, audit) {
				t.Fatal("replacement or deletion changed original audit")
			}
		})
	}
}

func TestPersistedPreviousBootSessionRecovery(t *testing.T) {
	for _, kind := range []string{"earlier-boot", "mixed-current", "current", "missing-boot", "pending-missing", "pending-current", "mismatched-attempt"} {
		t.Run(kind, func(t *testing.T) {
			s, instance, request := exportFixture(t)
			boot, err := native.CurrentBoot()
			if err != nil {
				t.Fatal(err)
			}
			old := "00000000-0000-0000-0000-000000000001"
			if old == boot {
				old = "00000000-0000-0000-0000-000000000002"
			}
			original := *instance.Execution
			original.TerminalExitCode = new(17)
			original.CollectionFailure = "original interrupted output gap"
			original.CleanupFailure = "session observation incomplete"
			original.NativeCleanup = []v1.NativeCleanupResult{{Component: "test", Attempt: original.Attempt, Confidence: v1.CleanupIncomplete, Reason: "cleanup_incomplete", Detail: "Original incomplete observation."}}
			if _, err := s.store.execution(instance.ID, v1.RuntimeReady, &original); err != nil {
				t.Fatal(err)
			}
			// The old-boot authority is fully shaped but synthetic. An owned listener
			// detects accidental Control contact; earlier boot must avoid it altogether.
			dir := shortTemp(t)
			control := filepath.Join(dir, "control.sock")
			listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: control, Net: "unix"})
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = listener.Close() }()
			id := native.Identity{Protocol: 1, Purpose: "application", Component: "test", Attempt: original.Attempt, PID: os.Getpid(), Birth: "synthetic-earlier-boot", Boot: old, Token: strings.Repeat("b", 64), Control: control}
			journal := runtimeRecord{Attempt: original.Attempt, Groups: []native.Identity{id, id}, LaunchPending: true, PendingBoot: old}
			switch kind {
			case "mixed-current":
				journal.Groups[1].Boot = boot
				journal.Groups[1].Control = filepath.Join(dir, "unavailable.sock")
			case "current":
				for i := range journal.Groups {
					journal.Groups[i].Boot = boot
					journal.Groups[i].Control = filepath.Join(dir, "unavailable.sock")
				}
				journal.PendingBoot = boot
			case "missing-boot":
				journal.Groups[1].Boot = ""
				journal.Groups[1].Control = filepath.Join(dir, "unavailable.sock")
			case "pending-missing":
				journal.PendingBoot = ""
			case "pending-current":
				journal.PendingBoot = boot
			case "mismatched-attempt":
				journal.Attempt = newID()
			}
			if err := s.store.saveRuntime(instance.ID, journal); err != nil {
				t.Fatal(err)
			}
			prior := original
			prior.Attempt = newID()
			prior.NativeCleanup = append([]v1.NativeCleanupResult(nil), original.NativeCleanup...)
			prior.NativeCleanup[0].Attempt = prior.Attempt
			audit := []nativeAbandonment{{Attempt: prior.Attempt, RecordedAt: time.Now().UTC().Format(time.RFC3339Nano), Reason: nativeWaiverWarning, Boot: old, Result: &prior}}
			if err := s.store.db.Update(func(tx *bolt.Tx) error {
				r, err := load(tx, instance.ID)
				if err != nil {
					return err
				}
				r.NativeAbandonments = audit
				return put(tx, "instances", instance.ID, r)
			}); err != nil {
				t.Fatal(err)
			}
			if err := s.store.recoverExecution(instance.ID); err != nil {
				t.Fatal("integrated recovery", err)
			}
			if err := listener.SetDeadline(time.Now().Add(20 * time.Millisecond)); err != nil {
				t.Fatal(err)
			}
			connection, err := listener.AcceptUnix()
			if connection != nil {
				_ = connection.Close()
				t.Fatal("earlier-boot authority contacted Control")
			}
			if err == nil {
				t.Fatal("missing contact observation")
			}
			reopenHistoryStore(t, s)
			cli := historyClient(t, s)
			status, err := cli.Inspect(context.Background(), instance.ID)
			if err != nil {
				t.Fatal("reopened strict status", err)
			}
			got := status.Instance.Execution
			released := kind == "earlier-boot"
			want := original.NativeCleanup[0]
			if released {
				want.ResourceRelease = "previous_boot"
			}
			if len(got.NativeCleanup) != 1 || got.NativeCleanup[0] != want || got.NativeCleanup[0].FencesResources(status.Instance.NativeOwnershipWarnings) == released || (got.CleanupFailure == "") != released {
				t.Fatal("historical confidence/resource eligibility conflated", got)
			}
			if got.CollectionFailure != original.CollectionFailure || got.TerminalExitCode == nil || *got.TerminalExitCode != 17 {
				t.Fatal("recovery rewrote original exit/collection")
			}
			scene, err := cli.SceneStatus(context.Background(), v1.SceneRequest{APIVersion: v1.Version, Target: v1.SceneTarget{InstanceID: instance.ID}})
			if err != nil || scene.Result.Instance.Execution.NativeCleanup[0] != want {
				t.Fatal("strict scene response", err)
			}
			doctor, err := cli.Doctor(context.Background(), v1.DoctorRequest{APIVersion: v1.Version, InstanceID: instance.ID})
			if err != nil {
				t.Fatal("Doctor response", err)
			}
			found := false
			for _, check := range doctor.Checks {
				if check.Name == "native_cleanup" {
					found = true
					if check.Status != v1.DoctorFailed || strings.Contains(check.Message, "earlier-boot proof") != released {
						t.Fatal("Doctor invented cleanup confidence", check)
					}
				}
			}
			if !found {
				t.Fatal("Doctor omitted historical cleanup")
			}
			request.AllowPartial = true
			exported, err := cli.Export(context.Background(), request)
			if released {
				if err != nil || !exported.Published || exported.Complete || exported.Result.NativeCleanup[0] != want || exported.Result.CollectionFailure != original.CollectionFailure || *exported.Result.TerminalExitCode != 17 {
					t.Fatal("partial export lost historical facts", err)
				}
				report := readExportReport(t, request.Output)
				if report.Result.NativeCleanup[0] != want || report.Complete {
					t.Fatal("portable report changed confidence")
				}
			} else {
				code(t, err, "export_active")
			}
			if !reflect.DeepEqual(historyRecord(t, s.store, instance.ID).NativeAbandonments, audit) {
				t.Fatal("recovery mutated prior audit observation slice")
			}
			persisted, err := s.store.runtime(instance.ID)
			if err != nil {
				t.Fatal(err)
			}
			if released && (persisted.LaunchPending || persisted.PendingBoot != "") {
				t.Fatal("old intent not released")
			}
		})
	}
}
