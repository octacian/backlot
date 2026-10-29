//go:build darwin && arm64

// Package acceptance exercises public commands against operator-approved providers.
package acceptance

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"image/png"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	v1 "github.com/octacian/backlot/api/v1"
	"github.com/octacian/backlot/internal/manifest"
	"github.com/octacian/backlot/internal/plan"
	"github.com/octacian/backlot/internal/projectconfig"
)

type harness struct {
	t                                    *testing.T
	root, binary, config, project, state string
	daemon                               *exec.Cmd
	daemonLog                            *os.File
	daemonDone                           chan error
	fixtureRepo                          string
	worktrees                            []string
	cleanupUnverified                    bool
	ownerUnresolved                      bool
	dependencies                         []*providerDependency
	logClosed                            bool
	collectionWitness                    string
}

func (h *harness) test(name string, run func(*testing.T)) {
	h.t.Run(name, func(t *testing.T) { parent := h.t; h.t = t; defer func() { h.t = parent }(); run(t) })
}

func (h *harness) command(args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, h.binary, args...)
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	start := time.Now()
	err := command.Run()
	quiet := args[0] == "list" || args[0] == "logs" || args[0] == "inspect" || args[0] == "status" || args[0] == "daemon" && len(args) > 1 && args[1] == "status"
	if !quiet {
		h.t.Logf("command=%s elapsed_ms=%d exit=%v", args[0], time.Since(start).Milliseconds(), err)
	}
	// Private raw responses may include initial disposable lease capabilities.
	// Never put their content in the test log or publication report.
	return stdout.Bytes(), err
}
func (h *harness) instance(operation, target string, extra ...string) (v1.Instance, error) {
	args := []string{operation}
	if operation == "run" || operation == "restart" || operation == "reset" {
		args = append(args, target, "--state-dir", h.state, "--json", "--stop-grace", "1s")
		if operation == "run" {
			args = append(args, "--project", h.project, "--config", h.config)
		}
	} else {
		args = append(args, "--state-dir", h.state, "--json", target)
	}
	args = append(args, extra...)
	data, err := h.command(args...)
	var response v1.InstanceResponse
	if decodeErr := json.Unmarshal(data, &response); decodeErr != nil {
		return v1.Instance{}, fmt.Errorf("%s did not return InstanceResponse: %w", operation, decodeErr)
	}
	if response.Instance.ID == "" {
		var envelope v1.ErrorResponse
		if json.Unmarshal(data, &envelope) == nil && envelope.Error.Code != "" {
			return response.Instance, fmt.Errorf("%s rejected: %s: %s", operation, envelope.Error.Code, envelope.Error.Message)
		}
	}
	if operation == "destroy" && err == nil && response.Instance.Status == v1.Destroyed {
		for _, d := range h.dependencies {
			delete(d.pending, response.Instance.ID)
		}
	}
	return response.Instance, err
}

// runAPI observes asynchronous first-start acceptance directly, before readiness.
func (h *harness) runAPI(scene string) (v1.Instance, error) {
	request := v1.RunRequest{APIVersion: v1.Version, Plan: v1.PlanRequest{Scene: scene, ProjectPath: h.project, ConfigPath: h.config}, Options: v1.ExecutionOptions{StopGrace: "1s"}}
	data, err := json.Marshal(request)
	if err != nil {
		return v1.Instance{}, err
	}
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", filepath.Join(h.state, "daemon.sock"))
	}}
	defer transport.CloseIdleConnections()
	requestHTTP, err := http.NewRequest(http.MethodPost, "http://backlot/v1/run", bytes.NewReader(data))
	if err != nil {
		return v1.Instance{}, err
	}
	requestHTTP.Header.Set("Content-Type", "application/json")
	requestHTTP.Header.Set("X-Backlot-API-Version", v1.Version)
	response, err := (&http.Client{Transport: transport, Timeout: 10 * time.Second}).Do(requestHTTP)
	if err != nil {
		return v1.Instance{}, err
	}
	defer func() { _ = response.Body.Close() }()
	var result v1.InstanceResponse
	if response.StatusCode != http.StatusOK || json.NewDecoder(response.Body).Decode(&result) != nil {
		return result.Instance, errors.New("public run API did not return InstanceResponse")
	}
	return result.Instance, nil
}
func (h *harness) require(operation, target string, extra ...string) v1.Instance {
	h.t.Helper()
	instance, err := h.instance(operation, target, extra...)
	if err != nil {
		h.t.Fatalf("%s failed: status=%s failure=%s collection=%s cleanup=%s", operation, instance.Status, detail(instance, "failure"), detail(instance, "collection"), detail(instance, "cleanup"))
	}
	return instance
}
func detail(i v1.Instance, kind string) string {
	if i.Execution == nil {
		return ""
	}
	switch kind {
	case "failure":
		return i.Execution.Failure
	case "collection":
		return i.Execution.CollectionFailure
	default:
		return i.Execution.CleanupFailure
	}
}
func (h *harness) start() {
	h.t.Helper()
	if err := h.startDaemon(10 * time.Second); err != nil {
		h.t.Fatal(err)
	}
}

