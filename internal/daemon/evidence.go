package daemon

import (
	"archive/tar"
	"context"
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
	mu    sync.Mutex
	age   time.Duration
	bytes int64
}

func (s *service) collectArtifacts(id string, snapshot plan.Snapshot, entry *execution, result *v1.ExecutionResult) error {
	var failures error
	for _, component := range snapshot.Plan.Components {
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
	if err := s.observeLegacyEvidence(); err != nil {
		return err
	}
	age, limit := s.evidence.age, s.evidence.bytes
	if age == 0 {
		age = 168 * time.Hour
	}
	if limit == 0 {
		limit = 1 << 30
	}
	var completed []v1.Instance
	var total int64
	err := s.store.db.View(func(tx *bolt.Tx) error {
		return tx.Bucket([]byte("instances")).ForEach(func(key, data []byte) error {
			var r record
			if err := decodeRecord(data, string(key), &r); err != nil {
				return err
			}
			size, err := s.evidenceSize(r.Instance.ID)
			if err != nil {
				return err
			}
			total += size
			result := r.Instance.Execution
			if result != nil && retentionStart(result) != "" && !active(r.Instance.Status) && !result.Kept && result.CleanupFailure == "" {
				if _, err := time.Parse(time.RFC3339Nano, retentionStart(result)); err != nil {
					return err
				}
				completed = append(completed, r.Instance)
			}
			return nil
		})
	})
	if err != nil {
		return err
	}
	sort.Slice(completed, func(i, j int) bool {
		left, _ := time.Parse(time.RFC3339Nano, retentionStart(completed[i].Execution))
		right, _ := time.Parse(time.RFC3339Nano, retentionStart(completed[j].Execution))
		return left.Before(right)
	})
	for _, instance := range completed {
		timestamp, err := time.Parse(time.RFC3339Nano, retentionStart(instance.Execution))
		if err != nil {
			return err
		}
		if !instance.Execution.EvidenceExpired && time.Since(timestamp) < age && total <= limit {
			continue
		}
		removed, err := s.pruneInstanceEvidence(instance.ID, instance.Execution.Attempt)
		if err != nil {
			return err
		}
		total -= removed
	}
	return nil
}
func (s *service) pruneInstanceEvidence(id, attempt string) (int64, error) {
	lock := s.mutationLock(id)
	if !lock.TryLock() {
		return 0, nil
	}
	defer lock.Unlock()
	instance, err := s.store.inspect(id)
	if err != nil {
		return 0, err
	}
	if active(instance.Status) || instance.Execution == nil || instance.Execution.Kept || instance.Execution.CleanupFailure != "" || instance.Execution.Attempt != attempt {
		return 0, nil
	}
	size, err := s.evidenceSize(id)
	if err != nil {
		return 0, err
	}
	// Commit expiration before removal and retry already-expired residual files.
	// The instance mutation lock fences a restart until both operations finish.
	err = s.store.db.Update(func(tx *bolt.Tx) error {
		r, err := load(tx, id)
		if err != nil {
			return err
		}
		r.Instance.Execution.EvidenceExpired = true
		return put(tx, "instances", id, r)
	})
	if err != nil {
		return 0, err
	}
	if err := s.removeEvidence(id); err != nil {
		return 0, err
	}
	return size, nil
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
		return nil
	})
}

func retentionStart(result *v1.ExecutionResult) string {
	if result.CompletedAt != "" {
		return result.CompletedAt
	}
	return result.EvidenceObservedAt
}
