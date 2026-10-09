package daemon

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/containerd/errdefs"
	"github.com/moby/moby/api/pkg/stdcopy"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/mount"
	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/client"
	v1 "github.com/octacian/backlot/api/v1"
	"github.com/octacian/backlot/internal/localipc"
	"github.com/octacian/backlot/internal/native"
	"github.com/octacian/backlot/internal/plan"
)

// dockerEngine is the official Docker client boundary, with explicit local
// endpoint selection and API negotiation. Image building/pulling is external.
type dockerEngine struct{ *client.Client }

func newDocker(endpoint string) (*dockerEngine, error) {
	parsed, parseErr := url.Parse(endpoint)
	if parseErr != nil || parsed.Scheme != "unix" || parsed.Host != "" || !filepath.IsAbs(parsed.Path) || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, problem("missing_provider", "Docker work requires a local Unix socket endpoint")
	}
	endpoint = "unix://" + parsed.Path
	// Bound response headers while leaving collected streams unbounded. The
	// explicit HTTP scheme is local Unix-socket transport, without TLS inference.
	c, err := client.New(client.WithHTTPClient(&http.Client{Transport: &http.Transport{ResponseHeaderTimeout: 5 * time.Second}}), client.WithHost(endpoint), client.WithScheme("http"))
	if err != nil {
		return nil, problem("missing_provider", "cannot configure local Docker client")
	}
	return &dockerEngine{c}, nil
}
func resourceLabels(r ownedResource) map[string]string {
	return map[string]string{"io.backlot.owner": r.Token}
}
func owned(labels map[string]string, r ownedResource) error {
	if !validID(r.Token) || labels["io.backlot.owner"] != r.Token {
		return problem("cleanup_failed", "Docker ownership uncertain; resource preserved")
	}
	return nil
}
func resourceHandle(r ownedResource) string {
	if r.ID != "" {
		return r.ID
	}
	return r.Name
}
func createDockerResource(ctx context.Context, d *dockerEngine, r ownedResource) (string, error) {
	if d == nil {
		return "", problem("missing_provider", "Docker resource requires configured local engine")
	}
	// VolumeCreate may return an existing volume. Never label or adopt that object.
	switch r.Kind {
	case "volume":
		if _, err := d.VolumeInspect(ctx, r.Name, client.VolumeInspectOptions{}); !errdefs.IsNotFound(err) {
			if err == nil {
				err = problem("conflict", "generated volume name already exists; preserved")
			}
			return "", err
		}
		out, err := d.VolumeCreate(ctx, client.VolumeCreateOptions{Name: r.Name, Labels: resourceLabels(r)})
		if err != nil {
			return "", err
		}
		if err = owned(out.Volume.Labels, r); err != nil {
			return "", err
		}
		return out.Volume.Name, nil
	case "network":
		out, err := d.NetworkCreate(ctx, r.Name, client.NetworkCreateOptions{Driver: "bridge", Labels: resourceLabels(r)})
		if err != nil {
			return "", err
		}
		return out.ID, nil
	}
	return "", problem("unsupported_runtime", "unsupported Docker resource")
}
func verifyDockerResource(ctx context.Context, d *dockerEngine, r ownedResource) error {
	if d == nil {
		return problem("missing_provider", "recorded Docker engine required for resource verification")
	}
	handle := resourceHandle(r)
	switch r.Kind {
	case "volume":
		out, err := d.VolumeInspect(ctx, handle, client.VolumeInspectOptions{})
		if err != nil {
			return err
		}
		return owned(out.Volume.Labels, r)
	case "network":
		out, err := d.NetworkInspect(ctx, handle, client.NetworkInspectOptions{})
		if err != nil {
			return err
		}
		return owned(out.Network.Labels, r)
	case "container":
		out, err := d.ContainerInspect(ctx, handle, client.ContainerInspectOptions{})
		if err != nil {
			return err
		}
		if out.Container.Config == nil {
			return problem("cleanup_failed", "container configuration missing; preserved")
		}
		return owned(out.Container.Config.Labels, r)
	}
	return problem("state_corrupt", "unknown Docker allocation kind")
}
func removeDockerResource(ctx context.Context, d *dockerEngine, r ownedResource) error {
	if err := verifyDockerResource(ctx, d, r); err != nil {
		if errdefs.IsNotFound(err) {
			return nil
		}
		return err
	}
	switch r.Kind {
	case "volume":
		_, err := d.VolumeRemove(ctx, resourceHandle(r), client.VolumeRemoveOptions{})
		return err
	case "network":
		_, err := d.NetworkRemove(ctx, resourceHandle(r), client.NetworkRemoveOptions{})
		return err
	}
	return problem("state_corrupt", "unsupported resource removal")
}
func (d *dockerEngine) noConsumers(ctx context.Context, g resourceGeneration) error {
	list, err := d.ContainerList(ctx, client.ContainerListOptions{All: true})
	if err != nil {
		return err
	}
	for _, item := range list.Items {
		inspect, err := d.ContainerInspect(ctx, item.ID, client.ContainerInspectOptions{})
		if errdefs.IsNotFound(err) {
			continue
		}
		if err != nil {
			return err
		}
		c := inspect.Container
		for _, r := range g.Resources {
			if r.Removed {
				continue
			}
			for _, m := range c.Mounts {
				if r.Kind == "volume" && m.Name == r.Name {
					return problem("cleanup_failed", "storage still has a Docker consumer; preserved")
				}
				if r.Kind == "directory" && m.Type == mount.TypeBind {
					overlap, err := bindSourcesOverlap(r.Name, m.Source)
					if err != nil {
						return err
					}
					if !overlap {
						continue
					}
					return problem("cleanup_failed", "storage still has a Docker consumer; preserved")
				}
			}
			if r.Kind == "network" && c.NetworkSettings != nil {
				for _, n := range c.NetworkSettings.Networks {
					if n.NetworkID == r.ID {
						return problem("cleanup_failed", "network still has a Docker consumer; preserved")
					}
				}
			}
		}
	}
	return nil
}

