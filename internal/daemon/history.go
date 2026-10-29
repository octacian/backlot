package daemon

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	v1 "github.com/octacian/backlot/api/v1"
	bolt "go.etcd.io/bbolt"
)

func attemptKey(id string, sequence int64) string { return fmt.Sprintf("%s/%020d", id, sequence) }

func attemptSource(i v1.Instance) v1.ExportSource {
	return v1.ExportSource{Project: i.Plan.Project, Scene: i.Plan.Scene, Lifetime: i.Plan.Lifetime, CheckoutID: i.Checkout.ID, ManifestDigest: i.Plan.ManifestDigest, ConfigDigest: i.Plan.ConfigDigest, EffectiveConfigDigest: i.Plan.EffectiveConfigDigest}
}

// syncAttempt covers every transactional instance writer. A missing same-attempt row
// is deliberately pruned, so a later cleanup/progress update must not recreate it.
func syncAttempt(tx *bolt.Tx, id string, r record) error {
	e := r.Instance.Execution
	if e == nil || r.Instance.Status == v1.Destroyed {
		return nil
	}
	var found *v1.Attempt
	n, err := strconv.ParseInt(string(tx.Bucket([]byte("attempt_sequences")).Get([]byte(id))), 10, 64)
	if err == nil {
		if data := tx.Bucket([]byte("attempts")).Get([]byte(attemptKey(id, n))); data != nil {
			var current v1.Attempt
			if err := json.Unmarshal(data, &current); err != nil {
				return err
			}
			if current.Result.Attempt == e.Attempt {
				found = &current
			}
		}
	}
	if found == nil {
		if data := tx.Bucket([]byte("instances")).Get([]byte(id)); data != nil {
			var old record
			if err := json.Unmarshal(data, &old); err != nil {
				return err
			}
			if old.Instance.Execution != nil && old.Instance.Execution.Attempt == e.Attempt {
				return nil
			}
		}
		return seedAttempt(tx, r, "")
	}
	found.Status = r.Instance.Status
	found.Result = publicAttemptResult(*e)
	found.NativeOwnershipWarnings = abandonmentWarnings(r.NativeAbandonments)
	return put(tx, "attempts", attemptKey(id, found.Sequence), found)
}

func seedAttempt(tx *bolt.Tx, r record, provenance string) error {
	i := r.Instance
	if i.Execution == nil {
		return nil
	}
	if !validID(i.Execution.Attempt) {
		return problem("state_corrupt", "invalid attempt identity")
	}
	seqs := tx.Bucket([]byte("attempt_sequences"))
	n := int64(0)
	if raw := seqs.Get([]byte(i.ID)); raw != nil {
		var err error
		n, err = strconv.ParseInt(string(raw), 10, 64)
		if err != nil || n <= 0 || n == 1<<63-1 {
			return problem("state_corrupt", "invalid attempt acceptance sequence")
		}
	}
	n++
	if err := seqs.Put([]byte(i.ID), []byte(strconv.FormatInt(n, 10))); err != nil {
		return err
	}
	a := v1.Attempt{Provenance: "cleanup_only", InstanceID: i.ID, Sequence: n, Status: i.Status, CreatedAt: i.CreatedAt, Source: attemptSource(i), Result: publicAttemptResult(*i.Execution), NativeOwnershipWarnings: abandonmentWarnings(r.NativeAbandonments)}
	if data := tx.Bucket([]byte("runtime")).Get([]byte(i.ID)); data != nil {
		journal, err := decodeRuntime(data)
		if err != nil {
			return err
		}
		if journal.Attempt == a.Result.Attempt {
			a.Provenance = "accepted"
			a.StartedAt = journal.StartedAt
			if journal.ExportSource != nil {
				a.Source = *journal.ExportSource
				a.Source.GitHead = immutableGitCommit(a.Source.GitHead)
			}
		}
	}
	if provenance != "" {
		a.Provenance = provenance
	}
	return put(tx, "attempts", attemptKey(i.ID, n), a)
}

