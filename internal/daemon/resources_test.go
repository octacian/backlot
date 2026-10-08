package daemon

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

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
			config := v1.MachineConfig{Version: v1.ManifestVersion, Docker: &v1.DockerConfig{Endpoint: "unix://" + filepath.Join(root, "unavailable.sock")}}
			switch name {
			case "explicit-passthrough":
				nativeJob.Environment.PassThrough = []string{"TMPDIR"}
			case "explicit-seed":
				nativeJob.Environment.Seeds = []string{"launch.env"}
			case "secret-input":
				m.Inputs = map[string]v1.Input{"location": {Secret: true}}
				config.Inputs = map[string]string{"location": a}
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
				config.Inputs["location"] = b
			}
			if name == "secret-assignment" {
				nativeJob.Environment.Assign["TMPDIR"] = v1.Value{Literal: &b, Secret: true}
				m.Components["native"] = nativeJob
			}
			write(b)
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