// bindSourcesOverlap resolves aliases before comparing path components in both
// directions. An unresolvable source cannot prove that deletion is safe.
func bindSourcesOverlap(a, b string) (bool, error) {
	a, err := canonicalBindSource(a)
	if err != nil {
		return false, err
	}
	b, err = canonicalBindSource(b)
	if err != nil {
		return false, err
	}
	within := func(parent, child string) bool {
		rel, err := filepath.Rel(parent, child)
		return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
	}
	return within(a, b) || within(b, a), nil
}

// canonicalBindSource resolves existing ancestors, retaining a missing suffix.
// Docker can retain an unrelated bind declaration after its host path disappears;
// absence alone must not make that declaration alias a different private path.
func canonicalBindSource(source string) (string, error) {
	if !filepath.IsAbs(source) {
		return "", problem("cleanup_failed", "Docker bind source is not absolute; storage preserved")
	}
	// Resolve before cleaning: a symlink followed by ".." has filesystem
	// semantics that differ from lexical path cleaning.
	if resolved, err := filepath.EvalSymlinks(source); err == nil {
		return resolved, nil
	}
	original := filepath.Clean(source)
	if original != source {
		return "", problem("cleanup_failed", "cannot resolve noncanonical Docker bind source; storage preserved")
	}
	for candidate := original; ; candidate = filepath.Dir(candidate) {
		_, err := os.Lstat(candidate)
		if err == nil {
			resolved, err := filepath.EvalSymlinks(candidate)
			if err != nil {
				return "", problem("cleanup_failed", "cannot resolve Docker bind source; storage preserved")
			}
			suffix, err := filepath.Rel(candidate, original)
			if err != nil {
				return "", err
			}
			return filepath.Join(resolved, suffix), nil
		}
		if !os.IsNotExist(err) || filepath.Dir(candidate) == candidate {
			return "", problem("cleanup_failed", "cannot inspect Docker bind source; storage preserved")
		}
	}
}

