//go:build darwin || linux

package daemon

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	v1 "github.com/octacian/backlot/api/v1"
)

func exportFixture(t *testing.T) (*service, v1.Instance, v1.ExportRequest) {
	t.Helper()
	project := shortTemp(t)
	config := writeFixture(t, project)
	store, err := openStore(shortTemp(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := store.db.Close(); err != nil {
			t.Error(err)
		}
	})
	s := &service{store: store, directory: store.directory, lease: time.Minute}
	prepared, err := s.prepare(context.Background(), request(project, config, "test"))
	if err != nil {
		t.Fatal(err)
	}
	code := 23
	result := &v1.ExecutionResult{Attempt: newID(), TerminalExitCode: &code, CompletedAt: time.Now().UTC().Format(time.RFC3339Nano), Components: []v1.ComponentResult{{Name: "test", Status: "completed", ExitCode: &code}}}
	instance, err := s.store.execution(prepared.Instance.ID, v1.Failed, result)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(s.directory, "logs"), 0700); err != nil {
		t.Fatal(err)
	}
	record := v1.LogRecord{InstanceID: instance.ID, Attempt: result.Attempt, Component: "test", Stream: "stdout", Time: time.Now().UTC().Format(time.RFC3339Nano), Message: "original output"}
	data, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.directory, "logs", instance.ID+"-output.ndjson"), append(data, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
	return s, instance, v1.ExportRequest{APIVersion: v1.Version, InstanceID: instance.ID, Output: filepath.Join(t.TempDir(), "bundle")}
}
func addExportArtifact(t *testing.T, s *service, instance v1.Instance) string {
	t.Helper()
	path := filepath.Join(s.directory, "artifacts", instance.ID, instance.Execution.Attempt, "test", "report")
	if err := os.MkdirAll(path, 0700); err != nil {
		t.Fatal(err)
	}
	result := *instance.Execution
	result.Artifacts = []v1.ArtifactRecord{{Component: "test", Name: "report", Attempt: result.Attempt, Path: path}}
	if _, err := s.store.execution(instance.ID, instance.Status, &result); err != nil {
		t.Fatal(err)
	}
	return path
}
func readExportReport(t *testing.T, path string) v1.ExportReport {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(path, "report.json"))
	if err != nil {
		t.Fatal(err)
	}
	var report v1.ExportReport
	if err := json.Unmarshal(data, &report); err != nil {
		t.Fatal(err)
	}
	return report
}
func TestExportIncompleteEvidence(t *testing.T) {
	for _, mode := range []string{"expired", "collection-gap", "missing-artifact", "missing-log", "corrupt-log"} {
		t.Run(mode, func(t *testing.T) {
			s, instance, request := exportFixture(t)
			result := *instance.Execution
			switch mode {
			case "expired":
				result.EvidenceExpired = true
			case "collection-gap":
				result.CollectionFailure = "native output collection interrupted"
			case "missing-artifact":
				path := addExportArtifact(t, s, instance)
				if err := os.RemoveAll(path); err != nil {
					t.Fatal(err)
				}
				current, err := s.store.inspect(instance.ID)
				if err != nil {
					t.Fatal(err)
				}
				result = *current.Execution
			case "missing-log":
				if err := os.Remove(filepath.Join(s.directory, "logs", instance.ID+"-output.ndjson")); err != nil {
					t.Fatal(err)
				}
			case "corrupt-log":
				if err := os.WriteFile(filepath.Join(s.directory, "logs", instance.ID+"-output.ndjson"), []byte("interrupted"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := s.store.execution(instance.ID, v1.Failed, &result); err != nil {
				t.Fatal(err)
			}
			response, err := s.export(context.Background(), request)
			if err == nil || response.Published {
				t.Fatal("strict export published incomplete evidence")
			}
			if _, err := os.Lstat(request.Output); !os.IsNotExist(err) {
				t.Fatal("failed export left destination", err)
			}
			request.AllowPartial = true
			response, err = s.export(context.Background(), request)
			if err != nil {
				t.Fatal(err)
			}
			if response.Complete || !response.Published || response.Result.TerminalExitCode == nil || *response.Result.TerminalExitCode != 23 {
				t.Fatalf("lost incomplete/original result: %+v", response)
			}
			report := readExportReport(t, request.Output)
			if report.Complete || len(report.Omissions) == 0 {
				t.Fatal("unlabelled partial report")
			}
			html, err := os.ReadFile(filepath.Join(request.Output, "index.html"))
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(html), "Incomplete evidence") {
				t.Fatal("partial invisible in HTML")
			}
			if mode == "expired" && len(report.Evidence) != 0 {
				t.Fatal("invented expired evidence")
			}
			stages, err := filepath.Glob(filepath.Join(filepath.Dir(request.Output), ".backlot-export-*"))
			if err != nil || len(stages) != 0 {
				t.Fatal("staging leaked", stages, err)
			}
		})
	}
}
func TestExportRejectsUnsafeEvidenceInPartialMode(t *testing.T) {
	for _, mode := range []string{"root-link", "inner-link", "hardlink", "key", "private-record", "unsafe-name", "wrong-attempt", "outside-state"} {
		t.Run(mode, func(t *testing.T) {
			s, instance, request := exportFixture(t)
			path := addExportArtifact(t, s, instance)
			switch mode {
			case "root-link":
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(t.TempDir(), path); err != nil {
					t.Fatal(err)
				}
			case "inner-link":
				if err := os.Symlink("/etc/passwd", filepath.Join(path, "link")); err != nil {
					t.Fatal(err)
				}
			case "hardlink":
				source := filepath.Join(t.TempDir(), "source")
				if err := os.WriteFile(source, []byte("private"), 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.Link(source, filepath.Join(path, "hard")); err != nil {
					t.Fatal(err)
				}
			case "key":
				if err := os.WriteFile(filepath.Join(path, "material.txt"), []byte("-----BEGIN PRIVATE KEY-----"), 0600); err != nil {
					t.Fatal(err)
				}
			case "private-record":
				if err := os.WriteFile(filepath.Join(path, "state.db"), []byte("private"), 0600); err != nil {
					t.Fatal(err)
				}
			case "unsafe-name":
				if err := os.WriteFile(filepath.Join(path, "unsafe:name"), []byte("x"), 0600); err != nil {
					t.Fatal(err)
				}
			case "wrong-attempt", "outside-state":
				current, err := s.store.inspect(instance.ID)
				if err != nil {
					t.Fatal(err)
				}
				if mode == "wrong-attempt" {
					current.Execution.Artifacts[0].Attempt = newID()
				} else {
					current.Execution.Artifacts[0].Path = t.TempDir()
				}
				if _, err := s.store.execution(instance.ID, v1.Failed, current.Execution); err != nil {
					t.Fatal(err)
				}
			}
			request.AllowPartial = true
			if response, err := s.export(context.Background(), request); err == nil || response.Published {
				t.Fatal("partial bypassed safety", mode)
			}
			if _, err := os.Lstat(request.Output); !os.IsNotExist(err) {
				t.Fatal("unsafe evidence published")
			}
		})
	}
}
func TestExportWaitingAndUnknownTargets(t *testing.T) {
	s, instance, request := exportFixture(t)
	if _, err := s.store.execution(instance.ID, v1.Prepared, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := s.export(context.Background(), request); err == nil {
		t.Fatal("instance with no execution exported")
	}
	if _, err := s.store.execution(instance.ID, v1.Failed, instance.Execution); err != nil {
		t.Fatal(err)
	}
	// Active and kept executions cannot use partial mode to bypass settlement.
	result := *instance.Execution
	result.Kept = true
	if _, err := s.store.execution(instance.ID, v1.Failed, &result); err != nil {
		t.Fatal(err)
	}
	request.AllowPartial = true
	if _, err := s.export(context.Background(), request); err == nil {
		t.Fatal("exported kept producer")
	}
	request.Wait = true
	request.WaitTimeout = "25ms"
	if _, err := s.export(context.Background(), request); err == nil {
		t.Fatal("bounded wait did not expire")
	}
	request.WaitTimeout = ""
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.export(ctx, request); err == nil {
		t.Fatal("cancelled export published")
	}
	current, err := s.store.inspect(instance.ID)
	if err != nil || !current.Execution.Kept || current.Execution.Cancelled {
		t.Fatal("export cancelled run", err)
	}
	request.InstanceID = newID()
	request.Wait = false
	if _, err := s.export(context.Background(), request); err == nil {
		t.Fatal("unknown ID exported")
	}
}
func TestExportNoReplacementPublication(t *testing.T) {
	s, _, request := exportFixture(t)
	var joined sync.WaitGroup
	results := make(chan error, 4)
	for range 4 {
		joined.Go(func() { _, err := s.export(context.Background(), request); results <- err })
	}
	joined.Wait()
	close(results)
	successes := 0
	for err := range results {
		if err == nil {
			successes++
		}
	}
	if successes != 1 {
		t.Fatal("publication collision", successes)
	}
	parent := t.TempDir()
	if err := os.Mkdir(filepath.Join(parent, "stage"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(parent, "existing"), 0700); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(parent)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	// The collision is created after staging, independently of daemon preflight.
	if err := publishExport(int(file.Fd()), "stage", "existing"); err == nil {
		t.Fatal("replaced raced destination")
	}
	if _, err := os.Stat(filepath.Join(parent, "stage")); err != nil {
		t.Fatal("publication removed staging on collision", err)
	}
}
func TestExportEscapesHostileSummary(t *testing.T) {
	s, instance, request := exportFixture(t)
	result := *instance.Execution
	result.Failure = `<script>alert("hostile")</script> & injected`
	if _, err := s.store.execution(instance.ID, v1.Failed, &result); err != nil {
		t.Fatal(err)
	}
	if _, err := s.export(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	html, err := os.ReadFile(filepath.Join(request.Output, "index.html"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(html), "<script") || !strings.Contains(string(html), "&lt;script&gt;") {
		t.Fatal("unsafe hostile summary")
	}
	if !strings.Contains(string(html), `class="status failed"`) {
		t.Fatal("failure not distinguished")
	}
	if strings.Contains(string(html), "npx playwright") {
		t.Fatal("irrelevant trace viewer hint")
	}
	report := readExportReport(t, request.Output)
	if report.Result.Failure != result.Failure {
		t.Fatal("agent text changed")
	}
}

func TestEmbeddedLogPreviewHeadTailBinaryAndEscaping(t *testing.T) {
	root, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	file, err := root.OpenFile("logs.ndjson", os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	encoder := json.NewEncoder(file)
	for i := range 500 {
		record := v1.LogRecord{Time: "2026-10-18T00:00:00Z", Component: "tests", Stream: "stdout", Message: strings.Repeat("x", 1024)}
		if i == 0 {
			record.Message = "HEAD <script>alert(1)</script> " + record.Message
		}
		if i == 250 {
			record.Message = "OMITTED-MIDDLE " + record.Message
		}
		if i == 499 {
			record.Message = "TAIL " + record.Message
		}
		if err := encoder.Encode(record); err != nil {
			t.Fatal(err)
		}
	}
	if err := encoder.Encode(v1.LogRecord{Time: "2026-10-18T00:00:00Z", Component: "tests", Stream: "stderr", Data: "/wA="}); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	preview, err := readExportLogPreview(root)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(preview.Head, "HEAD") || !strings.Contains(preview.Tail, "TAIL") || strings.Contains(preview.Head+preview.Tail, "OMITTED-MIDDLE") {
		t.Fatal("preview is not head/tail")
	}
	if !strings.Contains(preview.Tail, "binary chunk: 2 original bytes; base64 follows") || !strings.Contains(preview.Tail, "/wA=") {
		t.Fatal("binary representation changed")
	}
	if len(preview.Head)+len(preview.Tail) > embeddedLogBytes || preview.OmittedBytes != preview.TotalBytes-int64(len(preview.Head)+len(preview.Tail)) || preview.TotalRecords != 501 {
		t.Fatal("wrong preview budget or omission accounting")
	}
	var html strings.Builder
	if err := exportTemplate.Execute(&html, exportHTMLView{Logs: preview}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(html.String(), "<script") || !strings.Contains(html.String(), "&lt;script&gt;") || !strings.Contains(html.String(), "<details><summary>Show embedded logs") {
		t.Fatal("embedded logs are unsafe or not collapsed")
	}
}

func TestExportLatestAttemptAndDestinationExclusions(t *testing.T) {
	s, instance, request := exportFixture(t)
	oldData, err := os.ReadFile(filepath.Join(s.directory, "logs", instance.ID+"-output.ndjson"))
	if err != nil {
		t.Fatal(err)
	}
	result := *instance.Execution
	result.Attempt = newID()
	if _, err := s.store.execution(instance.ID, v1.Failed, &result); err != nil {
		t.Fatal(err)
	}
	next := v1.LogRecord{InstanceID: instance.ID, Attempt: result.Attempt, Component: "test", Time: time.Now().UTC().Format(time.RFC3339Nano), Stream: "stdout", Message: "LATEST-ONLY"}
	data, err := json.Marshal(next)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.directory, "logs", instance.ID+"-output.ndjson"), append(oldData, append(data, '\n')...), 0600); err != nil {
		t.Fatal(err)
	}
	private := request
	private.Output = filepath.Join(s.directory, "export")
	if _, err := s.export(context.Background(), private); err == nil {
		t.Fatal("exported into private state")
	}
	if _, err := s.export(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	logs, err := os.ReadFile(filepath.Join(request.Output, "logs.ndjson"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(logs), "original output") || !strings.Contains(string(logs), "LATEST-ONLY") {
		t.Fatal("mixed execution attempts")
	}
	report := readExportReport(t, request.Output)
	if report.Result.Attempt != result.Attempt || report.Source.GitHead != "" {
		t.Fatal("wrong attempt or invented historical launch provenance")
	}
}

func TestExportIndefiniteWaitObservesWithoutCancellation(t *testing.T) {
	s, instance, request := exportFixture(t)
	result := *instance.Execution
	result.Kept = true
	if _, err := s.store.execution(instance.ID, v1.Failed, &result); err != nil {
		t.Fatal(err)
	}
	request.Wait = true
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := s.export(ctx, request); done <- err }()
	// Settlement comes from an independent execution owner, not the exporter.
	result.Kept = false
	if _, err := s.store.execution(instance.ID, v1.Failed, &result); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	current, err := s.store.inspect(instance.ID)
	if err != nil || current.Execution.Cancelled {
		t.Fatal("wait cancelled original execution", err)
	}
}
