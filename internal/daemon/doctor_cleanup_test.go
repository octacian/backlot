//go:build darwin || linux

package daemon

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	v1 "github.com/octacian/backlot/api/v1"
	"github.com/octacian/backlot/internal/client"
	"github.com/octacian/backlot/internal/plan"
)

// This private Engine stand-in allows only reads and returns independently
// controlled absence/uncertainty. No real provider endpoint is contacted.
func doctorRecoveryEngine(t *testing.T) (string, *atomic.Bool, *atomic.Int64, *atomic.Int64) {
	t.Helper()
	listener, err := net.Listen("unix", filepath.Join(shortTemp(t), "engine.sock"))
	if err != nil {
		t.Fatal(err)
	}
	uncertain, inspections, images := &atomic.Bool{}, &atomic.Int64{}, &atomic.Int64{}
	server := &http.Server{ReadHeaderTimeout: time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path == "/_ping" {
			w.Header().Set("API-Version", "1.53")
			return
		}
		if req.Method != http.MethodGet {
			t.Error("no mutation authorized", req.Method)
			w.WriteHeader(403)
			return
		}
		if strings.Contains(req.URL.Path, "/images/") {
			images.Add(1)
		} else {
			inspections.Add(1)
		}
		if uncertain.Load() {
			w.WriteHeader(503)
			_, _ = w.Write([]byte("raw-provider-private-control-path-secret"))
			return
		}
		http.NotFound(w, req)
	})}
	done := make(chan struct{})
	t.Cleanup(func() {
		_ = server.Close()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Error("Engine listener did not join")
		}
	})
	go func() { defer close(done); _ = server.Serve(listener) }()
	return "unix://" + listener.Addr().String(), uncertain, inspections, images
}

func doctorCleanupMessage(t *testing.T, response v1.DoctorResponse, check string) string {
	t.Helper()
	if response.Validate() != nil || response.Healthy {
		t.Fatal("expected typed unhealthy response", response)
	}
	for _, c := range response.Checks {
		if c.Name == check && c.Status == v1.DoctorFailed {
			return c.Message
		}
	}
	t.Fatal("missing failed check", check)
	return ""
}