// startDaemon owns one Wait immediately after Start. A readiness failure kills
// only the retained child and joins it before unwinding dependent resources.
func (h *harness) startDaemon(readinessTimeout time.Duration) (result error) {
	if h.daemon != nil {
		return errors.New("owned daemon already retained; explicit cleanup required")
	}
	command := exec.Command(h.binary, "daemon", "serve", "--state-dir", h.state, "--lease-duration", "750ms")
	command.Stdout = h.daemonLog
	command.Stderr = h.daemonLog
	if err := command.Start(); err != nil {
		return fmt.Errorf("owned daemon Start failed: %w", err)
	}
	h.daemon = command
	h.daemonDone = make(chan error, 1)
	go func(done chan error) { done <- command.Wait() }(h.daemonDone)
	defer func() {
		if result == nil {
			return
		}
		killErr := command.Process.Kill()
		if errors.Is(killErr, os.ErrProcessDone) {
			killErr = nil
		}
		if h.consumeOwner(5 * time.Second).joined {
			result = errors.Join(result, killErr)
		} else {
			h.cleanupUnverified = true
			h.ownerUnresolved = true
			result = errors.Join(result, killErr, fmt.Errorf("owned startup child unjoined; preserve forwarding/log/state resources at %s", h.root))
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), readinessTimeout)
	defer cancel()
	for ctx.Err() == nil {
		status := exec.CommandContext(ctx, h.binary, "daemon", "status", "--state-dir", h.state, "--json")
		if err := status.Run(); err == nil {
			return nil
		}
		select {
		case <-ctx.Done():
		case <-time.After(25 * time.Millisecond):
		}
	}
	return fmt.Errorf("owned daemon did not become ready: %w", ctx.Err())
}

type ownerJoin struct {
	joined, forced bool
	exit           error
}