func migrateStateEleven(tx *bolt.Tx) error {
	for _, name := range stateElevenBuckets {
		if tx.Bucket([]byte(name)) == nil {
			return problem("state_corrupt", "source state bucket missing")
		}
	}
	for _, name := range []string{"attempts", "attempt_sequences", "evidence_deletions"} {
		if tx.Bucket([]byte(name)) != nil {
			return problem("state_corrupt", "history namespace already exists")
		}
	}
	if err := validateStateAuthority(tx); err != nil {
		return err
	}
	for _, name := range []string{"attempts", "attempt_sequences", "evidence_deletions"} {
		if _, err := tx.CreateBucket([]byte(name)); err != nil {
			return problem("state_corrupt", "history namespace already exists")
		}
	}
	return tx.Bucket([]byte("instances")).ForEach(func(k, v []byte) error {
		var r record
		if err := decodeRecord(v, string(k), &r); err != nil {
			return err
		}
		return seedAttempt(tx, r, "legacy_current")
	})
}

func (s *store) attempts(id string) ([]v1.Attempt, error) {
	out := []v1.Attempt{}
	err := s.db.View(func(tx *bolt.Tx) error {
		if _, err := load(tx, id); err != nil {
			return err
		}
		c := tx.Bucket([]byte("attempts")).Cursor()
		prefix := id + "/"
		for k, v := c.Seek([]byte(prefix)); k != nil && strings.HasPrefix(string(k), prefix); k, v = c.Next() {
			var a v1.Attempt
			if err := json.Unmarshal(v, &a); err != nil {
				return err
			}
			out = append(out, a)
		}
		return nil
	})
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, err
}
func attemptProtection(a v1.Attempt) string {
	if active(a.Status) {
		return "active"
	}
	if a.Result.Kept {
		return "kept"
	}
	if a.Result.CleanupFailure != "" {
		return "cleanup_unresolved"
	}
	if retentionStart(&a.Result) == "" {
		return "completion_unknown"
	}
	return ""
}
func attemptProtected(a v1.Attempt) bool { return attemptProtection(a) != "" }

func (s *store) attempt(q v1.AttemptRequest) (v1.AttemptResponse, error) {
	r := v1.AttemptResponse{APIVersion: v1.Version}
	if !validID(q.InstanceID) || q.Attempt != "" && !validID(q.Attempt) || (q.Attempt == "") == !q.LastCompleted {
		return r, problem("invalid_request", "select one exact attempt or last_completed")
	}
	entries, err := s.attempts(q.InstanceID)
	if err != nil {
		return r, err
	}
	for _, a := range entries {
		if q.Attempt == a.Result.Attempt || q.LastCompleted && a.Completed() {
			r.Attempt = a
			return r, nil
		}
	}
	return r, problem("attempt_unavailable", "selected attempt is absent or pruned")
}
func (s *store) history(q v1.HistoryRequest) (v1.HistoryResponse, error) {
	r := v1.HistoryResponse{APIVersion: v1.Version, InstanceID: q.InstanceID, Attempts: []v1.AttemptSummary{}}
	if !validID(q.InstanceID) || q.Limit < 0 || q.Limit > 100 || len(q.After) > 256 {
		return r, problem("invalid_request", "history requires recorded instance and limit 0 through 100")
	}
	before := int64(0)
	if q.After != "" {
		data, err := base64.RawURLEncoding.DecodeString(q.After)
		p := strings.Split(string(data), "/")
		if err != nil || len(p) != 2 || p[0] != q.InstanceID {
			return r, problem("invalid_cursor", "history cursor does not match instance")
		}
		before, err = strconv.ParseInt(p[1], 10, 64)
		if err != nil || before == 0 {
			return r, problem("invalid_cursor", "invalid history continuation")
		}
	}
	entries, err := s.attempts(q.InstanceID)
	if err != nil {
		return r, err
	}
	limit := q.Limit
	if limit == 0 {
		limit = 100
	}
	for _, a := range entries {
		if before != 0 && a.Sequence >= before {
			continue
		}
		if len(r.Attempts) == limit {
			last := r.Attempts[len(r.Attempts)-1]
			r.Continuation = base64.RawURLEncoding.EncodeToString([]byte(q.InstanceID + "/" + strconv.FormatInt(last.Sequence, 10)))
			break
		}
		r.Attempts = append(r.Attempts, v1.AttemptSummary{Provenance: a.Provenance, Attempt: a.Result.Attempt, Sequence: a.Sequence, Status: a.Status, CompletedAt: a.Result.CompletedAt, EvidenceExpired: a.Result.EvidenceExpired, Protected: attemptProtected(a)})
	}
	return r, nil
}

