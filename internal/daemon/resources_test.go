package daemon

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/moby/moby/client"
	v1 "github.com/octacian/backlot/api/v1"
	"github.com/octacian/backlot/internal/plan"
)

func TestRetainedDriftContracts(t *testing.T) {
	literal := func(text string) v1.PlannedValue { return v1.PlannedValue{Literal: &text} }
	base := plan.Snapshot{Plan: v1.PlanResponse{Resources: map[string]v1.Resource{"data": {Kind: "volume"}}, Components: []v1.PlannedComponent{{Name: "db", Runtime: v1.Container, Image: ptrPlanned(literal("db:1")), Resources: []string{"data"}, Mounts: []v1.Mount{{Resource: "data", Target: "/data"}}, Environment: map[string]v1.PlannedValue{"PASSWORD": {Redacted: true}, "ORDINARY": literal("one")}}}}, Secrets: map[string]v1.PlannedValue{"db/environment/PASSWORD": literal("private")}}
	clone := func() plan.Snapshot {
		copy := base
		copy.Plan.Components = append([]v1.PlannedComponent{}, base.Plan.Components...)
		copy.Secrets = map[string]v1.PlannedValue{}
		for key, v := range base.Secrets {
			copy.Secrets[key] = v
		}
		return copy
	}
	ordinary := clone()
	ordinary.Plan.Components[0].Environment = map[string]v1.PlannedValue{"PASSWORD": {Redacted: true}, "ORDINARY": literal("two")}
	ordinary.Plan.Components[0].Readiness = &v1.PlannedProbe{Timeout: "3s"}
	if err := safeRestart(base, ordinary); err != nil {
		t.Fatal("ordinary environment/probe drift blocked", err)
	}
	for _, mutate := range []func(*plan.Snapshot){
		func(s *plan.Snapshot) { s.Plan.Resources = map[string]v1.Resource{"data": {Kind: "directory"}} },
		func(s *plan.Snapshot) { s.Plan.Components[0].Image = ptrPlanned(literal("db:2")) },
		func(s *plan.Snapshot) { s.Plan.Components[0].Mounts = []v1.Mount{{Resource: "data", Target: "/other"}} },
		func(s *plan.Snapshot) {
			s.Plan.Components[0].HostMappings = []v1.HostMapping{{Origin: "origin", Host: "host-gateway"}}
		},
		func(s *plan.Snapshot) { s.Secrets["db/environment/PASSWORD"] = literal("changed") },
		func(s *plan.Snapshot) {
			s.Plan.Components[0].Policy = v1.FreshOnly
			s.Plan.Components[0].Initializes = []string{"data"}
		},
	} {
		next := clone()
		mutate(&next)
		if err := safeRestart(base, next); err == nil {
			t.Fatal("unsafe retained drift accepted")
		}
	}
}
func ptrPlanned(value v1.PlannedValue) *v1.PlannedValue { return &value }
func TestDirectoryOwnershipPreserved(t *testing.T) {
	dir := t.TempDir()
	r := ownedResource{Kind: "directory", Name: filepath.Join(dir, "data"), Token: newID()}
	if err := os.Mkdir(r.Name, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(r.Name, ".backlot-owner"), []byte("unrelated"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := verifyResource(context.Background(), nil, r); err == nil {
		t.Fatal("unrelated directory adopted")
	}
	if _, err := os.Stat(r.Name); err != nil {
		t.Fatal("uncertain directory modified", err)
	}
}

func TestInstanceNetworkLifecycle(t *testing.T) {
	for _, mode := range []string{"stopped", "missing", "pending", "remove-gap", "create-gap", "provider", "delete-denied", "ownership", "consumer", "container", "collision"} {
		t.Run(mode, func(t *testing.T) {
			root, dir := shortTemp(t), shortTemp(t)
			config := writeFixture(t, root)
			state, err := openStore(dir)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = state.db.Close() })
			svc := &service{store: state, directory: dir, lease: time.Minute}
			prepared, err := svc.prepare(context.Background(), request(root, config, "dev"))
			if err != nil {
				t.Fatal(err)
			}
			id, attempt := prepared.Instance.ID, newID()
			n := ownedResource{Kind: "network", Name: "backlot-" + newID(), Token: newID(), ID: newID()}
			var present, fail, foreign, mismatch atomic.Bool
			present.Store(mode != "missing")
			fail.Store(mode == "provider" || mode == "delete-denied")
			foreign.Store(mode == "consumer")
			mismatch.Store(mode == "ownership")
			var creates, removes atomic.Int32
			d := forceEngine(t, func(w http.ResponseWriter, r *http.Request) {
				switch {
				case fail.Load() && (mode != "delete-denied" || r.Method == http.MethodDelete):
					w.WriteHeader(http.StatusServiceUnavailable)
				case r.URL.Path == "/containers/json":
					if r.URL.Query().Get("all") != "1" {
						t.Error("stopped consumers omitted")
					}
					if foreign.Load() {
						_, _ = w.Write([]byte(`[{"Id":"foreign"}]`))
					} else {
						_, _ = w.Write([]byte(`[]`))
					}
				case r.URL.Path == "/containers/foreign/json":
					_ = json.NewEncoder(w).Encode(map[string]any{"NetworkSettings": map[string]any{"Networks": map[string]any{n.Name: map[string]string{"NetworkID": n.ID}}}})
				case r.URL.Path == "/networks/create":
					var body struct {
						Name   string
						Labels map[string]string
					}
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Name != n.Name || body.Labels["io.backlot.owner"] == n.Token {
						t.Error("replacement lost name or reused retired authority", err)
					}
					creates.Add(1)
					_ = json.NewEncoder(w).Encode(map[string]string{"Id": newID()})
				case strings.HasPrefix(r.URL.Path, "/networks/"):
					if !present.Load() {
						w.WriteHeader(http.StatusNotFound)
						return
					}
					if r.Method == http.MethodDelete {
						removes.Add(1)
						present.Store(false)
						w.WriteHeader(http.StatusNoContent)
						return
					}
					labels := resourceLabels(n)
					if mismatch.Load() {
						labels["io.backlot.owner"] = newID()
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"Id": n.ID, "Name": n.Name, "Labels": labels})
				default:
					t.Error("unexpected provider mutation/target", r.Method, r.URL.Path)
					w.WriteHeader(http.StatusInternalServerError)
				}
			})
			secret := ownedResource{Kind: "secret", Name: "secret", Token: newID(), Value: newID()}
			secret.ID = secret.Token
			g := resourceGeneration{ID: newID(), Endpoint: d.DaemonHost(), Resources: map[string]ownedResource{"@network": n, "secret": secret}, Initialized: map[string]bool{"init": true}, BuildImages: map[string]v1.BuildImageResult{"image": {ID: "sha256:" + newID()}}}
			if mode == "pending" || mode == "create-gap" {
				pending := n
				pending.ID = ""
				g.Resources["@network"] = pending
			}
			if mode == "container" {
				g.Containers = []ownedResource{{Kind: "container", Name: "owned", Token: newID(), Attempt: attempt}}
			}
			if err := state.saveResources(id, g); err != nil {
				t.Fatal(err)
			}
			result := &v1.ExecutionResult{Attempt: attempt, CompletedAt: time.Now().UTC().Format(time.RFC3339Nano), Outcome: v1.Stopped}
			if _, err := state.execution(id, v1.Stopped, result); err != nil {
				t.Fatal(err)
			}
			if err := state.saveRuntime(id, runtimeRecord{Attempt: attempt}); err != nil {
				t.Fatal(err)
			}
			if mode == "remove-gap" {
				state.sharedCheckpoint = func(string) error { return errors.New("remove effect not acknowledged") }
			}
			// Model an old stopped instance without an in-memory execution owner.
			var stopped v1.Instance
			if mode == "stopped" {
				response, stopErr := svc.scene(v1.SceneTarget{CWD: root, ProjectPath: root, Scene: "dev"}, true)
				if stopErr != nil || response.Result == nil || response.Outcome != v1.SceneAlreadyStopped {
					t.Fatal("named stop of historical instance", stopErr)
				}
				stopped = response.Result.Instance
			} else {
				stopped, err = svc.stopExecution(id)
			}
			wantFailure := mode == "provider" || mode == "delete-denied" || mode == "ownership" || mode == "consumer" || mode == "container" || mode == "remove-gap"
			if (err != nil) != wantFailure || stopped.Status != v1.Stopped || stopped.Execution.CollectionFailure != "" {
				t.Fatal("selected stop lost completion or failure facts", err)
			}
			if wantFailure {
				retained, _ := state.resources(id)
				ids, _ := state.runtimeRecoveryIDs()
				if retained.Resources["@network"].Removed || len(ids) != 1 || stopped.Execution.CleanupFailure == "" {
					t.Fatal("unsettled authority or retry selection lost")
				}
				if mode != "remove-gap" && removes.Load() != 0 {
					t.Fatal("unsafe removal attempted")
				}
				if mode == "remove-gap" {
					if _, err := state.execution(id, v1.Stopping, stopped.Execution); err != nil {
						t.Fatal(err)
					}
				}
				if mode == "provider" || mode == "delete-denied" || mode == "ownership" || mode == "consumer" {
					if mode == "delete-denied" {
						if err := state.saveRuntime(id, runtimeRecord{Attempt: attempt, LaunchPending: true}); err != nil {
							t.Fatal(err)
						}
						if err := state.recoverExecution(id); err != nil {
							t.Fatal(err)
						}
					}
					if _, err := state.abandonNative(context.Background(), id, attempt); err == nil {
						t.Fatal("native waiver erased independent network failure")
					}
					if err := state.saveRuntime(id, runtimeRecord{Attempt: attempt}); err != nil {
						t.Fatal(err)
					}
				}
				fail.Store(false)
				foreign.Store(false)
				mismatch.Store(false)
				g.Containers = nil
				retained.Containers = nil
				if err := state.saveResources(id, retained); err != nil {
					t.Fatal(err)
				}
				state.sharedCheckpoint = nil
				wantStatus := v1.Stopped
				if mode == "delete-denied" {
					wantStatus = v1.Interrupted
				}
				if retried, err := svc.stopExecution(id); err != nil || retried.Status != wantStatus || (retried.Execution.CollectionFailure != "") != (mode == "delete-denied") {
					t.Fatal("explicit retry", err)
				}
			}
			retired, err := state.resources(id)
			if err != nil || !retired.Resources["@network"].Removed || present.Load() {
				t.Fatal("network retirement not settled", err)
			}
			if mode == "collision" {
				present.Store(true)
				mismatch.Store(true)
			}
			snap := plan.Snapshot{Docker: &v1.DockerConfig{Endpoint: g.Endpoint}, Plan: v1.PlanResponse{Resources: map[string]v1.Resource{"secret": {Kind: "secret"}}, Components: []v1.PlannedComponent{{Runtime: v1.Container}}}}
			if mode == "missing" {
				retired.Resources["@network"] = n
			}
			if mode == "create-gap" {
				pending := n
				pending.ID = ""
				retired.Resources["@network"] = pending
				present.Store(true)
			}
			if err := state.saveResources(id, retired); err != nil {
				t.Fatal(err)
			}
			entry := &execution{}
			err = svc.allocateResources(context.Background(), id, snap, entry)
			if entry.docker != nil {
				_ = entry.docker.Close()
			}
			if (err != nil) != (mode == "collision") {
				t.Fatal("network replacement/reconciliation", err)
			}
			recreated, _ := state.resources(id)
			if recreated.ID != g.ID || recreated.Resources["secret"] != secret || !reflect.DeepEqual(recreated.Initialized, g.Initialized) || !reflect.DeepEqual(recreated.BuildImages, g.BuildImages) || recreated.Resources["@network"].Name != n.Name {
				t.Fatal("replacement changed retained generation or references")
			}
			if mode == "collision" && creates.Load() != 0 || mode == "create-gap" && (creates.Load() != 0 || recreated.Resources["@network"].ID != n.ID) {
				t.Fatal("foreign collision mutated or interrupted create duplicated")
			}
		})
	}
}

