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

func TestExportEscapesOwnershipWarnings(t *testing.T) {
	view := exportHTMLView{ExportReport: v1.ExportReport{Complete: true, Result: v1.ExportResult{NativeOwnershipWarnings: []v1.NativeOwnershipWarning{{
		Message: `<script>message</script> & warning`, Attempt: `<script>attempt</script>`, RecordedAt: `<script>time</script>`,
	}}}}}
	var html strings.Builder
	if err := exportTemplate.Execute(&html, view); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(html.String(), "<script") {
		t.Fatal("ownership warning inserted unescaped markup")
	}
	for _, escaped := range []string{"&lt;script&gt;message&lt;/script&gt; &amp; warning", "&lt;script&gt;attempt&lt;/script&gt;", "&lt;script&gt;time&lt;/script&gt;"} {
		if !strings.Contains(html.String(), escaped) {
			t.Fatalf("warning omitted escaped text %q", escaped)
		}
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

func TestExportEvidenceFenceInterruption(t *testing.T) {
	for _, mode := range []string{"deadline", "cancel", "already-cancelled"} {
		t.Run(mode, func(t *testing.T) {
			s, instance, request := exportFixture(t)
			// An unrelated instance's copy or a retention sweep owns this same global
			// fence. Keep it held until the request has returned, rather than letting
			// release accidentally satisfy the interruption assertion.
			s.evidence.mu.Lock()
			var release sync.Once
			unlock := func() { release.Do(s.evidence.mu.Unlock) }
			t.Cleanup(unlock)
			before, err := s.store.inspect(instance.ID)
			if err != nil {
				t.Fatal(err)
			}
			request.Wait = true
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if mode == "deadline" {
				request.WaitTimeout = "20ms"
			}
			if mode == "already-cancelled" {
				cancel()
			}
			done := make(chan error, 1)
			go func() { _, err := s.export(ctx, request); done <- err }()
			if mode == "cancel" {
				cancel()
			}
			select {
			case err := <-done:
				code(t, err, "export_interrupted")
			case <-time.After(time.Second):
				unlock()
				<-done
				t.Fatal("export ignored interruption while evidence fence was owned")
			}
			current, err := s.store.inspect(instance.ID)
			if err != nil {
				t.Fatal(err)
			}
			a, _ := json.Marshal(before)
			b, _ := json.Marshal(current)
			if string(a) != string(b) {
				t.Fatal("interrupted export mutated recorded run")
			}
			if _, err := os.Lstat(request.Output); !os.IsNotExist(err) {
				t.Fatal("interrupted export published", err)
			}
			unlock()
			// A later uncancelled caller can still acquire and publish a full snapshot.
			// Its budget is independent of the interrupted request's short deadline.
			retry := request
			retry.WaitTimeout = ""
			retryCtx, retryCancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer retryCancel()
			if response, err := s.export(retryCtx, retry); err != nil || !response.Complete {
				t.Fatal(response, err)
			}
		})
	}
}

func TestExportEvidenceFenceWaitsForRetention(t *testing.T) {
	s, instance, request := exportFixture(t)
	// A real retention sweep holds the global fence while waiting for its store
	// update. Keep that writer owned until the export has returned independently.
	tx, err := s.store.db.Begin(true)
	if err != nil {
		t.Fatal(err)
	}
	retention := make(chan error, 1)
	go func() { retention <- s.pruneEvidence() }()
	var rollback sync.Once
	releaseWriter := func() {
		rollback.Do(func() {
			if err := tx.Rollback(); err != nil {
				t.Error(err)
			}
		})
	}
	t.Cleanup(func() {
		releaseWriter()
		if err := <-retention; err != nil {
			t.Error(err)
		}
	})

	deadline := time.Now().Add(time.Second)
	for s.evidence.mu.TryLock() {
		s.evidence.mu.Unlock()
		if time.Now().After(deadline) {
			t.Fatal("retention did not acquire evidence fence")
		}
		time.Sleep(time.Millisecond)
	}
	request.Wait = true
	request.WaitTimeout = "20ms"
	done := make(chan error, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _, err := s.export(ctx, request); done <- err }()
	select {
	case err := <-done:
		code(t, err, "export_interrupted")
	case <-time.After(time.Second):
		// Release and join on regression rather than leak the blocked export.
		cancel()
		releaseWriter()
		<-done
		t.Fatal("retention blocked bounded export")
	}
	current, err := s.store.inspect(instance.ID)
	if err != nil || current.Execution.Cancelled || current.Execution.EvidenceExpired {
		t.Fatal("export changed run", err)
	}
}

func TestExportEvidenceFenceWaitsForUnrelatedSnapshot(t *testing.T) {
	s, instance, request := exportFixture(t)
	project := shortTemp(t)
	config := writeFixture(t, project)
	peer, err := s.prepare(context.Background(), v1.PrepareRequest{APIVersion: v1.Version, Plan: v1.PlanRequest{ProjectPath: project, ConfigPath: config, Scene: "test"}})
	if err != nil {
		t.Fatal(err)
	}
	result := *instance.Execution
	result.Components = nil // This service-free peer has no log producers.
	other, err := s.store.execution(peer.Instance.ID, v1.Failed, &result)
	if err != nil {
		t.Fatal(err)
	}
	if other.ID == instance.ID {
		t.Fatal("fixture did not create independent instance")
	}
	artifact := addExportArtifact(t, s, other)
	if err := os.WriteFile(filepath.Join(artifact, "report.txt"), []byte("peer evidence"), 0600); err != nil {
		t.Fatal(err)
	}
	other, err = s.store.inspect(other.ID)
	if err != nil {
		t.Fatal(err)
	}
	// Exercise the actual snapshot copy under its required fences. Hold the fence
	// after copying too, so waiter completion cannot be explained by owner release.
	lock := s.mutationLock(other.ID)
	lock.Lock()
	s.evidence.mu.Lock()
	var release sync.Once
	unlock := func() { release.Do(func() { lock.Unlock(); s.evidence.mu.Unlock() }) }
	t.Cleanup(unlock)
	copied := request
	copied.InstanceID = other.ID
	copied.Output = filepath.Join(t.TempDir(), "peer-bundle")
	if response, err := s.exportSnapshot(context.Background(), copied, other); err != nil || !response.Published {
		t.Fatal(response, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := s.export(ctx, request); done <- err }()
	select {
	case err := <-done:
		t.Fatal("export bypassed snapshot fence", err)
	case <-time.After(30 * time.Millisecond):
	}
	cancel()
	select {
	case err := <-done:
		code(t, err, "export_interrupted")
	case <-time.After(time.Second):
		unlock()
		<-done
		t.Fatal("snapshot blocked export cancellation")
	}
	if current, err := s.store.inspect(instance.ID); err != nil || current.Execution.Cancelled {
		t.Fatal("export cancelled original", err)
	}
	if readExportReport(t, copied.Output).Result.InstanceID != other.ID {
		t.Fatal("peer snapshot identity lost")
	}
}

func TestExportHistoricalSymbolicRevisionStaysUnknown(t *testing.T) {
	s, instance, request := exportFixture(t)
	journal, err := s.store.runtime(instance.ID)
	if err != nil {
		t.Fatal(err)
	}
	journal.Attempt = instance.Execution.Attempt
	journal.ExportSource = &v1.ExportSource{Project: instance.Plan.Project, Scene: instance.Plan.Scene, Lifetime: instance.Plan.Lifetime, CheckoutID: instance.Checkout.ID, GitHead: "ref: refs/heads/main"}
	if err := s.store.saveRuntime(instance.ID, journal); err != nil {
		t.Fatal(err)
	}
	if _, err := s.export(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	if report := readExportReport(t, request.Output); report.Source.GitHead != "" {
		t.Fatal("symbolic history claimed as launch revision", report.Source.GitHead)
	}
}

func TestExportRejectsUnsafeLogSources(t *testing.T) {
	for _, mode := range []string{"hardlink", "other-log-hardlink", "corrupt-before-hardlink", "file-link", "directory-link", "public-mode"} {
		t.Run(mode, func(t *testing.T) {
			s, instance, request := exportFixture(t)
			log := filepath.Join(s.directory, "logs", instance.ID+"-output.ndjson")
			switch mode {
			case "corrupt-before-hardlink":
				if err := os.WriteFile(filepath.Join(s.directory, "logs", instance.ID+"-aaa.ndjson"), []byte("corrupt\n"), 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.Link(log, filepath.Join(t.TempDir(), "alias.ndjson")); err != nil {
					t.Fatal(err)
				}
			case "hardlink", "other-log-hardlink":
				alias := filepath.Join(t.TempDir(), "alias.ndjson")
				if mode == "other-log-hardlink" {
					alias = filepath.Join(s.directory, "logs", instance.ID+"-other.ndjson")
				}
				if err := os.Link(log, alias); err != nil {
					t.Fatal(err)
				}
			case "file-link":
				if err := os.Remove(log); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(filepath.Join(t.TempDir(), "missing"), log); err != nil {
					t.Fatal(err)
				}
			case "directory-link":
				if err := os.RemoveAll(filepath.Dir(log)); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(t.TempDir(), filepath.Dir(log)); err != nil {
					t.Fatal(err)
				}
			case "public-mode":
				if err := os.Chmod(log, 0644); err != nil {
					t.Fatal(err)
				}
			}
			for _, partial := range []bool{false, true} {
				request.AllowPartial = partial
				response, err := s.export(context.Background(), request)
				if err == nil || response.Published {
					t.Fatalf("unsafe log accepted (partial=%t): %+v %v", partial, response, err)
				}
				if _, err := os.Lstat(request.Output); !os.IsNotExist(err) {
					t.Fatal("unsafe log published", err)
				}
			}
		})
	}
}