func historicalInstance(i v1.Instance, a v1.Attempt) v1.Instance {
	i.Execution = &a.Result
	i.Status = a.Status
	i.Operation.Status = a.Status
	i.NativeOwnershipWarnings = a.NativeOwnershipWarnings
	i.CreatedAt = a.CreatedAt
	i.Plan.Project = a.Source.Project
	i.Plan.Scene = a.Source.Scene
	i.Plan.Lifetime = a.Source.Lifetime
	i.Plan.ManifestDigest = a.Source.ManifestDigest
	i.Plan.ConfigDigest = a.Source.ConfigDigest
	i.Plan.EffectiveConfigDigest = a.Source.EffectiveConfigDigest
	return i
}

func validatePrune(q v1.HistoryPruneRequest) (time.Duration, error) {
	if err := q.Validate(); err != nil {
		return 0, err
	}
	if q.OlderThan == "" {
		return 0, nil
	}
	return time.ParseDuration(q.OlderThan)
}

// Fixtures remain current private state and never acquire historical retrieval.
func publicAttemptResult(e v1.ExecutionResult) v1.ExecutionResult {
	e.Fixtures = nil
	if e.Components == nil {
		e.Components = []v1.ComponentResult{}
	}
	return e
}

// History is diagnostic data, but corrupted identity/provenance must fail before
// any runtime recovery can act on neighboring private journals.
func validateAttemptState(tx *bolt.Tx) error {
	if tx.Bucket([]byte("attempts")) == nil {
		return nil
	} // retained source layouts
	return tx.Bucket([]byte("attempts")).ForEach(func(k, v []byte) error {
		var a v1.Attempt
		if json.Unmarshal(v, &a) != nil || a.Sequence <= 0 || !validID(a.InstanceID) || !validID(a.Result.Attempt) || string(k) != attemptKey(a.InstanceID, a.Sequence) || a.Source.Project == "" || a.Source.Scene == "" || !validID(a.Source.CheckoutID) || a.Source.Lifetime != v1.Persistent && a.Source.Lifetime != v1.Disposable || len(a.Result.Fixtures) != 0 {
			return problem("state_corrupt", "invalid public attempt history")
		}
		if _, err := load(tx, a.InstanceID); err != nil {
			return err
		}
		n, err := strconv.ParseInt(string(tx.Bucket([]byte("attempt_sequences")).Get([]byte(a.InstanceID))), 10, 64)
		if err != nil || n < a.Sequence {
			return problem("state_corrupt", "invalid attempt acceptance sequence")
		}
		for _, stamp := range []string{a.CreatedAt, a.StartedAt, a.Result.CompletedAt, a.Result.EvidenceObservedAt} {
			if stamp != "" {
				if _, err := time.Parse(time.RFC3339Nano, stamp); err != nil {
					return err
				}
			}
		}
		return nil
	})
}

func validateHistoryDeletionState(tx *bolt.Tx) error {
	if tx.Bucket([]byte("attempt_sequences")) == nil {
		return nil
	}
	if err := tx.Bucket([]byte("attempt_sequences")).ForEach(func(k, v []byte) error {
		if !validID(string(k)) {
			return problem("state_corrupt", "invalid sequence identity")
		}
		if _, err := load(tx, string(k)); err != nil {
			return err
		}
		n, err := strconv.ParseInt(string(v), 10, 64)
		if err != nil || n <= 0 || strconv.FormatInt(n, 10) != string(v) {
			return problem("state_corrupt", "invalid attempt acceptance sequence")
		}
		return nil
	}); err != nil {
		return err
	}
	return tx.Bucket([]byte("evidence_deletions")).ForEach(func(k, v []byte) error {
		var d evidenceDeletion
		if json.Unmarshal(v, &d) != nil || !validID(d.InstanceID) || d.Attempt != "" && !validID(d.Attempt) || string(k) != d.InstanceID+"/"+d.Attempt {
			return problem("state_corrupt", "invalid evidence deletion intent")
		}
		r, err := load(tx, d.InstanceID)
		if err != nil {
			return err
		}
		if d.Attempt == "" && r.Instance.Status != v1.Destroyed {
			return problem("state_corrupt", "instance erasure requires destroyed identity")
		}
		return nil
	})
}

func (s *store) otherRetainedEvidence(id, attempt string) (bool, error) {
	entries, err := s.attempts(id)
	if err != nil {
		return false, err
	}
	for _, a := range entries {
		if a.Result.Attempt != attempt && !a.Result.EvidenceExpired {
			return true, nil
		}
	}
	return false, nil
}