func TestDockerEndpointRemainsLocal(t *testing.T) {
	for _, endpoint := range []string{"", "tcp://remote.example:2375", "https://remote.example", "unix://host/tmp/docker.sock", "unix:///tmp/docker.sock?query=true"} {
		if d, err := newDocker(endpoint); err == nil {
			_ = d.Close()
			t.Fatal("nonlocal/invalid Docker endpoint accepted")
		}
	}
	d, err := newDocker("unix:/tmp/backlot-no-contact.sock")
	if err != nil {
		t.Fatal("supported local spelling rejected", err)
	}
	_ = d.Close()
}

func TestBindSourcesOverlap(t *testing.T) {
	root := t.TempDir()
	owned := filepath.Join(root, "data")
	child := filepath.Join(owned, "sub")
	sibling := filepath.Join(root, "data-other")
	for _, path := range []string{child, sibling} {
		if err := os.MkdirAll(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	alias := filepath.Join(root, "alias")
	if err := os.Symlink(child, alias); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, source string
		want         bool
	}{
		{"same", owned, true}, {"child", child, true}, {"parent", root, true},
		{"alias", alias, true}, {"missing-child", filepath.Join(owned, "missing"), true}, {"missing-sibling", filepath.Join(root, "missing"), false}, {"sibling-component", sibling, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := bindSourcesOverlap(owned, tc.source)
			if err != nil || got != tc.want {
				t.Fatalf("overlap=%v err=%v", got, err)
			}
		})
	}
	canonicalParent, err := canonicalBindSource(alias + "/..")
	canonicalOwned, ownedErr := filepath.EvalSymlinks(owned)
	if err != nil || ownedErr != nil || canonicalParent != canonicalOwned {
		t.Fatal("symlink parent resolved lexically", err, ownedErr)
	}
	if _, err := canonicalBindSource(alias + "/../missing"); err == nil {
		t.Fatal("unresolved alias parent accepted")
	}
	dangling := filepath.Join(root, "dangling")
	if err := os.Symlink(filepath.Join(root, "missing"), dangling); err != nil {
		t.Fatal(err)
	}
	if _, err := bindSourcesOverlap(owned, dangling); err == nil {
		t.Fatal("uncertain source accepted")
	}
}

