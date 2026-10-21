//go:build !windows

package daemon

import (
	"context"
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