// consumeOwner is the sole completion path, also used explicitly after exhaustion.
// It never calls Wait again or signals a reconstructed process identity.
func (h *harness) consumeOwner(timeout time.Duration) ownerJoin {
	if h.daemon == nil {
		return ownerJoin{joined: true}
	}
	select {
	case err := <-h.daemonDone:
		h.daemon = nil
		h.ownerUnresolved = false
		return ownerJoin{joined: true, exit: err}
	case <-time.After(timeout):
		return ownerJoin{}
	}
}
func (h *harness) joinOwner(grace, afterKill time.Duration) ownerJoin {
	if h.ownerUnresolved {
		return ownerJoin{}
	}
	result := h.consumeOwner(grace)
	if result.joined {
		return result
	}
	h.cleanupUnverified = true
	killErr := h.daemon.Process.Kill()
	if errors.Is(killErr, os.ErrProcessDone) {
		killErr = nil
	}
	result = h.consumeOwner(afterKill)
	result.forced = true
	result.exit = errors.Join(result.exit, killErr)
	if !result.joined {
		h.ownerUnresolved = true
	}
	return result
}
func (h *harness) join(expectedCollectionFailure bool) {
	result := h.joinOwner(10*time.Second, 5*time.Second)
	if !result.joined {
		h.t.Errorf("owned daemon remains unjoined; retained command/Wait/log/provider/state at %s; explicit completion required", h.root)
		return
	}
	if result.forced {
		h.t.Errorf("owned daemon graceful join exhausted; retained child killed and joined; evidence %s", h.root)
		return
	}
	var exit *exec.ExitError
	if expectedCollectionFailure && errors.As(result.exit, &exit) && exit.ExitCode() == 1 {
		return
	}
	if result.exit != nil || expectedCollectionFailure {
		h.cleanupUnverified = true
		h.t.Errorf("owned daemon exit: %v; private evidence %s", result.exit, h.root)
	}
}
func (h *harness) crash() {
	h.t.Helper()
	if h.daemon == nil || h.ownerUnresolved {
		h.t.Fatal("owned crash requires resolved retained child")
	}
	if err := h.daemon.Process.Kill(); err != nil {
		h.t.Fatal(err)
	}
	if !h.consumeOwner(5 * time.Second).joined {
		h.ownerUnresolved = true
		h.cleanupUnverified = true
		h.t.Fatal("retained daemon crash did not join; preserve owner/log/provider/state", h.root)
	}
}
func (h *harness) stop() { h.stopDaemon(false) }
func (h *harness) stopDaemon(expectedCollectionFailure bool) {
	fragment := ""
	if expectedCollectionFailure {
		fragment = "collection/missing"
	}
	h.stopDaemonCollection(fragment)
}
func (h *harness) stopDaemonCollection(fragment string) {
	expectedCollectionFailure := fragment != ""
	if h.daemon == nil || h.ownerUnresolved {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, h.binary, "daemon", "stop", "--state-dir", h.state, "--json")
	var stdout bytes.Buffer
	command.Stdout = &stdout
	err := command.Run()
	if expectedCollectionFailure {
		var envelope v1.ErrorResponse
		var exit *exec.ExitError
		if !errors.As(err, &exit) || exit.ExitCode() != 1 || json.Unmarshal(stdout.Bytes(), &envelope) != nil || envelope.Error.Code != "collection_failed" || !strings.Contains(envelope.Error.Message, fragment) {
			h.cleanupUnverified = true
			h.t.Errorf("shutdown did not report exact injected retained collection gap; evidence %s", h.root)
		}
	} else if err != nil {
		h.cleanupUnverified = true
		h.t.Errorf("owned daemon stop failed: %v; evidence %s", err, h.root)
	}
	h.join(expectedCollectionFailure)
}
func (h *harness) list() ([]v1.InstanceSummary, error) {
	var result []v1.InstanceSummary
	after := ""
	for {
		args := []string{"list", "--state-dir", h.state, "--json", "--limit", "3"}
		if after != "" {
			args = append(args, "--after", after)
		}
		data, err := h.command(args...)
		if err != nil {
			return result, fmt.Errorf("discovery failed: %w", err)
		}
		var page v1.ListResponse
		if err := json.Unmarshal(data, &page); err != nil {
			return result, fmt.Errorf("discovery decode failed: %w", err)
		}
		if page.APIVersion != v1.Version {
			return result, errors.New("discovery version mismatch")
		}
		result = append(result, page.Instances...)
		if page.NextAfterID == "" {
			return result, nil
		}
		if page.NextAfterID <= after || len(page.Instances) == 0 || page.Instances[len(page.Instances)-1].ID != page.NextAfterID {
			return result, errors.New("discovery cursor did not advance")
		}
		after = page.NextAfterID
	}
}
func (h *harness) cleanup() {
	instances, err := h.list()
	if err != nil {
		h.cleanupUnverified = true
		h.t.Errorf("cleanup discovery incomplete: %v; preserve %s", err, h.root)
	}
	for _, i := range instances {
		if _, err := h.instance("destroy", i.ID); err != nil {
			h.cleanupUnverified = true
			h.t.Errorf("cleanup preserved instance %s; evidence %s", i.ID, h.root)
		}
	}
	h.stop()
	if err := h.cleanupDependencies(); err != nil {
		h.cleanupUnverified = true
		h.t.Errorf("provider recovery incomplete: %v; preserve %s", err, h.root)
	}
	if !h.cleanupUnverified {
		h.removeWorktrees()
	} else {
		h.t.Errorf("fixture worktrees preserved with unverified cleanup at %s", h.root)
	}
	h.releaseLog()
	if !h.logClosed {
		h.t.Errorf("owner/provider cleanup unresolved; log/state retained at %s", h.root)
	}
	h.t.Logf("private acceptance state/evidence retained at %s (do not publish raw state)", h.root)
}

type applicationReport struct {
	Status      string    `json:"status"`
	Test        string    `json:"test"`
	Arguments   []string  `json:"arguments"`
	Passed      bool      `json:"passed"`
	StartedAt   time.Time `json:"started_at"`
	CompletedAt time.Time `json:"completed_at"`
}

