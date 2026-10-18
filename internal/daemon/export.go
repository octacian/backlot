package daemon

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	v1 "github.com/octacian/backlot/api/v1"
	"github.com/octacian/backlot/internal/evidenceaudit"
)

// export observes without stopping execution. Evidence/pruning and mutation locks
// fence the selected attempt until copied bytes and the report are published.
func (s *service) export(ctx context.Context, request v1.ExportRequest) (v1.ExportResponse, error) {
	var response v1.ExportResponse
	if !validID(request.InstanceID) || !filepath.IsAbs(request.Output) || filepath.Clean(request.Output) != request.Output {
		return response, problem("invalid_request", "export requires a recorded instance ID and clean absolute output path")
	}
	if request.WaitTimeout != "" {
		d, err := time.ParseDuration(request.WaitTimeout)
		if err != nil || d <= 0 || !request.Wait {
			return response, problem("invalid_request", "wait_timeout requires wait and a positive duration")
		}
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, d)
		defer cancel()
	}
	for {
		if err := ctx.Err(); err != nil {
			return response, problem("export_interrupted", "export wait interrupted or deadline exceeded; run was not cancelled")
		}
		s.evidence.mu.Lock()
		lock := s.mutationLock(request.InstanceID)
		if lock.TryLock() {
			instance, err := s.store.inspect(request.InstanceID)
			if err != nil {
				lock.Unlock()
				s.evidence.mu.Unlock()
				return response, err
			}
			s.mu.Lock()
			entry := s.executions[request.InstanceID]
			s.mu.Unlock()
			joined := true
			if entry != nil {
				select {
				case <-entry.done:
				default:
					joined = false
				}
			}
			settled := joined && !active(instance.Status) && instance.Execution != nil && !instance.Execution.Kept && instance.Execution.CleanupFailure == ""
			// A cleanup error may leave producers alive. Explicit destroy/stop must prove
			// shutdown before export; partial mode cannot turn live evidence into a snapshot.
			if settled {
				response, err = s.exportSnapshot(ctx, request, instance)
				lock.Unlock()
				s.evidence.mu.Unlock()
				return response, err
			}
			noExecution := instance.Execution == nil
			lock.Unlock()
			s.evidence.mu.Unlock()
			if noExecution {
				return response, problem("no_execution", "instance has no recorded execution attempt")
			}
		} else {
			s.evidence.mu.Unlock()
		}
		if !request.Wait {
			return response, problem("export_active", "evidence producers are not settled; stop kept services or use --wait")
		}
		timer := time.NewTimer(100 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
		case <-timer.C:
		}
	}
}

func within(parent, child string) bool {
	rel, err := filepath.Rel(parent, child)
	return err == nil && (rel == "." || filepath.IsLocal(rel))
}

