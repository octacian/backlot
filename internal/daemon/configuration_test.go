//go:build !windows

package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	v1 "github.com/octacian/backlot/api/v1"
	"github.com/octacian/backlot/internal/plan"
)

func TestRunCapturesOneConfigurationGenerationBeforeAcceptance(t *testing.T) {
	home := shortTemp(t)
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", home)
	project := shortTemp(t)
	config := writeFixture(t, project)
	// Remove the legacy input from machine config and establish a private source.
	if err := os.WriteFile(config, []byte(`{"version":"backlot/v1"}`), 0600); err != nil {
		t.Fatal(err)
	}
	setup, err := plan.OpenSetup(v1.ProjectSetupRequest{ProjectPath: project, ConfigPath: config, Edit: true})
	if err != nil {
		t.Fatal(err)
	}
	save := func(value string) {
		t.Helper()
		setup, err := plan.OpenSetup(v1.ProjectSetupRequest{ProjectPath: project, ConfigPath: config, Edit: true})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := setup.Save(context.Background(), v1.PrivateConfig{Version: v1.ManifestVersion, Inputs: map[string]string{"password": value}}, nil, nil, nil); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := setup.Save(context.Background(), v1.PrivateConfig{Version: v1.ManifestVersion, Inputs: map[string]string{"password": "captured-first"}}, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	store, err := openStore(shortTemp(t))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := store.db.Close(); err != nil {
			t.Error(err)
		}
	}()
	calls := 0
	accepted := ""
	svc := &service{store: store, lease: time.Second, mutations: map[string]*sync.Mutex{}, resolve: func(source plan.Source, request v1.PlanRequest) (plan.Snapshot, error) {
		calls++
		snapshot, err := plan.Prepare(source, request)
		if err == nil {
			save("published-after-capture")
		}
		return snapshot, err
	}}
	svc.beforeExecution = func(id string) { accepted = id; svc.mu.Lock(); svc.closing = true; svc.mu.Unlock() }
	_, err = svc.run(context.Background(), v1.RunRequest{APIVersion: v1.Version, Plan: v1.PlanRequest{Scene: "dev", ProjectPath: project, ConfigPath: config}})
	code(t, err, "shutting_down")
	if calls != 1 || accepted == "" {
		t.Fatalf("capture count=%d accepted=%t", calls, accepted != "")
	}
	snap, err := store.snapshot(accepted)
	if err != nil {
		t.Fatal(err)
	}
	if value := snap.Secrets["check/environment/PASSWORD"].Literal; value == nil || *value != "captured-first" {
		t.Fatal("preparation reread a newer private generation")
	}
	current, err := plan.Resolve(v1.PlanRequest{Scene: "dev", ProjectPath: project, ConfigPath: config})
	if err != nil {
		t.Fatal(err)
	}
	if !plan.ConfigurationDrift(snap.Plan, current) {
		t.Fatal("fixture did not cross configuration generations")
	}
	if snap.PrivateProvenance == nil || !filepath.IsAbs(snap.PrivateProvenance.Path) {
		t.Fatal("private launch provenance absent")
	}
}

func TestSceneRequestPathsRejectedBeforeResolution(t *testing.T) {
	home := shortTemp(t)
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", home)
	project := shortTemp(t)
	config := writeFixture(t, project)
	t.Chdir(project) // Empty and relative paths would otherwise discover a valid scene.
	state, err := openStore(shortTemp(t))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := state.db.Close(); err != nil {
			t.Error(err)
		}
	}()
	svc := &service{store: state, lease: time.Second}
	prepared, err := svc.prepare(context.Background(), request(project, config, "dev"))
	if err != nil {
		t.Fatal(err)
	}
	before, err := state.list(v1.ListRequest{})
	if err != nil {
		t.Fatal(err)
	}
	beforeJSON, err := json.Marshal(before)
	if err != nil {
		t.Fatal(err)
	}
	svc.resolve = func(plan.Source, v1.PlanRequest) (plan.Snapshot, error) {
		t.Fatal("invalid request reached resolution")
		return plan.Snapshot{}, nil
	}
	svc.checkpoint = func(string) error { t.Fatal("invalid request mutated state"); return nil }
	svc.beforeExecution = func(string) { t.Fatal("invalid request reached execution") }
	h := handler(svc, v1.DaemonStatusResponse{}, func() {})
	for _, route := range []string{"prepare", "run", "restart", "reset"} {
		for _, invalid := range []struct{ name, project, config string }{
			{"omitted project", "", config}, {"relative project", ".", config}, {"relative config", project, "machine.json"},
		} {
			t.Run(route+"/"+invalid.name, func(t *testing.T) {
				p := v1.PlanRequest{Scene: "dev", ProjectPath: invalid.project, ConfigPath: invalid.config}
				// A direct service call must reject even before a restart fallback store lookup.
				noStore := &service{}
				if route == "prepare" {
					_, err = noStore.prepare(context.Background(), v1.PrepareRequest{Plan: p})
				} else {
					_, err = noStore.runMode(context.Background(), v1.RunRequest{Plan: p, InstanceID: prepared.Instance.ID}, route == "reset")
				}
				code(t, err, "invalid_request")
				r := v1.RunRequest{APIVersion: v1.Version, Plan: p}
				if route == "restart" || route == "reset" {
					r.InstanceID = prepared.Instance.ID
				}
				var body []byte
				if route == "prepare" {
					body, err = json.Marshal(v1.PrepareRequest{APIVersion: v1.Version, Plan: p})
				} else {
					body, err = json.Marshal(r)
				}
				if err != nil {
					t.Fatal(err)
				}
				req := httptest.NewRequest(http.MethodPost, "/v1/"+route, bytes.NewReader(body))
				req.Header.Set("Content-Type", "application/json")
				req.Header.Set("X-Backlot-API-Version", v1.Version)
				rec := httptest.NewRecorder()
				h.ServeHTTP(rec, req)
				var envelope v1.ErrorResponse
				if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
					t.Fatal(err)
				}
				if rec.Code < 400 || envelope.Error.Code != "invalid_request" {
					t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
				}
				after, err := state.list(v1.ListRequest{})
				if err != nil {
					t.Fatal(err)
				}
				afterJSON, err := json.Marshal(after)
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(beforeJSON, afterJSON) {
					t.Fatal("rejection changed recorded instances")
				}
			})
		}
	}
}

