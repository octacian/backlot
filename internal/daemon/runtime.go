package daemon

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
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
	kept          bool
	cancel        context.CancelFunc
	done          chan struct{}
	mu            sync.Mutex
	groups        []*native.Group
	jobs          map[string]workload
	services      map[string]workload
	components    map[string]workload
	containers    []*dockerWork
	docker        *dockerEngine
	resources     *resourceGeneration
	outputRelease func()
	err           error
	source        capturedSource
	grace         time.Duration
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
func executablePlan(p v1.PlanResponse) error {
	for _, r := range p.Resources {
		if r.Kind == "origin" && p.Publish == nil {
			return problem("invalid_publication", "origin resources require a publication")
		}
	}
	for _, c := range p.Components {
		if c.Runtime == v1.Native && c.Readiness != nil && (c.Readiness.Kind == "tcp" || c.Readiness.Kind == "http") {
			if _, err := exec.LookPath("lsof"); err != nil {
				return problem("missing_tool", "lsof is required to prove native listener ownership")
			}
		}

	}
	return nil
}
func (s *service) run(ctx context.Context, request v1.RunRequest) (v1.InstanceResponse, error) {
	return s.runMode(ctx, request, false)
}
func (s *service) runMode(ctx context.Context, request v1.RunRequest, reset bool) (v1.InstanceResponse, error) {
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
	if err := executablePlan(snapshot.Plan); err != nil {
		return empty, err
	}
	if request.InstanceID != "" {
		previous, err := s.store.inspect(request.InstanceID)
		if err != nil {
			return empty, err
		}
		if previous.Status == v1.Destroyed {
			return empty, problem("destroyed", "instance was destroyed; run the scene to create a fresh identity")
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
	if s.beforeExecution != nil {
		s.beforeExecution(id)
	}
	unlock := s.lockMutation(id)
	defer unlock()
	response.Instance, err = s.store.inspect(id)
	if err != nil {
		return empty, err
	}
	if response.Instance.Status == v1.Destroyed {
		return empty, problem("destroyed", "instance was destroyed during start acceptance; run the scene again for a fresh identity")
	}

	if request.InstanceID == "" {
		snapshot, err = s.store.snapshot(id)
		if err != nil {
			return empty, err
		}
		if err := executablePlan(snapshot.Plan); err != nil {
			return empty, err
		}
	}
	if request.InstanceID != "" {
		if request.InstanceID != id || response.Instance.Plan.Lifetime != v1.Persistent {
			return empty, problem("conflict", "restart must address the same persistent checkout and scene")
		}
		if snapshot.Plan.Publish != nil && snapshot.Caddy == nil {
			return empty, problem("missing_provider", "published scenes require config.caddy and an operator-installed JSON subroute scope")
		}
		previous, err := s.store.snapshot(id)
		if err != nil {
			return empty, err
		}
		if snapshot.Plan.Publish != nil && snapshot.Caddy != nil && response.Instance.Execution != nil && response.Instance.Execution.Origin != "" {
			origin, err := url.Parse(response.Instance.Execution.Origin)
			if err != nil || origin.Hostname() != "bl-"+id[:40]+"."+snapshot.Caddy.DomainSuffix {
				return empty, problem("unsafe_drift", "published logical instance retains its hostname; use destroy for a changed domain suffix")
			}
		}
		if !reset {
			if err := safeRestart(previous, snapshot); err != nil {
				return empty, err
			}
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
	if reset {
		cleanup, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		cleanupErr := s.store.removeResources(cleanup, id)
		cancel()
		if cleanupErr != nil {
			return empty, problem("cleanup_failed", cleanupErr.Error())
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
	if existing := s.executions[id]; existing != nil && request.InstanceID == "" && response.Instance.Status != v1.Stopped {
		instance, err := s.store.inspect(id)
		response.Instance = instance
		return response, err
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
	if previous := response.Instance.Execution; previous != nil {
		result.Origin = previous.Origin
	}
	if snapshot.Plan.Publish != nil {
		if snapshot.Caddy == nil {
			return empty, problem("missing_provider", "published scenes require config.caddy and an operator-installed JSON subroute scope")
		}
		result.Origin = "https://bl-" + id[:40] + "." + snapshot.Caddy.DomainSuffix
		if snapshot.Caddy.HTTPSPort != nil && *snapshot.Caddy.HTTPSPort != 443 {
			result.Origin += ":" + strconv.Itoa(*snapshot.Caddy.HTTPSPort)
		}
	}
	r := runtimeRecord{Attempt: attempt, Groups: []native.Identity{}, Options: request.Options, ConfigPath: request.Plan.ConfigPath}
	response.Instance, err = s.store.acceptExecution(id, r, result, request.InstanceID != "" || response.Instance.Status == v1.Stopped)
	if err != nil {
		return empty, err
	}
	runCtx, cancel := context.WithCancel(context.Background())
	entry := &execution{cancel: cancel, done: make(chan struct{}), jobs: map[string]workload{}, services: map[string]workload{}, components: map[string]workload{}, source: identity, grace: mustGrace(request.Options)}
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
	instance, err := s.joinExecution(id)
	var typed *v1.PlanError
	if errors.As(err, &typed) && typed.Code == "cleanup_failed" {
		s.mu.Lock()
		entry := s.executions[id]
		s.mu.Unlock()
		if entry != nil {
			select {
			case <-entry.done:
			default:
				return instance, err
			}
		}

		if recoveryErr := s.store.recoverExecution(id); recoveryErr != nil {
			return instance, recoveryErr
		}
		instance, err = s.store.inspect(id)
		if err == nil && instance.Execution != nil && instance.Execution.CleanupFailure != "" {
			err = problem("cleanup_failed", instance.Execution.CleanupFailure)
		}
		if err == nil && entry != nil {
			entry.releaseOutputs()
		}
	}
	return instance, err
}
func (s *service) joinExecution(id string) (v1.Instance, error) {
	s.mu.Lock()
	entry := s.executions[id]
	s.mu.Unlock()
	if entry != nil {
		select {
		case <-entry.done:
			entry.mu.Lock()
			kept := entry.kept
			entry.mu.Unlock()
			if kept {
				return s.stopKept(id, entry)
			}
		default:
		}
	}
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
	case <-time.After(time.Until(time.Now().Add(entry.grace).Add(20 * time.Second))):
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
		if failure != nil && (ctx.Err() == nil || !errors.Is(failure, context.Canceled)) {
			result.Failure = failure.Error()
			var detail *v1.PlanError
			if errors.As(failure, &detail) {
				result.FailureDetail = detail
			}
			if ctx.Err() == nil {
				status = v1.Failed
			}
		}
		s.mu.Lock()
		shuttingDown := s.closing
		s.mu.Unlock()
		sanitizeResult(result, snapshot, entry)
		_ = save(v1.Stopping)
		potentialKeep := journal.Options.KeepOnFailure && snapshot.Plan.Lifetime == v1.Disposable && !shuttingDown
		var cleanup error
		var cleanupMu sync.Mutex
		var stopped sync.WaitGroup
		// Stop finite work before reading artifacts. Without investigation retention,
		// stop services concurrently so job collection cannot delay cancellation.
		stop := func(work workload) {
			stopped.Go(func() {
				err := work.Stop(d.grace)
				exit := work.Result()
				cleanupMu.Lock()
				defer cleanupMu.Unlock()
				cleanup = errors.Join(cleanup, err)
				if exit != nil && exit.CollectionFailure != "" {
					result.CollectionFailure = "application output collection failed"
				}
			})
		}
		for _, group := range entry.groups {
			if !potentialKeep || !healthyService(entry, group) {
				stop(group)
			}
		}
		for _, work := range entry.containers {
			if !potentialKeep || !healthyService(entry, work) {
				stop(work)
			}
		}
		stopped.Wait()

		if err := s.collectArtifacts(id, snapshot, entry, result); err != nil {
			result.CollectionFailure = err.Error()
			status = v1.Failed
		}
		keep := potentialKeep && (failure != nil || result.Cancelled || result.TerminalExitCode != nil && *result.TerminalExitCode != 0 || result.CollectionFailure != "" || cleanup != nil) && !shuttingDown
		result.Kept = keep
		entry.kept = keep
		_ = save(v1.Stopping)
		var joined sync.WaitGroup
		joined.Go(func() {
			cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cleanupCancel()
			var err error
			if !keep {
				err = s.store.removeGateway(cleanupCtx, id, &journal)
			}
			cleanupMu.Lock()
			defer cleanupMu.Unlock()
			cleanup = errors.Join(cleanup, err)
		})
		for _, group := range entry.groups {
			if keep && healthyService(entry, group) {
				continue
			}
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
		for _, w := range entry.containers {
			if keep && healthyService(entry, w) {
				continue
			}
			joined.Go(func() {
				stopErr := w.Stop(d.grace)
				exit := w.Result()
				cleanupMu.Lock()
				defer cleanupMu.Unlock()
				cleanup = errors.Join(cleanup, stopErr)
				if exit != nil && exit.CollectionFailure != "" {
					result.CollectionFailure = exit.CollectionFailure
					status = v1.Failed
				}
			})
		}

		joined.Wait()

		if entry.resources != nil && cleanup == nil && !keep {
			cleanupCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			cleanup = s.store.cleanupContainers(cleanupCtx, id, entry.resources, entry.docker)
			cancel()
		}
		if entry.docker != nil && !keep {
			_ = entry.docker.Close()
		}
		if snapshot.Plan.Lifetime == v1.Disposable && cleanup == nil && !keep && !shuttingDown {
			cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			cleanup = s.store.removeResources(cleanupCtx, id)
			cancel()
		}
		for i := range result.Components {
			component := &result.Components[i]
			if component.Status == "skipped" {
				continue
			}
			if cleanup != nil {
				component.Status = "unknown"
			} else if keep && entry.jobs[component.Name] == nil && entry.components[component.Name] != nil && entry.components[component.Name].Alive() {
				component.Status = "ready"
			} else if job := entry.jobs[component.Name]; job != nil {
				component.Status = "unknown"
				if exit := job.Result(); exit != nil && exit.Known {
					component.Status = "completed"
				}
			} else {
				component.Status = "stopped"
			}
			if group := entry.components[component.Name]; group != nil && component.ExitCode == nil {
				if exit := group.Result(); exit != nil && exit.Known {
					component.ExitCode = &exit.Code
				}
			}
		}
		for _, component := range result.Components {
			if component.Name == snapshot.Plan.TerminalJob && component.ExitCode != nil {
				result.TerminalExitCode = component.ExitCode
			}
		}
		if result.CollectionFailure != "" {
			status = v1.Failed
		}
		if cleanup != nil {
			result.CleanupFailure = cleanup.Error()
			result.CleanupDetails = cleanupDetails(cleanup)
			status = v1.Failed
		}
		result.CompletedAt = time.Now().UTC().Format(time.RFC3339Nano)
		sanitizeResult(result, snapshot, entry)
		if err := save(status); err != nil {
			entry.mu.Lock()
			entry.err = err
			entry.mu.Unlock()
		}
		if cleanup == nil && !keep {
			entry.releaseOutputs()
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
	entry.mu.Lock()
	entry.outputRelease = release
	entry.mu.Unlock()
	if failure = s.allocateResources(startup, id, snapshot, entry); failure != nil {
		return
	}
	for name, resource := range snapshot.Plan.Resources {
		if resource.Kind != "port" {
			continue
		}
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
	if failure = s.captureFixtures(id, snapshot, result, entry); failure != nil {
		return
	}
	services := map[string]workload{}
	consumedPorts := map[string]string{}
	published := false
	for _, component := range snapshot.Plan.Components {
		if err := startup.Err(); err != nil {
			failure = err
			return
		}
		if err := serviceFailure(services); err != nil {
			failure = err
			return
		}
		if component.Policy == v1.FreshOnly && entry.resources.Initialized[component.Name] {
			result.Components = append(result.Components, v1.ComponentResult{Name: component.Name, Status: "skipped"})
			continue
		}
		// Terminal execution receives its own budget after dependency startup succeeds.
		componentCtx := startup
		if component.Name == snapshot.Plan.TerminalJob {
			if snapshot.Plan.Publish != nil {
				if failure = awaitPublication(startup, result.Origin, snapshot.Plan.Publish.Probe.Timeout, services); failure != nil {
					return
				}
			}
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
		var group workload
		var err error
		for allocationAttempt := 0; allocationAttempt < 3; allocationAttempt++ {
			if err = identity.verify(); err != nil {
				break
			}
			if component.Runtime == v1.Container {
				group, err = s.launchContainer(componentCtx, id, component, snapshot, result, entry)
			} else {
				group, err = s.launch(componentCtx, id, component.Name, component.Executable, component.Command.Args, component.Environment, snapshot, result, entry, &journal)
			}
			if err == nil {
				entry.components[component.Name] = group
				if component.Kind == v1.Job {
					entry.jobs[component.Name] = group
				}
				if component.Kind != v1.Service {
					break
				}
				services[component.Name] = group
				err = s.awaitProbe(startup, id, component, snapshot, result, entry, &journal, group, services)
			} else {
				group = nil
			}
			var typed *v1.PlanError
			if !errors.As(err, &typed) || typed.Code != "port_conflict" {
				break
			}
			if group != nil {
				if cleanupErr := group.Stop(d.grace); cleanupErr != nil {
					err = cleanupErr
					break
				}
			}

			delete(services, component.Name)
			if allocationAttempt == 2 {
				break
			}
			err = nil
			for _, port := range component.Ports {
				if consumer := consumedPorts[port.Resource]; consumer != "" {
					err = problem("port_conflict", "allocated port was already consumed by component "+consumer+"; stopped without reallocation or retry")
					break
				}
			}
			if err != nil {
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
			entry.services[component.Name] = group
			services[component.Name] = group
			jobCancel()
			result.Components[index].Status = "ready"
		} else {
			entry.jobs[component.Name] = group
			var exit *native.Exit
			exit, failure = waitJob(jobCtx, group, services)
			jobCancel()
			if exit != nil {
				if exit.Known {
					result.Components[index].ExitCode = &exit.Code
					result.Components[index].Status = "completed"
					if component.Name == snapshot.Plan.TerminalJob {
						result.TerminalExitCode = &exit.Code
					}
				}
				if exit.CollectionFailure != "" {
					result.CollectionFailure = "job output collection failed"
				}
				if !exit.Known || exit.Code != 0 || exit.Error != "" {
					status = v1.Failed
					if !exit.Known || component.Name != snapshot.Plan.TerminalJob || exit.Code < 0 || exit.Error != "" && exit.Code == 0 {
						if !exit.Known {
							failure = fmt.Errorf("job %s exit status unavailable", component.Name)
						} else {
							failure = fmt.Errorf("job %s failed (exit %d)", component.Name, exit.Code)
						}
					}
					return
				}
			}
			if failure != nil {
				return
			}
			if err := group.Stop(d.grace); err != nil {
				failure = err
				return
			}
			if component.Policy == v1.FreshOnly {
				entry.resources.Initialized[component.Name] = true
				if failure = s.store.saveResources(id, *entry.resources); failure != nil {
					return
				}
			}
		}
		consumeComponentPorts(consumedPorts, component, snapshot)
		if !published && snapshot.Plan.Publish != nil && routesReady(*snapshot.Plan.Publish, services) {
			if failure = s.publish(startup, id, snapshot, result, &journal); failure != nil {
				return
			}
			published = true
		}
	}
	if snapshot.Plan.Publish != nil && snapshot.Plan.Lifetime == v1.Persistent {
		if failure = awaitPublication(startup, result.Origin, snapshot.Plan.Publish.Probe.Timeout, services); failure != nil {
			return
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
func serviceFailure(services map[string]workload) error {
	for name, g := range services {
		if g.Result() != nil || !g.Alive() {
			return fmt.Errorf("required service %s exited unexpectedly; explicit restart required", name)
		}
	}
	return nil
}
func waitJob(ctx context.Context, g workload, services map[string]workload) (*native.Exit, error) {
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
			if exit := g.Result(); exit != nil {
				return exit, nil
			}
			return nil, errors.New("runtime owner exited without a collected result")
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
	env, err := resolvedEnvironment(name, environment, id, snapshot, result, entry, v1.Native)
	if err != nil {
		return nil, err
	}
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

func (s *service) mutationLock(id string) *sync.Mutex {
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
	return lock
}
func (s *service) lockMutation(id string) func() {
	lock := s.mutationLock(id)
	lock.Lock()
	return lock.Unlock
}
func (s *service) stopRuntime(id string) (v1.Instance, error) {
	unlock := s.lockMutation(id)
	defer unlock()
	return s.stopExecution(id)
}

func mustGrace(options v1.ExecutionOptions) time.Duration {
	d, _ := executionDeadlines(options)
	return d.grace
}

func (s *service) cancelRuntime(id string) (v1.Instance, error) {
	unlock := s.lockMutation(id)
	defer unlock()
	s.mu.Lock()
	entry := s.executions[id]
	s.mu.Unlock()
	if entry != nil {
		return s.stopExecution(id)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.store.cancel(id)
}
func (s *service) expireRuntime(id string) error {
	unlock := s.lockMutation(id)
	defer unlock()
	s.mu.Lock()
	instance, err := s.store.inspect(id)
	if err != nil {
		s.mu.Unlock()
		return err
	}
	expiry, parseErr := time.Parse(time.RFC3339Nano, instance.LeaseExpiresAt)
	expired := instance.LeaseExpiresAt != "" && parseErr == nil && !expiry.After(time.Now()) && active(instance.Status) && instance.Status != v1.Stopping
	s.mu.Unlock()
	if !expired {
		return nil
	}
	_, err = s.stopExecution(id)
	if err != nil {
		var detail *v1.PlanError
		if errors.As(err, &detail) && (detail.Code == "collection_failed" || detail.Code == "cleanup_failed") {
			// Expected per-run failures are durable outcomes, not daemon-fatal
			// sweeper failures. Persistence/unfinished-owner errors still escape.
			current, inspectErr := s.store.inspect(id)
			if inspectErr != nil {
				return inspectErr
			}
			if current.Execution != nil && current.Execution.CompletedAt != "" &&
				(detail.Code == "collection_failed" && current.Execution.CollectionFailure != "" ||
					detail.Code == "cleanup_failed" && current.Execution.CleanupFailure != "") {
				return nil
			}
		}
	}
	return err
}

func (s *service) ownerChannels() []<-chan struct{} {
	s.mu.Lock()
	defer s.mu.Unlock()
	channels := make([]<-chan struct{}, 0, len(s.executions)+len(s.expiryWorkers))
	for _, entry := range s.executions {
		channels = append(channels, entry.done)
	}
	for _, done := range s.expiryWorkers {
		channels = append(channels, done)
	}
	return channels
}
func (s *service) ownersJoined() bool {
	for _, done := range s.ownerChannels() {
		select {
		case <-done:
		default:
			return false
		}
	}
	return true
}
func (s *service) waitOwners() {
	for _, done := range s.ownerChannels() {
		<-done
	}
}

// Completed jobs and live services retain the allocation values they consumed.
// Never replay them or silently move those resources during a later bind retry.
func consumeComponentPorts(consumed map[string]string, c v1.PlannedComponent, snapshot plan.Snapshot) {
	consume := func(value v1.PlannedValue) {
		r := value.Symbolic
		if r == nil || r.Field != "port" {
			return
		}
		resource := ""
		if r.Kind == "resource" {
			resource = r.Name
		}
		if r.Kind == "service" {
			for _, service := range snapshot.Plan.Components {
				if service.Name == r.Name {
					resource = service.Ports[r.Port].Resource
					break
				}
			}
		}
		if resource != "" && consumed[resource] == "" {
			consumed[resource] = c.Name
		}
	}
	for _, port := range c.Ports {
		if consumed[port.Resource] == "" {
			consumed[port.Resource] = c.Name
		}
	}
	for key, value := range c.Environment {
		if value.Redacted {
			value = snapshot.Secrets[c.Name+"/environment/"+key]
		}
		consume(value)
	}
	if c.Readiness != nil && c.Readiness.Target != nil {
		value := *c.Readiness.Target
		if value.Redacted {
			value = snapshot.Secrets[c.Name+"/readiness"]
		}
		consume(value)
	}
}

func (e *execution) releaseOutputs() {
	e.mu.Lock()
	release := e.outputRelease
	e.outputRelease = nil
	e.mu.Unlock()
	if release != nil {
		release()
	}
}
