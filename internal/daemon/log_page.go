package daemon

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	v1 "github.com/octacian/backlot/api/v1"
	"github.com/octacian/backlot/internal/localipc"
	"github.com/octacian/backlot/internal/manifest"
)

const logPageBytes = 512 << 10

// logCursor is caller-held metadata, never persistence or authority to open a path.
type logCursor struct {
	Version    int    `json:"version"`
	Generation string `json:"generation"`
	Instance   string `json:"instance"`
	Filters    string `json:"filters"`
	Source     string `json:"source"`
	Identity   string `json:"identity"`
	Position   int64  `json:"position"`
	Size       int64  `json:"size"`
}

func (s *service) logPage(request v1.LogPageRequest) (v1.LogPageResponse, error) {
	r := v1.LogPageResponse{APIVersion: v1.Version, InstanceID: request.InstanceID, Records: []v1.LogRecord{}, Gaps: []v1.EvidenceGap{}, HistoricalCollection: "unknown"}
	if err := request.Validate(); err != nil {
		return r, err
	}
	s.evidence.mu.Lock()
	defer s.evidence.mu.Unlock()
	instance, err := s.store.inspect(request.InstanceID)
	if err != nil {
		return r, err
	}
	if request.Attempt != "" {
		selected, err := s.store.attempt(v1.AttemptRequest{InstanceID: request.InstanceID, Attempt: request.Attempt})
		if err != nil {
			return r, err
		}
		instance = historicalInstance(instance, selected.Attempt)
		r.HistoricalCollection = "known"
	}
	r.Active = instance.Status == v1.Starting || instance.Status == v1.RuntimeReady || instance.Status == v1.Stopping
	r.NativeOwnershipWarnings = instance.NativeOwnershipWarnings
	if e := instance.Execution; e != nil {
		r.Active = r.Active || e.Kept || e.CleanupFailure != ""
		if e.CollectionFailure != "" {
			r.Gaps = append(r.Gaps, v1.EvidenceGap{Code: "collection_incomplete", Attempt: e.Attempt, Detail: e.CollectionFailure})
		}
	}
	filters := hash(request.Component + "\x00" + request.Attempt + "\x00" + request.Stream)
	cursor := logCursor{Version: 1, Generation: s.generation, Instance: request.InstanceID, Filters: filters}
	if request.After != "" {
		data, err := base64.RawURLEncoding.DecodeString(request.After)
		if err != nil || manifest.Decode(data, ".json", &cursor) != nil || cursor.Version != 1 || cursor.Instance != request.InstanceID || cursor.Filters != filters || cursor.Position < 0 || cursor.Size < cursor.Position || cursor.Source == "" || filepath.Base(cursor.Source) != cursor.Source || !strings.HasPrefix(cursor.Source, request.InstanceID+"-") || !strings.HasSuffix(cursor.Source, ".ndjson") || cursor.Identity == "" {
			return r, problem("invalid_cursor", "cursor does not match this log query")
		}
		if cursor.Generation != s.generation {
			return r, problem("daemon_changed", "log cursor belongs to another daemon generation; start a fresh query")
		}
	}
	if e := instance.Execution; e != nil && e.EvidenceExpired {
		r.Gaps = append(r.Gaps, v1.EvidenceGap{Code: "retention_expired", Detail: "evidence expired by retention policy"})
		if request.Attempt != "" {
			return r, nil
		}
		other, err := s.store.otherRetainedEvidence(instance.ID, e.Attempt)
		if err != nil {
			return r, err
		}
		if !other {
			return r, nil
		}
	}
	changed := func() (v1.LogPageResponse, error) {
		r.Records = []v1.LogRecord{}
		r.Continuation = ""
		r.More = false
		r.Gaps = append(r.Gaps, v1.EvidenceGap{Code: "source_changed", Detail: "retained source missing, replaced or truncated; start a fresh query"})
		return r, nil
	}
	dir := filepath.Join(s.directory, "logs")
	if _, err := os.Lstat(dir); err != nil {
		if os.IsNotExist(err) {
			if request.After != "" {
				return changed()
			}
			return r, nil
		}
		return r, err
	}
	if err := localipc.Directory(dir, false); err != nil {
		return r, err
	}
	paths, err := filepath.Glob(filepath.Join(dir, request.InstanceID+"-*.ndjson"))
	if err != nil {
		return r, err
	}
	if request.Attempt != "" {
		paths = selectedLogPaths(paths, request.InstanceID, request.Attempt)
	}
	start := 0
	if request.After != "" {
		for start < len(paths) && filepath.Base(paths[start]) != cursor.Source {
			start++
		}
		if start == len(paths) {
			return changed()
		}
	}
	// Empty retained files are sources, but do not imply more unscanned bytes.
	lastNonempty := -1
	for index, path := range paths {
		info, err := os.Lstat(path)
		if err != nil {
			return r, err
		}
		if info.Size() > 0 {
			lastNonempty = index
		}
	}
	limit := request.Limit
	if limit == 0 {
		limit = 100
	}
	scanned, encoded := 0, 0
	for index := start; index < len(paths); index++ {
		path := paths[index]
		if err := localipc.File(path, false); err != nil {
			if os.IsNotExist(err) && request.After != "" {
				return changed()
			}
			return r, err
		}
		info, err := os.Lstat(path)
		if err != nil {
			return r, err
		}
		file, err := os.Open(path)
		if err != nil {
			return r, err
		}
		opened, err := file.Stat()
		if err != nil || !os.SameFile(info, opened) {
			_ = file.Close()
			return changed()
		}
		identity, err := localipc.Identity(opened)
		if err != nil {
			_ = file.Close()
			return r, err
		}
		position := int64(0)
		if request.After != "" && index == start {
			if identity != cursor.Identity || opened.Size() < cursor.Size {
				_ = file.Close()
				return changed()
			}
			position = cursor.Position
			if position > 0 {
				b := []byte{0}
				if _, err := file.ReadAt(b, position-1); err != nil || b[0] != '\n' {
					_ = file.Close()
					return r, problem("invalid_cursor", "cursor must point to a complete record boundary")
				}
				scanned++
			}
		}
		if _, err := file.Seek(position, io.SeekStart); err != nil {
			_ = file.Close()
			return r, err
		}
		cursor = logCursor{Version: 1, Generation: s.generation, Instance: request.InstanceID, Filters: filters, Source: filepath.Base(path), Identity: identity, Position: position, Size: opened.Size()}
		// LimitReader bounds actual reads too, including filtered records and partial tails.
		reader := bufio.NewReaderSize(io.LimitReader(file, min(int64(logPageBytes-scanned), opened.Size()-position)), 64<<10)
		for {
			line, readErr := reader.ReadSlice('\n')
			scanned += len(line)
			if readErr != nil {
				if errors.Is(readErr, bufio.ErrBufferFull) {
					_ = file.Close()
					return r, problem("collection_failed", "retained log record exceeds bound")
				}
				if readErr != io.EOF {
					_ = file.Close()
					return r, readErr
				}
				if len(line) > 0 && scanned < logPageBytes && !r.Active {
					_ = file.Close()
					return r, problem("collection_failed", "incomplete retained output record")
				}
				r.More = cursor.Position < opened.Size() || index < lastNonempty
				break
			}
			var record v1.LogRecord
			if manifest.Decode(bytes.TrimSpace(line), ".json", &record) != nil || record.InstanceID != request.InstanceID || record.Validate() != nil {
				_ = file.Close()
				return r, problem("collection_failed", "retained log record corrupt or interrupted")
			}
			match := (request.Component == "" || request.Component == record.Component) && (request.Attempt == "" || request.Attempt == record.Attempt) && (request.Stream == "" || request.Stream == record.Stream)
			if match {
				data, err := json.Marshal(record)
				if err != nil {
					_ = file.Close()
					return r, err
				}
				if encoded+len(data) > logPageBytes {
					r.More = true
					break
				}
				encoded += len(data)
				r.Records = append(r.Records, record)
			}
			cursor.Position += int64(len(line))
			if len(r.Records) == limit || scanned == logPageBytes {
				r.More = cursor.Position < opened.Size() || index < lastNonempty
				break
			}
		}
		if err := file.Close(); err != nil {
			return r, err
		}
		data, err := json.Marshal(cursor)
		if err != nil {
			return r, err
		}
		r.Continuation = base64.RawURLEncoding.EncodeToString(data)
		if len(r.Records) == limit || scanned == logPageBytes || cursor.Position < opened.Size() {
			return r, nil
		}
	}
	return r, nil
}

func selectedLogPaths(paths []string, id, attempt string) []string {
	out := paths[:0]
	for _, path := range paths {
		base := filepath.Base(path)
		if base == id+"-output.ndjson" || base == id+"-"+attempt+"-output.ndjson" {
			out = append(out, path)
		}
	}
	return out
}
