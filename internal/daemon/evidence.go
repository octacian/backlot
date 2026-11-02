package daemon

import (
	"archive/tar"
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/moby/moby/client"
	v1 "github.com/octacian/backlot/api/v1"
	"github.com/octacian/backlot/internal/localipc"
	"github.com/octacian/backlot/internal/plan"
	bolt "go.etcd.io/bbolt"
)

// Evidence operations serialize readers and pruning; producers are protected by
// active instance status until collection and runtime cleanup have joined.
type evidencePolicy struct {
	mu       sync.Mutex
	age      time.Duration
	bytes    int64
	attempts int
}

func (s *service) collectArtifacts(id string, snapshot plan.Snapshot, entry *execution, result *v1.ExecutionResult) error {
	var failures error
	for _, component := range snapshot.Plan.Components {
		// Only a launched workload owns artifacts for this attempt. In particular,
		// a cancelled output-lock waiter must not read its active peer's checkout.
		if entry.components[component.Name] == nil {
			continue
		}
		for name, source := range component.Artifacts {
			destination := filepath.Join(s.directory, "artifacts", id, result.Attempt, component.Name, name)
			err := localipc.Directory(destination, true)
			if err == nil {
				if component.Runtime == v1.Native {
					err = copyNativeArtifact(filepath.Dir(snapshot.Plan.ManifestPath), source.Path, destination)
				} else {
					work, ok := entry.components[component.Name].(*dockerWork)
					if !ok {
						err = errors.New("component did not launch")
					} else {
						err = copyContainerArtifact(work, source.Path, destination)
					}
				}
			}
			if err != nil {
				// Files, engine messages and paths may contain application credentials.
				failures = errors.Join(failures, problem("collection_failed", "artifact "+component.Name+"/"+name+" could not be collected"))
				continue
			}
			result.Artifacts = append(result.Artifacts, v1.ArtifactRecord{Component: component.Name, Name: name, Attempt: result.Attempt, Path: destination})
		}
	}
	return failures
}

// os.Root prevents path races from escaping the checkout. Links are rejected,
// including internal links, rather than recreating links into runtime resources.
func copyNativeArtifact(checkout, source, destination string) error {
	root, err := os.OpenRoot(checkout)
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	return fs.WalkDir(root.FS(), source, func(name string, item fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(source, name)
		if err != nil {
			return err
		}
		if relative == "." {
			relative = filepath.Base(source)
		} else {
			relative = filepath.Join(filepath.Base(source), relative)
		}
		target := filepath.Join(destination, relative)
		if item.Type()&os.ModeSymlink != 0 {
			return errors.New("artifact links are unsupported")
		}
		if item.IsDir() {
			return os.MkdirAll(target, 0700)
		}
		info, err := item.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return errors.New("artifact must be regular")
		}
		input, err := root.Open(name)
		if err != nil {
			return err
		}
		defer func() { _ = input.Close() }()
		return copyArtifactFile(target, io.NewSectionReader(input, 0, info.Size()))
	})
}
func copyArtifactFile(target string, input io.Reader) error {
	if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
		return err
	}
	file, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(file, input)
	return errors.Join(copyErr, file.Close())
}
func copyContainerArtifact(work *dockerWork, source, destination string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := verifyDockerResource(ctx, work.engine, work.resource); err != nil {
		return err
	}
	output, err := work.engine.CopyFromContainer(ctx, resourceHandle(work.resource), client.CopyFromContainerOptions{SourcePath: source})
	if err != nil {
		return err
	}
	defer func() { _ = output.Content.Close() }()
	return extractArtifact(output.Content, destination)
}
func extractArtifact(input io.Reader, destination string) error {
	reader := tar.NewReader(input)
	count := 0
	for {
		header, err := reader.Next()
		if err == io.EOF {
			if count == 0 {
				return errors.New("empty artifact archive")
			}
			return nil
		}
		if err != nil {
			return err
		}
		name := strings.TrimSuffix(header.Name, "/")
		if !filepath.IsLocal(name) || strings.Contains(name, "\\") {
			return errors.New("unsafe artifact archive")
		}
		target := filepath.Join(destination, name)
		switch header.Typeflag {
		case tar.TypeDir:
			err = os.MkdirAll(target, 0700)
		case tar.TypeReg:
			err = copyArtifactFile(target, reader)
		default:
			return errors.New("artifact links and special files are unsupported")
		}
		if err != nil {
			return err
		}
		count++
	}
}

