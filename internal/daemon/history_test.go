//go:build darwin || linux

package daemon

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	v1 "github.com/octacian/backlot/api/v1"
	bolt "go.etcd.io/bbolt"
)

func historyFixture(t *testing.T) (*service, string) {
	t.Helper()
	s, root, config := sceneFixture(t)
	s.directory = s.store.directory
	return s, scenePrepare(t, s, root, config).Instance.ID
}

func acceptHistory(t *testing.T, s *service, id string, status v1.InstanceStatus, age time.Duration) v1.Attempt {
	t.Helper()
	i, err := s.store.inspect(id)
	if err != nil {
		t.Fatal(err)
	}
	e := &v1.ExecutionResult{Attempt: newID(), Components: []v1.ComponentResult{{Name: "app", Status: "completed"}}}
	if age >= 0 {
		e.CompletedAt = time.Now().Add(-age).UTC().Format(time.RFC3339Nano)
	}
	source := attemptSource(i)
	source.GitHead = strings.Repeat("a", 40)
	journal := runtimeRecord{Attempt: e.Attempt, ExportSource: &source, StartedAt: time.Now().UTC().Format(time.RFC3339Nano), ConfigPath: "private-config-sentinel"}
	if _, err := s.store.acceptExecution(id, journal, e, i.Status != v1.Prepared); err != nil {
		t.Fatal(err)
	}
	if _, err := s.store.execution(id, status, e); err != nil {
		t.Fatal(err)
	}
	got, err := s.store.attempt(v1.AttemptRequest{InstanceID: id, Attempt: e.Attempt})
	if err != nil {
		t.Fatal(err)
	}
	return got.Attempt
}

