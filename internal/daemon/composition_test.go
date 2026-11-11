//go:build darwin || linux

package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	v1 "github.com/octacian/backlot/api/v1"
	"github.com/octacian/backlot/internal/plan"
)

func TestCompositionRuntimeConsumersAndBounds(t *testing.T) {
	ref := func(field string) v1.Value { return fixtureRef("resource", "db", field) }
	endpoint := composeEndpoint(ref("host"), ref("port"))
	connection := composeFormat("postgres://%s:%s@%s/%s", ref("username"), ref("password"), endpoint, ref("database"))
	snapshot := plan.Snapshot{Secrets: map[string]v1.PlannedValue{}, Plan: v1.PlanResponse{Resources: map[string]v1.Resource{"db": {Kind: "allocation"}, "origin": {Kind: "origin"}}, Publish: &v1.Publication{Resource: "origin"}}}
	entry := &execution{shared: map[string]sharedConnection{"db": {Alias: "db-private", Port: 15432, InternalPort: 5432, Request: v1.ManagerRequest{Username: "app", Password: "p %s:@", Database: "db_name"}}}}
	result := &v1.ExecutionResult{Origin: "https://instance.example.test:8443", Ports: map[string]int{}}
	for _, tc := range []struct {
		runtime v1.Runtime
		want    string
	}{{v1.Native, "postgres://app:p %s:@@127.0.0.1:15432/db_name"}, {v1.Container, "postgres://app:p %s:@@db-private:5432/db_name"}} {
		got, err := resolvedValue(plannedValue(connection), "server/environment/DATABASE_URL", "id", snapshot, result, entry, tc.runtime)
		if err != nil || got != tc.want {
			t.Fatalf("%s: %q %v", tc.runtime, got, err)
		}

		path := v1.Value{FilePath: &v1.FilePathValue{Base: ref("host"), Parts: []v1.Value{ref("port"), fixtureLiteral(".."), fixtureLiteral("outside")}}}
		path.Secret = true
		snapshot.Secrets["server/environment/PRIVATE_PATH"] = plannedValue(composeFormat("%s", path))
		got, err = resolvedValue(v1.PlannedValue{Redacted: true}, "server/environment/PRIVATE_PATH", "id", snapshot, result, entry, tc.runtime)
		wantPath := "127.0.0.1/outside"
		if tc.runtime == v1.Container {
			wantPath = "db-private/outside"
		}
		if err != nil || got != wantPath {
			t.Fatalf("private file actual consumer %s: %q %v", tc.runtime, got, err)
		}
	}
	origin := fixtureRef("resource", "origin", "url")
	value := v1.Value{URLPath: &v1.URLPathValue{Base: origin, Parts: []v1.Value{fixtureLiteral("/api/a b/")}}}
	got, err := resolvedValue(plannedValue(value), "", "id", snapshot, result, entry, v1.Native)
	if err != nil || got != "https://instance.example.test:8443/api/a%20b/" {
		t.Fatal(got, err)
	}
	got, err = resolvedValue(plannedValue(fixtureRef("resource", "origin", "hostname")), "", "id", snapshot, result, entry, v1.Container)
	if err != nil || got != "instance.example.test" {
		t.Fatal(got, err)
	}
	for _, n := range []int{(1 << 20) - 1, 1 << 20} {
		entry.shared["db"] = sharedConnection{Request: v1.ManagerRequest{Password: strings.Repeat("x", n)}}
		_, err := resolvedValue(plannedValue(composeFormat("x%s", ref("password"))), "server/environment/PRIVATE", "id", snapshot, result, entry, v1.Native)
		if (err == nil) != (n == (1<<20)-1) {
			t.Fatal("hydrated cap", n, err)
		}
		if err != nil && strings.Contains(err.Error(), strings.Repeat("x", 100)) {
			t.Fatal("hydrated failure leaked")
		}
	}
}

