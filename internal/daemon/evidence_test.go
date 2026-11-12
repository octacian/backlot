//go:build !windows

package daemon

import (
	"archive/tar"
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	v1 "github.com/octacian/backlot/api/v1"
	bolt "go.etcd.io/bbolt"
)

func TestArtifactCopyRejectsEscapingLinksAndMissingSources(t *testing.T) {
	checkout := t.TempDir()
	outside := t.TempDir()
	destination := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "private"), []byte("private"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(checkout, "escape")); err != nil {
		t.Fatal(err)
	}
	if err := copyNativeArtifact(checkout, "escape/private", destination); err == nil {
		t.Fatal("copied escaping link")
	}
	if err := copyNativeArtifact(checkout, "missing", destination); err == nil {
		t.Fatal("missing declaration succeeded")
	}
	for _, header := range []*tar.Header{{Name: "../escape", Mode: 0600, Typeflag: tar.TypeReg}, {Name: "link", Linkname: "/private", Typeflag: tar.TypeSymlink}, {Name: "hard", Linkname: "../escape", Typeflag: tar.TypeLink}, {Name: "pipe", Typeflag: tar.TypeFifo}} {
		var archive bytes.Buffer
		writer := tar.NewWriter(&archive)
		if err := writer.WriteHeader(header); err != nil {
			t.Fatal(err)
		}
		if err := writer.Close(); err != nil {
			t.Fatal(err)
		}
		if err := extractArtifact(&archive, t.TempDir()); err == nil {
			t.Fatalf("accepted unsafe archive %s", header.Name)
		}
	}
	if err := os.Mkdir(filepath.Join(checkout, "reports"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(checkout, "reports", "result"), []byte("evidence"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := copyNativeArtifact(checkout, "reports", destination); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(destination, "reports", "result"))
	if err != nil || string(data) != "evidence" {
		t.Fatal(string(data), err)
	}
}

func TestEvidenceRetentionIndependentOfDataAndRetriesExpiration(t *testing.T) {
	root := shortTemp(t)
	config := writeFixture(t, root)
	store, err := openStore(shortTemp(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := store.db.Close(); err != nil {
			t.Error(err)
		}
	})
	s := &service{store: store, lease: time.Minute, directory: store.directory}
	prepare := func() v1.Instance {
		response, err := s.prepare(context.Background(), request(root, config, "test"))
		if err != nil {
			t.Fatal(err)
		}
		return response.Instance
	}
	sentinel := filepath.Join(s.directory, "data-sentinel")
	if err := os.WriteFile(sentinel, []byte("retained data"), 0600); err != nil {
		t.Fatal(err)
	}
	s.evidence.bytes = 42
	old := prepare()
	recent := prepare()
	activeRun := prepare()
	kept := prepare()
	uncertain := prepare()
	instances := []v1.Instance{old, recent, activeRun, kept, uncertain}
	base := time.Now().UTC().Add(-4 * time.Hour).Truncate(time.Second)
	for i, instance := range instances {
		result := &v1.ExecutionResult{Attempt: newID(), Components: []v1.ComponentResult{}, CompletedAt: time.Now().Add(time.Duration(i-4) * time.Hour).UTC().Format(time.RFC3339Nano)}
		status := v1.Succeeded
		if i == 0 {
			result.CompletedAt = base.Format(time.RFC3339Nano)
		}
		if i == 1 {
			result.CompletedAt = base.Add(100 * time.Millisecond).Format(time.RFC3339Nano)
		}
		if instance.ID == activeRun.ID {
			status = v1.Starting
			result.CompletedAt = ""
		}
		if instance.ID == uncertain.ID {
			status = v1.Failed
			result.CleanupFailure = "owned runtime absence unverified"
		}
		if instance.ID == kept.ID {
			status = v1.Failed
			result.Kept = true
		}
		if _, err := s.store.execution(instance.ID, status, result); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Join(s.directory, "logs"), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(s.directory, "logs", instance.ID+"-"+result.Attempt+"-output.ndjson"), []byte(strings.Repeat("x", 10)), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.pruneEvidence(); err != nil {
		t.Fatal(err)
	}
	for _, instance := range instances {
		got, err := s.store.inspect(instance.ID)
		if err != nil {
			t.Fatal(err)
		}
		wantExpired := instance.ID == old.ID
		if got.Execution.EvidenceExpired != wantExpired {
			t.Fatalf("expiration %s: %+v", instance.ID, got.Execution)
		}
	}
	// Reduce the cap below protected evidence. Completed evidence is removed,
	// while active and kept runtime evidence remain even above the total cap.
	s.evidence.bytes = 1
	if err := s.pruneEvidence(); err != nil {
		t.Fatal(err)
	}
	for _, instance := range instances {
		got, err := s.store.inspect(instance.ID)
		if err != nil {
			t.Fatal(err)
		}
		wantExpired := instance.ID == old.ID || instance.ID == recent.ID
		if got.Execution.EvidenceExpired != wantExpired {
			t.Fatal("active evidence policy", got.Execution)
		}
	}
	// Simulate interruption after metadata commit by recreating expired bytes;
	// the next sweep must remove them without touching runtime/data state.
	path := filepath.Join(s.directory, "logs", old.ID+"-"+func() string { i, _ := s.store.inspect(old.ID); return i.Execution.Attempt }()+"-output.ndjson")
	if err := os.WriteFile(path, []byte("residual"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := s.pruneEvidence(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("expired residual survived", err)
	}
	if data, err := os.ReadFile(sentinel); err != nil || string(data) != "retained data" {
		t.Fatal("data changed", err)
	}
	response, err := s.logs(v1.LogsRequest{InstanceID: old.ID})
	if err != nil || !strings.Contains(response.Gap, "expired") {
		t.Fatal(response, err)
	}
}

func TestKeptRuntimeFencesOutputDrift(t *testing.T) {
	root := shortTemp(t)
	config := writeFixture(t, root)
	state, err := openStore(shortTemp(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := state.db.Close(); err != nil {
			t.Error(err)
		}
	})
	s := &service{store: state, lease: time.Minute}
	response, err := s.prepare(context.Background(), request(root, config, "test"))
	if err != nil {
		t.Fatal(err)
	}
	err = state.db.Update(func(tx *bolt.Tx) error {
		r, err := load(tx, response.Instance.ID)
		if err != nil {
			return err
		}
		r.Instance.Status = v1.Failed
		r.Instance.Plan.Outputs = map[string]v1.Output{"report": {Path: "reports", ConcurrencyGroup: "old"}}
		r.Instance.Execution = &v1.ExecutionResult{Attempt: newID(), Kept: true}
		return put(tx, "instances", response.Instance.ID, r)
	})
	if err != nil {
		t.Fatal(err)
	}
	requested := response.Instance.Plan
	requested.Outputs = map[string]v1.Output{"report": {Path: "reports/result", ConcurrencyGroup: "new", EmptyBeforeExecution: true}}
	if err := state.outputConflict(requested); err == nil {
		t.Fatal("kept live output consumer allowed overlapping group drift")
	}
}

func TestLegacyEvidenceObservationSurvivesRestart(t *testing.T) {
	root := shortTemp(t)
	config := writeFixture(t, root)
	directory := shortTemp(t)
	state, err := openStore(directory)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := state.db.Close(); err != nil {
			t.Error(err)
		}
	})
	s := &service{store: state, lease: time.Minute, directory: directory}
	var instances []v1.Instance
	for i := range 4 {
		response, err := s.prepare(context.Background(), request(root, config, "test"))
		if err != nil {
			t.Fatal(err)
		}
		result := &v1.ExecutionResult{Attempt: newID(), Components: []v1.ComponentResult{}}
		status := v1.Succeeded
		switch i {
		case 1:
			status = v1.Starting
		case 2:
			status = v1.Failed
			result.Kept = true
		case 3:
			status = v1.Failed
			result.CleanupFailure = "unverified runtime"
		}
		if _, err := state.execution(response.Instance.ID, status, result); err != nil {
			t.Fatal(err)
		}
		instances = append(instances, response.Instance)
		if err := os.MkdirAll(filepath.Join(directory, "logs"), 0700); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(directory, "logs", response.Instance.ID+"-"+result.Attempt+"-output.ndjson")
		if err := os.WriteFile(path, []byte("legacy"), 0600); err != nil {
			t.Fatal(err)
		}
		old := time.Now().Add(-365 * 24 * time.Hour)
		if err := os.Chtimes(path, old, old); err != nil {
			t.Fatal(err)
		}
	}
	before := time.Now().UTC()
	if err := s.pruneEvidence(); err != nil {
		t.Fatal(err)
	}
	first, err := state.inspect(instances[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	observed, err := time.Parse(time.RFC3339Nano, first.Execution.EvidenceObservedAt)
	if err != nil || observed.Before(before) || observed.After(time.Now()) || first.Execution.CompletedAt != "" || first.Execution.EvidenceExpired {
		t.Fatal("legacy observation must be inferred now without expiring old files", first.Execution, err)
	}
	for _, instance := range instances[1:] {
		got, err := state.inspect(instance.ID)
		wantObserved := instance.ID != instances[1].ID
		if err != nil || (got.Execution.EvidenceObservedAt != "") != wantObserved || got.Execution.EvidenceExpired {
			t.Fatal("protected migration", got.Execution, err)
		}
	}
	if err := state.db.Close(); err != nil {
		t.Fatal(err)
	}
	state, err = openStore(directory)
	if err != nil {
		t.Fatal(err)
	}
	s = &service{store: state, lease: time.Minute, directory: directory}
	if err := s.pruneEvidence(); err != nil {
		t.Fatal(err)
	}
	restarted, err := state.inspect(instances[0].ID)
	if err != nil || restarted.Execution.EvidenceObservedAt != first.Execution.EvidenceObservedAt {
		t.Fatal("restart renewed legacy age", restarted.Execution, err)
	}
	// The size cap applies immediately, even though inferred age is fresh.
	s.evidence.bytes = 1
	if err := s.pruneEvidence(); err != nil {
		t.Fatal(err)
	}
	expired, err := state.inspect(instances[0].ID)
	if err != nil || !expired.Execution.EvidenceExpired {
		t.Fatal("legacy cap ignored", expired.Execution, err)
	}
	for _, instance := range instances[1:] {
		got, err := state.inspect(instance.ID)
		if err != nil || got.Execution.EvidenceExpired {
			t.Fatal("protected legacy evidence expired", got.Execution, err)
		}
	}
}

func TestRetentionLetsLogReadersProgressBeforeSweepCompletes(t *testing.T) {
	s, instance, _ := exportFixture(t)
	if err := s.store.db.Update(func(tx *bolt.Tx) error {
		template, err := load(tx, instance.ID)
		if err != nil {
			return err
		}
		template.Instance.Status, template.Instance.Operation.Status = v1.Starting, v1.Starting
		template.Instance.Execution.CompletedAt = ""
		for n := 1; n <= 64; n++ {
			template.Instance.ID = fmt.Sprintf("%064x", n)
			if err := put(tx, "instances", template.Instance.ID, template); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	// Hold the initial retention update so the log reader queues behind the sweep.
	writer, err := s.store.db.Begin(true)
	if err != nil {
		t.Fatal(err)
	}
	var release sync.Once
	unlock := func() {
		release.Do(func() {
			if err := writer.Rollback(); err != nil {
				t.Error(err)
			}
		})
	}
	defer unlock()
	done := make(chan struct{})
	var sweepErr error
	go func() { defer close(done); sweepErr = s.pruneEvidence() }()
	t.Cleanup(func() {
		unlock()
		<-done
		if sweepErr != nil {
			t.Error(sweepErr)
		}
	})
	deadline := time.Now().Add(5 * time.Second)
	for s.evidence.mu.TryLock() {
		s.evidence.mu.Unlock()
		if time.Now().After(deadline) {
			t.Fatal("retention did not acquire evidence fence")
		}
		time.Sleep(time.Millisecond)
	}
	read := make(chan error, 1)
	started := make(chan struct{})
	go func() {
		close(started)
		page, err := s.logs(v1.LogsRequest{InstanceID: instance.ID})
		if err == nil && (len(page.Records) != 1 || page.Records[0].Message != "original output") {
			err = fmt.Errorf("lost retained output: %+v", page)
		}
		select {
		case <-done:
			err = fmt.Errorf("log reader waited for the entire retention sweep: %v", err)
		default:
		}
		read <- err
	}()
	<-started
	unlock()
	select {
	case err := <-read:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		<-done
		<-read
		t.Fatal("retention blocked live log delivery")
	}
}

func TestRetentionSerializesDeletionWithoutBlockingLogs(t *testing.T) {
	for _, action := range []string{"prune", "destroy"} {
		t.Run(action, func(t *testing.T) {
			s, instance, _ := exportFixture(t)
			s.evidence.deletion.Lock()
			var release sync.Once
			unlock := func() { release.Do(s.evidence.deletion.Unlock) }
			defer unlock()
			done, started := make(chan struct{}), make(chan struct{})
			var actionErr error
			go func() {
				defer close(done)
				close(started)
				if action == "prune" {
					keep := 0
					_, actionErr = s.pruneHistory(context.Background(), v1.HistoryPruneRequest{APIVersion: v1.Version, InstanceID: instance.ID, Keep: &keep})
				} else {
					_, actionErr = s.destroy(context.Background(), instance.ID)
				}
			}()
			t.Cleanup(func() {
				unlock()
				<-done
				if actionErr != nil {
					t.Error(actionErr)
				}
			})
			<-started
			select {
			case <-done:
				t.Fatal("destructive operation bypassed retention accounting fence")
			case <-time.After(20 * time.Millisecond):
			}
			read := make(chan error, 1)
			go func() {
				page, err := s.logs(v1.LogsRequest{InstanceID: instance.ID})
				if err == nil && len(page.Records) != 1 {
					err = fmt.Errorf("evidence changed during sweep: %+v", page)
				}
				read <- err
			}()
			select {
			case err := <-read:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(time.Second):
				unlock()
				<-read
				t.Fatal("pending deletion blocked log reader")
			}
			unlock()
			<-done
			lock := s.mutationLock(instance.ID)
			if !lock.TryLock() {
				t.Fatal("destructive operation leaked mutation fence")
			}
			lock.Unlock()
		})
	}
}