func writeAttemptLog(t *testing.T, s *service, a v1.Attempt, legacy bool) {
	t.Helper()
	dir := filepath.Join(s.directory, "logs")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	name := a.InstanceID + "-" + a.Result.Attempt + "-output.ndjson"
	if legacy {
		name = a.InstanceID + "-output.ndjson"
	}
	f, err := os.OpenFile(filepath.Join(dir, name), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	r := v1.LogRecord{InstanceID: a.InstanceID, Attempt: a.Result.Attempt, Component: "app", Time: time.Now().UTC().Format(time.RFC3339Nano), Stream: "stdout", Message: "attempt-output"}
	if err := json.NewEncoder(f).Encode(r); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestAttemptWritersProjectionAndReplacement(t *testing.T) {
	s, id := historyFixture(t)
	first := acceptHistory(t, s, id, v1.Failed, time.Hour)
	// The shared write boundary covers image-pin/waiver/resource and legacy writers
	// which deliberately do not call execution(). Fixture values acquire no history.
	if err := s.store.db.Update(func(tx *bolt.Tx) error {
		r, err := load(tx, id)
		if err != nil {
			return err
		}
		r.Instance.Execution.Failure = "original-failure"
		r.Instance.Execution.Fixtures = []v1.FixtureRecord{{Name: "private-fixture", Value: "fixture-sentinel"}}
		return put(tx, "instances", id, r)
	}); err != nil {
		t.Fatal(err)
	}
	next := acceptHistory(t, s, id, v1.RuntimeReady, -1)
	selected, err := s.store.attempt(v1.AttemptRequest{InstanceID: id, LastCompleted: true})
	if err != nil {
		t.Fatal(err)
	}
	if selected.Attempt.Result.Attempt != first.Result.Attempt || selected.Attempt.Result.Failure != "original-failure" || selected.Attempt.Source.GitHead != strings.Repeat("a", 40) {
		t.Fatal("replacement lost public results or provenance")
	}
	data, _ := json.Marshal(selected)
	for _, private := range []string{"fixture-sentinel", "private-config-sentinel", "groups", "lease_hash", "ownership_token"} {
		if bytes.Contains(data, []byte(private)) {
			t.Fatalf("history leaked %s", private)
		}
	}
	if next.Sequence != first.Sequence+1 {
		t.Fatal("acceptance sequence not monotonic")
	}
	if _, err := s.store.execution(id, v1.Failed, &first.Result); err == nil {
		t.Fatal("stale writer replaced successor")
	}
	current, _ := s.store.inspect(id)
	if current.Execution.Attempt != next.Result.Attempt {
		t.Fatal("current-state truth changed")
	}
	page, err := s.store.history(v1.HistoryRequest{InstanceID: id, Limit: 1})
	if err != nil || len(page.Attempts) != 1 || page.Attempts[0].Attempt != next.Result.Attempt || page.Continuation == "" {
		t.Fatal("first page", page, err)
	}
	tail, err := s.store.history(v1.HistoryRequest{InstanceID: id, After: page.Continuation})
	if err != nil || len(tail.Attempts) != 1 || tail.Attempts[0].Attempt != first.Result.Attempt || tail.Continuation != "" {
		t.Fatal("continuation", tail, err)
	}
	_, err = s.store.history(v1.HistoryRequest{InstanceID: newID(), After: page.Continuation})
	code(t, err, "invalid_cursor")
	_, err = s.store.attempt(v1.AttemptRequest{InstanceID: id, Attempt: newID()})
	code(t, err, "attempt_unavailable")
}

func TestHistoryPruneUnionPreviewAndNoResurrection(t *testing.T) {
	s, id := historyFixture(t)
	old := acceptHistory(t, s, id, v1.Failed, 4*time.Hour)
	writeAttemptLog(t, s, old, true)
	recent := acceptHistory(t, s, id, v1.Stopped, time.Hour)
	writeAttemptLog(t, s, recent, true)
	latest := acceptHistory(t, s, id, v1.Stopped, 0)
	writeAttemptLog(t, s, latest, false)
	sentinel := filepath.Join(s.directory, "application-data")
	if err := os.WriteFile(sentinel, []byte("keep-data"), 0600); err != nil {
		t.Fatal(err)
	}
	snapshots := storedBytes(t, s.store, "snapshots", id)
	secrets := storedBytes(t, s.store, "secrets", id)
	keep := 1
	q := v1.HistoryPruneRequest{APIVersion: v1.Version, InstanceID: id, OlderThan: "2h", Keep: &keep, DryRun: true}
	preview, err := s.pruneHistory(context.Background(), q)
	if err != nil || len(preview.Attempts) != 2 {
		t.Fatal(preview, err)
	}
	entries, _ := s.store.attempts(id)
	if len(entries) != 3 {
		t.Fatal("preview mutated history")
	}
	q.DryRun = false
	applied, err := s.pruneHistory(context.Background(), q)
	if err != nil || len(applied.Attempts) != 2 {
		t.Fatal(applied, err)
	}
	for _, a := range []v1.Attempt{old, recent} {
		_, err := s.store.attempt(v1.AttemptRequest{InstanceID: id, Attempt: a.Result.Attempt})
		code(t, err, "attempt_unavailable")
	}
	legacy, err := os.ReadFile(filepath.Join(s.directory, "logs", id+"-output.ndjson"))
	if err != nil || len(legacy) != 0 {
		t.Fatal("legacy records retained", err)
	}
	// Pruning current history preserves current inspect, and later writes do not
	// reconstruct the deliberately removed row from that remaining snapshot.
	keep = 0
	if _, err := s.pruneHistory(context.Background(), q); err != nil {
		t.Fatal(err)
	}
	current, _ := s.store.inspect(id)
	if current.Execution == nil || current.Execution.Attempt != latest.Result.Attempt || !current.Execution.EvidenceExpired {
		t.Fatal("prune changed current result identity")
	}
	if _, err := s.store.execution(id, v1.Stopped, &latest.Result); err != nil {
		t.Fatal(err)
	}
	entries, _ = s.store.attempts(id)
	if len(entries) != 0 {
		t.Fatal("pruned current row resurrected")
	}
	successor := acceptHistory(t, s, id, v1.Stopped, 0)
	if successor.Sequence <= latest.Sequence {
		t.Fatal("pruning reset acceptance sequence")
	}
	if !bytes.Equal(snapshots, storedBytes(t, s.store, "snapshots", id)) || !bytes.Equal(secrets, storedBytes(t, s.store, "secrets", id)) {
		t.Fatal("prune removed private configuration or credentials")
	}
	if data, err := os.ReadFile(sentinel); err != nil || string(data) != "keep-data" {
		t.Fatal("prune removed application data", err)
	}
}

func TestHistoryProtectionAndAutomaticLimits(t *testing.T) {
	s, id := historyFixture(t)
	s.evidence.attempts = 2
	old := acceptHistory(t, s, id, v1.Failed, 200*time.Hour)
	writeAttemptLog(t, s, old, false)
	protected := acceptHistory(t, s, id, v1.Failed, time.Hour)
	protected.Result.Kept = true
	protected.Result.CleanupFailure = "unresolved"
	if _, err := s.store.execution(id, v1.Failed, &protected.Result); err != nil {
		t.Fatal(err)
	}
	// Acceptance here models a later cleanup-authorized replacement; the history
	// obligation itself remains protected until its own observation is resolved.
	newest := acceptHistory(t, s, id, v1.Stopped, 0)
	writeAttemptLog(t, s, newest, false)
	activeAttempt := acceptHistory(t, s, id, v1.RuntimeReady, -1)
	writeAttemptLog(t, s, activeAttempt, false)
	if err := s.pruneEvidence(); err != nil {
		t.Fatal(err)
	}
	_, err := s.store.attempt(v1.AttemptRequest{InstanceID: id, Attempt: old.Result.Attempt})
	code(t, err, "attempt_unavailable")
	entries, _ := s.store.attempts(id)
	if len(entries) != 3 {
		t.Fatal("count removed protected work", len(entries))
	}
	keep := 0
	preview, err := s.pruneHistory(context.Background(), v1.HistoryPruneRequest{APIVersion: v1.Version, InstanceID: id, Keep: &keep, DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	protectedCount := 0
	for _, a := range preview.Attempts {
		if a.Protected {
			protectedCount++
		}
	}
	if protectedCount != 1 {
		t.Fatal("completed cleanup obligation not reported protected", preview)
	}
	s.evidence.bytes = 1
	if err := s.pruneEvidence(); err != nil {
		t.Fatal(err)
	}
	got, err := s.store.attempt(v1.AttemptRequest{InstanceID: id, Attempt: newest.Result.Attempt})
	if err != nil || !got.Attempt.Result.EvidenceExpired {
		t.Fatal("byte expiry lost metadata", err)
	}
	live, _ := s.store.attempt(v1.AttemptRequest{InstanceID: id, Attempt: activeAttempt.Result.Attempt})
	if live.Attempt.Result.EvidenceExpired {
		t.Fatal("active evidence expired")
	}
	kept, _ := s.store.attempt(v1.AttemptRequest{InstanceID: id, Attempt: protected.Result.Attempt})
	if kept.Attempt.Result.EvidenceExpired {
		t.Fatal("cleanup/kept evidence expired")
	}
}

func TestAttemptPruneRetriesResidualsAndInvalidatesLegacyCursor(t *testing.T) {
	s, id := historyFixture(t)
	old := acceptHistory(t, s, id, v1.Stopped, time.Hour)
	writeAttemptLog(t, s, old, true)
	next := acceptHistory(t, s, id, v1.Stopped, 0)
	writeAttemptLog(t, s, next, true)
	page, err := s.logPage(v1.LogPageRequest{APIVersion: v1.Version, InstanceID: id, Attempt: next.Result.Attempt, Limit: 1})
	if err != nil || page.Continuation == "" {
		t.Fatal(page, err)
	}
	if err := s.deleteAttempt(old, true); err != nil {
		t.Fatal(err)
	}
	resumed, err := s.logPage(v1.LogPageRequest{APIVersion: v1.Version, InstanceID: id, Attempt: next.Result.Attempt, After: page.Continuation})
	if err != nil || len(resumed.Gaps) != 1 || resumed.Gaps[0].Code != "source_changed" {
		t.Fatal("rewritten legacy source did not invalidate cursor", resumed, err)
	}
	// A nonempty directory at a selected log path prevents removal after intent
	// commit. Both absence and cleanup intent must survive reopening the DB.
	path := filepath.Join(s.directory, "logs", id+"-"+next.Result.Attempt+"-output.ndjson")
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "block"), []byte("owned"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := s.deleteAttempt(next, true); err == nil {
		t.Fatal("erasure failure hidden")
	}
	if data := storedBytes(t, s.store, "evidence_deletions", id+"/"+next.Result.Attempt); len(data) == 0 {
		t.Fatal("retry intent missing")
	}
	// Reopen metadata only: no recovery or historical runtime actuation.
	if err := s.store.db.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := openStore(s.directory)
	if err != nil {
		t.Fatal(err)
	}
	s.store = reopened
	t.Cleanup(func() { _ = reopened.db.Close() })
	if err := os.RemoveAll(path); err != nil {
		t.Fatal(err)
	}
	if err := s.pruneEvidence(); err != nil {
		t.Fatal(err)
	}
	if data := storedBytes(t, s.store, "evidence_deletions", id+"/"+next.Result.Attempt); len(data) != 0 {
		t.Fatal("residual intent not completed")
	}
	_, err = s.store.attempt(v1.AttemptRequest{InstanceID: id, Attempt: next.Result.Attempt})
	code(t, err, "attempt_unavailable")
}

func TestHistoryAgeExpiresBytesAndRetainsMetadata(t *testing.T) {
	s, id := historyFixture(t)
	old := acceptHistory(t, s, id, v1.Stopped, 200*time.Hour)
	writeAttemptLog(t, s, old, false)
	if err := s.pruneEvidence(); err != nil {
		t.Fatal(err)
	}
	r, err := s.store.attempt(v1.AttemptRequest{InstanceID: id, Attempt: old.Result.Attempt})
	if err != nil || !r.Attempt.Result.EvidenceExpired {
		t.Fatal(r, err)
	}
	page, err := s.logPage(v1.LogPageRequest{APIVersion: v1.Version, InstanceID: id, Attempt: old.Result.Attempt})
	if err != nil || len(page.Records) != 0 || len(page.Gaps) != 1 || page.Gaps[0].Code != "retention_expired" {
		t.Fatal(page, err)
	}
}

func TestHistoryMigrationRollbackAndOriginalFixture(t *testing.T) {
	data, err := os.ReadFile("testdata/state/v11/state.db")
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(data)
	if hex.EncodeToString(hash[:]) != "6ca1ade1b29836ad6ab9f95f005b9fec8eee968c4a0adfefbf1cd60e7417787d" {
		t.Fatal("genuine predecessor bytes changed")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "state.db")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	db, err := bolt.Open(path, 0600, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	before := stateContents(t, &store{db: db})
	injected := errors.New("after seed")
	steps := append([]stateMigration{}, stateMigrations...)
	last := steps[len(steps)-1].apply
	steps[len(steps)-1].apply = func(tx *bolt.Tx) error {
		if err := last(tx); err != nil {
			return err
		}
		return injected
	}
	if err := db.Update(func(tx *bolt.Tx) error { return openStateVersion(tx, v1.StateVersion, steps) }); err == nil {
		t.Fatal("failed migration accepted")
	}
	if !reflect.DeepEqual(before, stateContents(t, &store{db: db})) {
		t.Fatal("failed migration changed source bytes/layout")
	}
	if err := db.Update(openStateTransaction); err != nil {
		t.Fatal(err)
	}
	state := &store{db: db}
	instances, err := state.list(v1.ListRequest{APIVersion: v1.Version})
	if err != nil {
		t.Fatal(err)
	}
	for _, summary := range instances.Instances {
		i, err := state.inspect(summary.ID)
		if err != nil {
			t.Fatal(err)
		}
		rows, err := state.attempts(i.ID)
		if err != nil {
			t.Fatal(err)
		}
		if i.Execution == nil {
			if len(rows) != 0 {
				t.Fatal("invented attempt for prepared identity")
			}
			continue
		}
		if len(rows) != 1 || !reflect.DeepEqual(rows[0].Result, publicAttemptResult(*i.Execution)) {
			t.Fatal("migration rewrote known current result")
		}
	}
}

func TestDestroyContractRejectsBeforeAllEffects(t *testing.T) {
	s, id := historyFixture(t)
	a := acceptHistory(t, s, id, v1.Stopped, 0)
	writeAttemptLog(t, s, a, false)
	before := stateContents(t, s.store)
	logPath := filepath.Join(s.directory, "logs", id+"-"+a.Result.Attempt+"-output.ndjson")
	logBefore, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	server := handler(s, v1.DaemonStatusResponse{}, func() {})
	for _, force := range []bool{false, true} {
		for _, marker := range []string{"", "unknown"} {
			q := v1.DestroyRequest{APIVersion: v1.Version, InstanceID: id, Force: force, Contract: marker}
			body, _ := json.Marshal(q)
			if marker == "" {
				body = bytes.Replace(body, []byte(`"contract":"",`), nil, 1)
			}
			request := httptest.NewRequest(http.MethodPost, "/v1/destroy", bytes.NewReader(body))
			request.Header.Set("X-Backlot-API-Version", v1.Version)
			request.Header.Set("Content-Type", "application/json")
			recorder := httptest.NewRecorder()
			server.ServeHTTP(recorder, request)
			if recorder.Code != http.StatusUpgradeRequired {
				t.Fatal("incompatible destroy admitted", recorder.Code)
			}
			if !reflect.DeepEqual(before, stateContents(t, s.store)) {
				t.Fatal("rejected marker changed durable state")
			}
			data, err := os.ReadFile(logPath)
			if err != nil || !bytes.Equal(logBefore, data) {
				t.Fatal("rejected marker changed evidence", err)
			}
		}
	}
}

func TestFailedDestroyRetainsDiagnosticsAndData(t *testing.T) {
	s, id := historyFixture(t)
	a := acceptHistory(t, s, id, v1.Stopped, 0)
	writeAttemptLog(t, s, a, false)
	data := t.TempDir()
	if err := os.Chmod(data, 0700); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(data, "sentinel")
	if err := os.WriteFile(sentinel, []byte("preserved"), 0600); err != nil {
		t.Fatal(err)
	}
	g := resourceGeneration{ID: newID(), Resources: map[string]ownedResource{"data": {Kind: "directory", Name: data, Token: newID()}}, Initialized: map[string]bool{}}
	if err := s.store.saveResources(id, g); err != nil {
		t.Fatal(err)
	}
	_, err := s.destroyWithForce(context.Background(), id, true)
	code(t, err, "cleanup_failed")
	retained, err := s.store.attempt(v1.AttemptRequest{InstanceID: id, Attempt: a.Result.Attempt})
	if err != nil || retained.Attempt.Result.CleanupFailure == "" {
		t.Fatal("failed destroy lost diagnostic history", err)
	}
	if value, err := os.ReadFile(sentinel); err != nil || string(value) != "preserved" {
		t.Fatal("uncertain data removed", err)
	}
	if _, err := os.Stat(filepath.Join(s.directory, "logs", id+"-"+a.Result.Attempt+"-output.ndjson")); err != nil {
		t.Fatal("failed destroy erased logs", err)
	}
}

func TestDestroyErasesPrivateResultCopiesAndRetries(t *testing.T) {
	s, id := historyFixture(t)
	a := acceptHistory(t, s, id, v1.Stopped, 0)
	writeAttemptLog(t, s, a, false)
	if err := s.store.db.Update(func(tx *bolt.Tx) error {
		r, err := load(tx, id)
		if err != nil {
			return err
		}
		r.NativeAbandonments = []nativeAbandonment{{Attempt: a.Result.Attempt, RecordedAt: time.Now().UTC().Format(time.RFC3339Nano), Result: &a.Result}}
		return put(tx, "instances", id, r)
	}); err != nil {
		t.Fatal(err)
	}
	// This isolates the post-runtime/data erasure transaction; failed runtime/data
	// fences are tested through destroyWithForce above, never bypassed in production.
	i, err := s.eraseDestroyed(id)
	if err != nil || i.Execution != nil || i.Status != v1.Destroyed || len(i.Plan.Components) != 0 {
		t.Fatal(i, err)
	}
	for _, bucket := range []string{"runtime", "snapshots", "secrets"} {
		if len(storedBytes(t, s.store, bucket, id)) != 0 {
			t.Fatal("destroy retained private result/fixture payload", bucket)
		}
	}
	if bytes.Contains(storedBytes(t, s.store, "instances", id), []byte("original_result")) {
		t.Fatal("destroy retained original result copy")
	}
	if entries, err := s.store.attempts(id); err != nil || len(entries) != 0 {
		t.Fatal("destroy retained history", err)
	}
	if _, err := s.destroyWithForce(context.Background(), id, false); err != nil {
		t.Fatal("idempotent marked lifecycle retry", err)
	}
}

// replaceTestExecution installs a synthetic successor in tests of stale writers;
// production acceptance remains subject to lifetime, lease and snapshot checks.
func (s *store) replaceTestExecution(id string, status v1.InstanceStatus, e *v1.ExecutionResult) (v1.Instance, error) {
	var i v1.Instance
	err := s.db.Update(func(tx *bolt.Tx) error {
		r, err := load(tx, id)
		if err != nil {
			return err
		}
		r.Instance.Status = status
		r.Instance.Operation.Status = status
		r.Instance.Execution = e
		i = r.Instance
		return put(tx, "instances", id, r)
	})
	return i, err
}

func TestHistoryFreezesLegacySourceAndUnknownCompletionTime(t *testing.T) {
	s, id := historyFixture(t)
	a := acceptHistory(t, s, id, v1.Stopped, 0)
	// A legacy terminal result has an observed outcome, but no invented completion
	// timestamp. Replacing current configuration must not rewrite its provenance.
	if err := s.store.db.Update(func(tx *bolt.Tx) error {
		a.Provenance = "legacy_current"
		a.Result.CompletedAt = ""
		r, err := load(tx, id)
		if err != nil {
			return err
		}
		r.Instance.Execution.CompletedAt = ""
		if err := put(tx, "instances", id, r); err != nil {
			return err
		}
		return put(tx, "attempts", attemptKey(id, a.Sequence), a)
	}); err != nil {
		t.Fatal(err)
	}
	snap, err := s.store.snapshot(id)
	if err != nil {
		t.Fatal(err)
	}
	snap.Plan.ManifestDigest = strings.Repeat("c", 64)
	if err := s.store.replaceSnapshot(id, snap); err != nil {
		t.Fatal(err)
	}
	got, err := s.store.attempt(v1.AttemptRequest{InstanceID: id, LastCompleted: true})
	if err != nil || got.Attempt.Result.Attempt != a.Result.Attempt || got.Attempt.Source.ManifestDigest != a.Source.ManifestDigest || got.Attempt.Result.CompletedAt != "" {
		t.Fatal("legacy facts changed", got, err)
	}
}

func TestHistoryPruneRejectsEvidenceParentLinks(t *testing.T) {
	s, id := historyFixture(t)
	a := acceptHistory(t, s, id, v1.Stopped, time.Hour)
	application := t.TempDir()
	if err := os.Chmod(application, 0700); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(application, "sentinel")
	if err := os.WriteFile(sentinel, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(application, filepath.Join(s.directory, "artifacts")); err != nil {
		t.Fatal(err)
	}
	keep := 0
	_, err := s.pruneHistory(context.Background(), v1.HistoryPruneRequest{APIVersion: v1.Version, InstanceID: id, Keep: &keep})
	if err == nil {
		t.Fatal("evidence link admitted")
	}
	expired := a.Result
	expired.EvidenceExpired = true
	if _, err := s.store.execution(id, v1.Stopped, &expired); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(application, filepath.Join(s.directory, "logs")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.logs(v1.LogsRequest{InstanceID: id}); err == nil {
		t.Fatal("aggregate logs followed evidence parent link")
	}
	if _, err := s.logPage(v1.LogPageRequest{APIVersion: v1.Version, InstanceID: id}); err == nil {
		t.Fatal("aggregate page followed evidence parent link")
	}
	if data, err := os.ReadFile(sentinel); err != nil || string(data) != "keep" {
		t.Fatal("prune followed evidence link", err)
	}
	if _, err := s.store.attempt(v1.AttemptRequest{InstanceID: id, Attempt: a.Result.Attempt}); err != nil {
		t.Fatal("unsafe preflight removed history", err)
	}
}

func TestAggregateLogOffsetsFollowAcceptanceOrder(t *testing.T) {
	s, id := historyFixture(t)
	makeAttempt := func(value string) v1.Attempt {
		t.Helper()
		e := &v1.ExecutionResult{Attempt: strings.Repeat(value, 64), CompletedAt: time.Now().UTC().Format(time.RFC3339Nano), Components: []v1.ComponentResult{}}
		if _, err := s.store.replaceTestExecution(id, v1.Stopped, e); err != nil {
			t.Fatal(err)
		}
		a, err := s.store.attempt(v1.AttemptRequest{InstanceID: id, Attempt: e.Attempt})
		if err != nil {
			t.Fatal(err)
		}
		writeAttemptLog(t, s, a.Attempt, false)
		return a.Attempt
	}
	first := makeAttempt("f")
	page, err := s.logs(v1.LogsRequest{InstanceID: id})
	if err != nil || len(page.Records) != 1 {
		t.Fatal(page, err)
	}
	next := makeAttempt("a") // lexical filename order is deliberately reversed
	tail, err := s.logs(v1.LogsRequest{InstanceID: id, Offset: page.NextOffset})
	if err != nil || len(tail.Records) != 1 || tail.Records[0].Attempt != next.Result.Attempt {
		t.Fatal("aggregate offset replayed predecessor", tail, err)
	}
	all, err := s.logs(v1.LogsRequest{InstanceID: id})
	if err != nil || len(all.Records) != 2 || all.Records[0].Attempt != first.Result.Attempt {
		t.Fatal("aggregate order changed", all, err)
	}
}

func mixedPredecessor(t *testing.T) (*service, v1.Instance, string) {
	t.Helper()
	dir := shortTemp(t)
	if err := os.CopyFS(dir, os.DirFS("testdata/state/v11-mixed")); err != nil {
		t.Fatal(err)
	}
	if err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		mode := os.FileMode(0600)
		if d.IsDir() {
			mode = 0700
		}
		return os.Chmod(path, mode)
	}); err != nil {
		t.Fatal(err)
	}
	state, err := openStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = state.db.Close() })
	s := &service{store: state, directory: dir, lease: time.Minute, generation: newID()}
	list, err := state.list(v1.ListRequest{APIVersion: v1.Version})
	if err != nil || len(list.Instances) != 2 {
		t.Fatal(list, err)
	}
	selected := ""
	for _, row := range list.Instances {
		if row.Scene == "dev" {
			selected = row.ID
		} else {
			// Keep this retention scenario scoped to dev's copied evidence.
			if err := s.removeEvidence(row.ID); err != nil {
				t.Fatal(err)
			}
		}
	}
	i, err := state.inspect(selected)
	if err != nil {
		t.Fatal(err)
	}
	logs, err := s.logs(v1.LogsRequest{InstanceID: i.ID})
	if err != nil || len(logs.Records) != 2 {
		t.Fatal(logs, err)
	}
	unknown := ""
	for _, r := range logs.Records {
		if r.Attempt != i.Execution.Attempt {
			unknown = r.Attempt
		}
	}
	if unknown == "" {
		t.Fatal("genuine mixed predecessor lost overwritten attempt bytes")
	}
	return s, i, unknown
}

func TestUnrepresentedLegacyEvidenceRetention(t *testing.T) {
	t.Run("byte-protection-cursor-reopen", func(t *testing.T) {
		s, i, unknown := mixedPredecessor(t)
		e := *i.Execution
		represented, err := s.attemptEvidenceSize(i.ID, e.Attempt)
		if err != nil {
			t.Fatal(err)
		}
		s.evidence.bytes = represented + 1
		original, err := s.evidenceSize(i.ID)
		if err != nil || original <= s.evidence.bytes {
			t.Fatal("fixture does not exceed cap", original, err)
		}
		e.CleanupFailure = "unresolved retained cleanup"
		if _, err := s.store.execution(i.ID, v1.Failed, &e); err != nil {
			t.Fatal(err)
		}
		if err := s.pruneEvidence(); err != nil {
			t.Fatal(err)
		}
		size, err := s.evidenceSize(i.ID)
		if err != nil || size != original {
			t.Fatal("unknown bytes pruned through cleanup protection", size, err)
		}
		e.CleanupFailure = ""
		if _, err := s.store.execution(i.ID, v1.Stopped, &e); err != nil {
			t.Fatal(err)
		}
		page, err := s.logPage(v1.LogPageRequest{APIVersion: v1.Version, InstanceID: i.ID, Attempt: e.Attempt})
		if err != nil || page.Continuation == "" {
			t.Fatal(page, err)
		}
		if err := s.pruneEvidence(); err != nil {
			t.Fatal(err)
		}
		size, err = s.evidenceSize(i.ID)
		if err != nil || size != represented {
			t.Fatal("unknown evidence byte accounting", size, represented, err)
		}
		selected, err := s.store.attempt(v1.AttemptRequest{InstanceID: i.ID, Attempt: e.Attempt})
		if err != nil || selected.Attempt.Result.EvidenceExpired {
			t.Fatal("unknown cleanup expired represented evidence", err)
		}
		_, err = s.store.attempt(v1.AttemptRequest{InstanceID: i.ID, Attempt: unknown})
		code(t, err, "attempt_unavailable")
		if _, err := os.Stat(filepath.Join(s.directory, "artifacts", i.ID, unknown)); !os.IsNotExist(err) {
			t.Fatal("unknown artifact bytes remain", err)
		}
		changed, err := s.logPage(v1.LogPageRequest{APIVersion: v1.Version, InstanceID: i.ID, Attempt: e.Attempt, After: page.Continuation})
		if err != nil || len(changed.Gaps) != 1 || changed.Gaps[0].Code != "source_changed" {
			t.Fatal("legacy rewrite cursor not invalidated", changed, err)
		}
		if err := s.store.db.Close(); err != nil {
			t.Fatal(err)
		}
		reopened, err := openStore(s.directory)
		if err != nil {
			t.Fatal(err)
		}
		s.store = reopened
		t.Cleanup(func() { _ = reopened.db.Close() })
		if err := s.pruneEvidence(); err != nil {
			t.Fatal(err)
		}
		rows, err := s.store.attempts(i.ID)
		if err != nil || len(rows) != 1 || rows[0].Result.Attempt != e.Attempt {
			t.Fatal("unknown evidence cleanup fabricated/resurrected history", err)
		}
		size, err = s.evidenceSize(i.ID)
		if err != nil || size != represented {
			t.Fatal("repeat/reopen changed retained bytes", size, err)
		}
	})
	t.Run("age-after-selected-prune", func(t *testing.T) {
		s, i, unknown := mixedPredecessor(t)
		keep := 0
		if _, err := s.pruneHistory(context.Background(), v1.HistoryPruneRequest{APIVersion: v1.Version, InstanceID: i.ID, Keep: &keep}); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(s.directory, "logs", i.ID+"-output.ndjson")
		future := time.Now().Add(365 * 24 * time.Hour)
		if err := os.Chtimes(path, future, future); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(filepath.Join(s.directory, "artifacts", i.ID, unknown)); err != nil {
			t.Fatal("manual selected prune removed unknown evidence", err)
		}
		logs, err := s.logs(v1.LogsRequest{InstanceID: i.ID})
		if err != nil || len(logs.Records) != 1 || logs.Records[0].Attempt != unknown || logs.Gap != "evidence expired by retention policy" {
			t.Error("aggregate logs hid retained unknown evidence or lost current expiry gap", logs, err)
		}
		page, err := s.logPage(v1.LogPageRequest{APIVersion: v1.Version, InstanceID: i.ID, Limit: 1})
		if err != nil || len(page.Records) != 1 || page.Records[0].Attempt != unknown || len(page.Gaps) != 1 || page.Gaps[0].Code != "retention_expired" {
			t.Error("aggregate page hid retained unknown evidence or lost current expiry gap", page, err)
		}
		_, err = s.logPage(v1.LogPageRequest{APIVersion: v1.Version, InstanceID: i.ID, Attempt: i.Execution.Attempt})
		code(t, err, "attempt_unavailable")
		_, err = s.store.attempt(v1.AttemptRequest{InstanceID: i.ID, Attempt: unknown})
		code(t, err, "attempt_unavailable")
		s.evidence.age = time.Nanosecond
		if err := s.pruneEvidence(); err != nil {
			t.Fatal(err)
		}
		size, err := s.evidenceSize(i.ID)
		if err != nil || size != 0 {
			t.Fatal("unknown evidence escaped inherited age policy", size, err)
		}
		current, err := s.store.inspect(i.ID)
		if err != nil || current.Execution.CompletedAt != i.Execution.CompletedAt {
			t.Fatal("legacy cleanup fabricated/renewed completion time", err)
		}
		logs, err = s.logs(v1.LogsRequest{InstanceID: i.ID})
		if err != nil || len(logs.Records) != 0 || logs.Gap == "" {
			t.Fatal("expired unknown evidence remained readable", logs, err)
		}
		page, err = s.logPage(v1.LogPageRequest{APIVersion: v1.Version, InstanceID: i.ID})
		if err != nil || len(page.Records) != 0 || len(page.Gaps) != 1 || page.Gaps[0].Code != "retention_expired" {
			t.Fatal("expired unknown evidence remained paginatable", page, err)
		}
		rows, err := s.store.attempts(i.ID)
		if err != nil || len(rows) != 0 {
			t.Fatal("selected-pruned history resurrected", err)
		}
	})
}

func TestUnrepresentedLegacyBytesRespectHistoricalCleanup(t *testing.T) {
	s, i, _ := mixedPredecessor(t)
	e := *i.Execution
	e.CleanupFailure = "historical cleanup obligation"
	if _, err := s.store.execution(i.ID, v1.Failed, &e); err != nil {
		t.Fatal(err)
	}
	// Install a synthetic clean successor solely to exercise the private retention
	// guard against an older unresolved obligation, without provider actuation.
	acceptHistory(t, s, i.ID, v1.Stopped, 0)
	before, err := s.evidenceSize(i.ID)
	if err != nil {
		t.Fatal(err)
	}
	s.evidence.bytes = 1
	if err := s.pruneEvidence(); err != nil {
		t.Fatal(err)
	}
	after, err := s.evidenceSize(i.ID)
	if err != nil || after != before {
		t.Fatal("historical cleanup lost unknown bytes", before, after, err)
	}
}

func TestLegacyDestroyedResultRemainsUntilMarkedDestroy(t *testing.T) {
	s, _, _ := mixedPredecessor(t)
	list, err := s.store.list(v1.ListRequest{APIVersion: v1.Version})
	if err != nil {
		t.Fatal(err)
	}
	id := ""
	for _, row := range list.Instances {
		if row.Scene == "destroyed" {
			id = row.ID
		}
	}
	current, err := s.store.inspect(id)
	if err != nil || current.Status != v1.Destroyed || current.Execution == nil {
		t.Fatal("genuine predecessor destroyed result missing", err)
	}
	selected, err := s.store.attempt(v1.AttemptRequest{InstanceID: id, LastCompleted: true})
	if err != nil || selected.Attempt.Result.Attempt != current.Execution.Attempt || selected.Attempt.Provenance != "legacy_current" {
		t.Fatal("migration discarded known legacy destroyed result", err)
	}
	if _, err := s.destroyWithForce(context.Background(), id, false); err != nil {
		t.Fatal(err)
	}
	erased, err := s.store.inspect(id)
	if err != nil || erased.Execution != nil {
		t.Fatal("explicit matching destroy retained legacy result", err)
	}
}
