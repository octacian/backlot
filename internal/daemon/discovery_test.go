//go:build darwin || linux

package daemon

import (
	"context"
	"encoding/json"
	bolt "go.etcd.io/bbolt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	v1 "github.com/octacian/backlot/api/v1"
)

func TestFilteredDiscoveryRecordedLocations(t *testing.T) {
	s, root, config := sceneFixture(t)
	outer := scenePrepare(t, s, root, config)
	nested := filepath.Join(root, "nested")
	if err := os.Mkdir(nested, 0700); err != nil {
		t.Fatal(err)
	}
	writeFixture(t, nested)
	if err := os.WriteFile(filepath.Join(nested, "backlot.json"), []byte(strings.Replace(fixtureManifest, `"project":"fixture"`, `"project":"nested"`, 1)), 0600); err != nil {
		t.Fatal(err)
	}
	inner := scenePrepare(t, s, nested, config)
	historical, err := s.prepare(context.Background(), request(root, config, "test"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.destroy(context.Background(), historical.Instance.ID); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(nested, "backlot.json")); err != nil {
		t.Fatal(err)
	}
	// A linked worktree with the same project ID is a separate recorded checkout.
	git(t, root, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "--allow-empty", "-qm", "fixture")
	peerRoot := shortTemp(t)
	git(t, root, "worktree", "add", "--detach", peerRoot, "HEAD")
	peerConfig := writeFixture(t, peerRoot)
	scenePrepare(t, s, peerRoot, peerConfig)
	q := v1.FilteredListRequest{APIVersion: v1.Version, ProjectPath: filepath.Join(nested, "backlot.json"), Limit: 1}
	r, err := s.store.filteredList(q)
	if err != nil || len(r.Instances) != 1 || r.Instances[0].ID != inner.Instance.ID || r.NextAfterID != "" {
		t.Fatal(r, err)
	}
	q.ProjectPath = ""
	q.CheckoutPath = root
	got := []string{}
	for {
		r, err = s.store.filteredList(q)
		if err != nil {
			t.Fatal(err)
		}
		for _, i := range r.Instances {
			got = append(got, i.ID)
		}
		if r.NextAfterID == "" {
			break
		}
		q.AfterID = r.NextAfterID
	}
	want := []string{outer.Instance.ID, inner.Instance.ID, historical.Instance.ID}
	sort.Strings(want)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatal(got, want)
	}
	q.AfterID = ""
	q.State = v1.Destroyed
	q.Lifetime = v1.Disposable
	q.ProjectID = "fixture"
	q.Scene = "test"
	r, err = s.store.filteredList(q)
	if err != nil || len(r.Instances) != 1 || r.Instances[0].ID != historical.Instance.ID || r.NextAfterID != "" {
		t.Fatal(r, err)
	}
	q.Scene = "absent"
	r, err = s.store.filteredList(q)
	if err != nil || r.Instances == nil || len(r.Instances) != 0 {
		t.Fatal(r, err)
	}
	moved := root + "-original"
	if err := os.Rename(root, moved); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(moved) })
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	git(t, root, "init", "-q")
	_, err = s.store.filteredList(q)
	code(t, err, "checkout_replaced")
	if _, err := s.store.inspect(outer.Instance.ID); err != nil {
		t.Fatal("explicit history lost", err)
	}
}