func TestCompositionPublicationUsesPathAndAuthority(t *testing.T) {
	seen := make(chan string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case seen <- r.URL.Path:
		default:
		}
		w.Header().Set("Backlot-Instance", "127.0.0.1")
		if r.URL.Path != "/api/ready" {
			w.WriteHeader(503)
		}
	}))
	defer server.Close()
	origin := fixtureRef("resource", "origin", "url")
	value := v1.Value{URLPath: &v1.URLPathValue{Base: origin, Parts: []v1.Value{fixtureLiteral("/api/ready")}}}
	snapshot := plan.Snapshot{Plan: v1.PlanResponse{Publish: &v1.Publication{Resource: "origin", Probe: v1.Probe{Target: &value}}}}
	target, err := publicationTarget("id", snapshot, &v1.ExecutionResult{Origin: server.URL}, &execution{})
	if err != nil {
		t.Fatal(err)
	}
	if err := awaitPublication(context.Background(), target, "1s", nil); err != nil {
		t.Fatal("aggregate path unused", err)
	}
	if path := <-seen; path != "/api/ready" {
		t.Fatal("aggregate path unused", path)
	}
	value = composeFormat("%s@evil.invalid/", origin)
	snapshot.Plan.Publish.Probe.Target = &value
	if _, err := publicationTarget("id", snapshot, &v1.ExecutionResult{Origin: server.URL}, &execution{}); err == nil {
		t.Fatal("authority replacement accepted")
	}
}

func TestCompositionConsumedPortsAndRetainedArguments(t *testing.T) {
	port := fixtureRef("resource", "port", "port")
	value := v1.Value{FilePath: &v1.FilePathValue{Base: fixtureLiteral("base"), Parts: []v1.Value{composeFormat("%s", port), fixtureLiteral(".."), fixtureLiteral("leaf")}}}
	for _, role := range []string{"native argv", "container argv", "probe argv", "probe target", "private env"} {
		c := v1.PlannedComponent{Name: "consumer"}
		snapshot := plan.Snapshot{Secrets: map[string]v1.PlannedValue{"consumer/environment/PRIVATE": plannedValue(value)}}
		switch role {
		case "native argv":
			c.Command = &v1.Command{Args: []v1.Argument{{Value: value}}}
		case "container argv":
			c.Args = []v1.Argument{{Value: value}}
		case "probe argv":
			c.Readiness = &v1.PlannedProbe{Command: &v1.Command{Args: []v1.Argument{{Value: value}}}}
		case "probe target":
			v := plannedValue(value)
			c.Readiness = &v1.PlannedProbe{Target: &v}
		case "private env":
			c.Environment = map[string]v1.PlannedValue{"PRIVATE": {Redacted: true}}
		}
		consumed := map[string]string{}
		consumeComponentPorts(consumed, c, snapshot)
		if consumed["port"] != "consumer" {
			t.Fatal("lost consumed port", role)
		}
	}
	old := plan.Snapshot{Plan: v1.PlanResponse{Resources: map[string]v1.Resource{"data": {Kind: "directory"}}, Components: []v1.PlannedComponent{{Name: "init", Runtime: v1.Native, Policy: v1.FreshOnly, Resources: []string{"data"}, Command: &v1.Command{Tool: "sh", Args: v1.StringArguments("a b")}}}}}
	data, _ := json.Marshal(old)
	var next plan.Snapshot
	if err := json.Unmarshal(data, &next); err != nil {
		t.Fatal(err)
	}
	next.Plan.Components[0].Command.Args = []v1.Argument{{Value: fixtureLiteral("a b")}}
	if err := safeRestart(old, next); err != nil {
		t.Fatal("equivalent scalar/literal argv drift", err)
	}
	next.Plan.Components[0].Command.Args = []v1.Argument{{Value: port}}
	if err := safeRestart(old, next); err == nil {
		t.Fatal("fresh-only symbolic argv drift ignored")
	}
	after, _ := json.Marshal(old)
	if !bytes.Equal(data, after) {
		t.Fatal("compatibility comparison rewrote snapshot")
	}
}

