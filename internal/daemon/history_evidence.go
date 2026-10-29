package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"github.com/octacian/backlot/internal/native"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	v1 "github.com/octacian/backlot/api/v1"
	"github.com/octacian/backlot/internal/localipc"
	bolt "go.etcd.io/bbolt"
)

type evidenceDeletion struct {
	InstanceID string `json:"instance_id"`
	Attempt    string `json:"attempt,omitempty"`
}

// deleteAttempt commits absence/expiry before touching files. Residual deletion
// intent survives crashes and is retried by the existing retention owner.
func (s *service) deleteAttempt(a v1.Attempt, history bool) error {
	key := attemptKey(a.InstanceID, a.Sequence)
	intent := evidenceDeletion{InstanceID: a.InstanceID, Attempt: a.Result.Attempt}
	committed := false
	if err := s.store.db.Update(func(tx *bolt.Tx) error {
		data := tx.Bucket([]byte("attempts")).Get([]byte(key))
		if data == nil {
			return nil
		}
		var current v1.Attempt
		if err := json.Unmarshal(data, &current); err != nil {
			return err
		}
		if current.Result.Attempt != a.Result.Attempt || attemptProtected(current) {
			return nil
		}
		a = current
		committed = true
		if history {
			if err := tx.Bucket([]byte("attempts")).Delete([]byte(key)); err != nil {
				return err
			}
		} else {
			a.Result.EvidenceExpired = true
			if err := put(tx, "attempts", key, a); err != nil {
				return err
			}
		}
		r, err := load(tx, a.InstanceID)
		if err != nil {
			return err
		}
		if r.Instance.Execution != nil && r.Instance.Execution.Attempt == a.Result.Attempt {
			r.Instance.Execution.EvidenceExpired = true
			if err := put(tx, "instances", a.InstanceID, r); err != nil {
				return err
			}
		}
		return put(tx, "evidence_deletions", a.InstanceID+"/"+a.Result.Attempt, intent)
	}); err != nil {
		return err
	}
	if !committed {
		return nil
	}
	return s.finishEvidenceDeletion(intent)
}

func (s *service) finishEvidenceDeletion(d evidenceDeletion) error {
	if !validID(d.InstanceID) || d.Attempt != "" && !validID(d.Attempt) {
		return problem("state_corrupt", "invalid evidence deletion identity")
	}
	if err := s.validateEvidencePaths("logs", filepath.Join("artifacts", d.InstanceID)); err != nil {
		return err
	}
	if d.Attempt == "" {
		if err := s.removeEvidence(d.InstanceID); err != nil {
			return err
		}
	} else {
		if err := os.RemoveAll(filepath.Join(s.directory, "artifacts", d.InstanceID, d.Attempt)); err != nil {
			return err
		}
		if err := os.Remove(filepath.Join(s.directory, "logs", d.InstanceID+"-"+d.Attempt+"-output.ndjson")); err != nil && !os.IsNotExist(err) {
			return err
		}
		if err := s.removeLegacyAttempt(d.InstanceID, d.Attempt); err != nil {
			return err
		}
	}
	return s.store.db.Update(func(tx *bolt.Tx) error {
		return tx.Bucket([]byte("evidence_deletions")).Delete([]byte(d.InstanceID + "/" + d.Attempt))
	})
}

// Only legacy mixed files need rewriting. Atomic replacement invalidates cursors;
// a failed decode leaves the original and deletion intent for diagnosis/retry.
func (s *service) removeLegacyAttempt(id, attempt string) error {
	path := filepath.Join(s.directory, "logs", id+"-output.ndjson")
	if _, err := os.Lstat(path); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return err
	}
	output, err := os.CreateTemp(filepath.Dir(path), ".prune-")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(output.Name()) }()
	matched := false
	err = s.legacyRecords(id, func(record v1.LogRecord, line []byte) error {
		if record.Attempt == attempt {
			matched = true
			return nil
		}
		_, err := output.Write(line)
		return err
	})
	if err := errors.Join(err, output.Close()); err != nil {
		return err
	}
	if !matched {
		return nil
	}
	return os.Rename(output.Name(), path)
}

func (s *service) attemptEvidenceSize(id, attempt string) (int64, error) {
	size, err := s.attemptArtifactSize(id, attempt)
	if err != nil {
		return 0, err
	}
	path := filepath.Join(s.directory, "logs", id+"-"+attempt+"-output.ndjson")
	if info, err := os.Lstat(path); err == nil {
		if err := localipc.File(path, false); err != nil {
			return 0, err
		}
		size += info.Size()
	} else if !os.IsNotExist(err) {
		return 0, err
	}
	err = s.legacyRecords(id, func(record v1.LogRecord, line []byte) error {
		if record.Attempt == attempt {
			size += int64(len(line))
		}
		return nil
	})
	return size, err
}

