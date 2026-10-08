package daemon

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"sync"
	"time"

	v1 "github.com/octacian/backlot/api/v1"
	"github.com/octacian/backlot/internal/localipc"
	"github.com/octacian/backlot/internal/native"
	"github.com/octacian/backlot/internal/plan"
)

type execution struct {
	cancel     context.CancelFunc
	done       chan struct{}
	mu         sync.Mutex
	groups     []*native.Group
	jobs       map[string]*native.Group
	components map[string]*native.Group
	err        error
	source     capturedSource
}
type deadlines struct{ startup, job, grace time.Duration }

func executionDeadlines(options v1.ExecutionOptions) (deadlines, error) {
	var d deadlines
	var err error
	d.startup, err = parseDuration(options.StartupTimeout, 5*time.Minute)
	if err != nil {
		return d, err
	}
	d.job, err = parseDuration(options.JobTimeout, 30*time.Minute)
	if err != nil {
		return d, err
	}
	d.grace, err = parseDuration(options.StopGrace, 10*time.Second)
	return d, err
}
func withDeadline(ctx context.Context, d time.Duration) (context.Context, context.CancelFunc) {
	if d == 0 {
		return context.WithCancel(ctx)
	}
	return context.WithTimeout(ctx, d)
}
func nativePlan(p v1.PlanResponse) error {
	if p.Publish != nil {
		return problem("unsupported_runtime", "native slice does not execute publication")
	}
	for _, r := range p.Resources {
		if r.Kind != "port" {
			return problem("unsupported_runtime", "native slice supports only port resources")
		}
	}
	for _, c := range p.Components {
		if c.Readiness != nil && (c.Readiness.Kind == "tcp" || c.Readiness.Kind == "http") {
			if _, err := exec.LookPath("lsof"); err != nil {
				return problem("missing_tool", "lsof is required to prove native listener ownership")
			}
		}
		if c.Runtime != v1.Native || c.Policy == v1.FreshOnly {
			return problem("unsupported_runtime", "selected work requires unsupported provider or fresh-only initialization")
		}
	}
	return nil
}
func (s *service) run(ctx context.Context, request v1.RunRequest) (v1.InstanceResponse, error) {
	var empty v1.InstanceResponse
	if request.InstanceID != "" && request.Plan.ConfigPath == "" {
		previous, err := s.store.runtime(request.InstanceID)
		if err != nil {
			return empty, err
		}
		request.Plan.ConfigPath = previous.ConfigPath
	}
	if _, err := executionDeadlines(request.Options); err != nil {
		return empty, err
	}
	source, err := plan.Locate(request.Plan.ProjectPath)
	if err != nil {
		return empty, err
	}
	identity, err := captureSource(source)
	if err != nil {
		return empty, err
	}
	snapshot, err := plan.Prepare(source, request.Plan)
	if err != nil {
		return empty, err
	}
	if err := nativePlan(snapshot.Plan); err != nil {
		return empty, err
	}
	if request.InstanceID != "" {
		previous, err := s.store.inspect(request.InstanceID)
		if err != nil {
			return empty, err
		}
		if previous.Plan.Project != snapshot.Plan.Project || previous.Plan.Scene != snapshot.Plan.Scene || previous.Plan.Lifetime != v1.Persistent || previous.Checkout.ID != identity.checkout.ID {
			return empty, problem("conflict", "restart must address the same persistent project, checkout and scene")
		}
	}
	response, err := s.prepare(ctx, v1.PrepareRequest{APIVersion: v1.Version, Plan: request.Plan})
	if err != nil {
		return empty, err
	}
	id := response.Instance.ID
	unlock := s.lockMutation(id)
	defer unlock()
	if request.InstanceID == "" {
		snapshot, err = s.store.snapshot(id)
		if err != nil {
			return empty, err
		}
		if err := nativePlan(snapshot.Plan); err != nil {
			return empty, err
		}
	}
	if request.InstanceID != "" {
		if request.InstanceID != id || response.Instance.Plan.Lifetime != v1.Persistent {
			return empty, problem("conflict", "restart must address the same persistent checkout and scene")
		}
		if !reflect.DeepEqual(response.Instance.Plan.Resources, snapshot.Plan.Resources) {
			return empty, problem("unsafe_drift", "resource changes require a future reset/destroy operation; runtime preserved")
		}
		if err := identity.verify(); err != nil {
			return empty, err
		}
		if _, err := s.stopExecution(id); err != nil {
			var typed *v1.PlanError
			if !errors.As(err, &typed) || typed.Code != "collection_failed" {
				return empty, err
			}
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closing {
		return empty, problem("shutting_down", "daemon is shutting down")
	}
	if s.executions == nil {
		s.executions = map[string]*execution{}
	}
	if existing := s.executions[id]; existing != nil && request.InstanceID == "" {
		instance, err := s.store.inspect(id)
		response.Instance = instance
		return response, err
	}
	if response.Instance.Status != v1.Prepared && request.InstanceID == "" {
		return empty, problem("restart_required", "failed, stopped or interrupted instances require explicit restart")
	}
	if err := identity.verify(); err != nil {
		return empty, err
	}
	if request.InstanceID != "" {
		if err := s.store.replaceSnapshot(id, snapshot); err != nil {
			return empty, err
		}
		response.ManifestDrift = false
		response.ConfigDrift = false
	}
	attempt := newID()
	result := &v1.ExecutionResult{Attempt: attempt, Components: []v1.ComponentResult{}, Ports: map[string]int{}}
	r := runtimeRecord{Attempt: attempt, Groups: []native.Identity{}, Options: request.Options, ConfigPath: request.Plan.ConfigPath}
	if err := s.store.saveRuntime(id, r); err != nil {
		return empty, err
	}
	response.Instance, err = s.store.execution(id, v1.Starting, result)
	if err != nil {
		return empty, err
	}
	runCtx, cancel := context.WithCancel(context.Background())
	entry := &execution{cancel: cancel, done: make(chan struct{}), jobs: map[string]*native.Group{}, components: map[string]*native.Group{}, source: identity}
	s.executions[id] = entry
	response.Instance, err = s.store.inspect(id)
	if err != nil {
		delete(s.executions, id)
		cancel()
		return empty, err
	}
	go s.execute(runCtx, id, snapshot, r, entry, result, identity)
	return response, nil
}
func (s *service) stopExecution(id string) (v1.Instance, error) {
	s.mu.Lock()
	entry := s.executions[id]
	s.mu.Unlock()
	if entry == nil {
		instance, err := s.store.inspect(id)
		if err != nil {
			return instance, err
		}
		if instance.Execution != nil && instance.Execution.CleanupFailure != "" {
			return instance, problem("cleanup_failed", instance.Execution.CleanupFailure)
		}
		return instance, nil
	}
	entry.mu.Lock()
	entry.cancel()
	entry.mu.Unlock()
	select {
	case <-entry.done:
	case <-time.After(15 * time.Second):
		return v1.Instance{}, problem("cleanup_failed", "runtime cancellation did not join within shutdown budget")
	}
	entry.mu.Lock()
	entryErr := entry.err
	entry.mu.Unlock()
	if entryErr != nil {
		return v1.Instance{}, entryErr
	}
	instance, err := s.store.inspect(id)
	if err == nil && instance.Execution != nil && instance.Execution.CleanupFailure != "" {
		err = problem("cleanup_failed", instance.Execution.CleanupFailure)
	}
	if err == nil && instance.Execution != nil && instance.Execution.CollectionFailure != "" {
		err = problem("collection_failed", instance.Execution.CollectionFailure)
	}
	return instance, err
}
func (s *service) acquireOutputs(ctx context.Context, p v1.PlanResponse) (func(), error) {
	groups := map[string]bool{}
	for _, c := range p.Components {
		for _, name := range c.Outputs {
			groups[p.Checkout+"/"+p.Outputs[name].ConcurrencyGroup] = true
		}
	}
	keys := make([]string, 0, len(groups))
	for k := range groups {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var acquired []chan struct{}
	release := func() {
		for _, lock := range acquired {
			<-lock
		}
	}
	for _, key := range keys {
		s.mu.Lock()
		if s.outputs == nil {
			s.outputs = map[string]chan struct{}{}
		}
		lock := s.outputs[key]
		if lock == nil {
			lock = make(chan struct{}, 1)
			s.outputs[key] = lock
		}
		s.mu.Unlock()
		select {
		case lock <- struct{}{}:
			acquired = append(acquired, lock)
		case <-ctx.Done():
			release()
			return nil, ctx.Err()
		}
	}
	return release, nil
}
func (s *service) execute(ctx context.Context, id string, snapshot plan.Snapshot, journal runtimeRecord, entry *execution, result *v1.ExecutionResult, identity capturedSource) {
	defer close(entry.done)
	d, _ := executionDeadlines(journal.Options)
	status := v1.Failed
	var failure error
	var release func()
	save := func(current v1.InstanceStatus) error { _, err := s.store.execution(id, current, result); return err }
	defer func() {
		if ctx.Err() != nil {
			result.Cancelled = true
			status = v1.Stopped
			if snapshot.Plan.Lifetime == v1.Disposable {
				status = v1.Cancelled
			}
		}
		if failure != nil {
			result.Failure = failure.Error()
			if ctx.Err() == nil {
				status = v1.Failed
			}
		}
		_ = save(v1.Stopping)
		var cleanup error
		var cleanupMu sync.Mutex
		var joined sync.WaitGroup
		for _, group := range entry.groups {
			joined.Go(func() {
				err := group.Stop(d.grace)
				cleanupMu.Lock()
				defer cleanupMu.Unlock()
				cleanup = errors.Join(cleanup, err)
				if exit := group.Result(); exit != nil && exit.CollectionFailure != "" {
					result.CollectionFailure = exit.CollectionFailure
					status = v1.Failed
				}
			})
		}
		joined.Wait()
		for i := range result.Components {
			component := &result.Components[i]
			if cleanup != nil {
				component.Status = "unknown"
			} else if entry.jobs[component.Name] != nil {
				component.Status = "completed"
			} else {
				component.Status = "stopped"
			}
			if group := entry.components[component.Name]; group != nil && component.ExitCode == nil {
				if exit := group.Result(); exit != nil {
					component.ExitCode = &exit.Code
				}
			}
		}
		if cleanup != nil {
			result.CleanupFailure = cleanup.Error()
			status = v1.Failed
		}
		if err := save(status); err != nil {
			entry.mu.Lock()
			entry.err = err
			entry.mu.Unlock()
		}
		if release != nil && cleanup == nil {
			release()
		}
	}()
	startup, cancel := withDeadline(ctx, d.startup)
	defer cancel()
	if failure = s.store.outputConflict(snapshot.Plan); failure != nil {
		return
	}
	release, failure = s.acquireOutputs(startup, snapshot.Plan)
	if failure != nil {
		return
	}
	for name := range snapshot.Plan.Resources {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			failure = err
			return
		}
		result.Ports[name] = listener.Addr().(*net.TCPAddr).Port
		if err := listener.Close(); err != nil {
			failure = err
			return
		}
	}
	services := map[string]*native.Group{}
	for _, component := range snapshot.Plan.Components {
		if err := startup.Err(); err != nil {
			failure = err
			return
		}
		if err := serviceFailure(services); err != nil {
			failure = err
			return
		}
		// Terminal execution receives its own budget after dependency startup succeeds.
		componentCtx := startup
		if component.Name == snapshot.Plan.TerminalJob {
			componentCtx = ctx
		}
		jobCtx, jobCancel := withDeadline(componentCtx, d.job)
		result.Components = append(result.Components, v1.ComponentResult{Name: component.Name, Status: "starting"})
		index := len(result.Components) - 1
		if err := save(v1.Starting); err != nil {
			jobCancel()
			failure = err
			return
		}
		var group *native.Group
		var err error
		for allocationAttempt := 0; allocationAttempt < 3; allocationAttempt++ {
			if err = identity.verify(); err != nil {
				break
			}
			group, err = s.launch(componentCtx, id, component.Name, component.Executable, component.Command.Args, component.Environment, snapshot, result, entry, &journal)
			if err != nil {
				break
			}
			entry.components[component.Name] = group
			if component.Kind != v1.Service {
				break
			}
			services[component.Name] = group
			err = s.awaitProbe(startup, id, component, snapshot, result, entry, &journal, group, services)
			var typed *v1.PlanError
			if !errors.As(err, &typed) || typed.Code != "port_conflict" {
				break
			}
			if cleanupErr := group.Stop(d.grace); cleanupErr != nil {
				err = cleanupErr
				break
			}
			delete(services, component.Name)
			if allocationAttempt == 2 {
				break
			}
			for _, port := range component.Ports {
				listener, allocationErr := net.Listen("tcp", "127.0.0.1:0")
				if allocationErr != nil {
					err = allocationErr
					break
				}
				result.Ports[port.Resource] = listener.Addr().(*net.TCPAddr).Port
				err = listener.Close()
				if err != nil {
					break
				}
			}
			if err != nil {
				break
			}
		}
		if err != nil {
			jobCancel()
			failure = err
			return
		}
		result.Components[index].Status = "running"
		if err := save(v1.Starting); err != nil {
			jobCancel()
			failure = err
			return
		}
		if component.Kind == v1.Service {
			services[component.Name] = group
			jobCancel()
			result.Components[index].Status = "ready"
		} else {
			entry.jobs[component.Name] = group
			var exit *native.Exit
			exit, failure = waitJob(jobCtx, group, services)
			jobCancel()
			if exit != nil {
				result.Components[index].ExitCode = &exit.Code
				result.Components[index].Status = "completed"
				if exit.Code != 0 || exit.Error != "" {
					failure = fmt.Errorf("job %s failed (exit %d): %s", component.Name, exit.Code, exit.Error)
				}
			}
			if failure != nil {
				return
			}
			if err := group.Stop(d.grace); err != nil {
				failure = err
				return
			}
		}
	}
	if snapshot.Plan.Lifetime == v1.Disposable {
		status = v1.Succeeded
		return
	}
	status = v1.RuntimeReady
	if failure = save(v1.RuntimeReady); failure != nil {
		return
	}
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if failure = serviceFailure(services); failure != nil {
				return
			}
		}
	}
}
func serviceFailure(services map[string]*native.Group) error {
	for name, g := range services {
		if g.Result() != nil || !g.Alive() {
			return fmt.Errorf("required service %s exited unexpectedly; explicit restart required", name)
		}
	}
	return nil
}
func waitJob(ctx context.Context, g *native.Group, services map[string]*native.Group) (*native.Exit, error) {
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		if err := serviceFailure(services); err != nil {
			return g.Result(), err
		}
		if exit := g.Result(); exit != nil {
			return exit, nil
		}
		if !g.Alive() {
			return nil, errors.New("guardian exited unexpectedly")
		}
		select {
		case <-ctx.Done():
			return g.Result(), ctx.Err()
		case <-ticker.C:
		}
	}
}
func (s *service) launch(ctx context.Context, id, name, executable string, args []string, environment map[string]v1.PlannedValue, snapshot plan.Snapshot, result *v1.ExecutionResult, entry *execution, journal *runtimeRecord) (*native.Group, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := entry.source.verify(); err != nil {
		return nil, err
	}
	directory := filepath.Join(s.directory, "logs")
	if err := localipc.Directory(directory, true); err != nil {
		return nil, err
	}
	path := filepath.Join(directory, id+"-output.ndjson")
	if _, err := os.Lstat(path); err == nil {
		if err := localipc.File(path, false); err != nil {
			return nil, err
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return nil, err
	}
	self, err := os.Executable()
	if err != nil {
		_ = file.Close()
		return nil, err
	}
	journal.LaunchPending = true
	if err := s.store.saveRuntime(id, *journal); err != nil {
		_ = file.Close()
		return nil, err
	}
	group, startErr := native.Start(self, file, file)
	journal.LaunchPending = false
	closeErr := file.Close()
	if group != nil {
		entry.groups = append(entry.groups, group)
		journal.Groups = append(journal.Groups, group.Identity)
		if err := s.store.saveRuntime(id, *journal); err != nil {
			return nil, errors.Join(startErr, closeErr, err)
		}
	}
	if startErr != nil {
		if group == nil {
			startErr = errors.Join(startErr, s.store.saveRuntime(id, *journal))
		}
		return nil, errors.Join(startErr, closeErr)
	}
	var env []string
	for key, value := range environment {
		if value.Redacted {
			value = snapshot.Secrets[name+"/environment/"+key]
		}
		text, err := runtimeValue(value, id, snapshot.Plan, result.Ports)
		if err != nil {
			return nil, err
		}
		env = append(env, key+"="+text)
	}
	sort.Strings(env)
	if closeErr != nil {
		return nil, closeErr
	}
	entry.mu.Lock()
	defer entry.mu.Unlock()
	if err := entry.source.verify(); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	err = group.Activate(native.Spec{Executable: executable, Args: args, Environment: env, Directory: filepath.Dir(snapshot.Plan.ManifestPath), InstanceID: id, Component: name, Attempt: result.Attempt})
	return group, err
}
func runtimeValue(value v1.PlannedValue, id string, p v1.PlanResponse, ports map[string]int) (string, error) {
	if value.Literal != nil {
		return *value.Literal, nil
	}
	r := value.Symbolic
	if r == nil {
		return "", problem("state_corrupt", "resolved private value missing")
	}
	switch r.Kind {
	case "instance":
		if r.Field == "id" {
			return id, nil
		}
	case "resource":
		if r.Field == "port" {
			return strconv.Itoa(ports[r.Name]), nil
		}
	case "service":
		if r.Field == "host" {
			return "127.0.0.1", nil
		}
		if r.Field == "port" {
			for _, c := range p.Components {
				if c.Name == r.Name {
					return strconv.Itoa(ports[c.Ports[r.Port].Resource]), nil
				}
			}
		}
	}
	return "", problem("unsupported_runtime", "allocation reference cannot be resolved by native slice")
}

func (s *service) lockMutation(id string) func() {
	s.mu.Lock()
	if s.mutations == nil {
		s.mutations = map[string]*sync.Mutex{}
	}
	lock := s.mutations[id]
	if lock == nil {
		lock = &sync.Mutex{}
		s.mutations[id] = lock
	}
	s.mu.Unlock()
	lock.Lock()
	return lock.Unlock
}
func (s *service) stopRuntime(id string) (v1.Instance, error) {
	unlock := s.lockMutation(id)
	defer unlock()
	return s.stopExecution(id)
}
