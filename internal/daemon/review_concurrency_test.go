//go:build darwin || linux

package daemon

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	v1 "github.com/octacian/backlot/api/v1"
)

func cloneFixtureEnvironment(environment v1.Environment) v1.Environment {
	copy := environment
	copy.Assign = map[string]v1.Value{}
	for key, value := range environment.Assign {
		copy.Assign[key] = value
	}
	return copy
}

func testConsumedPortConflict(t *testing.T, binary, kind, reference string) {
	h := newNativeHarness(t, binary)
	first := h.manifest.Components["server"]
	first.Environment = cloneFixtureEnvironment(first.Environment)
	delete(first.Environment.Assign, "FIXTURE_CONTROL")
	firstPIDs := filepath.Join(h.project, "ahead-pids")
	h.extraPIDFiles = append(h.extraPIDFiles, firstPIDs)
	first.Resources = []string{"port"}
	ref := &v1.Reference{Kind: "resource", Name: "port", Field: "port"}
	if reference == "service" {
		ref = &v1.Reference{Kind: "service", Name: "server", Field: "port", Port: "http"}
	}
	first.Environment.Assign["BACKEND_PORT"] = v1.Value{Ref: ref, Secret: kind == "job"}
	resources := []string{"port"}
	condition := v1.Completed
	if kind == "service" {
		first.Resources = append(first.Resources, "first")
		first.Ports = map[string]v1.ServicePort{"http": {Resource: "first"}}
		first.Environment.Assign["PORT"] = v1.Value{Ref: &v1.Reference{Kind: "resource", Name: "first", Field: "port"}}
		first.Environment.Assign["PIDFILE"] = v1.Value{Literal: ptr(firstPIDs)}
		first.Readiness = &v1.Probe{Kind: "tcp", Target: &v1.Value{Ref: &v1.Reference{Kind: "resource", Name: "first", Field: "port"}}, Timeout: "2s"}
		h.manifest.Resources["first"] = v1.Resource{Kind: "port"}
		resources = append(resources, "first")
		condition = v1.Ready
	} else {
		first.Kind = v1.Job
		first.Policy = v1.EachStart
		first.Ports = nil
		first.Readiness = nil
		first.Command = &v1.Command{Tool: "fixture", Args: []string{"-test.run=^TestNativeFixtureProcess$", "--", "prep"}}
		first.Environment.Assign["JOBPIDFILE"] = v1.Value{Literal: ptr(firstPIDs)}
	}
	h.manifest.Components["ahead"] = first
	server := h.manifest.Components["server"]
	server.Environment = cloneFixtureEnvironment(server.Environment)
	gatePath := filepath.Join(h.project, "bind-gate.sock")
	gate, err := net.ListenUnix("unix", &net.UnixAddr{Name: gatePath, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = gate.Close() })
	server.Environment.Assign["FIXTURE_BIND_GATE"] = v1.Value{Literal: ptr(gatePath)}
	server.DependsOn = []v1.Dependency{{Component: "ahead", Condition: condition}}
	h.manifest.Components["server"] = server
	h.manifest.Scenes["dev"] = v1.Scene{Lifetime: v1.Persistent, Components: []string{"ahead", "server"}, Resources: resources}
	delete(h.manifest.Scenes, "test")
	h.write()
	request := h.request("dev")
	request.Options.StartupTimeout = "10s"
	response, err := h.cli.Run(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	originalPort := 0
	deadline := time.Now().Add(time.Second)
	for originalPort == 0 && time.Now().Before(deadline) {
		current, err := h.cli.Inspect(context.Background(), response.Instance.ID)
		if err != nil {
			t.Fatal(err)
		}
		originalPort = current.Instance.Execution.Ports["port"]
		time.Sleep(time.Millisecond)
	}
	if originalPort == 0 {
		t.Fatal("allocation not observed")
	}
	// Hold the real root before bind, rather than assuming that activation has
	// emitted output when allocation first becomes visible. An early conflict
	// may legitimately cancel a root before its first application instruction.
	if err := gate.SetDeadline(time.Now().Add(4 * time.Second)); err != nil {
		t.Fatal(err)
	}
	connection, err := gate.AcceptUnix()
	if err != nil {
		t.Fatal("late server did not reach its bind gate", err)
	}
	t.Cleanup(func() { _ = connection.Close() })
	fixtureNumbers(t, h.pids)
	witnessDeadline := time.NewTimer(4 * time.Second)
	defer witnessDeadline.Stop()
	witnessPoll := time.NewTicker(10 * time.Millisecond)
	defer witnessPoll.Stop()
	for {
		logs, err := h.cli.Logs(context.Background(), v1.LogsRequest{APIVersion: v1.Version, InstanceID: response.Instance.ID, Component: "server"})
		if err != nil {
			t.Fatal(err)
		}
		var text strings.Builder
		for _, record := range logs.Records {
			text.WriteString(record.Message)
		}
		if strings.Contains(text.String(), "fixture server stdout") {
			break
		}
		select {
		case <-witnessDeadline.C:
			t.Fatal("late server startup output not collected before conflict")
		case <-witnessPoll.C:
		}
	}
	sentinel, err := net.Listen("tcp4", net.JoinHostPort("127.0.0.1", strconv.Itoa(originalPort)))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sentinel.Close() }()
	// EOF explicitly releases the private bind gate after the sentinel owns the
	// port. Readiness may already cancel the root; closing our retained connection
	// is safe in either ordering and never signals an observed process identity.
	if err := connection.Close(); err != nil {
		t.Fatal(err)
	}
	final := h.state(response.Instance.ID, v1.Failed)
	result := final.Instance.Execution
	if result.Ports["port"] != originalPort || !strings.Contains(result.Failure, "already consumed") || result.CleanupFailure != "" || result.CollectionFailure != "" {
		t.Fatalf("allocation failure not truthful/clean: %+v", result)
	}
	for _, component := range []string{"ahead", "server"} {
		logs, err := h.cli.Logs(context.Background(), v1.LogsRequest{APIVersion: v1.Version, InstanceID: response.Instance.ID, Component: component})
		if err != nil {
			t.Fatal(err)
		}
		var text strings.Builder
		for _, record := range logs.Records {
			text.WriteString(record.Message)
		}
		if component == "ahead" && !strings.Contains(text.String(), fmt.Sprintf("BACKEND_PORT=%d", originalPort)) {
			t.Fatal("earlier consumer did not use the original allocation", text.String())
		}
		mode := "server"
		if component == "ahead" && kind == "job" {
			mode = "prep"
		}
		if count := strings.Count(text.String(), "fixture "+mode+" stdout"); count != 1 {
			t.Fatal("application was retried", component, count, text.String())
		}
	}
	h.absent()
	conn, err := net.DialTimeout("tcp4", sentinel.Addr().String(), time.Second)
	if err != nil {
		t.Fatal("sentinel lost", err)
	}
	_ = conn.Close()
	t.Logf("%s consumed %s port=%d; late bind failed without mutation/retry; sentinel preserved, owned PIDs/groups absent", kind, reference, originalPort)
}