func (s *service) pruneEvidence() error {
	s.evidence.mu.Lock()
	defer s.evidence.mu.Unlock()
	if err := s.retryEvidenceDeletions(); err != nil {
		return err
	}
	if err := s.observeLegacyEvidence(); err != nil {
		return err
	}
	age, limit := s.evidenceLimits()
	var ids []string
	if err := s.store.db.View(func(tx *bolt.Tx) error {
		return tx.Bucket([]byte("instances")).ForEach(func(k, _ []byte) error { ids = append(ids, string(k)); return nil })
	}); err != nil {
		return err
	}
	type candidate struct {
		id, attempt string
		start       time.Time
		expired     bool
	}
	var all []candidate
	var total int64
	count := s.evidence.attempts
	if count == 0 {
		count = DefaultRetentionAttempts
	}
	for _, id := range ids {
		entries, err := s.store.attempts(id)
		if err != nil {
			return err
		}
		completed := 0
		for _, a := range entries {
			if retentionStart(&a.Result) != "" {
				completed++
			}
			lock := s.mutationLock(id)
			if !lock.TryLock() {
				continue
			}
			if completed > count && !attemptProtected(a) {
				err = s.deleteAttempt(a, true)
			} else {
				if !attemptProtected(a) {
					start, err := time.Parse(time.RFC3339Nano, retentionStart(&a.Result))
					if err != nil {
						return err
					}
					all = append(all, candidate{id: id, attempt: a.Result.Attempt, start: start, expired: a.Result.EvidenceExpired})
				}
			}
			lock.Unlock()
			if err != nil {
				return err
			}
		}
		i, err := s.store.inspect(id)
		if err != nil {
			return err
		}
		if e := i.Execution; e != nil && retentionStart(e) != "" {
			start, err := time.Parse(time.RFC3339Nano, retentionStart(e))
			if err != nil {
				return err
			}
			all = append(all, candidate{id: id, start: start})
		}
		size, err := s.evidenceSize(id)
		if err != nil {
			return err
		}
		total += size
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].start.Equal(all[j].start) {
			return all[i].attempt == "" && all[j].attempt != ""
		}
		return all[i].start.Before(all[j].start)
	})
	for _, c := range all {
		if !c.expired && time.Since(c.start) < age && total <= limit {
			continue
		}
		lock := s.mutationLock(c.id)
		if !lock.TryLock() {
			continue
		}
		var size int64
		var err error
		if c.attempt == "" {
			size, err = s.expireUnknownEvidence(c.id, age, total > limit)
		} else {
			selected, selectErr := s.store.attempt(v1.AttemptRequest{InstanceID: c.id, Attempt: c.attempt})
			err = selectErr
			if err == nil {
				size, err = s.attemptEvidenceSize(c.id, c.attempt)
				if err == nil {
					err = s.deleteAttempt(selected.Attempt, false)
				}
			}
		}
		lock.Unlock()
		if err != nil {
			return err
		}
		total -= size
	}

	return nil
}
func (s *service) evidenceSize(id string) (int64, error) {
	var size int64
	for _, directory := range []string{filepath.Join(s.directory, "artifacts", id), filepath.Join(s.directory, "logs")} {
		err := filepath.WalkDir(directory, func(path string, item fs.DirEntry, err error) error {
			if os.IsNotExist(err) {
				return nil
			}
			if err != nil {
				return err
			}
			if item.IsDir() {
				return nil
			}
			if filepath.Base(directory) == "logs" && !strings.HasPrefix(item.Name(), id+"-") {
				return nil
			}
			if item.Type()&os.ModeSymlink != 0 {
				return problem("permissions", "evidence contains a link")
			}
			info, err := item.Info()
			if err != nil {
				return err
			}
			size += info.Size()
			return nil
		})
		if err != nil {
			return 0, err
		}
	}
	return size, nil
}
func (s *service) removeEvidence(id string) error {
	if err := s.validateEvidencePaths("logs", filepath.Join("artifacts", id)); err != nil {
		return err
	}
	if err := os.RemoveAll(filepath.Join(s.directory, "artifacts", id)); err != nil {
		return err
	}
	paths, err := filepath.Glob(filepath.Join(s.directory, "logs", id+"-*.ndjson"))
	if err != nil {
		return err
	}
	for _, path := range paths {
		if err := os.Remove(path); err != nil {
			return err
		}
	}
	return nil
}

// Legacy completion time is unknown. Persist the first completed observation once,
// independently of file mtimes, so restarting the daemon cannot renew its age.
func (s *service) observeLegacyEvidence() error {
	observed := time.Now().UTC().Format(time.RFC3339Nano)
	return s.store.db.Update(func(tx *bolt.Tx) error {
		var updates []record
		err := tx.Bucket([]byte("instances")).ForEach(func(key, data []byte) error {
			var r record
			if err := decodeRecord(data, string(key), &r); err != nil {
				return err
			}
			result := r.Instance.Execution
			if result != nil && !active(r.Instance.Status) && result.CompletedAt == "" && result.EvidenceObservedAt == "" {
				result.EvidenceObservedAt = observed
				updates = append(updates, r)
			}
			return nil
		})
		if err != nil {
			return err
		}
		for _, r := range updates {
			if err := put(tx, "instances", r.Instance.ID, r); err != nil {
				return err
			}
		}
		var historical []v1.Attempt
		if err := tx.Bucket([]byte("attempts")).ForEach(func(_, data []byte) error {
			var a v1.Attempt
			if err := json.Unmarshal(data, &a); err != nil {
				return err
			}
			if !active(a.Status) && a.Result.CompletedAt == "" && a.Result.EvidenceObservedAt == "" {
				a.Result.EvidenceObservedAt = observed
				historical = append(historical, a)
			}
			return nil
		}); err != nil {
			return err
		}
		for _, a := range historical {
			if err := put(tx, "attempts", attemptKey(a.InstanceID, a.Sequence), a); err != nil {
				return err
			}
		}
		return nil
	})
}

func retentionStart(result *v1.ExecutionResult) string {
	if result.CompletedAt != "" {
		return result.CompletedAt
	}
	return result.EvidenceObservedAt
}

func (s *service) evidenceLimits() (time.Duration, int64) {
	age, limit := s.evidence.age, s.evidence.bytes
	if age == 0 {
		age = DefaultRetentionAge
	}
	if limit == 0 {
		limit = DefaultRetentionBytes
	}
	return age, limit
}
