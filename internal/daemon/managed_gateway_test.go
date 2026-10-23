package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	v1 "github.com/octacian/backlot/api/v1"
	"github.com/octacian/backlot/internal/gateway"
	"github.com/octacian/backlot/internal/manifest"
	"github.com/octacian/backlot/internal/native"
	"github.com/octacian/backlot/internal/plan"
)

func TestManagedGatewayUncertainty(t *testing.T) {
	dir := shortTemp(t)
	state, err := openStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := state.db.Close(); err != nil {
			t.Error(err)
		}
	}()
	svc := &service{store: state, directory: dir}
	err = svc.requireManaged(context.Background(), manifest.CaddyDefaults(v1.CaddyConfig{Mode: "managed", Executable: "/explicit/caddy"}))
	var unavailable *v1.PlanError
	if !errors.As(err, &unavailable) || unavailable.Code != "gateway_unavailable" {
		t.Fatal("unstarted gateway diagnostic is misleading")
	}
	status, err := svc.gatewayControl(context.Background(), "stop", v1.GatewayRequest{})
	if err != nil || status.Status != "unconfigured" {
		t.Fatal("unconfigured stop changed configuration", status, err)
	}
	r := managedRecord{Scope: newID(), Config: manifest.CaddyDefaults(v1.CaddyConfig{Mode: "managed", Executable: "/explicit/caddy"}), Pending: true, Status: "unconfigured"}
	if err := state.saveManaged(r); err != nil {
		t.Fatal(err)
	}
	if status := svc.gatewayStatus(context.Background(), r); status.ConfigSource != "" {
		t.Fatal("unconfigured gateway reported a selected source", status)
	}
	sentinel := filepath.Join(dir, "unrelated")
	if err := os.WriteFile(sentinel, []byte("preserve"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := state.recoverManaged(); err != nil {
		t.Fatal(err)
	}
	status, err = svc.gatewayControl(context.Background(), "status", v1.GatewayRequest{})
	if err != nil || status.Status != "uncertain" || status.Message == "" {
		t.Fatal("launch uncertainty hidden", status, err)
	}
	if _, err := svc.gatewayControl(context.Background(), "start", v1.GatewayRequest{}); err == nil {
		t.Fatal("pending guardian launch bypassed")
	}
	if data, err := os.ReadFile(sentinel); err != nil || string(data) != "preserve" {
		t.Fatal("unrelated file changed", err)
	}
}

// TestManagedScopeRejectsReplacement covers the publication seam after listener
// readiness, including replacement between GET and conditional PATCH. The fake
// API is contract evidence; the separate managed suite runs stock Caddy.
func TestManagedScopeRejectsReplacement(t *testing.T) {
	for _, replacement := range []string{"before-observation", "after-observation"} {
		t.Run(replacement, func(t *testing.T) {
			dir := shortTemp(t)
			state, err := openStore(dir)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := state.db.Close(); err != nil {
					t.Error(err)
				}
			}()
			r := managedRecord{Scope: newID(), Status: "stopped", Config: manifest.CaddyDefaults(v1.CaddyConfig{Mode: "managed", Executable: "/explicit/caddy"})}
			owned, err := gateway.ManagedConfig(managedCaddy(r), "/private/fixture/data")
			if err != nil {
				t.Fatal(err)
			}
			// The foreign listener has the conventional scope plus its own route.
			foreign := []byte(strings.Replace(string(owned), r.Scope, "backlot-managed", 1))
			foreign = bytes.Replace(foreign, []byte(`"routes":[]`), []byte(`"routes":[{"@id":"foreign-sentinel","handle":[{"handler":"static_response","body":"preserve"}]}]`), 1)
			var mu sync.Mutex
			current := foreign
			if replacement == "after-observation" {
				current = owned
			}
			patches, applied := 0, 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				if req.Method == http.MethodGet {
					w.Header().Set("Etag", `"foreign"`)
					if bytes.Equal(current, owned) {
						w.Header().Set("Etag", `"owned"`)
					}
					_, _ = w.Write(current)
					current = foreign
					return
				}
				patches++
				if req.Header.Get("If-Match") != `"foreign"` {
					w.WriteHeader(http.StatusPreconditionFailed)
					return
				}
				applied++
				w.WriteHeader(http.StatusOK)
			}))
			defer server.Close()
			r.Config.Endpoint = server.URL
			if err := state.saveManaged(r); err != nil {
				t.Fatal(err)
			}
			svc := &service{store: state, directory: dir}
			snapshot := plan.Snapshot{Caddy: &r.Config, Plan: v1.PlanResponse{Publish: &v1.Publication{Routes: []v1.Route{{Path: "/", Service: "web", Port: "http", Prefix: "preserve"}}}, Components: []v1.PlannedComponent{{Name: "web", Ports: map[string]v1.ServicePort{"http": {Resource: "port"}}}}}}
			journal := runtimeRecord{}
			err = svc.publish(context.Background(), newID(), snapshot, &v1.ExecutionResult{Origin: "https://fixture.backlot.localhost", Ports: map[string]int{"port": 12345}}, &journal)
			var detail *v1.PlanError
			if !errors.As(err, &detail) || detail.Code != "gateway_scope" {
				t.Fatal("replacement gateway was not rejected by private scope")
			}
			mu.Lock()
			defer mu.Unlock()
			if applied != 0 || !bytes.Equal(current, foreign) || replacement == "before-observation" && patches != 0 || replacement == "after-observation" && patches != 1 {
				t.Fatal("foreign gateway configuration/routes changed or ETag race was not exercised")
			}
			if journal.Gateway != nil {
				t.Fatal("definitively refused publication retained false ownership")
			}
		})
	}
}