func TestForeignBindUncertaintyAndAliases(t *testing.T) {
	root := t.TempDir()
	owned := filepath.Join(root, "data")
	if err := os.Mkdir(owned, 0700); err != nil {
		t.Fatal(err)
	}
	dangling := filepath.Join(root, "dangling")
	if err := os.Symlink(filepath.Join(root, "absent"), dangling); err != nil {
		t.Fatal(err)
	}
	for _, source := range []string{dangling, filepath.Join(root, "missing"), root + "/missing/../other", "relative"} {
		if overlap, err := engineBindSourceOverlap(owned, source, false, filepath.EvalSymlinks); overlap || err == nil {
			t.Fatal("unknown foreign source was classified unrelated", overlap, err)
		}
		if err := validatePrivateBindBoundary(source, owned); err == nil && source != filepath.Join(root, "missing") {
			t.Fatal("foreign policy relaxed ordinary private mount validation")
		}
	}
	// Existing noncanonical aliases retain actual filesystem semantics.
	alias := filepath.Join(root, "alias")
	if err := os.Symlink(owned, alias); err != nil {
		t.Fatal(err)
	}
	if overlap, err := engineBindSourceOverlap(owned, alias+"/..", false, filepath.EvalSymlinks); !overlap || err != nil {
		t.Fatal("resolvable noncanonical parent hidden", overlap, err)
	}

	t.Run("case-alias", func(t *testing.T) {
		alias := filepath.Join(root, "DATA")
		if _, err := os.Stat(alias); os.IsNotExist(err) {
			t.Skip("filesystem is case sensitive")
		}
		if overlap, err := engineBindSourceOverlap(owned, alias, false, filepath.EvalSymlinks); !overlap || err != nil {
			t.Fatal("case alias hidden", overlap, err)
		}
	})
}