func (s *service) exportSnapshot(ctx context.Context, request v1.ExportRequest, instance v1.Instance) (v1.ExportResponse, error) {
	var response v1.ExportResponse
	parentPath, err := filepath.EvalSymlinks(filepath.Dir(request.Output))
	if err != nil {
		return response, problem("export_destination", "output parent must already exist")
	}
	private, err := filepath.EvalSymlinks(s.directory)
	if err != nil {
		return response, err
	}
	output := filepath.Join(parentPath, filepath.Base(request.Output))
	if within(private, output) || within(output, private) {
		return response, problem("export_destination", "output must be outside private daemon state")
	}
	parent, err := os.OpenRoot(parentPath)
	if err != nil {
		return response, err
	}
	defer func() { _ = parent.Close() }()
	dir, err := parent.Open(".")
	if err != nil {
		return response, err
	}
	defer func() { _ = dir.Close() }()
	info, err := dir.Stat()
	if err != nil || !exportParentOwned(info) {
		return response, problem("export_destination", "output parent must be caller-owned and not writable by other users")
	}
	name := filepath.Base(output)
	if _, err := parent.Lstat(name); !os.IsNotExist(err) {
		return response, problem("export_exists", "output already exists or cannot be inspected; choose a new directory")
	}
	staging := ".backlot-export-" + newID()
	if err := parent.Mkdir(staging, 0700); err != nil {
		return response, err
	}
	defer func() { _ = parent.RemoveAll(staging) }()
	stage, err := parent.OpenRoot(staging)
	if err != nil {
		return response, err
	}
	defer func() { _ = stage.Close() }()
	source, err := os.OpenRoot(s.directory)
	if err != nil {
		return response, err
	}
	defer func() { _ = source.Close() }()
	e := instance.Execution
	report := v1.ExportReport{SchemaVersion: v1.ExportSchemaVersion, ExportedAt: time.Now().UTC().Format(time.RFC3339Nano), CreatedAt: instance.CreatedAt, CompletedAt: e.CompletedAt,
		Result:   v1.ExportResult{InstanceID: instance.ID, Attempt: e.Attempt, Status: instance.Status, TerminalExitCode: e.TerminalExitCode, Cancelled: e.Cancelled, Failure: e.Failure, CollectionFailure: e.CollectionFailure, CleanupFailure: e.CleanupFailure, Components: e.Components},
		Source:   v1.ExportSource{Project: instance.Plan.Project, Scene: instance.Plan.Scene, Lifetime: instance.Plan.Lifetime, CheckoutID: instance.Checkout.ID, ManifestDigest: instance.Plan.ManifestDigest, ConfigDigest: instance.Plan.ConfigDigest},
		Complete: true, Omissions: []string{}, Evidence: []v1.ExportEvidence{}}
	journal, err := s.store.runtime(instance.ID)
	if err != nil {
		return response, err
	}
	if journal.Attempt == e.Attempt && journal.ExportSource != nil {
		report.Source = *journal.ExportSource
		report.StartedAt = journal.StartedAt
	}
	if e.Outcome != "" {
		report.Result.Status = e.Outcome
	}

	response = v1.ExportResponse{APIVersion: v1.Version, Result: report.Result}
	omit := func(message string) { report.Complete = false; report.Omissions = append(report.Omissions, message) }
	if e.EvidenceExpired {
		omit("Retained evidence expired; pruned bytes cannot be recovered.")
	}
	if e.CollectionFailure != "" {
		omit("Runtime collection failed; some logs or declared artifacts are unavailable.")
	}

	if !e.EvidenceExpired {
		// Logs are parsed using the same validation/gap semantics as the public log API.
		page, err := s.logsLocked(v1.LogsRequest{InstanceID: instance.ID})
		if err != nil {
			return response, err
		}
		if page.Gap != "" {
			omit("Retained logs have a known collection gap.")
		}
		file, err := stage.OpenFile("logs.ndjson", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			return response, err
		}
		encoder := json.NewEncoder(file)
		for {
			for _, record := range page.Records {
				if record.Attempt == e.Attempt {
					if err := encoder.Encode(record); err != nil {
						_ = file.Close()
						return response, err
					}
				}
			}
			next, err := s.logsLocked(v1.LogsRequest{InstanceID: instance.ID, Offset: page.NextOffset})
			if err != nil {
				_ = file.Close()
				return response, err
			}
			if next.NextOffset == page.NextOffset {
				break
			}
			page = next
		}
		if err := file.Close(); err != nil {
			return response, err
		}
		if err := inventoryExport(stage, "logs.ndjson", "logs", "", "", &report); err != nil {
			return response, err
		}
		for index, artifact := range e.Artifacts {
			if artifact.Attempt != e.Attempt {
				return response, problem("export_unsafe", "artifact attempt does not match selected execution")
			}
			expected := filepath.Join(s.directory, "artifacts", instance.ID, e.Attempt, artifact.Component, artifact.Name)
			if !safeExportSegment(artifact.Component) || !safeExportSegment(artifact.Name) || artifact.Path != expected {
				return response, problem("export_unsafe", "artifact source is outside selected retained evidence")
			}
			rel, _ := filepath.Rel(s.directory, expected)
			target := fmt.Sprintf("artifacts/%d", index)
			if _, err := source.Lstat(rel); os.IsNotExist(err) {
				omit("Declared retained artifact unavailable: " + artifact.Component + "/" + artifact.Name)
				continue
			} else if err != nil {
				return response, err
			}
			if err := copyExportTree(ctx, source, stage, rel, target); err != nil {
				return response, err
			}
			if err := inventoryExport(stage, target, "artifact", artifact.Component, artifact.Name, &report); err != nil {
				return response, err
			}
		}
	}
	if !report.Complete && !request.AllowPartial {
		return response, problem("export_incomplete", "evidence is incomplete; use --allow-partial for an explicitly incomplete report")
	}
	if len(report.Evidence) > 10000 {
		return response, problem("export_limit", "evidence inventory exceeds 10000 files")
	}
	if err := writeExportJSON(stage, "report.json", report); err != nil {
		return response, err
	}
	html, err := stage.OpenFile("index.html", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return response, err
	}
	renderErr := exportTemplate.Execute(html, report)
	if err := errors.Join(renderErr, html.Close()); err != nil {
		return response, err
	}
	if err := evidenceaudit.AuditFS(stage.FS()); err != nil {
		return response, problem("export_unsafe", "evidence safety audit failed: "+err.Error())
	}
	if err := ctx.Err(); err != nil {
		return response, problem("export_interrupted", "export interrupted before publication; run was not cancelled")
	}
	if err := publishExport(int(dir.Fd()), staging, name); err != nil {
		return response, problem("export_publish", "atomic publication failed; destination was not replaced")
	}
	return v1.ExportResponse{Published: true, APIVersion: v1.Version, Output: output, Report: "report.json", HTML: "index.html", Complete: report.Complete, Result: report.Result}, nil
}
func safeExportSegment(value string) bool {
	return value != "." && value != ".." && value != "" && !strings.ContainsAny(value, "/\\:")
}
func writeExportJSON(root *os.Root, name string, value any) error {
	file, err := root.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	return errors.Join(json.NewEncoder(file).Encode(value), file.Close())
}
func copyExportTree(ctx context.Context, source, destination *os.Root, from, to string) error {
	return fs.WalkDir(source.FS(), from, func(name string, item fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		info, err := item.Info()
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(from, name)
		if err != nil {
			return err
		}
		target := to
		if rel != "." {
			target = filepath.Join(to, rel)
		}
		if !safeExportSegment(item.Name()) {
			return problem("export_unsafe", "unsafe evidence filename")
		}
		if item.IsDir() {
			return destination.MkdirAll(target, 0700)
		}
		if !info.Mode().IsRegular() {
			return problem("export_unsafe", "evidence links and special files are unsupported")
		}
		if err := destination.MkdirAll(filepath.Dir(target), 0700); err != nil {
			return err
		}
		input, err := source.Open(name)
		if err != nil {
			return err
		}
		defer func() { _ = input.Close() }()
		output, err := destination.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			return err
		}
		_, err = io.Copy(output, input)
		return errors.Join(err, output.Close())
	})
}
func inventoryExport(root *os.Root, from, kind, component, name string, report *v1.ExportReport) error {
	return fs.WalkDir(root.FS(), from, func(path string, item fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if item.IsDir() {
			return nil
		}
		file, err := root.Open(path)
		if err != nil {
			return err
		}
		hash := sha256.New()
		size, err := io.Copy(hash, file)
		err = errors.Join(err, file.Close())
		if err != nil {
			return err
		}
		report.Evidence = append(report.Evidence, v1.ExportEvidence{Path: filepath.ToSlash(path), Kind: kind, Component: component, Name: name, Bytes: size, SHA256: hex.EncodeToString(hash.Sum(nil))})
		sort.Slice(report.Evidence, func(i, j int) bool { return report.Evidence[i].Path < report.Evidence[j].Path })
		return nil
	})
}