func artifactFile(t *testing.T, i v1.Instance, component, name, file string) []byte {
	t.Helper()
	for _, a := range i.Execution.Artifacts {
		if a.Component == component && a.Name == name && a.Attempt == i.Execution.Attempt {
			data, err := os.ReadFile(filepath.Join(a.Path, file))
			if err != nil || len(data) == 0 {
				t.Fatalf("retained %s/%s/%s empty or unreadable: %v", component, name, file, err)
			}
			return data
		}
	}
	t.Fatalf("retained artifact missing: %s/%s", component, name)
	return nil
}
func browserArtifacts(t *testing.T, i v1.Instance) {
	t.Helper()
	var report applicationReport
	if json.Unmarshal(artifactFile(t, i, "ui-test", "result", "result.json"), &report) != nil || report.Test != "ui-test" || !report.Passed || !reflect.DeepEqual(report.Arguments, []string{"-run", "TestBrowser"}) || report.StartedAt.IsZero() || !report.CompletedAt.After(report.StartedAt) {
		t.Fatal("collected browser report invalid")
	}
	// This fixture emits well-nested HTML. The permissive standard decoder handles
	// HTML void elements/entities while requiring scoped body and element witnesses.
	decoder := xml.NewDecoder(bytes.NewReader(artifactFile(t, i, "ui-test", "browser", "browser.html")))
	decoder.Strict = false
	decoder.AutoClose = xml.HTMLAutoClose
	decoder.Entity = xml.HTMLEntity
	passed := false
	id := ""
	text := map[string]string{}
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal("collected DOM malformed", err)
		}
		switch x := token.(type) {
		case xml.StartElement:
			id = ""
			for _, a := range x.Attr {
				if x.Name.Local == "body" && a.Name.Local == "data-acceptance" && a.Value == "passed" {
					passed = true
				}
				if a.Name.Local == "id" {
					id = a.Value
				}
			}
		case xml.CharData:
			if id == "ssr" || id == "result" {
				text[id] += string(x)
			}
		case xml.EndElement:
			id = ""
		}
	}
	if !passed || text["ssr"] != "SSR: browser-mutated" || text["result"] != "API: browser-mutated; WebSocket verified" {
		t.Fatal("collected scoped DOM witnesses invalid")
	}
	picture, err := png.Decode(bytes.NewReader(artifactFile(t, i, "ui-test", "screenshot", "browser.png")))
	if err != nil || picture.Bounds().Dx() <= 0 || picture.Bounds().Dy() <= 0 {
		t.Fatal("collected screenshot not a nonempty PNG", err)
	}
}
func httpValue(t *testing.T, origin, mutation string) string {
	t.Helper()
	client := &http.Client{Timeout: 10 * time.Second}
	var response *http.Response
	var err error
	if mutation != "" {
		data, _ := json.Marshal(struct {
			Value string `json:"value"`
		}{mutation})
		response, err = client.Post(origin+"/api/value", "application/json", bytes.NewReader(data))
	} else {
		response, err = client.Get(origin + "/api/value")
	}
	if err != nil {
		t.Fatal("trusted origin request failed", err)
	}
	defer func() { _ = response.Body.Close() }()
	var value struct {
		Value string `json:"value"`
	}
	if response.StatusCode != 200 || json.NewDecoder(response.Body).Decode(&value) != nil {
		t.Fatal("public API did not return data")
	}
	return value.Value
}