func TestDockerConsumerInventoryFailuresAndMixedIdentities(t *testing.T) {
	owned := t.TempDir()
	for _, mode := range []string{"list-error", "inspect-error", "unknown-then-known", "known-then-unknown", "unknown-only"} {
		t.Run(mode, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch {
				case strings.HasSuffix(r.URL.Path, "/containers/json"):
					if r.URL.Query().Get("all") != "1" {
						t.Error("stopped declarations omitted")
					}
					if mode == "list-error" {
						w.WriteHeader(http.StatusServiceUnavailable)
						return
					}
					_ = json.NewEncoder(w).Encode([]map[string]any{{"Id": "foreign"}})
				case strings.HasSuffix(r.URL.Path, "/containers/foreign/json"):
					if mode == "inspect-error" {
						w.WriteHeader(http.StatusServiceUnavailable)
						return
					}
					sources := []string{filepath.Join(owned, "absent")}
					if mode == "unknown-then-known" {
						sources = append(sources, owned)
					}
					if mode == "known-then-unknown" {
						sources = append([]string{owned}, sources...)
					}
					var mounts []map[string]any
					for _, source := range sources {
						mounts = append(mounts, map[string]any{"Type": "bind", "Source": source})
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"Id": "foreign", "Mounts": mounts})
				default:
					http.NotFound(w, r)
				}
			}))
			t.Cleanup(server.Close)
			engine, err := client.New(client.WithHost(server.URL), client.WithAPIVersion("1.53"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = engine.Close() })
			err = (&dockerEngine{engine}).noConsumers(context.Background(), resourceGeneration{Resources: map[string]ownedResource{"data": {Kind: "directory", Name: owned}}})
			if (err == nil) != (mode == "unknown-only") {
				t.Fatal("consumer classification", err)
			}
		})
	}
}

