//go:build darwin || linux

package daemon

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	v1 "github.com/octacian/backlot/api/v1"
)

func TestAttemptHistoryNativeFlow(t *testing.T) {
	binary := buildFixtureBinary(t, t.TempDir(), "../../cmd/backlot")
	h := newNativeHarness(t, binary, "3s")
	config := filepath.Join(h.project, "machine.json")
	if err := os.WriteFile(config, []byte(`{"version":"backlot/v1"}`), 0600); err != nil {
		t.Fatal(err)
	}
	artifact := filepath.Join(h.project, "report.txt")
	if err := os.WriteFile(artifact, []byte("first-attempt"), 0600); err != nil {
		t.Fatal(err)
	}
	server := h.manifest.Components["server"]
	server.Artifacts = map[string]v1.ArtifactSource{"report": {Path: "report.txt"}}
	h.manifest.Components["server"] = server
	h.write()
	q := h.request("dev")
	q.Plan.ConfigPath = config
	accepted, err := h.cli.Run(context.Background(), q)
	if err != nil {
		t.Fatal(err)
	}
	first := h.state(accepted.Instance.ID, v1.RuntimeReady).Instance
	id := first.ID
	attempt := first.Execution.Attempt
	stopped, err := h.cli.StopExecution(context.Background(), id)
	if err != nil || stopped.Instance.Execution.CompletedAt == "" {
		t.Fatal("first completion", err)
	}
	if err := os.WriteFile(artifact, []byte("second-attempt"), 0600); err != nil {
		t.Fatal(err)
	}
	value := "changed"
	server.Environment.Assign["ORDINARY_HISTORY_DRIFT"] = v1.Value{Literal: &value}
	h.manifest.Components["server"] = server
	h.write()
	q.InstanceID = id
	accepted, err = h.cli.Restart(context.Background(), q)
	if err != nil {
		t.Fatal(err)
	}
	newer := h.state(id, v1.RuntimeReady).Instance
	if newer.Execution.Attempt == attempt || newer.Execution.CompletedAt != "" {
		t.Fatal("replacement not active")
	}
	selected, err := h.cli.Attempt(context.Background(), v1.AttemptRequest{APIVersion: v1.Version, InstanceID: id, LastCompleted: true})
	if err != nil || selected.Attempt.Result.Attempt != attempt || selected.Attempt.Source.ManifestDigest != first.Plan.ManifestDigest || selected.Attempt.Source.ManifestDigest == newer.Plan.ManifestDigest {
		t.Fatal("last-completed selection/provenance", err)
	}
	output := filepath.Join(privateExportParent(t), "old-attempt")
	command := exec.Command(binary, "export", id, "--attempt", attempt, "--output", output, "--state-dir", h.dir, "--json")
	data, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("historical CLI export: %v %s", err, data)
	}
	var exported v1.ExportResponse
	if json.Unmarshal(data, &exported) != nil || !exported.Published || !exported.Complete || exported.Result.Attempt != attempt {
		t.Fatal("wrong exported attempt")
	}
	report := readExportReport(t, output)
	found := false
	for _, item := range report.Evidence {
		if item.Kind == "artifact" {
			content, err := os.ReadFile(filepath.Join(output, item.Path))
			if err != nil || string(content) != "first-attempt" {
				t.Fatal("export mixed current artifacts", err)
			}
			found = true
		}
	}
	if !found {
		t.Fatal("historical artifact unavailable")
	}
	page, err := h.cli.LogPage(context.Background(), v1.LogPageRequest{APIVersion: v1.Version, InstanceID: id, Attempt: attempt})
	if err != nil || page.Active || page.HistoricalCollection != "known" || len(page.Records) == 0 {
		t.Fatal("selected settled logs", err)
	}
	for _, record := range page.Records {
		if record.Attempt != attempt {
			t.Fatal("logs mixed attempts")
		}
	}
	keep := 0
	preview, err := h.cli.PruneHistory(context.Background(), v1.HistoryPruneRequest{APIVersion: v1.Version, InstanceID: id, Keep: &keep, DryRun: true})
	if err != nil || len(preview.Attempts) != 1 || preview.Attempts[0].Protected {
		t.Fatal("prune preview", preview, err)
	}
	if _, err := h.cli.PruneHistory(context.Background(), v1.HistoryPruneRequest{APIVersion: v1.Version, InstanceID: id, Keep: &keep}); err != nil {
		t.Fatal(err)
	}
	_, err = h.cli.Attempt(context.Background(), v1.AttemptRequest{APIVersion: v1.Version, InstanceID: id, Attempt: attempt})
	code(t, err, "attempt_unavailable")
	current, err := h.cli.Inspect(context.Background(), id)
	if err != nil || current.Instance.Status != v1.RuntimeReady || current.Instance.Execution.Attempt != newer.Execution.Attempt {
		t.Fatal("prune changed active runtime", err)
	}
	destroyed, err := h.cli.Destroy(context.Background(), id)
	if err != nil || destroyed.Instance.Execution != nil {
		t.Fatal("marked destroy", err)
	}
	if _, err := os.Stat(filepath.Join(output, "report.json")); err != nil {
		t.Fatal("destroy removed caller-owned export", err)
	}
	if _, err := os.Stat(filepath.Join(h.dir, "artifacts", id)); !os.IsNotExist(err) {
		t.Fatal("destroy retained artifacts", err)
	}
	h.absent()
}