func TestCompositionProbeRetryProvenance(t *testing.T) {
	c := v1.PlannedComponent{Name: "server", Ports: map[string]v1.ServicePort{"http": {Resource: "port"}}}
	ports := map[string]int{"port": 12345}
	reference := fixtureRef("service", "server", "port")
	reference.Ref.Port = "http"
	value := composeFormat("http://%s/ready", composeEndpoint(fixtureLiteral("127.0.0.1"), reference))
	if !probeUsesAllocation(plannedValue(value), c, ports, "http", "http://127.0.0.1:12345/ready") {
		t.Fatal("nested destination allocation lost retry eligibility")
	}
	if probeUsesAllocation(plannedValue(value), c, ports, "http", "http://127.0.0.1:54321/12345") {
		t.Fatal("path reference conferred retry provenance for unrelated destination")
	}
	if probeUsesAllocation(v1.PlannedValue{Literal: func() *string { s := "127.0.0.1:12345"; return &s }()}, c, ports, "tcp", "127.0.0.1:12345") {
		t.Fatal("literal acquired retry provenance")
	}
}

func TestCompositionSharedSQL(t *testing.T) {
	f := newSharedSQLFixture(t, false, "postgresql")
	ref := func(field string) v1.Value { return fixtureRef("resource", "database", field) }
	endpoint := composeEndpoint(ref("host"), ref("port"))
	dsn := composeFormat("postgres://%s:%s@%s/%s?sslmode=disable&connect_timeout=3", ref("username"), ref("password"), endpoint, ref("database"))
	for _, name := range []string{"native", "container"} {
		c := f.h.manifest.Components[name]
		c.Environment = cloneFixtureEnvironment(c.Environment)
		c.Environment.Assign["DB_DSN"] = dsn
		f.h.manifest.Components[name] = c
	}
	// A mismatching composed password must fail even though the old separate env values are correct.
	wrong := f.h.manifest.Components["native"]
	wrong.Environment = cloneFixtureEnvironment(wrong.Environment)
	wrong.Environment.Assign["DB_DSN"] = composeFormat("postgres://%s:wrong-%s@%s/%s?sslmode=disable&connect_timeout=3", ref("username"), ref("password"), endpoint, ref("database"))
	f.h.manifest.Components["wrong"] = wrong
	f.h.manifest.Scenes["wrong"] = v1.Scene{Lifetime: v1.Persistent, Components: []string{"wrong"}, Resources: []string{"database"}}
	f.writeBoth(t)
	for _, tc := range []struct{ scene, path, component string }{{"host", f.h.project, "native"}, {"bridge", f.linked, "container"}} {
		response := f.run(tc.scene, tc.path)
		value := response.Instance.Plan.Components[0].Environment["DB_DSN"]
		if !value.Redacted || value.Literal != nil || value.Composition != nil || value.Symbolic != nil {
			t.Fatal("database DSN exposed")
		}
		credentials := f.credential(t, response.Instance.ID, tc.component)
		if bytes.Contains(f.cli("inspect", "--state-dir", f.h.dir, "--json", response.Instance.ID), []byte(credentials.password)) {
			t.Fatal("ordinary inspection exposed DSN password")
		}
		t.Logf("real %s SQL workload consumed composed endpoint/connection string, performed DDL/DML and retained whole-value redaction", tc.component)
	}
	response, err := f.h.cli.Run(context.Background(), f.request("wrong", f.h.project))
	if err != nil {
		t.Fatal(err)
	}
	failed := f.wait(t, response.Instance.ID, v1.Failed, v1.RuntimeReady)
	if failed.Instance.Status != v1.Failed || failed.Instance.Execution == nil || failed.Instance.Execution.CleanupFailure != "" {
		t.Fatal("composed SQL password was ignored or cleanup failed")
	}
	t.Log("mismatching composed password rejected by actual SQL workload despite correct separate env credentials")
}

func TestCompositionRetainsReferenceFailure(t *testing.T) {
	snapshot := plan.Snapshot{Plan: v1.PlanResponse{Components: []v1.PlannedComponent{{Name: "native", Runtime: v1.Native}}}}
	value := composeFormat("http://%s", composeEndpoint(fixtureRef("service", "native", "host"), fixtureLiteral("8080")))
	_, err := resolvedValue(plannedValue(value), "client/environment/URL", "id", snapshot, &v1.ExecutionResult{}, &execution{}, v1.Container)
	var detail *v1.PlanError
	if !errors.As(err, &detail) || detail.Code != "missing_host_address" {
		t.Fatal("nested reference lost actionable safe failure", err)
	}
}