func TestLogPageBoundsFiltersAndContinuation(t *testing.T) {
	s, i, _ := exportFixture(t)
	s.generation = newID()
	if err := os.WriteFile(filepath.Join(s.directory, "logs", i.ID+"-z-empty.ndjson"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	q := v1.LogPageRequest{APIVersion: v1.Version, InstanceID: i.ID, Limit: 1}
	r, err := s.logPage(q)
	if err != nil || len(r.Records) != 1 || r.Continuation == "" || r.More || r.HistoricalCollection != "unknown" || r.Validate(q) != nil {
		t.Fatal(r, err)
	}
	end := r.Continuation
	path := filepath.Join(s.directory, "logs", i.ID+"-output.ndjson")
	original, _ := os.ReadFile(path)
	older := newID()
	record := r.Records[0]
	record.Attempt = older
	record.Stream = "stderr"
	record.Message = "historical"
	data, _ := json.Marshal(record)
	if err := os.WriteFile(path, append(original, append(data, '\n')...), 0600); err != nil {
		t.Fatal(err)
	}
	q.After = end
	r, err = s.logPage(q)
	if err != nil || len(r.Records) != 1 || r.Records[0].Attempt != older || r.More {
		t.Fatal(r, err)
	}
	q.After = ""
	historical := i
	result := *i.Execution
	result.Attempt = older
	historical.Execution = &result
	if err := s.store.db.Update(func(tx *bolt.Tx) error {
		parent, err := load(tx, i.ID)
		if err != nil {
			return err
		}
		parent.Instance = historical
		return seedAttempt(tx, parent, "legacy_current")
	}); err != nil {
		t.Fatal(err)
	}
	q.Attempt = older
	q.Stream = "stderr"
	q.Component = "test"
	r, err = s.logPage(q)
	if err != nil || len(r.Records) != 1 || r.Records[0].Message != "historical" {
		t.Fatal(r, err)
	}
	q.Stream = "stdout"
	q.After = r.Continuation
	_, err = s.logPage(q)
	code(t, err, "invalid_cursor")
	// Filtered-out bytes still exhaust the scan budget, advancing an empty page.
	record.Message = strings.Repeat("x", 60<<10)
	data, _ = json.Marshal(record)
	var large []byte
	for range 12 {
		large = append(large, append(data, '\n')...)
	}
	if err := os.WriteFile(path, large, 0600); err != nil {
		t.Fatal(err)
	}
	q.After = ""
	q.Stream = "stdout"
	r, err = s.logPage(q)
	if err != nil || len(r.Records) != 0 || !r.More || r.Continuation == "" {
		t.Fatal("filtered page failed to advance", r, err)
	}
	first := r.Continuation
	q.After = first
	r, err = s.logPage(q)
	if err != nil || r.More || r.Continuation == first {
		t.Fatal("filtered scan did not finish", r, err)
	}
	// Count and encoded-byte budgets both constrain unfiltered results.
	q.After = ""
	q.Stream = ""
	q.Limit = 1000
	r, err = s.logPage(q)
	if err != nil || len(r.Records) == 0 || len(r.Records) >= 12 || !r.More || r.Validate(q) != nil {
		t.Fatal("unbounded record page", len(r.Records), err)
	}
	q.After = r.Continuation
	s.generation = newID()
	_, err = s.logPage(q)
	code(t, err, "daemon_changed")
}

func TestLogPageTailsGapsAndSourceChanges(t *testing.T) {
	s, i, _ := exportFixture(t)
	s.generation = newID()
	q := v1.LogPageRequest{APIVersion: v1.Version, InstanceID: i.ID}
	path := filepath.Join(s.directory, "logs", i.ID+"-output.ndjson")
	r, err := s.logPage(q)
	if err != nil {
		t.Fatal(err)
	}
	q.After = r.Continuation
	if err := os.Truncate(path, 0); err != nil {
		t.Fatal(err)
	}
	r, err = s.logPage(q)
	if err != nil || len(r.Gaps) != 1 || r.Gaps[0].Code != "source_changed" || r.Continuation != "" {
		t.Fatal(r, err)
	}
	q.After = ""
	if err := os.WriteFile(path, []byte(`{"unfinished":`), 0600); err != nil {
		t.Fatal(err)
	}
	_, err = s.logPage(q)
	code(t, err, "collection_failed")
	if _, err := s.store.execution(i.ID, v1.Starting, i.Execution); err != nil {
		t.Fatal(err)
	}
	r, err = s.logPage(q)
	if err != nil || !r.Active || len(r.Records) != 0 || !r.More {
		t.Fatal("active tail treated as complete", r, err)
	}
	// Replacement must invalidate continuation even at the same pathname and size.
	q.After = r.Continuation
	if err := os.Rename(path, path+".previous"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"unfinished":`), 0600); err != nil {
		t.Fatal(err)
	}
	r, err = s.logPage(q)
	if err != nil || len(r.Gaps) != 1 || r.Gaps[0].Code != "source_changed" {
		t.Fatal(r, err)
	}
	q.After = ""
	i.Execution.CollectionFailure = "collector interrupted"
	i.Execution.EvidenceExpired = true
	if _, err := s.store.execution(i.ID, v1.Failed, i.Execution); err != nil {
		t.Fatal(err)
	}
	r, err = s.logPage(q)
	if err != nil || len(r.Gaps) != 2 || r.Gaps[0].Code != "collection_incomplete" || r.Gaps[1].Code != "retention_expired" || len(r.Records) != 0 {
		t.Fatal(r, err)
	}
}
