package daemon

import (
	"bufio"
	"encoding/json"
	v1 "github.com/octacian/backlot/api/v1"
	"github.com/octacian/backlot/internal/localipc"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
)

func (s *service) logs(request v1.LogsRequest) (v1.LogsResponse, error) {
	s.evidence.mu.Lock()
	defer s.evidence.mu.Unlock()
	return s.logsLocked(request)
}

// logsLocked requires evidence.mu, also held by portable snapshot collection.
func (s *service) logsLocked(request v1.LogsRequest) (v1.LogsResponse, error) {
	return s.logsSourceLocked(request, nil)
}

// logsSourceLocked additionally fences export reads to the private state root and
// rejects unsafe sources before decoding can turn them into copied records.
func (s *service) logsSourceLocked(request v1.LogsRequest, source *os.Root) (v1.LogsResponse, error) {
	return s.logsForInstance(request, source, nil)
}

func (s *service) logsForInstance(request v1.LogsRequest, source *os.Root, selected *v1.Instance) (v1.LogsResponse, error) {
	response := v1.LogsResponse{APIVersion: v1.Version, Records: []v1.LogRecord{}, NextOffset: request.Offset}
	if !validID(request.InstanceID) || request.Offset < 0 {
		return response, problem("invalid_request", "recorded instance_id and nonnegative log offset required")
	}
	instance, err := s.store.inspect(request.InstanceID)
	if err != nil {
		return response, err
	}
	if selected != nil {
		instance = *selected
	}
	response.NativeOwnershipWarnings = instance.NativeOwnershipWarnings
	response.Active = instance.Status == v1.Starting || instance.Status == v1.RuntimeReady || instance.Status == v1.Stopping
	if instance.Execution != nil {
		response.Gap = instance.Execution.CollectionFailure
		if instance.Execution.EvidenceExpired {
			response.Gap = "evidence expired by retention policy"
			if selected != nil {
				return response, nil
			}
			other, err := s.store.otherRetainedEvidence(instance.ID, instance.Execution.Attempt)
			if err != nil {
				return response, err
			}
			if !other {
				return response, nil
			}
		}
		response.Active = response.Active || instance.Execution.Kept || instance.Execution.CleanupFailure != ""
	}
	logDir := filepath.Join(s.directory, "logs")
	if source != nil {
		if err := exportSourcePath(source, "logs"); err != nil && !os.IsNotExist(err) {
			return response, err
		}
	}
	if _, err := os.Lstat(logDir); os.IsNotExist(err) {
		return response, nil
	} else if err != nil {
		return response, err
	}
	if err := localipc.Directory(logDir, false); err != nil {
		return response, err
	}
	paths, err := filepath.Glob(filepath.Join(logDir, request.InstanceID+"-*.ndjson"))
	if source != nil {
		// Preflight every selected source before decoding any records. A corrupt
		// early page in partial mode must not hide a later unsafe source.
		var names []string
		names, err = fs.Glob(source.FS(), "logs/"+request.InstanceID+"-*.ndjson")
		paths = nil
		if err == nil {
			for _, name := range names {
				if err := exportSourcePath(source, name); err != nil {
					return response, err
				}
				info, err := source.Lstat(name)
				if err != nil {
					return response, err
				}
				if !exportLogOwned(info) {
					return response, problem("export_unsafe", "retained log source must be private, owned, regular and single-linked")
				}
				paths = append(paths, filepath.Join(s.directory, name))
			}
		}
	}
	if err != nil {
		return response, err
	}
	if selected != nil {
		paths = selectedLogPaths(paths, instance.ID, instance.Execution.Attempt)
	}
	// Aggregate offsets must append across replacements even when random attempt
	// IDs sort before their predecessors. Finite log pages keep lexical ordering.
	attempts, err := s.store.attempts(instance.ID)
	if err != nil {
		return response, err
	}
	order := map[string]int64{}
	for _, a := range attempts {
		order[instance.ID+"-"+a.Result.Attempt+"-output.ndjson"] = a.Sequence
	}
	sort.Slice(paths, func(i, j int) bool {
		left, right := order[filepath.Base(paths[i])], order[filepath.Base(paths[j])]
		if left != right {
			return left < right
		}
		return paths[i] < paths[j]
	})
	index := 0
	size := 0
	for _, path := range paths {
		if err := localipc.File(path, false); err != nil {
			return response, err
		}
		var file *os.File
		if source != nil {
			name := filepath.Join("logs", filepath.Base(path))
			if err := exportSourcePath(source, name); err != nil {
				return response, err
			}
			info, err := source.Lstat(name)
			if err != nil {
				return response, err
			}
			if !exportLogOwned(info) {
				return response, problem("export_unsafe", "retained log source must be private, owned, regular and single-linked")
			}
			file, err = source.Open(name)
			if err == nil {
				info, statErr := file.Stat()
				if statErr != nil || !exportLogOwned(info) {
					_ = file.Close()
					return response, problem("export_unsafe", "opened retained log source is unsafe")
				}
			}
		} else {
			file, err = os.Open(path)
		}
		if err != nil {
			return response, err
		}
		reader := bufio.NewReaderSize(file, 64<<10)
		for {
			line, err := reader.ReadSlice('\n')
			if err == io.EOF {
				if len(line) > 0 && !response.Active {
					_ = file.Close()
					return response, problem("collection_failed", "incomplete retained output record")
				}
				break
			}
			if err != nil {
				_ = file.Close()
				return response, err
			}
			if len(line) > 64<<10 {
				_ = file.Close()
				return response, problem("collection_failed", "retained log record exceeds bound")
			}
			var record v1.LogRecord
			if err := json.Unmarshal(line, &record); err != nil {
				_ = file.Close()
				return response, problem("collection_failed", "retained log record corrupt or interrupted")
			}
			index++
			if index <= request.Offset {
				continue
			}
			if size+len(line) > 512<<10 {
				_ = file.Close()
				return response, nil
			}
			response.NextOffset = index
			if request.Component == "" || request.Component == record.Component {
				response.Records = append(response.Records, record)
				size += len(line)
			}
		}
		if err := file.Close(); err != nil {
			return response, err
		}
	}
	return response, nil
}
