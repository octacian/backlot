//go:build darwin && arm64

package acceptance

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	v1 "github.com/octacian/backlot/api/v1"
)

// A truncated Docker multiplex frame is a transport collection failure, rather
// than clean EOF. The job and provider remain live until public supervision stops
// them. Only this private proxy's one owned job is affected.
type truncatedJobLogs struct {
	io.ReadCloser
	armed  *atomic.Bool
	faults *atomic.Int32
	broken bool
}

func (r *truncatedJobLogs) Read(p []byte) (int, error) {
	if r.broken {
		return 0, io.ErrUnexpectedEOF
	}
	n, err := r.ReadCloser.Read(p)
	if n > 0 && r.armed.CompareAndSwap(true, false) {
		r.broken = true
		r.faults.Add(1)
		return 1, nil
	}
	return n, err
}
func (h *harness) jobObservationFailures() {
	for _, collection := range []bool{false, true} {
		name := map[bool]string{false: "inspection", true: "log-collection"}[collection]
		h.test(name, func(t *testing.T) { h.jobObservationFailure(collection) })
	}
}
func (h *harness) jobObservationFailure(collection bool) {
	t := h.t
	root, err := os.MkdirTemp(h.root, "job-loss-")
	if err != nil {
		t.Fatal(err)
	}
	config := h.machine()
	upstream, err := url.Parse(config.Docker.Endpoint)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "engine.sock")
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Chmod(path, 0600); err != nil {
		t.Fatal(err)
	}
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", upstream.Path)
	}}
	target, _ := url.Parse("http://docker")
	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.Transport = transport
	proxy.ErrorLog = log.New(io.Discard, "", 0)
	proxy.ErrorHandler = func(w http.ResponseWriter, _ *http.Request, _ error) { w.WriteHeader(http.StatusBadGateway) }
	var armed atomic.Bool
	var faults atomic.Int32
	var stopped atomic.Pointer[int]
	var ownedID atomic.Pointer[string]
	var ignoredForeign atomic.Int32
	proxy.ModifyResponse = func(response *http.Response) error {
		path := response.Request.URL.Path
		if strings.Contains(path, "/containers/") && strings.HasSuffix(path, "/json") && response.StatusCode == http.StatusOK {
			data, err := io.ReadAll(response.Body)
			_ = response.Body.Close()
			if err != nil {
				return err
			}
			response.Body = io.NopCloser(bytes.NewReader(data))
			var observation struct {
				ID    string
				State struct {
					Running  bool
					ExitCode int
				}
			}
			if json.Unmarshal(data, &observation) == nil && observation.ID != "" {
				if observation.State.Running {
					id := observation.ID
					ownedID.CompareAndSwap(nil, &id)
				}
				if !observation.State.Running {
					if id := ownedID.Load(); id != nil && *id == observation.ID {
						code := observation.State.ExitCode
						stopped.Store(&code)
					} else {
						ignoredForeign.Add(1)
					}
				}
			}
		}
		if collection && strings.Contains(path, "/containers/") && strings.HasSuffix(path, "/logs") && response.Request.URL.Query().Get("follow") == "1" {
			response.Body = &truncatedJobLogs{ReadCloser: response.Body, armed: &armed, faults: &faults}
		}
		return nil
	}
	server := &http.Server{ReadHeaderTimeout: 5 * time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !collection && r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/containers/") && strings.HasSuffix(r.URL.Path, "/json") && armed.CompareAndSwap(true, false) {
			faults.Add(1)
			select {
			case <-time.After(3 * time.Second):
			case <-r.Context().Done():
				return
			}
		}
		proxy.ServeHTTP(w, r)
	})}
	var child *harness
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	defer func() {
		if child != nil && child.daemon != nil {
			t.Error("owned child unjoined; forwarding resources preserved", root)
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(ctx); err != nil {
			t.Error("job observation proxy shutdown failed", err)
		}
		select {
		case <-done:
		case <-ctx.Done():
			t.Error("job observation proxy unjoined; preserve", root)
		}
		transport.CloseIdleConnections()
	}()
	config.Docker.Endpoint = "unix://" + path
	configPath := filepath.Join(root, "machine.json")
	data, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(configPath, data, 0600); err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(h.project, "backlot.json")
	original, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := os.WriteFile(manifestPath, original, 0600); err != nil {
			t.Error(err)
		}
	}()
	var m v1.Manifest
	if json.Unmarshal(original, &m) != nil {
		t.Fatal("job manifest malformed")
	}
	image := literal("alpine:3.21")
	m.Components["observation-job"] = v1.Component{Kind: v1.Job, Runtime: v1.Container, Policy: v1.EachStart, Image: &image, Args: []string{"sh", "-c", `echo job-ready; while true; do echo job-tick; sleep 0.1; done`}}
	m.Scenes["observation-job"] = v1.Scene{Lifetime: v1.Disposable, Components: []string{"observation-job"}, TerminalJob: "observation-job"}
	h.writeManifest(m)
	child = &harness{t: t, root: root, binary: h.binary, config: configPath, project: h.project, state: filepath.Join(root, "state")}
	child.daemonLog, err = os.OpenFile(filepath.Join(root, "daemon.log"), os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if child.daemon == nil {
			_ = child.daemonLog.Close()
		} else {
			t.Error("owned child unjoined; log preserved", root)
		}
	}()
	collectionObserved := false
	defer func() {
		if collectionObserved {
			child.stopDaemonCollection("container output collection failed")
		} else {
			child.stop()
		}
	}()
	child.start()
	p := child.launch(child.project, configPath, "observation-job")
	started := child.started(child.project, "observation-job", "observation-job", "job-ready")
	defer func() {
		if _, err := child.instance("destroy", started.ID); err != nil {
			t.Error("job-loss fixture destroy failed; preserve", root)
		}
	}()
	if ownedID.Load() == nil {
		t.Fatal("owned running container inspection witness absent")
	}
	armed.Store(true)
	failed, err := p.result(t)
	collectionObserved = collection && failed.Execution != nil && failed.Execution.CollectionFailure != ""
	cause := "Docker inspection failed"
	if collection {
		cause = "container output collection failed"
	}
	if err == nil || failed.ID != started.ID || failed.Status != v1.Failed || faults.Load() != 1 || !strings.Contains(detail(failed, "failure"), cause) || strings.Contains(detail(failed, "failure"), "owner exited") || strings.Contains(detail(failed, "failure"), "exit status unavailable") || failed.Execution.CleanupFailure != "" {
		t.Fatalf("job failure cause/status lost: failure=%s collection=%s cleanup=%s", detail(failed, "failure"), detail(failed, "collection"), detail(failed, "cleanup"))
	}
	if collection != (failed.Execution.CollectionFailure != "") {
		t.Fatal("inspection and collection failures were conflated")
	}
	actual := stopped.Load()
	terminal := failed.Execution.TerminalExitCode
	componentExit := component(failed, "observation-job").ExitCode
	if actual == nil || terminal == nil || componentExit == nil || *actual != *terminal || *actual != *componentExit {
		t.Fatalf("post-stop owned Docker exit mismatch: observed=%s terminal=%s component=%s", exitWitness(actual), exitWitness(terminal), exitWitness(componentExit))
	}
	recovered := child.require("inspect", failed.ID)
	if recovered.Execution.TerminalExitCode == nil || *recovered.Execution.TerminalExitCode != *actual || !strings.Contains(recovered.Execution.Failure, cause) {
		t.Fatal("historical job failure/actual exit provenance lost")
	}
	t.Logf("public job loss cause=%s injected_faults=%d actual_post_stop_exit=%d; overall failure retained, collection distinction preserved; unrelated cleanup inspections ignored=%d", cause, faults.Load(), *actual, ignoredForeign.Load())
}

func exitWitness(code *int) string {
	if code == nil {
		return "unknown"
	}
	return fmt.Sprint(*code)
}