// TestReleaseWorkflows requires explicit permission/configuration. It never changes
// operator DNS/TLS/gateway setup and invokes only Backlot public lifecycle commands.
func TestReleaseWorkflows(t *testing.T) {
	config := os.Getenv("BACKLOT_ACCEPTANCE_CONFIG")
	if config == "" {
		t.Skip("set BACKLOT_ACCEPTANCE_CONFIG to an approved private machine file")
	}
	config, err := filepath.Abs(config)
	if err != nil {
		t.Fatal(err)
	}
	root, err := newAcceptanceRoot("bl-public-")
	if err != nil {
		t.Fatal(err)
	}
	h := &harness{t: t, root: root, binary: filepath.Join(root, "backlot"), config: config, state: filepath.Join(root, "state")}
	t.Log("candidate source: caller checkout; record git revision and dirty patch digest beside this log")
	for _, build := range []struct{ target, source string }{{h.binary, "../../cmd/backlot"}, {filepath.Join(root, "fixture"), "../../examples/release-acceptance"}} {
		command := exec.Command("go", "build", "-o", build.target, build.source)
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("build fixture: %v %s", err, output)
		}
	}
	repo := filepath.Join(root, "repo")
	if err := os.Mkdir(repo, 0700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"backlot.json", "message.txt"} {
		data, err := os.ReadFile(filepath.Join("../../examples/release-acceptance", name))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(repo, name), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	data, err := os.ReadFile(filepath.Join(root, "fixture"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "fixture"), data, 0700); err != nil {
		t.Fatal(err)
	}
	git := func(args ...string) {
		command := exec.Command("git", append([]string{"-C", repo}, args...)...)
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("fixture git: %v %s", err, output)
		}
	}
	git("init", "-q")
	git("add", ".")
	git("-c", "user.name=Acceptance", "-c", "user.email=acceptance@example.invalid", "commit", "-qm", "fixture")
	one, two := filepath.Join(root, "one"), filepath.Join(root, "two")
	git("worktree", "add", "--detach", one, "HEAD")
	git("worktree", "add", "--detach", two, "HEAD")
	h.project = one
	h.fixtureRepo = repo
	h.worktrees = []string{one, two}
	t.Setenv("BACKLOT_PROJECT_CONFIG_DIR", filepath.Join(root, "project-config"))
	h.daemonLog, err = os.OpenFile(filepath.Join(root, "daemon.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(h.cleanup)
	h.start()
	// Each copied fixture repository explicitly enrolls in owned external storage;
	// never import inputs from machine configuration or use the operator registry.
	inputPath := os.Getenv("BACKLOT_ACCEPTANCE_PROJECT_INPUTS")
	if inputPath == "" {
		t.Fatal("BACKLOT_ACCEPTANCE_PROJECT_INPUTS must select a private inputs document")
	}
	inputData, err := projectconfig.Read(inputPath)
	if err != nil {
		t.Fatal(err)
	}
	var private v1.PrivateConfig
	if err := manifest.Decode(inputData, filepath.Ext(inputPath), &private); err != nil {
		t.Fatal(err)
	}
	setup, err := plan.OpenSetup(v1.ProjectSetupRequest{ProjectPath: one, ConfigPath: config, Edit: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := setup.Save(context.Background(), private, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	sentinels := h.sentinels()
	defer sentinels.cleanup()
	// All clients start before any initial ready response is available.
	var wg sync.WaitGroup
	duplicates := make(chan v1.Instance, 2)
	failures := make(chan error, 2)
	for range 2 {
		wg.Go(func() { i, err := h.runAPI("dev"); duplicates <- i; failures <- err })
	}
	wg.Wait()
	close(duplicates)
	close(failures)
	for err := range failures {
		if err != nil {
			t.Fatal("concurrent initial start failed", err)
		}
	}
	var dev v1.Instance
	for i := range duplicates {
		if i.Status != v1.Starting || dev.ID != "" && (i.ID != dev.ID || i.Execution.Attempt != dev.Execution.Attempt) {
			t.Fatal("concurrent public first-start requests did not join one starting attempt")
		}
		dev = i
	}
	dev = h.await(dev.ID, v1.RuntimeReady)
	origin := dev.Execution.Origin
	t.Logf("dev instance=%s origin=%s", dev.ID, origin)
	if httpValue(t, origin, "persistent-mutation") != "persistent-mutation" {
		t.Fatal("mutation not saved")
	}
	ready := h.require("run", "dev")
	if ready.ID != dev.ID || ready.Execution.Attempt != dev.Execution.Attempt {
		t.Fatal("ready lookup started new work")
	}
	production := h.require("run", "production")
	if production.Execution.Origin == origin || httpValue(t, production.Execution.Origin, "") != "fresh" {
		t.Fatal("preview shares development data/origin")
	}
	h.project = two
	other := h.require("run", "dev")
	if other.ID == dev.ID || other.Execution.Origin == origin || httpValue(t, other.Execution.Origin, "") != "fresh" {
		t.Fatal("real worktree isolation failed")
	}
	h.project = one
	h.test("disposable-isolation-and-selectors", func(t *testing.T) {
		var runs [2]v1.Instance
		var errs [2]error
		for n := range 2 {
			wg.Go(func() {
				runs[n], errs[n] = h.instance("run", "backend-test", "--", "-run", "TestDatabase", "--wait", "500ms")
			})
		}
		wg.Wait()
		for n, i := range runs {
			if errs[n] != nil || i.Status != v1.Succeeded || i.Execution.TerminalExitCode == nil || *i.Execution.TerminalExitCode != 0 || len(i.Execution.Artifacts) != 1 {
				t.Fatal("selected concurrent database run failed")
			}
		}
		var intervals [2]struct {
			StartedAt   time.Time `json:"started_at"`
			CompletedAt time.Time `json:"completed_at"`
		}
		for n, i := range runs {
			data, err := os.ReadFile(filepath.Join(i.Execution.Artifacts[0].Path, "result.json"))
			if err != nil || json.Unmarshal(data, &intervals[n]) != nil {
				t.Fatal("test timing report unavailable")
			}
		}
		overlap := intervals[0].StartedAt.Before(intervals[1].CompletedAt) && intervals[1].StartedAt.Before(intervals[0].CompletedAt)
		if intervals[0].StartedAt.IsZero() || intervals[1].StartedAt.IsZero() || overlap {
			t.Fatal("shared output executions overlapped")
		}
		if runs[0].ID == runs[1].ID || httpValue(t, origin, "") != "persistent-mutation" {
			t.Fatal("disposable mutated persistent preview")
		}
	})
	h.test("selector-variants", func(t *testing.T) {
		for _, pattern := range []string{"^Test(Data|Browser)", "NoSuchTest", "["} {
			i, err := h.instance("run", "backend-test", "--", "-run", pattern)
			var report applicationReport
			if json.Unmarshal(artifactFile(t, i, "backend-test", "result", "result.json"), &report) != nil {
				t.Fatal("selector report malformed")
			}
			if pattern == "^Test(Data|Browser)" {
				if err != nil || i.Status != v1.Succeeded || !report.Passed || !reflect.DeepEqual(report.Arguments, []string{"-run", pattern}) {
					t.Fatal("regex selector failed")
				}
			} else {
				if err == nil || i.Status != v1.Failed || i.Execution.TerminalExitCode == nil || *i.Execution.TerminalExitCode != 1 || report.Passed || report.Status != "not_completed" {
					t.Fatal("invalid/no-match selector did not fail truthfully")
				}
			}
		}
	})
	h.test("failure-evidence-and-keep", func(t *testing.T) {
		failed, err := h.instance("run", "backend-test", "--keep-on-failure", "--", "-run", "TestDatabase", "--fail")
		if err == nil || failed.Status != v1.Failed || !failed.Execution.Kept || failed.Execution.TerminalExitCode == nil || *failed.Execution.TerminalExitCode != 1 || len(failed.Execution.Artifacts) != 1 {
			t.Fatal("kept test failure untruthful")
		}
		logs, err := h.command("logs", "--state-dir", h.state, "--json", failed.ID)
		if err != nil || !bytes.Contains(logs, []byte("TestDatabase")) {
			t.Fatal("historical logs unavailable")
		}
		h.require("destroy", failed.ID)
		logs, err = h.command("logs", "--state-dir", h.state, "--json", failed.ID)
		if err != nil || !bytes.Contains(logs, []byte("TestDatabase")) {
			t.Fatal("destroy erased evidence")
		}
	})
	h.test("retained-data-and-reset", func(t *testing.T) {
		h.require("stop", dev.ID)
		warm := h.require("run", "dev")
		if warm.ID != dev.ID || warm.Execution.Origin != origin || httpValue(t, origin, "") != "persistent-mutation" {
			t.Fatal("stop/start lost retained identity/data")
		}
		h.require("restart", dev.ID)
		if httpValue(t, origin, "") != "persistent-mutation" {
			t.Fatal("restart lost mutation")
		}
		reset := h.require("reset", dev.ID)
		if reset.ID != dev.ID || reset.Execution.Origin != origin || httpValue(t, origin, "") != "fresh" {
			t.Fatal("reset did not replace mutable data")
		}
	})
	sentinels.verify()
	h.test("explicit-preview-update", func(t *testing.T) {
		if err := os.WriteFile(filepath.Join(one, "message.txt"), []byte("explicit-update-witness"), 0600); err != nil {
			t.Fatal(err)
		}
		get := func() string {
			response, err := http.Get(production.Execution.Origin)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = response.Body.Close() }()
			body, _ := io.ReadAll(response.Body)
			return string(body)
		}
		if strings.Contains(get(), "explicit-update-witness") {
			t.Fatal("preview updated without restart")
		}
		h.require("restart", production.ID)
		if !strings.Contains(get(), "explicit-update-witness") {
			t.Fatal("explicit restart did not build preview")
		}
	})
	h.test("production-browser", func(t *testing.T) {
		ui := h.require("run", "ui-test", "--job-timeout", "60s", "--", "-run", "TestBrowser")
		if ui.Status != v1.Succeeded || len(ui.Execution.Artifacts) != 3 {
			t.Fatal("browser evidence missing")
		}
		browserArtifacts(t, ui)
	})

	h.test("published-disposable-worktree-overlap", func(t *testing.T) { h.publishedOverlap(one, two, dev, production) })
	h.test("public-disposable-job-observation-loss", func(t *testing.T) { h.jobObservationFailures() })
	h.test("fixture-secret-boundary", func(t *testing.T) {
		ordinary, err := h.command("fixtures", "--state-dir", h.state, "--json", dev.ID)
		if err != nil {
			t.Fatal(err)
		}
		secret, err := h.command("secret", "--state-dir", h.state, "--component", "api", "--json", dev.ID, "password")
		if err != nil {
			t.Fatal(err)
		}
		var result v1.FixturesResponse
		if json.Unmarshal(secret, &result) != nil || len(result.Fixtures) != 1 || result.Fixtures[0].Value == "" {
			t.Fatal("explicit secret access unavailable")
		}
		if bytes.Contains(ordinary, []byte(result.Fixtures[0].Value)) {
			t.Fatal("ordinary fixture discovery exposed secret")
		}
		for _, args := range [][]string{
			{"run", "dev", "--state-dir", h.state, "--project", h.project, "--config", h.config, "--json"},
			{"run", "dev", "--state-dir", h.state, "--project", h.project, "--config", h.config},
			{"inspect", "--state-dir", h.state, "--json", dev.ID},
			{"status", "--state-dir", h.state, "--json", dev.ID},
			{"list", "--state-dir", h.state, "--json"},
			{"logs", "--state-dir", h.state, "--json", dev.ID},
			{"plan", "dev", "--project", h.project, "--config", h.config, "--json"},
			{"daemon", "status", "--state-dir", h.state, "--json"},
		} {
			output, err := h.command(args...)
			if err != nil || bytes.Contains(output, []byte(result.Fixtures[0].Value)) || bytes.Contains(output, []byte(`"ownership_token"`)) {
				t.Fatalf("ordinary %s output leaked authority/secret or failed", args[0])
			}
		}
		before := sha256.Sum256(secret)
		h.require("restart", dev.ID)
		secret, err = h.command("secret", "--state-dir", h.state, "--component", "api", "--json", dev.ID, "password")
		if err != nil || sha256.Sum256(secret) != before {
			t.Fatal("restart rotated retained credential")
		}
	})
	h.test("cancellation-and-client-loss", func(t *testing.T) {
		for _, loss := range []bool{false, true} {
			data, err := h.command("list", "--state-dir", h.state, "--json")
			if err != nil {
				t.Fatal(err)
			}
			var old v1.ListResponse
			if json.Unmarshal(data, &old) != nil {
				t.Fatal("list decode")
			}
			known := map[string]bool{}
			for _, i := range old.Instances {
				known[i.ID] = true
			}
			command := exec.Command(h.binary, "run", "backend-test", "--project", h.project, "--config", h.config, "--state-dir", h.state, "--json", "--stop-grace", "1s", "--", "-run", "TestDatabase", "--wait", "30s")
			var stdout, stderr bytes.Buffer
			command.Stdout = &stdout
			command.Stderr = &stderr
			if err := command.Start(); err != nil {
				t.Fatal(err)
			}
			var once sync.Once
			reap := func() { once.Do(func() { _ = command.Process.Kill(); _ = command.Wait() }) }
			t.Cleanup(reap)
			id := ""
			started := false
			deadline := time.Now().Add(20 * time.Second)
			for time.Now().Before(deadline) {
				data, err := h.command("list", "--state-dir", h.state, "--json")
				if err != nil {
					t.Fatal(err)
				}
				var list v1.ListResponse
				if json.Unmarshal(data, &list) != nil {
					t.Fatal("list decode")
				}
				for _, i := range list.Instances {
					if !known[i.ID] && i.Scene == "backend-test" {
						id = i.ID
					}
				}
				if id != "" {
					logs, err := h.command("logs", "--state-dir", h.state, "--json", id)
					if err == nil && bytes.Contains(logs, []byte("selected test started")) {
						started = true
						break
					}
				}
				time.Sleep(50 * time.Millisecond)
			}
			if !started {
				phase := "undiscovered"
				if id != "" {
					phase = string(h.require("inspect", id).Status)
				}
				t.Fatalf("terminal start witness absent: instance=%s phase=%s", id, phase)
			}
			if loss {
				if err := command.Process.Kill(); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := command.Process.Signal(os.Interrupt); err != nil {
					t.Fatal(err)
				}
			}
			var exitErr error
			once.Do(func() { exitErr = command.Wait() })
			if !loss {
				var code *exec.ExitError
				if !errors.As(exitErr, &code) || code.ExitCode() != 130 {
					t.Fatal("SIGINT did not preserve executable exit 130")
				}
			}
			deadline = time.Now().Add(15 * time.Second)
			var final v1.Instance
			for time.Now().Before(deadline) {
				final = h.require("inspect", id)
				if final.Status == v1.Cancelled {
					break
				}
				time.Sleep(50 * time.Millisecond)
			}
			if final.ID != id || final.Status != v1.Cancelled || final.Execution == nil || !final.Execution.Cancelled || final.Execution.CleanupFailure != "" {
				t.Fatal("cancel/client-loss cleanup untruthful")
			}
			terminalObserved := false
			for _, c := range final.Execution.Components {
				if c.Name == "backend-test" && c.ExitCode != nil {
					terminalObserved = true
				}
			}
			var partial applicationReport
			if !terminalObserved || json.Unmarshal(artifactFile(t, final, "backend-test", "result", "result.json"), &partial) != nil || partial.Status != "not_completed" || partial.Passed || !partial.CompletedAt.IsZero() {
				t.Fatal("cancelled terminal/partial report untruthful")
			}
			if httpValue(t, origin, "") != "fresh" {
				t.Fatal("cancellation touched persistent data")
			}
		}
	})
	h.test("failed-preparation-and-probe", func(t *testing.T) {
		path := filepath.Join(one, "backlot.json")
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var manifest v1.Manifest
		if json.Unmarshal(data, &manifest) != nil {
			t.Fatal("manifest decode")
		}
		prep := manifest.Components["prepare"]
		prep.Command = &v1.Command{Tool: "fixture", Args: []string{"fail"}}
		manifest.Components["fail-prepare"] = prep
		job := manifest.Components["backend-test"]
		job.Artifacts = nil // This case isolates preparation failure; its terminal job never runs.
		job.DependsOn = []v1.Dependency{{Component: "fail-prepare", Condition: v1.Completed}}
		manifest.Components["prep-test"] = job
		scene := manifest.Scenes["backend-test"]
		scene.Components = []string{"database", "fail-prepare", "prep-test"}
		scene.TerminalJob = "prep-test"
		manifest.Scenes["failed-prep"] = scene
		bad := manifest.Components["api"]
		bad.Readiness = &v1.Probe{Kind: "command", Command: &v1.Command{Tool: "fixture", Args: []string{"fail"}}, Timeout: "200ms"}
		manifest.Components["bad-api"] = bad
		probe := manifest.Scenes["dev"]
		probe.Components = []string{"database", "prepare", "bad-api"}
		probe.Publish = nil
		probe.Resources = append([]string(nil), manifest.Scenes["backend-test"].Resources...)
		probe.Resources = append(probe.Resources, "api-port")
		manifest.Scenes["failed-probe"] = probe
		updated, err := json.Marshal(manifest)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, updated, 0600); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.WriteFile(path, data, 0600) })
		for _, scene := range []string{"failed-prep", "failed-probe"} {
			i, err := h.instance("run", scene)
			if err == nil || i.Status != v1.Failed || i.Execution == nil || i.Execution.Failure == "" {
				t.Fatalf("%s: runtime failure was not observed: status=%s err=%v", scene, i.Status, err)
			}
		}
	})
	h.test("inspection-failure-diagnosis", func(t *testing.T) {
		old := h.project
		h.project = two
		defer func() { h.project = old }()
		// Use a separate fixture scene identity by destroying its earlier worktree dev.
		h.require("destroy", other.ID)
		h.inspectionFailure()
	})
	h.test("shutdown-and-owned-crash-recovery", func(t *testing.T) {
		httpValue(t, origin, "before-shutdown")
		h.stop()
		h.start()
		h.require("restart", dev.ID)
		if httpValue(t, origin, "") != "before-shutdown" {
			t.Fatal("shutdown lost retained data")
		}
		// Retained direct child handle authorizes this fault, never an observed PID.
		h.crash()
		h.start()
		recovered := h.require("status", dev.ID)
		if recovered.Status != v1.Interrupted || recovered.Execution.CollectionFailure == "" || recovered.Execution.CleanupFailure != "" {
			t.Fatal("crash recovery result untruthful")
		}
		h.require("restart", dev.ID)
		if httpValue(t, origin, "") != "before-shutdown" {
			t.Fatal("crash recovery lost retained data")
		}
	})
	h.test("public-negative-workflows", func(t *testing.T) { h.negativeWorkflows() })
	h.test("shutdown-after-history-erasure", func(t *testing.T) {
		if h.collectionWitness == "" {
			t.Fatal("injected collection failure witness absent")
		}
		before := h.require("inspect", h.collectionWitness)
		h.stopDaemon(false)
		h.start()
		after := h.require("inspect", h.collectionWitness)
		if before.Status != v1.Destroyed || after.Status != v1.Destroyed || before.Execution != nil || after.Execution != nil {
			t.Fatal("destroyed diagnostic history resurrected after restart")
		}
		if _, err := os.Stat(filepath.Join(h.state, "artifacts", h.collectionWitness)); !os.IsNotExist(err) {
			t.Fatal("destroy retained artifacts", err)
		}
	})
	sentinels.verify()
}