// workload allows the coordinator to supervise native and container work using
// the same finite-job, service-health and cancellation decisions.
type workload interface {
	Result() *native.Exit
	Alive() bool
	Stop(time.Duration) error
	PGID() int
}
type dockerWork struct {
	engine    *dockerEngine
	resource  ownedResource
	mu        sync.Mutex
	exit      *native.Exit
	logDone   chan struct{}
	logReader io.ReadCloser
	logErr    error
}

// PGID returns zero because Docker owns the isolated process namespace.
func (w *dockerWork) PGID() int { return 0 }

// Result observes termination or an infrastructure failure without retrying work.
func (w *dockerWork) Result() *native.Exit {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.exit != nil && w.exit.Known {
		result := *w.exit
		if w.logErr != nil {
			result.CollectionFailure = "container output collection failed"
		}
		return &result
	}
	if w.logErr != nil && w.exit == nil {
		return &native.Exit{Error: "container output collection failed", CollectionFailure: "container output collection failed"}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	out, err := w.engine.ContainerInspect(ctx, resourceHandle(w.resource), client.ContainerInspectOptions{})
	if err != nil {
		return &native.Exit{Error: "Docker inspection failed"}
	}
	if out.Container.State != nil && !out.Container.State.Running {
		w.exit = &native.Exit{Known: true, Code: out.Container.State.ExitCode, Error: out.Container.State.Error}
	}
	return w.exit
}

// Alive reports whether the container is still running.
func (w *dockerWork) Alive() bool { return w.Result() == nil }

// Stop verifies ownership, joins output and confirms the consumer stopped.
func (w *dockerWork) Stop(grace time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), grace+10*time.Second)
	defer cancel()
	defer w.joinLogs()
	if err := verifyDockerResource(ctx, w.engine, w.resource); err != nil {
		if errdefs.IsNotFound(err) {
			return nil
		}
		return err
	}
	seconds := int((grace + time.Second - 1) / time.Second)
	if _, err := w.engine.ContainerStop(ctx, resourceHandle(w.resource), client.ContainerStopOptions{Timeout: &seconds}); err != nil {
		return err
	}
	out, err := w.engine.ContainerInspect(ctx, resourceHandle(w.resource), client.ContainerInspectOptions{})
	if err != nil {
		return err
	}
	if out.Container.State == nil || out.Container.State.Running {
		return problem("cleanup_failed", "Docker consumer did not stop; data retained")
	}
	w.joinLogs()
	w.mu.Lock()
	defer w.mu.Unlock()
	w.exit = &native.Exit{Known: true, Code: out.Container.State.ExitCode, Error: out.Container.State.Error}
	if w.logErr != nil {
		w.exit.CollectionFailure = "container output collection failed"
	}
	return nil
}

func (w *dockerWork) joinLogs() {
	select {
	case <-w.logDone:
	case <-time.After(3 * time.Second):
		_ = w.logReader.Close()
		<-w.logDone
	}
}

type dockerLogWriter struct {
	file                           *os.File
	mu                             *sync.Mutex
	id, component, attempt, stream string
}