func TestManagedGatewayPreflightPreservesState(t *testing.T) {
	dir := shortTemp(t)
	state, err := openStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := state.db.Close(); err != nil {
			t.Error(err)
		}
	}()
	svc := &service{store: state, directory: dir}
	r := managedRecord{Scope: newID(), Config: manifest.CaddyDefaults(v1.CaddyConfig{Mode: "managed", Executable: filepath.Join(dir, "absent-executable")}), Status: "interrupted", Failure: "retained interruption"}
	if err := svc.startManaged(context.Background(), &r); err == nil {
		t.Fatal("missing executable accepted")
	}
	root, _, _ := managedPaths(dir)
	if _, err := os.Lstat(root); !os.IsNotExist(err) {
		t.Fatal("executable preflight allocated state", err)
	}
	if err := state.saveManaged(r); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(dir, "machine.json")
	changed := r.Config
	changed.Executable = "/changed/caddy"
	data, _ := json.Marshal(v1.MachineConfig{Version: v1.ManifestVersion, Caddy: &changed})
	if err := os.WriteFile(configPath, data, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.gatewayControl(context.Background(), "restart", v1.GatewayRequest{ConfigPath: configPath}); err == nil {
		t.Fatal("changed restart config accepted")
	}
	after, err := state.managed()
	if err != nil || after.Status != r.Status || after.Failure != r.Failure {
		t.Fatal("rejected restart mutated retained state", err)
	}
}

func TestLegacyCaddyRestartCompatibility(t *testing.T) {
	c := &v1.CaddyConfig{Endpoint: "http://127.0.0.1:2019", Scope: "backlot", DomainSuffix: "example.test", HostAddress: "localhost"}
	old := plan.Snapshot{Caddy: c, Plan: v1.PlanResponse{Publish: &v1.Publication{}, Resources: map[string]v1.Resource{}}}
	next := old
	value := manifest.CaddyDefaults(*c)
	next.Caddy = &value
	if err := safeRestart(old, next); err != nil {
		t.Fatal("explicit external default broke retained scene", err)
	}
}

func TestManagedPendingBootRecovery(t *testing.T) {
	boot, err := native.CurrentBoot()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, boot string
		recover    bool
	}{
		{name: "same-boot", boot: boot},
		{name: "legacy-missing"},
		{name: "invalid", boot: "invalid"},
		{name: "previous-boot", boot: "00000000-0000-0000-0000-000000000001", recover: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			state, err := openStore(shortTemp(t))
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = state.db.Close() }()
			r := managedRecord{Scope: newID(), Config: manifest.CaddyDefaults(v1.CaddyConfig{Mode: "managed", Executable: "/explicit/caddy"}), Pending: true, PendingBoot: tc.boot, Status: "uncertain"}
			if err := state.saveManaged(r); err != nil {
				t.Fatal(err)
			}
			err = state.stopManaged(&r, nil)
			if (err == nil) != tc.recover {
				t.Fatal("incorrect pending recovery decision", err)
			}
			got, err := state.managed()
			if err != nil {
				t.Fatal(err)
			}
			if tc.recover && (got.Pending || got.PendingBoot != "" || got.Status != "stopped") {
				t.Fatal("prior-boot launch intent remained uncertain")
			}
			if !tc.recover && (!got.Pending || got.PendingBoot != tc.boot) {
				t.Fatal("uncertain intent changed")
			}
		})
	}
}

func TestGatewayDefaultRequestValidation(t *testing.T) {
	for _, tc := range []struct {
		name, action string
		request      v1.GatewayRequest
	}{
		{"relative", "start", v1.GatewayRequest{DefaultConfigPath: "relative.yaml"}},
		{"conflicting", "restart", v1.GatewayRequest{ConfigPath: "/explicit.yaml", DefaultConfigPath: "/default.yaml"}},
		{"stop", "stop", v1.GatewayRequest{DefaultConfigPath: "/default.yaml"}},
		{"status", "status", v1.GatewayRequest{DefaultConfigPath: "/default.yaml"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Invalid candidates must fail before any store access or lifecycle effect.
			_, err := (&service{}).gatewayControl(context.Background(), tc.action, tc.request)
			code(t, err, "invalid_request")
		})
	}
}