// Foreign metadata only blocks cleanup when a viable identity positively overlaps.
// Unknown identities remain unknown, but ownership authorizes directory deletion.
func TestDockerBindConsumerIdentity(t *testing.T) {
	root := t.TempDir()
	owned, sibling := filepath.Join(root, "data"), filepath.Join(root, "data-other")
	child := filepath.Join(owned, "sub")
	for _, path := range []string{child, sibling} {
		if err := os.MkdirAll(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	alias := filepath.Join(root, "alias")
	if err := os.Symlink(child, alias); err != nil {
		t.Fatal(err)
	}
	canonical, err := filepath.EvalSymlinks(owned)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, source string
		blocked      bool
	}{
		{"same", owned, true}, {"parent", root, true}, {"child", child, true},
		{"alias", alias, true}, {"sibling", sibling, false},
		{"desktop-same", "/host_mnt" + canonical, runtime.GOOS == "darwin"},
		{"desktop-parent", "/host_mnt" + filepath.Dir(canonical), runtime.GOOS == "darwin"},
		{"desktop-child", "/host_mnt" + filepath.Join(canonical, "sub"), runtime.GOOS == "darwin"},
		{"desktop-sibling", "/host_mnt" + filepath.Join(filepath.Dir(canonical), "data-other"), false},
		{"desktop-sibling-unknown-provider", "/host_mnt" + filepath.Join(filepath.Dir(canonical), "data-other"), false},
		{"desktop-sibling-nonlinux-provider", "/host_mnt" + filepath.Join(filepath.Dir(canonical), "data-other"), false},
		{"desktop-sibling-info-unavailable", "/host_mnt" + filepath.Join(filepath.Dir(canonical), "data-other"), false},
		{"unresolved-sibling", filepath.Join(root, "missing"), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch {
				case strings.HasSuffix(r.URL.Path, "/containers/json"):
					_ = json.NewEncoder(w).Encode([]map[string]any{{"Id": "foreign"}})
				case strings.HasSuffix(r.URL.Path, "/containers/foreign/json"):
					_ = json.NewEncoder(w).Encode(map[string]any{"Id": "foreign", "Mounts": []map[string]any{{"Type": "bind", "Source": tc.source, "Destination": "/used", "RW": true}}})
				case strings.HasSuffix(r.URL.Path, "/info"):
					operatingSystem, osType := "Docker Desktop", "linux"
					switch tc.name {
					case "desktop-sibling-unknown-provider":
						operatingSystem = "Other Engine"
					case "desktop-sibling-nonlinux-provider":
						osType = "windows"
					case "desktop-sibling-info-unavailable":
						w.WriteHeader(http.StatusServiceUnavailable)
						return
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"OperatingSystem": operatingSystem, "OSType": osType})
				default:
					http.NotFound(w, r)
				}
			}))
			t.Cleanup(server.Close)
			engine, err := client.New(client.WithHost(server.URL), client.WithAPIVersion("1.53"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = engine.Close() })
			err = (&dockerEngine{engine}).noConsumers(context.Background(), resourceGeneration{Resources: map[string]ownedResource{"data": {Kind: "directory", Name: owned}}})
			if (err != nil) != tc.blocked {
				t.Fatalf("blocked=%v want=%v error=%v", err != nil, tc.blocked, err)
			}
			var detail *v1.PlanError
			if err != nil && (!errors.As(err, &detail) || detail.Code != "cleanup_failed") {
				t.Fatal("expected explicit preservation diagnostic", err)
			}
		})
	}
}