func (w dockerLogWriter) Write(data []byte) (int, error) {
	size := len(data)
	for len(data) > 0 {
		// JSON escaping expands a UTF-8 control byte to six bytes. Keep
		// serialized records below the retained reader's 64 KiB bound.
		n := min(len(data), 8<<10)
		chunk := data[:n]
		data = data[n:]
		record := v1.LogRecord{InstanceID: w.id, Component: w.component, Attempt: w.attempt, Time: time.Now().UTC().Format(time.RFC3339Nano), Stream: w.stream, Message: string(chunk)}
		if !utf8.Valid(chunk) {
			record.Message = ""
			record.Data = base64.StdEncoding.EncodeToString(chunk)
		}
		w.mu.Lock()
		err := json.NewEncoder(w.file).Encode(record)
		w.mu.Unlock()
		if err != nil {
			return 0, err
		}
	}
	return size, nil
}
func (s *store) captureContainer(ctx context.Context, id string, d *dockerEngine, r ownedResource, follow bool) (io.ReadCloser, chan struct{}, *dockerWork, error) {
	directory := filepath.Join(s.directory, "logs")
	if err := localipc.Directory(directory, true); err != nil {
		return nil, nil, nil, err
	}
	path := filepath.Join(directory, id+"-output.ndjson")
	if _, err := os.Lstat(path); err == nil {
		if err = localipc.File(path, false); err != nil {
			return nil, nil, nil, err
		}
	} else if !os.IsNotExist(err) {
		return nil, nil, nil, err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return nil, nil, nil, err
	}
	reader, err := d.ContainerLogs(ctx, resourceHandle(r), client.ContainerLogsOptions{ShowStdout: true, ShowStderr: true, Follow: follow})
	if err != nil {
		_ = file.Close()
		return nil, nil, nil, err
	}
	w := &dockerWork{engine: d, resource: r, logDone: make(chan struct{}), logReader: reader}
	var mu sync.Mutex
	stdout := dockerLogWriter{file: file, mu: &mu, id: id, component: r.Component, attempt: r.Attempt, stream: "stdout"}
	stderr := stdout
	stderr.stream = "stderr"
	go func() {
		_, copyErr := stdcopy.StdCopy(stdout, stderr, reader)
		closeErr := errors.Join(reader.Close(), file.Close())
		w.mu.Lock()
		w.logErr = errors.Join(copyErr, closeErr)
		w.mu.Unlock()
		close(w.logDone)
	}()
	return reader, w.logDone, w, nil
}
func (s *service) launchContainer(ctx context.Context, id string, c v1.PlannedComponent, snap plan.Snapshot, result *v1.ExecutionResult, entry *execution) (*dockerWork, error) {
	if err := entry.source.verify(); err != nil {
		return nil, err
	}
	image, err := resolvedValue(*c.Image, c.Name+"/image", id, snap, result, entry, c.Runtime)
	if err != nil {
		return nil, err
	}
	env, err := resolvedEnvironment(c.Name, c.Environment, id, snap, result, entry, c.Runtime)
	if err != nil {
		return nil, err
	}
	r := ownedResource{Kind: "container", Token: newID(), Component: c.Name, Attempt: result.Attempt}
	r.Name = "backlot-" + r.Token
	config := &container.Config{Image: image, Cmd: c.Args, Env: env, Labels: resourceLabels(r), ExposedPorts: network.PortSet{}}
	host := &container.HostConfig{RestartPolicy: container.RestartPolicy{Name: container.RestartPolicyDisabled}, PortBindings: network.PortMap{}}
	for _, port := range c.Ports {
		p, err := network.ParsePort(strconv.Itoa(port.ContainerPort) + "/tcp")
		if err != nil {
			return nil, err
		}
		config.ExposedPorts[p] = struct{}{}
		host.PortBindings[p] = []network.PortBinding{{HostIP: publishAddress(snap.Docker), HostPort: strconv.Itoa(result.Ports[port.Resource])}}
	}
	for _, m := range c.Mounts {
		x := mount.Mount{Target: m.Target, ReadOnly: m.ReadOnly, Type: mount.TypeBind}
		if m.Resource != "" {
			allocation := entry.resources.Resources[m.Resource]
			x.Source = allocation.Name
			if allocation.Kind == "volume" {
				x.Type = mount.TypeVolume
			}
		} else {
			project := filepath.Dir(snap.Plan.ManifestPath)
			output := snap.Plan.Outputs[m.Output]
			x.Source, err = plan.OutputPath(project, output.Path)
			if err != nil {
				return nil, err
			}
			root, openErr := os.OpenRoot(project)
			if openErr != nil {
				return nil, openErr
			}
			mkdirErr := root.MkdirAll(output.Path, 0755)
			closeErr := root.Close()
			if err = errors.Join(mkdirErr, closeErr); err != nil {
				return nil, err
			}
			checked, checkErr := plan.OutputPath(project, output.Path)
			if checkErr != nil {
				return nil, checkErr
			}
			if checked != x.Source {
				return nil, problem("invalid_output", "checkout output changed during container mount preparation")
			}

		}
		host.Mounts = append(host.Mounts, x)
	}
	networking := &network.NetworkingConfig{EndpointsConfig: map[string]*network.EndpointSettings{}}
	for name, allocation := range entry.resources.Resources {
		if allocation.Kind == "network" && (name == "@network" || contains(c.Resources, name)) {
			networking.EndpointsConfig[allocation.Name] = &network.EndpointSettings{NetworkID: allocation.ID, Aliases: []string{c.Name}}
		}
	}
	entry.resources.Containers = append(entry.resources.Containers, r)
	index := len(entry.resources.Containers) - 1
	if err = s.store.saveResources(id, *entry.resources); err != nil {
		return nil, err
	}
	out, err := entry.docker.ContainerCreate(ctx, client.ContainerCreateOptions{Name: r.Name, Config: config, HostConfig: host, NetworkingConfig: networking})
	if err != nil {
		return nil, providerFailure("create container (use external Docker tooling to pull/build the declared image)", err)
	}
	r.ID = out.ID
	entry.resources.Containers[index] = r
	if err = s.store.saveResources(id, *entry.resources); err != nil {
		return nil, err
	}
	if _, err = entry.docker.ContainerStart(ctx, r.ID, client.ContainerStartOptions{}); err != nil {
		conflict := false
		for _, port := range c.Ports {
			listener, bindErr := net.Listen("tcp", net.JoinHostPort(publishAddress(snap.Docker).String(), strconv.Itoa(result.Ports[port.Resource])))
			if bindErr != nil {
				conflict = true
			} else {
				_ = listener.Close()
			}
		}
		if conflict {
			cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			// Keep the full journal unchanged until this individual failed consumer is gone.
			if cleanupErr := verifyDockerResource(cleanupCtx, entry.docker, r); cleanupErr != nil {
				return nil, cleanupErr
			}
			zero := 0
			if _, cleanupErr := entry.docker.ContainerStop(cleanupCtx, r.ID, client.ContainerStopOptions{Timeout: &zero}); cleanupErr != nil {
				return nil, cleanupErr
			}
			inspect, cleanupErr := entry.docker.ContainerInspect(cleanupCtx, r.ID, client.ContainerInspectOptions{})
			if cleanupErr != nil {
				return nil, cleanupErr
			}
			if inspect.Container.State == nil || inspect.Container.State.Running {
				return nil, problem("cleanup_failed", "failed bind consumer remains live")
			}
			if _, cleanupErr = entry.docker.ContainerRemove(cleanupCtx, r.ID, client.ContainerRemoveOptions{RemoveVolumes: true}); cleanupErr != nil {
				return nil, cleanupErr
			}
			r.Removed = true
			entry.resources.Containers[index] = r
			if cleanupErr = s.store.saveResources(id, *entry.resources); cleanupErr != nil {
				return nil, cleanupErr
			}
			return nil, problem("port_conflict", "Docker bind conflict; retry only if no earlier consumer used the allocation")
		}
		return nil, providerFailure("start container", err)
	}
	// Output owner outlives startup/job deadlines, and is joined before removal.
	_, _, w, err := s.store.captureContainer(context.Background(), id, entry.docker, r, true)
	if w != nil {
		entry.containers = append(entry.containers, w)
	}
	return w, err
}
func contains(values []string, value string) bool {
	for _, v := range values {
		if v == value {
			return true
		}
	}
	return false
}
func removeContainer(ctx context.Context, d *dockerEngine, r ownedResource) error {
	if err := verifyDockerResource(ctx, d, r); err != nil {
		if errdefs.IsNotFound(err) {
			return nil
		}
		return err
	}
	zero := 0
	if _, err := d.ContainerStop(ctx, resourceHandle(r), client.ContainerStopOptions{Timeout: &zero}); err != nil {
		return err
	}
	out, err := d.ContainerInspect(ctx, resourceHandle(r), client.ContainerInspectOptions{})
	if err != nil {
		return err
	}
	if out.Container.State == nil || out.Container.State.Running {
		return problem("cleanup_failed", "owned container remains live; resources preserved")
	}
	_, err = d.ContainerRemove(ctx, resourceHandle(r), client.ContainerRemoveOptions{RemoveVolumes: true})
	return err
}
func (s *store) containerRemoved(id string, g *resourceGeneration, i int) error {
	g.Containers[i].Removed = true
	return s.saveResources(id, *g)
}
func (s *store) cleanupContainers(ctx context.Context, id string, g *resourceGeneration, d *dockerEngine) error {
	var failures error
	for i, r := range g.Containers {
		if r.Removed {
			continue
		}
		if err := removeContainer(ctx, d, r); err != nil {
			failures = errors.Join(failures, err)
			continue
		}
		failures = errors.Join(failures, s.containerRemoved(id, g, i))
	}
	return failures
}
func resolvedEnvironment(name string, environment map[string]v1.PlannedValue, id string, snap plan.Snapshot, result *v1.ExecutionResult, entry *execution, runtime v1.Runtime) ([]string, error) {
	var env []string
	for key, value := range environment {
		text, err := resolvedValue(value, name+"/environment/"+key, id, snap, result, entry, runtime)
		if err != nil {
			return nil, err
		}
		env = append(env, key+"="+text)
	}
	sort.Strings(env)
	return env, nil
}
func resolvedValue(value v1.PlannedValue, key, id string, snap plan.Snapshot, result *v1.ExecutionResult, entry *execution, runtime v1.Runtime) (string, error) {
	if value.Redacted {
		value = snap.Secrets[key]
	}
	r := value.Symbolic
	if r != nil && r.Kind == "resource" && r.Field == "url" {
		if snap.Plan.Publish == nil || r.Name != snap.Plan.Publish.Resource || result.Origin == "" {
			return "", problem("invalid_publication", "origin reference requires the scene publication")
		}
		return result.Origin, nil
	}
	if r != nil && r.Kind == "resource" && r.Field != "port" {
		resource, ok := entry.resources.Resources[r.Name]
		if !ok {
			return "", problem("state_corrupt", "resource allocation missing")
		}
		if r.Field == "value" {
			return resource.Value, nil
		}
		return resource.Name, nil
	}
	if r != nil && r.Kind == "service" && runtime == v1.Container {
		for _, c := range snap.Plan.Components {
			if c.Name == r.Name {
				if c.Runtime != v1.Container {
					if snap.Docker == nil || snap.Docker.HostAddress == "" {
						return "", problem("missing_host_address", "container-to-native references require config.docker.host_address and a native listener bound to a reachable interface")
					}
					if r.Field == "host" {
						return snap.Docker.HostAddress, nil
					}
					return strconv.Itoa(result.Ports[c.Ports[r.Port].Resource]), nil
				}
				if r.Field == "host" {
					return r.Name, nil
				}
				return strconv.Itoa(c.Ports[r.Port].ContainerPort), nil
			}
		}
	}
	if r != nil && r.Kind == "service" && r.Field == "host" && runtime == v1.Native {
		for _, component := range snap.Plan.Components {
			if component.Name == r.Name && component.Runtime == v1.Container {
				return mappedAddress(snap.Docker).String(), nil
			}
		}
	}
	return runtimeValue(value, id, snap.Plan, result.Ports)
}