func fixtureNumbers(t *testing.T, path string) []int {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(strings.Fields(string(data))) < 2 {
		t.Fatal("incomplete fixture witness", path)
	}
	var numbers []int
	for _, field := range strings.Fields(string(data)) {
		number, err := strconv.Atoi(field)
		if err != nil || number <= 1 {
			t.Fatal("invalid fixture witness", field, err)
		}
		numbers = append(numbers, number)
	}
	return numbers
}

func testIndependentNativeExpiry(t *testing.T, binary, action string) {
	h := newNativeHarness(t, binary, "500ms")
	job := h.manifest.Components["job"]
	job.Command = &v1.Command{Tool: "fixture", Args: []string{"-test.run=^TestNativeFixtureProcess$", "--", "never"}}
	h.manifest.Components["job"] = job
	start := func(name, grace string) v1.InstanceResponse {
		serverPIDs := filepath.Join(h.project, name+"-server-pids")
		jobPIDs := filepath.Join(h.project, name+"-job-pids")
		h.extraPIDFiles = append(h.extraPIDFiles, serverPIDs, jobPIDs)
		server := h.manifest.Components["server"]
		server.Environment = cloneFixtureEnvironment(server.Environment)
		server.Environment.Assign["PIDFILE"] = v1.Value{Literal: ptr(serverPIDs)}
		// Each live root retains its own private fixture control endpoint.
		server.Environment.Assign["FIXTURE_CONTROL"] = v1.Value{Literal: ptr(filepath.Join(h.project, name+".sock"))}
		h.manifest.Components["server"] = server
		job := h.manifest.Components["job"]
		job.Environment = cloneFixtureEnvironment(job.Environment)
		job.Environment.Assign["JOBPIDFILE"] = v1.Value{Literal: ptr(jobPIDs)}
		h.manifest.Components["job"] = job
		h.write()
		request := h.request("test")
		request.Options.StartupTimeout = "0s"
		request.Options.JobTimeout = "0s"
		request.Options.StopGrace = grace
		response, err := h.cli.Run(context.Background(), request)
		if err != nil {
			t.Fatal(err)
		}
		deadline := time.Now().Add(4 * time.Second)
		for time.Now().Before(deadline) {
			serverData, serverErr := os.ReadFile(serverPIDs)
			jobData, jobErr := os.ReadFile(jobPIDs)
			if serverErr == nil && jobErr == nil && len(strings.Fields(string(serverData))) == 3 && len(strings.Fields(string(jobData))) == 2 {
				return response
			}
			if _, err := h.cli.Renew(context.Background(), response.Instance.ID, response.LeaseToken); err != nil {
				t.Fatal(err)
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Fatal("unlimited job did not start", name)
		return response
	}
	long := start("long", "4s")
	longServer := fixtureNumbers(t, filepath.Join(h.project, "long-server-pids"))
	h.state(long.Instance.ID, v1.Stopping)
	if syscall.Kill(longServer[1], 0) != nil {
		t.Fatal("long-grace descendant not live")
	}
	short := start("short", "0s")
	shortServer := fixtureNumbers(t, filepath.Join(h.project, "short-server-pids"))
	shortJob := fixtureNumbers(t, filepath.Join(h.project, "short-job-pids"))
	var stopped chan error
	if action == "shutdown" {
		stopped = make(chan error, 1)
		go func() { _, err := h.cli.Stop(context.Background()); stopped <- err }()
	} else {
		h.state(short.Instance.ID, v1.Stopping, v1.Cancelled)
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) && syscall.Kill(shortServer[1], 0) != syscall.ESRCH {
		time.Sleep(20 * time.Millisecond)
	}
	if syscall.Kill(shortServer[1], 0) != syscall.ESRCH || syscall.Kill(shortJob[0], 0) != syscall.ESRCH {
		t.Fatal("independent short owner still running behind long cleanup")
	}
	if syscall.Kill(longServer[1], 0) != nil {
		t.Fatal("long grace finished before independence assertion")
	}
	if action == "shutdown" {
		if err := <-stopped; err != nil {
			t.Fatal(err)
		}
		if err := h.process.Wait(); err != nil {
			t.Fatal(err)
		}
		h.waited = true
	} else {
		h.state(short.Instance.ID, v1.Cancelled)
		h.state(long.Instance.ID, v1.Cancelled)
	}
	h.absent()
	t.Log("long grace4s and unlimited jobs; short owner independently cancelled during long cleanup via", action, "all recorded process/groups absent")
}