func (s *service) attemptArtifactSize(id, attempt string) (int64, error) {
	var size int64
	if err := s.validateEvidencePaths("logs", filepath.Join("artifacts", id, attempt)); err != nil {
		return 0, err
	}
	root := filepath.Join(s.directory, "artifacts", id, attempt)
	err := filepath.WalkDir(root, func(_ string, d fs.DirEntry, err error) error {
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if d.Type()&os.ModeSymlink != 0 {
			return problem("permissions", "evidence contains a link")
		}
		info, err := d.Info()
		if err == nil {
			size += info.Size()
		}
		return err
	})
	if err != nil {
		return 0, err
	}
	return size, nil
}

// legacyRecords validates one bounded record at a time; callback bytes are borrowed.
func (s *service) legacyRecords(id string, visit func(v1.LogRecord, []byte) error) error {
	path := filepath.Join(s.directory, "logs", id+"-output.ndjson")
	if _, err := os.Lstat(path); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return err
	}
	if err := localipc.File(path, false); err != nil {
		return err
	}
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()
	reader := bufio.NewReaderSize(file, 64<<10)
	for {
		line, err := reader.ReadSlice('\n')
		if len(line) > 0 {
			var record v1.LogRecord
			if line[len(line)-1] != '\n' || json.Unmarshal(line, &record) != nil || record.Validate() != nil || record.InstanceID != id {
				return problem("collection_failed", "legacy log cannot be safely pruned")
			}
			if err := visit(record, line); err != nil {
				return err
			}
		}
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

// expireUnknownEvidence applies the inherited instance evidence clock to bytes
// whose results were overwritten before history existed. It never invents rows.
func (s *service) expireUnknownEvidence(id string, age time.Duration, overBytes bool) (int64, error) {
	var r record
	if err := s.store.db.View(func(tx *bolt.Tx) error { var err error; r, err = load(tx, id); return err }); err != nil {
		return 0, err
	}
	e := r.Instance.Execution
	if e == nil || active(r.Instance.Status) || e.Kept || e.CleanupFailure != "" || retentionStart(e) == "" {
		return 0, nil
	}
	stamp, err := time.Parse(time.RFC3339Nano, retentionStart(e))
	if err != nil {
		return 0, err
	}
	if time.Since(stamp) < age && !overBytes {
		return 0, nil
	}
	entries, err := s.store.attempts(id)
	if err != nil {
		return 0, err
	}
	represented := map[string]bool{}
	for _, a := range entries {
		if attemptProtected(a) {
			return 0, nil
		}
		represented[a.Result.Attempt] = true
	}
	for _, a := range r.NativeAbandonments {
		if old, err := native.PreviousBoot(a.Boot); err != nil || !old {
			return 0, nil
		}
	}
	if err := s.validateEvidencePaths("logs", filepath.Join("artifacts", id)); err != nil {
		return 0, err
	}
	unknown := map[string]int64{}
	if err := s.legacyRecords(id, func(record v1.LogRecord, line []byte) error {
		if !represented[record.Attempt] {
			unknown[record.Attempt] += int64(len(line))
		}
		return nil
	}); err != nil {
		return 0, err
	}
	directories, err := os.ReadDir(filepath.Join(s.directory, "artifacts", id))
	if err != nil && !os.IsNotExist(err) {
		return 0, err
	}
	for _, dir := range directories {
		if !validID(dir.Name()) || !dir.IsDir() {
			return 0, problem("permissions", "invalid legacy artifact attempt path")
		}
		if !represented[dir.Name()] {
			size, err := s.attemptArtifactSize(id, dir.Name())
			if err != nil {
				return 0, err
			}
			unknown[dir.Name()] += size
		}
	}
	names := make([]string, 0, len(unknown))
	for attempt := range unknown {
		names = append(names, attempt)
	}
	sort.Strings(names)
	var removed int64
	// shortcut: legacy cohorts reuse per-attempt intents; batch rewrites if measured migration latency warrants it.
	for _, attempt := range names {
		d := evidenceDeletion{InstanceID: id, Attempt: attempt}
		if err := s.store.db.Update(func(tx *bolt.Tx) error { return put(tx, "evidence_deletions", id+"/"+attempt, d) }); err != nil {
			return removed, err
		}
		if err := s.finishEvidenceDeletion(d); err != nil {
			return removed, err
		}
		removed += unknown[attempt]
	}
	return removed, nil
}

func (s *service) pruneHistory(ctx context.Context, q v1.HistoryPruneRequest) (v1.HistoryPruneResponse, error) {
	response := v1.HistoryPruneResponse{APIVersion: v1.Version, InstanceID: q.InstanceID, DryRun: q.DryRun, Attempts: []v1.PrunedAttempt{}}
	age, err := validatePrune(q)
	if err != nil {
		return response, err
	}
	ctx, finish, err := s.beginMutation(ctx)
	if err != nil {
		return response, err
	}
	defer finish()
	if err := lockExportEvidence(ctx, &s.evidence.mu); err != nil {
		return response, err
	}
	defer s.evidence.mu.Unlock()
	unlock := s.lockMutation(q.InstanceID)
	defer unlock()
	entries, err := s.store.attempts(q.InstanceID)
	if err != nil {
		return response, err
	}
	selected := []v1.Attempt{}
	completed := 0
	for _, a := range entries {
		stamp := retentionStart(&a.Result)
		if stamp == "" {
			continue
		}
		completed++
		t, err := time.Parse(time.RFC3339Nano, stamp)
		if err != nil {
			return response, err
		}
		reasons := []string{}
		if age > 0 && time.Since(t) > age {
			reasons = append(reasons, "older_than")
		}
		if q.Keep != nil && completed > *q.Keep {
			reasons = append(reasons, "keep")
		}
		if len(reasons) == 0 {
			continue
		}
		size, err := s.attemptEvidenceSize(a.InstanceID, a.Result.Attempt)
		if err != nil {
			return response, err
		}
		protected := attemptProtected(a)
		response.Attempts = append(response.Attempts, v1.PrunedAttempt{Reasons: reasons, Protection: attemptProtection(a), Attempt: a.Result.Attempt, Protected: protected, Bytes: size})
		if !protected {
			selected = append(selected, a)
		}
	}
	encoded, err := json.Marshal(response)
	if err != nil {
		return response, err
	}
	if len(encoded) > 512<<10 {
		return response, problem("history_limit", "prune selection exceeds response bound; use a narrower age selector")
	}
	if !q.DryRun {
		for _, a := range selected {
			if err := s.deleteAttempt(a, true); err != nil {
				return response, err
			}
		}
	}
	return response, nil
}

func (s *service) retryEvidenceDeletions() error {
	var pending []evidenceDeletion
	if err := s.store.db.View(func(tx *bolt.Tx) error {
		return tx.Bucket([]byte("evidence_deletions")).ForEach(func(_, v []byte) error {
			var d evidenceDeletion
			if err := json.Unmarshal(v, &d); err != nil {
				return err
			}
			pending = append(pending, d)
			return nil
		})
	}); err != nil {
		return err
	}
	for _, d := range pending {
		lock := s.mutationLock(d.InstanceID)
		if !lock.TryLock() {
			continue
		}
		err := s.finishEvidenceDeletion(d)
		lock.Unlock()
		if err != nil {
			return err
		}
	}
	return nil
}

func (s *service) eraseDestroyed(id string) (v1.Instance, error) {
	var instance v1.Instance
	err := s.store.db.Update(func(tx *bolt.Tx) error {
		r, err := load(tx, id)
		if err != nil {
			return err
		}
		r.Instance.Status = v1.Destroyed
		r.Instance.Operation.Status = v1.Destroyed
		r.Instance.Execution = nil
		r.Instance.Operation.Reason = ""
		r.Instance.LeaseExpiresAt = ""
		r.LeaseHash = ""
		r.NativeAbandonments = nil
		r.Instance.NativeOwnershipWarnings = nil
		// Keep only recorded identity/location, rather than launch selections/details.
		p := r.Instance.Plan
		r.Instance.Plan = v1.PlanResponse{APIVersion: v1.Version, Project: p.Project, Scene: p.Scene, Lifetime: p.Lifetime, ManifestPath: p.ManifestPath, Checkout: p.Checkout, Components: []v1.PlannedComponent{}}
		if err := put(tx, "instances", id, r); err != nil {
			return err
		}
		instance = r.Instance
		b := tx.Bucket([]byte("attempts"))
		c := b.Cursor()
		prefix := id + "/"
		for k, _ := c.Seek([]byte(prefix)); k != nil && strings.HasPrefix(string(k), prefix); k, _ = c.Next() {
			if err := c.Delete(); err != nil {
				return err
			}
		}
		if err := tx.Bucket([]byte("attempt_sequences")).Delete([]byte(id)); err != nil {
			return err
		}
		pending := tx.Bucket([]byte("evidence_deletions")).Cursor()
		for k, _ := pending.Seek([]byte(prefix)); k != nil && strings.HasPrefix(string(k), prefix); k, _ = pending.Next() {
			if err := pending.Delete(); err != nil {
				return err
			}
		}
		for _, bucket := range []string{"runtime", "snapshots", "secrets"} {
			if err := tx.Bucket([]byte(bucket)).Delete([]byte(id)); err != nil {
				return err
			}
		}
		key := p.Project + "/" + r.Instance.Checkout.ID + "/" + p.Scene
		index := tx.Bucket([]byte("persistent"))
		if string(index.Get([]byte(key))) == id {
			if err := index.Delete([]byte(key)); err != nil {
				return err
			}
		}
		return put(tx, "evidence_deletions", id+"/", evidenceDeletion{InstanceID: id})
	})
	if err != nil {
		return instance, err
	}
	return instance, s.finishEvidenceDeletion(evidenceDeletion{InstanceID: id})
}

func (s *service) retentionAttempts() int {
	if s.evidence.attempts == 0 {
		return DefaultRetentionAttempts
	}
	return s.evidence.attempts
}

// Reuse export's component-wise link checks before evidence traversal/removal;
// an evidence-parent link must never redirect pruning into application storage.
func (s *service) validateEvidencePaths(paths ...string) error {
	root, err := os.OpenRoot(s.directory)
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	for _, path := range paths {
		if err := exportSourcePath(root, path); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}