func TestEngineBindSourceAmbiguity(t *testing.T) {
	root := t.TempDir()
	owned, sibling := filepath.Join(root, "data"), filepath.Join(root, "other")
	for _, path := range []string{owned, sibling} {
		if err := os.Mkdir(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	// All resolver results remain real owned paths. No global /host_mnt directory
	// is created, and both raw/translated spellings exercise the production check.
	source := "/host_mnt" + owned
	for _, tc := range []struct {
		name, raw, translated string
		want                  bool
	}{
		{"raw-sibling-translated-owned", sibling, owned, true},
		{"raw-owned-translated-sibling", owned, sibling, true},
		{"both-sibling", sibling, sibling, false},
		{"raw-unresolved-translated-owned", "", owned, true},
		{"raw-owned-translated-unresolved", owned, "", true},
		{"raw-denied-translated-owned", "denied", owned, true},
		{"raw-owned-translated-denied", owned, "denied", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resolve := func(path string) (string, error) {
				if path == source {
					if tc.raw == "" {
						return "", os.ErrNotExist
					}
					if tc.raw == "denied" {
						return "", os.ErrPermission
					}
					return tc.raw, nil
				}
				if path == owned {
					if tc.translated == "" {
						return "", os.ErrNotExist
					}
					if tc.translated == "denied" {
						return "", os.ErrPermission
					}
					return tc.translated, nil
				}
				t.Fatal("unexpected namespace candidate", path)
				return "", os.ErrNotExist
			}
			overlap, err := engineBindSourceOverlap(owned, source, true, resolve)
			if err != nil || overlap != tc.want {
				t.Fatal("dual identity comparison", overlap, tc.want, err)
			}
		})
	}
	for _, tc := range []struct {
		name, source string
		desktop      bool
	}{
		{"unverified-provider", source, false},
		{"unresolved-counterpart", "/host_mnt" + filepath.Join(root, "missing"), true},
		{"unclean-metadata", "/host_mnt/../tmp", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := engineBindSourceOverlap(owned, tc.source, tc.desktop, filepath.EvalSymlinks); err == nil {
				t.Fatal("uncertain engine metadata declared unrelated")
			}
		})
	}
	if overlap, err := engineBindSourceOverlap(owned, source, false, func(string) (string, error) { return sibling, nil }); overlap || err == nil {
		t.Fatal("unknown namespace must remain unknown even with unrelated raw identity", overlap, err)
	}
	if overlap, err := engineBindSourceOverlap(owned, source, false, func(string) (string, error) { return owned, nil }); !overlap || err != nil {
		t.Fatal("unverified translation hid known raw overlap", overlap, err)
	}
}