func containerProbeOwned(ctx context.Context, d *dockerEngine, w *dockerWork, c v1.PlannedComponent, value v1.PlannedValue, ports map[string]int, address netip.Addr) error {
	r := value.Symbolic
	resource := ""
	if r != nil && r.Kind == "service" && r.Name == c.Name && r.Field == "port" {
		resource = c.Ports[r.Port].Resource
	}
	if r != nil && r.Kind == "resource" && r.Field == "port" {
		resource = r.Name
	}
	var port v1.ServicePort
	for _, p := range c.Ports {
		if p.Resource == resource {
			port = p
		}
	}
	if resource == "" || port.ContainerPort == 0 {
		return problem("readiness_unowned", "container network probes require a symbolic port owned by that component; use a native command probe for other checks")
	}
	if err := verifyDockerResource(ctx, d, w.resource); err != nil {
		return err
	}
	inspect, err := d.ContainerInspect(ctx, w.resource.ID, client.ContainerInspectOptions{})
	if err != nil {
		return err
	}
	p, err := network.ParsePort(strconv.Itoa(port.ContainerPort) + "/tcp")
	if err != nil {
		return err
	}
	if inspect.Container.NetworkSettings != nil {
		for _, binding := range inspect.Container.NetworkSettings.Ports[p] {
			if binding.HostIP == address && binding.HostPort == strconv.Itoa(ports[resource]) {
				return nil
			}
		}
	}
	return problem("readiness_unowned", "Docker port binding does not match owned allocation")
}

