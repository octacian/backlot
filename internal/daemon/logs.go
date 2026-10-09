package daemon

import (
	"bufio"
	"encoding/json"
	v1 "github.com/octacian/backlot/api/v1"
	"github.com/octacian/backlot/internal/localipc"
	"io"
	"os"
	"path/filepath"
)

func (s *service) logs(request v1.LogsRequest) (v1.LogsResponse, error) {
	s.evidence.mu.Lock()
	defer s.evidence.mu.Unlock()
	response := v1.LogsResponse{APIVersion: v1.Version, Records: []v1.LogRecord{}, NextOffset: request.Offset}
	if !validID(request.InstanceID) || request.Offset < 0 {
		return response, problem("invalid_request", "recorded instance_id and nonnegative log offset required")
	}
	instance, err := s.store.inspect(request.InstanceID)
	if err != nil {
		return response, err
	}
	response.Active = instance.Status == v1.Starting || instance.Status == v1.RuntimeReady || instance.Status == v1.Stopping
	if instance.Execution != nil {
		response.Gap = instance.Execution.CollectionFailure
		if instance.Execution.EvidenceExpired {
			response.Gap = "evidence expired by retention policy"
			return response, nil
		}
		response.Active = response.Active || instance.Execution.Kept || instance.Execution.CleanupFailure != ""
	}
	logDir := filepath.Join(s.directory, "logs")
	if _, err := os.Lstat(logDir); os.IsNotExist(err) {
		return response, nil
	} else if err != nil {
		return response, err
	}
	if err := localipc.Directory(logDir, false); err != nil {
		return response, err
	}
	paths, err := filepath.Glob(filepath.Join(logDir, request.InstanceID+"-*.ndjson"))
	if err != nil {
		return response, err
	}
	index := 0
	size := 0
	for _, path := range paths {
		if err := localipc.File(path, false); err != nil {
			return response, err
		}
		file, err := os.Open(path)
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