func TestDockerLogBytesThroughHistoricalAPI(t *testing.T) {
	root := shortTemp(t)
	config := writeFixture(t, root)
	dir := shortTemp(t)
	state, err := openStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = state.db.Close() })
	svc := &service{store: state, directory: dir, lease: time.Minute}
	prepared, err := svc.prepare(context.Background(), request(root, config, "dev"))
	if err != nil {
		t.Fatal(err)
	}
	id := prepared.Instance.ID
	if err := os.Mkdir(filepath.Join(dir, "logs"), 0700); err != nil {
		t.Fatal(err)
	}
	file, err := os.OpenFile(filepath.Join(dir, "logs", id+"-output.ndjson"), os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	writer := dockerLogWriter{file: file, mu: &sync.Mutex{}, id: id, component: "fixture", attempt: newID(), stream: "stdout"}
	want := append(bytes.Repeat([]byte{0}, 16384), bytes.Repeat([]byte{255, 254}, 8192)...)
	want = append(want, []byte("ordinary text \u2603\n")...)
	if _, err := writer.Write(want); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.destroy(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(v1.LogsRequest{APIVersion: v1.Version, InstanceID: id})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/logs", bytes.NewReader(body))
	req.Header.Set("X-Backlot-API-Version", v1.Version)
	req.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	handler(svc, v1.DaemonStatusResponse{}, func() {}).ServeHTTP(recorder, req)
	if recorder.Code != http.StatusOK {
		t.Fatal("historical logs API failed", recorder.Code, recorder.Body.String())
	}
	var response v1.LogsResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	var got bytes.Buffer
	binary := false
	for _, record := range response.Records {
		if record.Data != "" {
			binary = true
			data, err := base64.StdEncoding.DecodeString(record.Data)
			if err != nil {
				t.Fatal(err)
			}
			got.Write(data)
		} else {
			got.WriteString(record.Message)
		}
	}
	if response.Active || !binary || !bytes.Equal(got.Bytes(), want) {
		t.Fatal("control, binary or ordinary bytes not retained after destroy")
	}
}

func TestPlannerRestartBaselineProvenance(t *testing.T) {
	for _, name := range []string{"implicit-location", "native-graph-addition", "explicit-passthrough", "explicit-seed", "secret-input", "secret-assignment", "fresh-only-baseline"} {
		t.Run(name, func(t *testing.T) {
			root := shortTemp(t)
			a, b := filepath.Join(root, "launch-a"), filepath.Join(root, "launch-b")
			for _, path := range []string{a, b} {
				if err := os.Mkdir(path, 0700); err != nil {
					t.Fatal(err)
				}
			}
			t.Setenv("TMPDIR", a)
			tool, err := exec.LookPath("true")
			if err != nil {
				t.Fatal(err)
			}
			nativeJob := v1.Component{Kind: v1.Job, Runtime: v1.Native, Policy: v1.EachStart, Command: &v1.Command{Tool: "true"}}
			m := v1.Manifest{Version: v1.ManifestVersion, Project: "baseline-fixture", Tools: map[string]string{"true": tool}, Resources: map[string]v1.Resource{"data": {Kind: "volume"}, "directory": {Kind: "directory"}}, Components: map[string]v1.Component{
				"image": {Kind: v1.Job, Runtime: v1.Container, Policy: v1.EachStart, Image: &v1.Value{Literal: ptrString("alpine:3.21")}, Resources: []string{"data"}, Mounts: []v1.Mount{{Resource: "data", Target: "/data"}}},
			}, Scenes: map[string]v1.Scene{"dev": {Lifetime: v1.Persistent, Components: []string{"image", "native"}, Resources: []string{"data", "directory"}}}}
			home := shortTemp(t)
			t.Setenv("HOME", home)
			t.Setenv("XDG_CONFIG_HOME", home)
			inputs := map[string]string{}
			config := v1.MachineConfig{Version: v1.ManifestVersion, Docker: &v1.DockerConfig{Endpoint: "unix://" + filepath.Join(root, "unavailable.sock")}}
			switch name {
			case "explicit-passthrough":
				nativeJob.Environment.PassThrough = []string{"TMPDIR"}
			case "explicit-seed":
				nativeJob.Environment.Seeds = []string{"launch.env"}
			case "secret-input":
				m.Inputs = map[string]v1.Input{"location": {Secret: true}}
				inputs = map[string]string{"location": a}
				nativeJob.Environment.Assign = map[string]v1.Value{"TMPDIR": {Ref: &v1.Reference{Kind: "input", Name: "location"}}}
			case "secret-assignment":
				nativeJob.Environment.Assign = map[string]v1.Value{"TMPDIR": {Literal: &a, Secret: true}}
			case "fresh-only-baseline":
				nativeJob.Policy = v1.FreshOnly
				nativeJob.Resources = []string{"directory"}
				nativeJob.Initializes = []string{"directory"}
			}
			m.Components["native"] = nativeJob
			write := func(seed string) {
				t.Helper()
				for path, value := range map[string]any{"backlot.json": m, "machine.json": config} {
					data, err := json.Marshal(value)
					if err != nil {
						t.Fatal(err)
					}
					if err = os.WriteFile(filepath.Join(root, path), data, 0600); err != nil {
						t.Fatal(err)
					}
				}
				if err := os.WriteFile(filepath.Join(root, "launch.env"), []byte("TMPDIR="+seed+"\n"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			write(a)
			if len(inputs) > 0 {
				fixtureInputs(t, root, filepath.Join(root, "machine.json"), inputs)
			}
			source, err := plan.Locate(root)
			if err != nil {
				t.Fatal(err)
			}
			request := v1.PlanRequest{ProjectPath: root, ConfigPath: filepath.Join(root, "machine.json"), Scene: "dev"}
			old, err := plan.Prepare(source, request)
			if err != nil {
				t.Fatal(err)
			}
			// Provenance survives the private snapshot persistence format; values
			// themselves remain in the separately protected secrets map.
			data, err := json.Marshal(old)
			if err != nil {
				t.Fatal(err)
			}
			var persisted plan.Snapshot
			if err = json.Unmarshal(data, &persisted); err != nil || !reflect.DeepEqual(old.ImplicitBaseline, persisted.ImplicitBaseline) {
				t.Fatal("implicit provenance not retained", err)
			}
			persisted.Secrets = old.Secrets
			old = persisted
			t.Setenv("TMPDIR", b)
			if name == "native-graph-addition" {
				t.Setenv("TMPDIR", a)
				m.Components["extra"] = v1.Component{Kind: v1.Job, Runtime: v1.Native, Policy: v1.EachStart, Command: &v1.Command{Tool: "true"}}
				scene := m.Scenes["dev"]
				scene.Components = append(scene.Components, "extra")
				m.Scenes["dev"] = scene
			}
			if name == "secret-input" {
				inputs["location"] = b
			}
			if name == "secret-assignment" {
				nativeJob.Environment.Assign["TMPDIR"] = v1.Value{Literal: &b, Secret: true}
				m.Components["native"] = nativeJob
			}
			write(b)
			if len(inputs) > 0 {
				fixtureInputs(t, root, filepath.Join(root, "machine.json"), inputs)
			}
			next, err := plan.Prepare(source, request)
			if err != nil {
				t.Fatal(err)
			}
			err = safeRestart(old, next)
			wantReject := name != "implicit-location" && name != "native-graph-addition"
			if (err != nil) != wantReject {
				t.Fatalf("protected drift rejection=%v want=%v: %v", err != nil, wantReject, err)
			}
			key := "native/environment/TMPDIR"
			if (name == "explicit-passthrough" || name == "explicit-seed" || name == "secret-input" || name == "secret-assignment") && (old.ImplicitBaseline[key] || next.ImplicitBaseline[key]) {
				t.Fatal("explicit configuration mislabeled implicit")
			}
		})
	}
}

// TestResourceFreeRestartSnapshot checks the actual durable JSON round trip,
// which omits an allocated-empty map. Real resource additions/removals remain drift.
func TestResourceFreeRestartSnapshot(t *testing.T) {
	next := plan.Snapshot{Plan: v1.PlanResponse{Resources: map[string]v1.Resource{}}}
	data, err := json.Marshal(next)
	if err != nil {
		t.Fatal(err)
	}
	var saved plan.Snapshot
	if err := json.Unmarshal(data, &saved); err != nil {
		t.Fatal(err)
	}
	if saved.Plan.Resources != nil {
		t.Fatal("fixture did not exercise omitted empty resources")
	}
	if err := safeRestart(saved, next); err != nil {
		t.Fatal("resource-free restart rejected", err)
	}
	withResource := next
	withResource.Plan.Resources = map[string]v1.Resource{"port": {Kind: "port"}}
	for _, pair := range [][2]plan.Snapshot{{saved, withResource}, {withResource, next}} {
		var diagnostic *v1.PlanError
		if err := safeRestart(pair[0], pair[1]); !errors.As(err, &diagnostic) || diagnostic.Code != "unsafe_drift" {
			t.Fatal("actual resource drift accepted", err)
		}
	}
}