func publishAddress(config *v1.DockerConfig) netip.Addr {
	if config != nil && config.PublishAddress != "" {
		return netip.MustParseAddr(config.PublishAddress)
	}
	return netip.MustParseAddr("127.0.0.1")
}

// A wildcard bind is dialed through loopback in its own address family.
func mappedAddress(config *v1.DockerConfig) netip.Addr {
	address := publishAddress(config)
	if address.IsUnspecified() {
		if address.Is6() {
			return netip.MustParseAddr("::1")
		}
		return netip.MustParseAddr("127.0.0.1")
	}
	return address
}

// Recovery stops only containers bearing the recorded capability; output gaps
// remain explicit. It never resumes jobs or silently recreates retained state.
func (s *store) recoverContainers(id string, g *resourceGeneration) error {
	if g.ID == "" || len(g.Containers) == 0 {
		return nil
	}
	d, err := newDocker(g.Endpoint)
	if err != nil {
		return err
	}
	defer func() { _ = d.Close() }()
	var failures error
	for i, r := range g.Containers {
		if r.Removed {
			continue
		}
		// Give each independent capability its own budget so a failed consumer
		// cannot exhaust the recovery budget of later verifiably owned work.
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		err := s.recoverContainer(ctx, id, d, r)
		cancel()
		if err != nil {
			failures = errors.Join(failures, err)
			continue
		}
		failures = errors.Join(failures, s.containerRemoved(id, g, i))
	}
	return failures
}
func (s *store) recoverContainer(ctx context.Context, id string, d *dockerEngine, r ownedResource) error {
	if err := verifyDockerResource(ctx, d, r); err != nil {
		if errdefs.IsNotFound(err) {
			return nil
		}
		return err
	}
	zero := 0
	if _, err := d.ContainerStop(ctx, resourceHandle(r), client.ContainerStopOptions{Timeout: &zero}); err != nil {
		return err
	}
	_, done, w, err := s.captureContainer(ctx, id, d, r, false)
	if err != nil {
		return err
	}
	<-done
	if w.logErr != nil {
		return problem("collection_failed", "recovery container logs could not be retained; container preserved")
	}
	return removeContainer(ctx, d, r)
}

// providerFailure retains its cause for classification without publishing engine
// diagnostics that can contain a sensitive image/configuration value.
type providerError struct {
	operation string
	cause     error
}

// Error returns a safe diagnostic without engine-supplied sensitive details.
func (e *providerError) Error() string {
	return "Docker " + e.operation + " failed; check the configured local engine"
}

// Unwrap retains the provider cause for internal error classification.
func (e *providerError) Unwrap() error { return e.cause }
func providerFailure(operation string, cause error) error {
	return &providerError{operation: operation, cause: cause}
}
