package daemon

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"time"

	moby "github.com/moby/moby/client"
	v1 "github.com/octacian/backlot/api/v1"
	"github.com/octacian/backlot/internal/gateway"
	"github.com/octacian/backlot/internal/manifest"
	"github.com/octacian/backlot/internal/plan"
)

func diagnosticAdd(r *v1.DoctorResponse, name, execution string, status v1.DoctorStatus, message string) {
	r.Checks = append(r.Checks, v1.DoctorCheck{Name: name, Context: execution, Status: status, Message: message})
	if status == v1.DoctorFailed {
		r.Healthy = false
	}
}
func diagnosticResult(r *v1.DoctorResponse, name, execution string, err error, ok, failure string) {
	status, message := v1.DoctorOK, ok
	if err != nil {
		status, message = v1.DoctorFailed, failure
	}
	diagnosticAdd(r, name, execution, status, message)
}
func (s *service) doctor(ctx context.Context, request v1.DoctorRequest) (v1.DoctorResponse, error) {
	r := v1.DoctorResponse{APIVersion: v1.Version, Healthy: true, Scope: "daemon", Checks: []v1.DoctorCheck{}, Paths: []v1.DoctorPath{}}
	if err := request.Validate(); err != nil {
		return r, err
	}
	addStoragePaths(&r, s.directory, request.InstanceID)
	s.mu.Lock()
	active := map[string]bool{}
	for key := range s.diagnosticOwners {
		active[key] = true
	}
	recoveryErr := s.diagnosticRecoveryErr
	s.mu.Unlock()
	pending, pendingErr := s.store.diagnosticPending(active)
	if recoveryErr == nil && (pending || pendingErr != nil) {
		recoveryErr = problem("cleanup_failed", "diagnostic recovery pending")
	}
	if recoveryErr != nil {
		diagnosticAdd(&r, "diagnostic_cleanup", "daemon/owned Docker", v1.DoctorFailed, diagnosticRecoveryMessage(recoveryErr))
	}
	diagnosticAdd(&r, "daemon_api_state", "daemon", v1.DoctorOK, "v1 daemon and supported state format reachable; this does not establish provider readiness")
	if request.Scene == "" && request.InstanceID == "" {
		diagnosticAdd(&r, "providers", "daemon", v1.DoctorDeferred, "select --scene with --project or --instance for provider observations")
		return r, nil
	}
	var snap plan.Snapshot
	var instance v1.Instance
	if request.InstanceID != "" {
		r.Scope = "instance"
		r.InstanceID = request.InstanceID
		// Serialize with lifecycle mutations; runtime owners retain immutable snapshots
		// and persisted observed ports rather than exporting in-memory capabilities.
		unlock := s.lockMutation(request.InstanceID)
		defer unlock()
		var err error
		instance, err = s.store.inspect(request.InstanceID)
		if err != nil {
			return r, err
		}
		snap, err = s.store.snapshot(request.InstanceID)
		if err != nil {
			return r, err
		}
		diagnosticAdd(&r, "recorded_runtime", "daemon", v1.DoctorOK, "recorded instance state: "+string(instance.Status)+"; observations below do not assert aggregate readiness")
		if snap.ConfigProvenance == nil {
			journal, e := s.store.runtime(request.InstanceID)
			if e != nil {
				return r, e
			}
			if journal.ConfigPath != "" {
				snap.ConfigProvenance = &plan.ConfigProvenance{Path: journal.ConfigPath, Present: true}
			}
		}
		addLaunchPaths(&r, snap)
		checkCurrentFile(&r, "current_manifest", snap.Plan.ManifestPath, snap.Plan.ManifestDigest)
		if snap.ConfigProvenance != nil {
			if snap.ConfigProvenance.Present {
				checkCurrentFile(&r, "current_machine_config", snap.ConfigProvenance.Path, snap.Plan.ConfigDigest)
			} else {
				addPath(&r, "current_machine_config", snap.ConfigProvenance.Path, "")
			}
		} else {
			r.Paths = append(r.Paths, v1.DoctorPath{Name: "current_machine_config", State: "unrecorded"})
		}
		validateRecordedTools(&r, snap)
	} else {
		r.Scope = "scene"
		source, err := plan.Locate(request.ProjectPath)
		if err != nil {
			diagnosticAdd(&r, "runtime_inputs", "daemon/host", v1.DoctorFailed, "cannot discover selected manifest; verify --project and retain exactly one supported manifest")
			return r, nil
		}
		r.Paths = append(r.Paths, v1.DoctorPath{Name: "selected_manifest", Path: source.ManifestPath, State: "present"})
		snap, err = plan.Prepare(source, v1.PlanRequest{Scene: request.Scene, ProjectPath: request.ProjectPath, ConfigPath: request.ConfigPath})
		if err != nil {
			diagnosticAdd(&r, "runtime_inputs", "daemon/host", v1.DoctorFailed, "selected scene inputs are invalid or unavailable; run plan for field-specific guidance and check tools, seeds and required inputs")
			path := request.ConfigPath
			if path == "" {
				if dir, e := os.UserConfigDir(); e == nil {
					path = filepath.Join(dir, "backlot", "config.yaml")
				}
			}
			addPath(&r, "effective_machine_config", path, "")
			return r, nil
		}
		addConfigPath(&r, "effective_machine_config", snap)
		diagnosticAdd(&r, "runtime_inputs", "daemon/host", v1.DoctorOK, "selected manifest, tools, seeds and required inputs validated without executing application work")
	}
	// Bound all network observations; each dial also has its own finite timeout.
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	dockerRelevant := hasDocker(snap.Plan) || request.ContainerProbeImage != ""
	if dockerRelevant {
		if snap.Docker == nil {
			diagnosticAdd(&r, "docker_endpoint", "daemon/host", v1.DoctorFailed, "selected work requires config.docker.endpoint; supply machine configuration")
		} else {
			d, err := newDocker(snap.Docker.Endpoint)
			if err == nil {
				_, err = d.Ping(ctx, moby.PingOptions{})
				_ = d.Close()
			}
			diagnosticResult(&r, "docker_endpoint", "daemon/host", err, "configured local Docker endpoint responds", "cannot access selected Docker endpoint; check config.docker.endpoint and local socket permissions")
		}
	}
	if snap.Plan.Publish != nil {
		s.doctorGateway(ctx, &r, snap, instance)
	}
	if instance.Execution != nil {
		for _, cleanup := range instance.Execution.NativeCleanup {
			status := v1.DoctorOK
			if cleanup.Confidence != v1.ObservedEmpty {
				status = v1.DoctorFailed
			}
			diagnosticAdd(&r, "native_cleanup", cleanup.Component, status, cleanup.Detail)
		}
	}
	running := instance.Execution != nil && (instance.Status == v1.RuntimeReady || instance.Status == v1.Starting || instance.Execution.Kept)
	if !running {
		diagnosticAdd(&r, "upstreams", "daemon/host", v1.DoctorDeferred, "no available recorded live runtime target; start the scene explicitly then use doctor --instance")
	} else {
		doctorUpstreams(ctx, &r, snap, instance)
	}
	if snap.Plan.Publish != nil && (!running || instance.Execution.Origin == "") {
		diagnosticAdd(&r, "host_dns_tls", "daemon/host system trust", v1.DoctorDeferred, "canonical origin is unavailable without a live recorded instance; use --instance after explicit start")
	}
	if request.ContainerProbeImage != "" {
		if !running || instance.Execution.Origin == "" {
			diagnosticAdd(&r, "container_probe", "container", v1.DoctorDeferred, "container probe requires an available recorded published runtime")
		} else if snap.Docker != nil {
			s.doctorContainer(ctx, &r, request, snap, instance)
		}
	} else if hasDocker(snap.Plan) {
		diagnosticAdd(&r, "container_origin", "container", v1.DoctorDeferred, "host observations do not establish container DNS/reachability/trust; opt in with --instance and --container-probe-image")
	}
	return r, nil
}
func addStoragePaths(r *v1.DoctorResponse, directory, id string) {
	for _, p := range []struct{ name, path string }{{"daemon_state_directory", directory}, {"daemon_socket", SocketPath(directory)}, {"state_database", filepath.Join(directory, "state.db")}, {"daemon_log", filepath.Join(directory, "daemon.log")}, {"runtime_logs_directory", filepath.Join(directory, "logs")}, {"evidence_directory", filepath.Join(directory, "artifacts", id)}, {"runtime_data_directory", filepath.Join(directory, "data")}} {
		addPath(r, p.name, p.path, "")
	}
}
func addPath(r *v1.DoctorResponse, name, path, digest string) {
	state := "unrecorded"
	if path != "" {
		state = "present"
		if _, err := os.Stat(path); os.IsNotExist(err) {
			state = "absent"
		} else if err != nil {
			state = "unavailable"
		}
	}
	r.Paths = append(r.Paths, v1.DoctorPath{Name: name, Path: path, State: state, Digest: digest})
}
func addConfigPath(r *v1.DoctorResponse, name string, snap plan.Snapshot) {
	if snap.ConfigProvenance == nil {
		r.Paths = append(r.Paths, v1.DoctorPath{Name: name, State: "unrecorded", Digest: snap.Plan.ConfigDigest})
		return
	}
	state := "present"
	if !snap.ConfigProvenance.Present {
		state = "absent"
	}
	r.Paths = append(r.Paths, v1.DoctorPath{Name: name, Path: snap.ConfigProvenance.Path, State: state, Digest: snap.Plan.ConfigDigest})
}
func addLaunchPaths(r *v1.DoctorResponse, snap plan.Snapshot) {
	r.Paths = append(r.Paths, v1.DoctorPath{Name: "launch_manifest", Path: snap.Plan.ManifestPath, State: "present", Digest: snap.Plan.ManifestDigest})
	addConfigPath(r, "launch_machine_config", snap)
	diagnosticAdd(r, "launch_provenance", "daemon", v1.DoctorOK, "launch paths/digests describe the preserved snapshot, not current file contents; absent means no config was loaded, unrecorded means older provenance is unknown")
}
func checkCurrentFile(r *v1.DoctorResponse, name, path, digest string) {
	data, err := readDiagnosticFile(path)
	state := "present"
	if err != nil {
		state = "unavailable"
	} else {
		sum := sha256.Sum256(data)
		if hex.EncodeToString(sum[:]) != digest {
			state = "changed"
		}
	}
	r.Paths = append(r.Paths, v1.DoctorPath{Name: name, Path: path, State: state})
	if state != "present" {
		diagnosticAdd(r, name, "daemon/host", v1.DoctorFailed, "current file is changed or unavailable; runtime diagnostics still use the original recorded snapshot")
	}
}
func readDiagnosticFile(path string) ([]byte, error) {
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > manifest.MaxBytes {
		return nil, fmt.Errorf("unavailable regular diagnostic input")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, manifest.MaxBytes+1))
	if len(data) > manifest.MaxBytes {
		return nil, fmt.Errorf("oversized diagnostic input")
	}
	return data, err
}
func (s *service) doctorGateway(ctx context.Context, r *v1.DoctorResponse, snap plan.Snapshot, instance v1.Instance) {
	if snap.Caddy == nil {
		diagnosticAdd(r, "gateway_config", "daemon/host", v1.DoctorFailed, "published scene requires config.caddy; supply machine configuration")
		return
	}
	config := *snap.Caddy
	caPath := ""
	if config.Mode == "managed" {
		record, err := s.store.managed()
		if err != nil || record.Status != "running" || !reflect.DeepEqual(record.Config, config) {
			diagnosticAdd(r, "gateway_state", "daemon/host", v1.DoctorFailed, "matching managed gateway is unavailable; inspect gateway status and start it explicitly with its original config")
		} else {
			s.mu.Lock()
			ownedStatus := s.gatewayStatus(ctx, record).Status
			s.mu.Unlock()
			var ownershipErr error
			if ownedStatus != "running" {
				ownershipErr = problem("gateway_unavailable", "managed gateway listener ownership lost")
			}
			diagnosticResult(r, "gateway_owned_listeners", "daemon/host", ownershipErr, "authenticated managed gateway owns its configured listeners", "managed gateway process or listener ownership is unavailable; inspect gateway status and explicitly restart after stopping active scenes")
			config = managedCaddy(record)
			diagnosticResult(r, "gateway_saved_config", "daemon/host", validateManagedFiles(s.directory, record), "saved managed configuration and pinned CA match", "saved managed config or retained CA is missing, changed or invalid; preserve state and inspect gateway status")
		}
		root, saved, storage := managedPaths(s.directory)
		addPath(r, "gateway_state_directory", root, "")
		addPath(r, "gateway_saved_config", saved, "")
		caPath = filepath.Join(storage, "pki", "authorities", "local", "root.crt")
		addPath(r, "public_ca_certificate", caPath, "")
		diagnosticAdd(r, "manual_trust", "operator", v1.DoctorDeferred, "public_ca_certificate is the actual public root file for explicit manual trust; no browser/application trust is inferred or installed")
		for _, name := range []string{"root", "intermediate"} {
			_, err := diagnosticCA(filepath.Join(storage, "pki", "authorities", "local", name+".crt"))
			diagnosticResult(r, name+"_certificate_dates", "daemon/host public CA", err, "public CA certificate is readable, valid now and contains no key material", "public CA is unreadable, malformed, expired or not yet valid; inspect public_ca_certificate and preserve retained CA state")
		}
	} else {
		diagnosticAdd(r, "public_ca_certificate", "daemon/host", v1.DoctorUnsupported, "external gateway owns its certificate files; no public CA file is configured or inferred by Backlot")
	}
	var intent *gateway.Intent
	if instance.ID != "" {
		journal, err := s.store.runtime(instance.ID)
		if err == nil {
			intent = journal.Gateway
		}
		if instance.Status == v1.RuntimeReady && intent == nil {
			diagnosticAdd(r, "gateway_route", "daemon/host", v1.DoctorFailed, "live publication has no recorded route intent; preserve state and inspect instance/gateway diagnostics")
		}
	}
	diagnosticResult(r, "gateway_api_scope", "daemon/host", gateway.Diagnose(ctx, config, intent), "configured Caddy API and owned scope/recorded route match", "configured gateway API, owned scope or recorded route is unavailable/changed; inspect Caddy endpoint/configuration without replacing foreign state")
	listener := ""
	if config.Mode == "managed" {
		listener = net.JoinHostPort("127.0.0.1", strconv.Itoa(*config.HTTPSPort))
	} else if instance.Execution != nil && instance.Execution.Origin != "" {
		if origin, e := url.Parse(instance.Execution.Origin); e == nil {
			listener = diagnosticHTTPSAddress(origin)
		}
	}
	if listener == "" {
		diagnosticAdd(r, "gateway_listener", "daemon/host", v1.DoctorDeferred, "external gateway listener requires the recorded canonical runtime origin; no hostname is guessed from the domain suffix")
	} else {
		conn, err := (&net.Dialer{Timeout: 2 * time.Second}).DialContext(ctx, "tcp", listener)
		if err == nil {
			_ = conn.Close()
		}
		diagnosticResult(r, "gateway_listener", "daemon/host", err, "configured HTTPS listener accepts TCP; TLS is checked separately", "configured HTTPS listener is unreachable; verify the configured listener/address and port")
	}

	if instance.Execution == nil || instance.Execution.Origin == "" || (instance.Status != v1.RuntimeReady && instance.Status != v1.Starting && !instance.Execution.Kept) {
		return
	}
	origin, err := url.Parse(instance.Execution.Origin)
	if err != nil {
		return
	}
	addresses, err := net.DefaultResolver.LookupHost(ctx, origin.Hostname())
	if err == nil && len(addresses) == 0 {
		err = fmt.Errorf("no addresses")
	}
	diagnosticResult(r, "host_dns", "daemon/host resolver", err, "canonical origin resolves through the normal host resolver", "canonical origin does not resolve; configure operator DNS for this host without resolver overrides")
	doctorTLS(ctx, r, origin, nil, "host_tls_system", "daemon/host system trust")
	if caPath != "" {
		pool, err := diagnosticCA(caPath)
		if err == nil {
			doctorTLS(ctx, r, origin, pool, "host_tls_public_ca", "daemon/host explicit public CA")
		} else {
			diagnosticAdd(r, "host_tls_public_ca", "daemon/host explicit public CA", v1.DoctorDeferred, "explicit public CA TLS cannot run until the public certificate is usable")
		}
	}
}
func diagnosticCA(path string) (*x509.CertPool, error) {
	data, err := readDiagnosticFile(path)
	if err != nil {
		return nil, err
	}
	pool := x509.NewCertPool()
	count := 0
	for len(data) > 0 {
		block, rest := pem.Decode(data)
		if block == nil || block.Type != "CERTIFICATE" {
			return nil, fmt.Errorf("public certificate required")
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil || !cert.IsCA || time.Now().Before(cert.NotBefore) || !time.Now().Before(cert.NotAfter) {
			return nil, fmt.Errorf("invalid public CA dates")
		}
		pool.AddCert(cert)
		count++
		data = rest
	}
	if count == 0 {
		return nil, fmt.Errorf("empty CA")
	}
	return pool, nil
}

// diagnosticHTTPSAddress preserves explicit ports and IPv6 while supplying the
// HTTPS default for canonical origins that omit port 443.
func diagnosticHTTPSAddress(origin *url.URL) string {
	port := origin.Port()
	if port == "" {
		port = "443"
	}
	return net.JoinHostPort(origin.Hostname(), port)
}
func doctorTLS(ctx context.Context, r *v1.DoctorResponse, origin *url.URL, roots *x509.CertPool, name, execution string) {
	dialer := tls.Dialer{NetDialer: &net.Dialer{Timeout: 3 * time.Second}, Config: &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots, ServerName: origin.Hostname()}}
	conn, err := dialer.DialContext(ctx, "tcp", diagnosticHTTPSAddress(origin))
	if err == nil {
		_ = conn.Close()
	}
	diagnosticResult(r, name, execution, err, "canonical hostname TLS chain and certificate dates verified in this context only", "normal TLS validation failed; check DNS, listener, certificate dates/hostname and trust for this execution context")
}
func doctorUpstreams(ctx context.Context, r *v1.DoctorResponse, snap plan.Snapshot, instance v1.Instance) {
	count := 0
	for _, component := range snap.Plan.Components {
		if component.Kind != v1.Service {
			continue
		}
		for name, port := range component.Ports {
			allocated := instance.Execution.Ports[port.Resource]
			if allocated == 0 {
				diagnosticAdd(r, "upstream_"+component.Name+"_"+name, "daemon/host", v1.DoctorDeferred, "runtime port is not allocated")
				continue
			}
			count++
			address := "127.0.0.1"
			if component.Runtime == v1.Container && snap.Docker != nil && snap.Docker.PublishAddress != "" {
				address = snap.Docker.PublishAddress
				if address == "0.0.0.0" {
					address = "127.0.0.1"
				}
				if address == "::" {
					address = "::1"
				}
			}
			conn, err := (&net.Dialer{Timeout: 2 * time.Second}).DialContext(ctx, "tcp", net.JoinHostPort(address, strconv.Itoa(allocated)))
			if err == nil {
				_ = conn.Close()
			}
			diagnosticResult(r, "upstream_"+component.Name+"_"+name, "daemon/host", err, "recorded upstream accepts TCP; this does not assert application health or gateway-context reachability", "recorded upstream is unreachable from daemon/host; inspect service status, logs and bind address")
		}
	}
	if count == 0 {
		diagnosticAdd(r, "upstreams", "daemon/host", v1.DoctorUnsupported, "selected services expose no recorded TCP ports; command/application health is not executed by doctor")
	}
	if snap.Caddy != nil && snap.Caddy.Mode != "managed" {
		diagnosticAdd(r, "gateway_upstreams", "gateway", v1.DoctorDeferred, "daemon/host reachability does not prove external gateway reachability; inspect gateway logs and configured upstream address")
	}
}

// validateRecordedTools checks original launch executable inputs without running
// commands or replacing snapshot declarations with a changed current manifest.
func validateRecordedTools(r *v1.DoctorResponse, snap plan.Snapshot) {
	for _, c := range snap.Plan.Components {
		for _, executable := range []string{c.Executable, probeExecutable(c)} {
			if executable == "" {
				continue
			}
			info, err := os.Stat(executable)
			if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0111 == 0 {
				diagnosticAdd(r, "runtime_input_"+c.Name, "daemon/host", v1.DoctorFailed, "recorded executable is unavailable or not executable; restore configured tool and restart explicitly")
			}
		}
	}
}
func probeExecutable(c v1.PlannedComponent) string {
	if c.Readiness != nil {
		return c.Readiness.Executable
	}
	return ""
}