func TestRestartRetainsMachineSelectionWithOneCapture(t *testing.T) {
	home := shortTemp(t)
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", home)
	project := shortTemp(t)
	config := writeFixture(t, project)
	state, err := openStore(shortTemp(t))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := state.db.Close(); err != nil {
			t.Error(err)
		}
	}()
	svc := &service{store: state, lease: time.Second, mutations: map[string]*sync.Mutex{}}
	prepared, err := svc.prepare(context.Background(), request(project, config, "dev"))
	if err != nil {
		t.Fatal(err)
	}
	// Model the machine selection retained by an earlier accepted execution.
	if err := state.saveRuntime(prepared.Instance.ID, runtimeRecord{ConfigPath: config}); err != nil {
		t.Fatal(err)
	}
	calls := 0
	svc.resolve = func(source plan.Source, p v1.PlanRequest) (plan.Snapshot, error) {
		calls++
		if p.ConfigPath != config {
			t.Fatal("restart lost recorded machine selection")
		}
		return plan.Prepare(source, p)
	}
	svc.beforeExecution = func(string) { svc.mu.Lock(); svc.closing = true; svc.mu.Unlock() }
	_, err = svc.run(context.Background(), v1.RunRequest{APIVersion: v1.Version, InstanceID: prepared.Instance.ID, Plan: v1.PlanRequest{ProjectPath: project, Scene: "dev"}})
	code(t, err, "shutting_down")
	if calls != 1 {
		t.Fatalf("resolved %d times", calls)
	}
}