func TestDoctorRecoveryArtifactDiagnostics(t *testing.T) {
	state, originalKey, original, data := diagnosticCopyFixture(t)
	if err := state.forgetDiagnosticProbe(originalKey); err != nil {
		t.Fatal(err)
	}
	endpoint, uncertain, inspections, _ := doctorRecoveryEngine(t)
	records := map[string]diagnosticProbeRecord{}
	contents := map[string][]byte{}
	modes := map[string]os.FileMode{}
	const total = 11
	for i := range total {
		key := fmt.Sprintf("%064x", i+1)
		ca := *original.CA
		r := diagnosticProbeRecord{Endpoint: endpoint, Resource: ownedResource{Kind: "container", Name: "backlot-doctor-" + key, Token: newID()}, Network: ownedResource{Kind: "network", Name: "backlot-doctor-network-" + key, Token: newID()}, CA: &ca}
		if err := state.saveDiagnosticProbe(key, r); err != nil {
			t.Fatal(err)
		}
		path := diagnosticCACopyPath(state.directory, key)
		if i == 1 {
			if err := state.allocateDiagnosticCACopy(key, &r, data); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(path, 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte("tampered-certificate-marker"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(path, 0444); err != nil {
				t.Fatal(err)
			}
		} else if err := os.WriteFile(path, []byte("unidentified-private-marker"), 0600); err != nil {
			t.Fatal(err)
		}
		records[key] = r
		var err error
		contents[key], err = os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		info, err := os.Lstat(path)
		if err != nil {
			t.Fatal(err)
		}
		modes[key] = info.Mode()
	}
	unrelated := diagnosticCACopyPath(state.directory, strings.Repeat("f", 64))
	if err := os.WriteFile(unrelated, []byte("unrelated-preserve"), 0444); err != nil {
		t.Fatal(err)
	}
	failure := state.recoverDiagnosticProbes()
	code(t, failure, "cleanup_failed")
	svc := service{store: state, directory: state.directory, diagnosticRecoveryErr: failure}
	response, err := svc.doctor(context.Background(), v1.DoctorRequest{APIVersion: v1.Version})
	if err != nil {
		t.Fatal(err)
	}
	message := doctorCleanupMessage(t, response, "diagnostic_cleanup")
	if len(message) >= 2048 || strings.Count(message, "doctor-public-ca-") != 8 || !strings.Contains(message, "omitted entries: 3;") {
		t.Fatal("unbounded or incorrect omission", message)
	}
	for key, r := range records {
		want := diagnosticCACopyName(key) + ": unidentified allocation"
		if key == fmt.Sprintf("%064x", 2) {
			want = diagnosticCACopyName(key) + ": copy mismatch"
		}
		if key <= fmt.Sprintf("%064x", 8) {
			if !strings.Contains(message, want) {
				t.Fatal("lost artifact/cause correlation", want, message)
			}
		} else if strings.Contains(message, key) {
			t.Fatal("omitted entry escaped bound")
		}
		for _, private := range []string{r.Resource.Token, r.Network.Token, r.Endpoint, r.CA.Parent, r.CA.Identity, r.CA.SHA256} {
			if private != "" && strings.Contains(message, private) {
				t.Fatal("private field in recovery message")
			}
		}
		current, err := os.ReadFile(diagnosticCACopyPath(state.directory, key))
		if err != nil || !bytes.Equal(current, contents[key]) {
			t.Fatal("copy changed during failed recovery", err)
		}
		info, err := os.Lstat(diagnosticCACopyPath(state.directory, key))
		if err != nil || info.Mode() != modes[key] {
			t.Fatal("copy mode changed during failed recovery", err)
		}
	}
	if !reflect.DeepEqual(readDoctorCopyRecords(t, state), records) {
		t.Fatal("pending journal metadata changed")
	}
	// An unresolved copy does not prevent a distinct diagnostic owner reaching
	// image inspection. The stand-in's 404 deliberately stops before allocation.
	probeEndpoint, _, _, probeImages := doctorRecoveryEngine(t)
	probe := v1.DoctorResponse{APIVersion: v1.Version, Scope: "daemon", Healthy: true, Checks: []v1.DoctorCheck{}, Paths: []v1.DoctorPath{}}
	svc.doctorContainer(context.Background(), &probe, v1.DoctorRequest{ContainerProbeImage: "already-present-probe"}, plan.Snapshot{Docker: &v1.DockerConfig{Endpoint: probeEndpoint}}, v1.Instance{Execution: &v1.ExecutionResult{Origin: "https://fixture.backlot.localhost:8443"}})
	assertDoctorCheck(t, probe, "container_probe_image", v1.DoctorFailed)
	if probeImages.Load() != 1 || len(svc.diagnosticOwners) != 0 {
		t.Fatal("pending copies fenced new owner or owner not joined")
	}
	// Independently quarantine the exact entries; missing files alone still do
	// not authorize journal forgetting while Engine absence is uncertain.
	for key := range records {
		path := diagnosticCACopyPath(state.directory, key)
		if err := os.Rename(path, path+".quarantined"); err != nil {
			t.Fatal(err)
		}
	}
	uncertain.Store(true)
	before := inspections.Load()
	if err := state.recoverDiagnosticProbes(); err == nil || len(readDoctorCopyRecords(t, state)) != total || inspections.Load() <= before {
		t.Fatal("external absence bypassed Engine proof", err)
	}
	uncertain.Store(false)
	before = inspections.Load()
	if err := state.recoverDiagnosticProbes(); err != nil || len(readDoctorCopyRecords(t, state)) != 0 || inspections.Load() < before+2*total {
		t.Fatal("absence recovery did not inspect both consumer and bridge", err)
	}
	for key := range records {
		current, err := os.ReadFile(diagnosticCACopyPath(state.directory, key) + ".quarantined")
		if err != nil || !bytes.Equal(current, contents[key]) {
			t.Fatal("quarantine changed", err)
		}
	}
	current, err := os.ReadFile(unrelated)
	if err != nil || string(current) != "unrelated-preserve" {
		t.Fatal("unrelated artifact changed", err)
	}
}

func TestDoctorCleanupCausePrivacy(t *testing.T) {
	key := newID()
	r := diagnosticProbeRecord{CA: &diagnosticCACopy{Parent: "1:2", SHA256: strings.Repeat("a", 64)}}
	if !validDiagnosticCACopy(r) {
		t.Fatal("invalid formatting fixture")
	}
	private := strings.Repeat("private-key-token-control-path-PEM\n", 1024)
	for _, cause := range []diagnosticCleanupCause{diagnosticUnidentified, diagnosticCopyMismatch, diagnosticParentMismatch, diagnosticProviderUncertain, diagnosticRemovalFailed} {
		response := v1.DoctorResponse{APIVersion: v1.Version, Scope: "daemon", Healthy: true, Checks: []v1.DoctorCheck{}, Paths: []v1.DoctorPath{}}
		diagnosticCleanupResult(&response, key, r, errors.Join(cause, errors.New(private)))
		message := doctorCleanupMessage(t, response, "container_probe_cleanup")
		if !strings.Contains(message, diagnosticCACopyName(key)+": "+string(cause)) || len(message) >= 2048 || strings.Contains(message, "private-key") {
			t.Fatal("live cleanup lost cause or exposed raw error", message)
		}
	}
	malformed := r
	ca := *r.CA
	ca.Parent = "/arbitrary-private-control-path"
	malformed.CA = &ca
	message := diagnosticRecoveryMessage(diagnosticRecoveryFailureFor(nil, "../../token", malformed, errors.New(private)))
	if strings.Contains(message, "token") || strings.Contains(message, "private") || !strings.Contains(message, "omitted entries: 1;") {
		t.Fatal("invalid metadata exposed", message)
	}
}

func TestDoctorDaemonJoinTimeout(t *testing.T) {
	for _, joined := range []bool{false, true} {
		t.Run(fmt.Sprint("post-kill-joined-", joined), func(t *testing.T) {
			done := make(chan error, 1)
			j := &fixtureOwnerJoin{done: done}
			private := errors.New("private kill cause")
			kills := 0
			kill := func() error {
				kills++
				if joined {
					done <- errors.New("killed child")
				}
				return private
			}
			result := make(chan error, 1)
			go func() { result <- j.join(5*time.Millisecond, 5*time.Millisecond, kill) }()
			var failure error
			select {
			case failure = <-result:
			case <-time.After(time.Second):
				t.Fatal("timeout fallback did not report within its bounded budgets")
			}
			if failure == nil || !strings.Contains(failure.Error(), "exceeded finite join budget") || strings.Contains(failure.Error(), private.Error()) || j.killErr != private || j.joined != joined {
				t.Fatal("timeout outcome or private cause lost", failure)
			}
			if !joined && !strings.Contains(failure.Error(), "Wait owner unfinished; preserving owned fixture state") {
				t.Fatal("unfinished owner not reported", failure)
			}
			// Simulate registered cleanup after failure: it must neither kill again nor
			// consume a late result and relabel the earlier uncertainty as success.
			if !joined {
				done <- nil
			}
			if again := j.join(time.Hour, time.Hour, kill); again != failure || kills != 1 || j.joined != joined {
				t.Fatal("cleanup reentered the owner or discarded its failure", again, kills)
			}
			t.Log(failure)
		})
	}
}

func TestDoctorUnresolvedCopyDaemonAvailability(t *testing.T) {
	retained := os.Getenv("BACKLOT_DOCTOR_RECOVERY_TEST_ROOT")
	rootParent := retained
	if rootParent == "" {
		rootParent = "/tmp"
	}
	root, err := os.MkdirTemp(rootParent, "bl-doctor-")
	if err != nil {
		t.Fatal(err)
	}
	h := &nativeHarness{t: t, lease: "2s", dir: filepath.Join(root, "state"), project: filepath.Join(root, "project"), pids: filepath.Join(root, "pids")}
	h.registerStateCleanup(root, retained != "")
	binary := buildFixtureBinary(t, root, "../../cmd/backlot")
	h.binary = binary
	endpoint, _, inspections, _ := doctorRecoveryEngine(t)
	for _, dir := range []string{h.dir, h.project} {
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	literal := func(s string) v1.Value { return v1.Value{Literal: &s} }
	port := v1.Value{Ref: &v1.Reference{Kind: "resource", Name: "port", Field: "port"}}
	h.manifest = v1.Manifest{Version: v1.ManifestVersion, Project: "doctor-recovery", Tools: map[string]string{"fixture": self}, Resources: map[string]v1.Resource{"port": {Kind: "port"}}, Components: map[string]v1.Component{"server": {Kind: v1.Service, Runtime: v1.Native, Command: &v1.Command{Tool: "fixture", Args: []string{"-test.run=TestNativeFixtureProcess", "--", "server"}}, Resources: []string{"port"}, Ports: map[string]v1.ServicePort{"http": {Resource: "port"}}, Environment: v1.Environment{Assign: map[string]v1.Value{"BACKLOT_NATIVE_FIXTURE": literal("1"), "PORT": port, "PIDFILE": literal(h.pids)}}, Readiness: &v1.Probe{Kind: "tcp", Target: &port, Timeout: "2s"}}}, Scenes: map[string]v1.Scene{"dev": {Lifetime: v1.Persistent, Components: []string{"server"}, Resources: []string{"port"}}}}
	h.write()
	h.cli = client.New(SocketPath(h.dir))
	// Cleanup is retained before allocating the unresolved artifact or starting
	// the daemon child. The configured evidence root is deliberately not removed.
	h.waited = true
	var owner *fixtureOwnerJoin
	join := func() error {
		if owner == nil {
			owner = &fixtureOwnerJoin{done: waitFixtureChild(h.process)}
		}
		err := owner.join(harnessShutdownBudget, 5*time.Second, h.process.Process.Kill)
		h.waited = owner.joined
		if err != nil {
			h.cleanupUnverified = true
			// Keep the raw kill cause only in private fixture evidence.
			if writeErr := os.WriteFile(filepath.Join(root, "daemon-join-private.log"), []byte(fmt.Sprintf("kill: %v; wait: %v\n", owner.killErr, owner.waitErr)), 0600); writeErr != nil {
				t.Error(writeErr)
			}
		}
		return err
	}
	closeDaemon := func() {
		if h.process != nil && h.process.Process != nil && !h.waited {
			ctx, cancel := context.WithTimeout(context.Background(), harnessShutdownBudget)
			_, stopErr := h.cli.Stop(ctx)
			cancel()
			if err := join(); err != nil {
				t.Error(err)
			}
			if owner.joined && owner.waitErr != nil && stopErr == nil {
				t.Error("daemon failed after successful shutdown request", owner.waitErr)
			}
		}
		// Wait owns stderr copying; reading its buffer before join would race.
		if owner != nil && !owner.joined {
			t.Error("daemon Wait owner unfinished; preserving", root)
			h.cli.Close()
			return
		}
		if strings.Contains(h.daemonLog.String(), "DATA RACE") {
			t.Error("daemon race", h.daemonLog.String())
		}
		h.cli.Close()
		h.absent()
	}
	t.Cleanup(closeDaemon)
	state, err := openStore(h.dir)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(h.dir)
	if err != nil {
		t.Fatal(err)
	}
	parent, err := fileIdentity(info)
	if err != nil {
		t.Fatal(err)
	}
	key := newID()
	public := []byte("unidentified-public-certificate-allocation")
	digest := sha256.Sum256(public)
	r := diagnosticProbeRecord{Endpoint: endpoint, Resource: ownedResource{Kind: "container", Name: "backlot-doctor-" + key, Token: newID()}, Network: ownedResource{Kind: "network", Name: "backlot-doctor-network-" + key, Token: newID()}, CA: &diagnosticCACopy{Parent: parent, SHA256: hex.EncodeToString(digest[:])}}
	if err := state.saveDiagnosticProbe(key, r); err != nil {
		t.Fatal(err)
	}
	path := diagnosticCACopyPath(h.dir, key)
	if err := os.WriteFile(path, public, 0600); err != nil {
		t.Fatal(err)
	}
	if err := state.db.Close(); err != nil {
		t.Fatal(err)
	}
	// The existing harness installed direct-child cleanup before starting/faulting.
	h.start()
	response, err := h.cli.Doctor(context.Background(), v1.DoctorRequest{APIVersion: v1.Version})
	if err != nil {
		t.Fatal("unresolved well-formed copy blocked startup", err)
	}
	message := doctorCleanupMessage(t, response, "diagnostic_cleanup")
	if !strings.Contains(message, diagnosticCACopyName(key)+": unidentified allocation") {
		t.Fatal("daemon discarded artifact/cause", message)
	}
	// Run an actual native service and exercise a new TCP observation through the
	// daemon API: the diagnostic journal is not an application/native fence.
	run := h.run("dev")
	ready := h.state(run.Instance.ID, v1.RuntimeReady)
	response, err = h.cli.Doctor(context.Background(), v1.DoctorRequest{APIVersion: v1.Version, InstanceID: ready.Instance.ID})
	if err != nil {
		t.Fatal(err)
	}
	assertDoctorCheck(t, response, "upstream_server_http", v1.DoctorOK)
	_ = doctorCleanupMessage(t, response, "diagnostic_cleanup")
	if _, err := h.cli.StopExecution(context.Background(), ready.Instance.ID); err != nil {
		t.Fatal(err)
	}
	h.state(ready.Instance.ID, v1.Stopped)
	h.absent()
	_, stopErr := h.cli.Stop(context.Background())
	code(t, stopErr, "cleanup_failed")
	if err := join(); err != nil {
		t.Fatal(err)
	}
	var exit *exec.ExitError
	if !errors.As(owner.waitErr, &exit) || exit.ExitCode() == 0 {
		t.Fatal("unresolved copy shutdown must be nonzero", owner.waitErr)
	}
	if strings.Contains(h.daemonLog.String(), "DATA RACE") {
		t.Fatal("daemon race", h.daemonLog.String())
	}
	if err := os.WriteFile(filepath.Join(root, "daemon-unresolved.log"), h.daemonLog.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	state, err = openStore(h.dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = state.db.Close() }()
	if len(readDoctorCopyRecords(t, state)) != 1 {
		t.Fatal("shutdown lost unresolved journal")
	}
	current, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(current, public) {
		t.Fatal("daemon changed unresolved copy", err)
	}
	// Operator quarantine happens only after actual daemon/diagnostic/native joins.
	if err := os.Rename(path, path+".quarantined"); err != nil {
		t.Fatal(err)
	}
	before := inspections.Load()
	if err := state.recoverDiagnosticProbes(); err != nil || inspections.Load() < before+2 || len(readDoctorCopyRecords(t, state)) != 0 {
		t.Fatal("exact absent copy failed recovery", err)
	}
	if err := state.db.Close(); err != nil {
		t.Fatal(err)
	}
	owner = nil // The prior daemon is joined; the next child gets its own owner.
	h.start()
	response, err = h.cli.Doctor(context.Background(), v1.DoctorRequest{APIVersion: v1.Version})
	if err != nil || !response.Healthy {
		t.Fatal("reconciled daemon unhealthy", response, err)
	}
	closeDaemon()
	t.Log("actual daemon served unresolved copy, admitted new native service/TCP diagnostic, reported unhealthy/nonzero shutdown, joined owners and recovered only after quarantine plus Engine absence")
}
