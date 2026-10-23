package daemon

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"time"

	v1 "github.com/octacian/backlot/api/v1"
	"github.com/octacian/backlot/internal/gateway"
	"github.com/octacian/backlot/internal/localipc"
	"github.com/octacian/backlot/internal/manifest"
	"github.com/octacian/backlot/internal/native"
	"github.com/octacian/backlot/internal/plan"
	bolt "go.etcd.io/bbolt"
)

// managedRecord is a separate daemon resource, never an application instance.
// Pending is committed before creating an inactive guardian; absence of its
// authenticated effect is uncertainty, not authority to signal a PID.
type managedRecord struct {
	ClaimRevision      string              `json:"claim_revision,omitempty"`
	Scope              string              `json:"scope"`
	Config             v1.CaddyConfig      `json:"config"`
	Identity           *native.Identity    `json:"identity,omitempty"`
	Pending            bool                `json:"pending,omitempty"`
	PendingBoot        string              `json:"pending_boot,omitempty"`
	Initialized        bool                `json:"initialized,omitempty"`
	RootHash           string              `json:"root_hash,omitempty"`
	Status             string              `json:"status"`
	Failure            string              `json:"failure,omitempty"`
	NativeAbandonments []nativeAbandonment `json:"native_abandonments,omitempty"`
}

func (s *store) managed() (r managedRecord, err error) {
	err = s.db.View(func(tx *bolt.Tx) error {
		data := tx.Bucket([]byte("meta")).Get([]byte("managed_gateway"))
		r, err = decodeManagedRecord(data)
		return err
	})
	return
}

// decodeManagedRecord is shared by reads, waiver comparison and the v5 fence.
func decodeManagedRecord(data []byte) (r managedRecord, err error) {
	if data == nil {
		r.Status = "unconfigured"
	} else {
		if json.Unmarshal(data, &r) != nil || r.Config.Mode != "managed" || !validID(r.Scope) || r.Config.HTTPSPort == nil || !reflect.DeepEqual(r.Config, manifest.CaddyDefaults(r.Config)) || manifest.ValidateMachine(v1.MachineConfig{Version: v1.ManifestVersion, Caddy: &r.Config}) != nil {
			return r, problem("gateway_state", "invalid managed gateway ownership record; preserved")
		}
		switch r.Status {
		case "unconfigured", "running", "stopped", "interrupted", "uncertain":
		default:
			return r, problem("gateway_state", "invalid managed gateway status; preserved")
		}
	}
	if r.ClaimRevision != "" && !validID(r.ClaimRevision) {
		return r, problem("gateway_state", "invalid managed claim revision; preserved")
	}
	if r.ClaimRevision == "" {
		// Legacy claims have no generation. Hash only their private claim tuple;
		// this public precondition conveys no cleanup authority.
		claim, _ := json.Marshal(struct {
			Scope    string
			Identity *native.Identity
			Pending  bool
			Boot     string
		}{r.Scope, r.Identity, r.Pending, r.PendingBoot})
		digest := sha256.Sum256(claim)
		r.ClaimRevision = hex.EncodeToString(digest[:])
	}
	return r, nil
}

func (s *store) saveManaged(r managedRecord) error {
	return s.db.Update(func(tx *bolt.Tx) error { return put(tx, "meta", "managed_gateway", r) })
}

// managedCaddy selects the persisted private scope capability for gateway effects.
// User configuration remains stable; ordinary responses never expose this ID.
func managedCaddy(r managedRecord) v1.CaddyConfig {
	c := r.Config
	c.Scope = r.Scope
	return c
}

func managedPaths(directory string) (root, saved, storage string) {
	root = filepath.Join(directory, "gateway")
	return root, filepath.Join(root, "config", "caddy", "autosave.json"), filepath.Join(root, "data")
}

// validateCA checks both key pairs and pins the root identity before any restart.
func validateCA(storage, expected string) (string, error) {
	dir := filepath.Join(storage, "pki", "authorities", "local")
	var root []byte
	var rootCert *x509.Certificate
	for _, name := range []string{"root", "intermediate"} {
		cert, key := filepath.Join(dir, name+".crt"), filepath.Join(dir, name+".key")
		if localipc.File(cert, false) != nil || localipc.File(key, false) != nil {
			return "", problem("gateway_ca", "managed CA state is missing or unsafe; preserve state and restore the original CA")
		}
		pair, err := tls.LoadX509KeyPair(cert, key)
		if err != nil {
			return "", problem("gateway_ca", "managed CA certificate/key is unusable; preserve original state")
		}
		parsed, err := x509.ParseCertificate(pair.Certificate[0])
		if err != nil || !parsed.IsCA {
			return "", problem("gateway_ca", "managed CA is not a usable certificate authority; preserved")
		}
		if name == "root" {
			root = pair.Certificate[0]
			rootCert = parsed
			if parsed.CheckSignatureFrom(parsed) != nil || time.Now().Before(parsed.NotBefore) || time.Now().After(parsed.NotAfter) {
				return "", problem("gateway_ca", "managed root CA is invalid or expired; preserve original state")
			}
		} else if parsed.CheckSignatureFrom(rootCert) != nil {
			return "", problem("gateway_ca", "managed intermediate CA does not belong to retained root; preserved")
		}
	}
	hash := sha256.Sum256(root)
	value := hex.EncodeToString(hash[:])
	if expected != "" && expected != value {
		return "", problem("gateway_ca", "managed CA identity changed; restore the original CA rather than silently replacing trust")
	}
	return value, nil
}

func validateManagedFiles(directory string, r managedRecord) error {
	root, saved, storage := managedPaths(directory)
	for _, dir := range []string{root, filepath.Join(root, "config"), filepath.Dir(saved), storage, filepath.Join(storage, "pki"), filepath.Join(storage, "pki", "authorities"), filepath.Join(storage, "pki", "authorities", "local")} {
		if err := localipc.Directory(dir, false); err != nil {
			return problem("gateway_state", "managed private state directory is missing or unsafe; preserved")
		}
	}
	if err := localipc.File(saved, false); err != nil {
		return problem("gateway_state", "managed saved configuration missing or unsafe; refusing stale bootstrap fallback")
	}
	data, err := os.ReadFile(saved)
	if err != nil || len(data) > manifest.MaxBytes {
		return problem("gateway_state", "managed saved configuration unavailable or too large; preserved")
	}
	if err := gateway.ValidateManagedConfig(data, managedCaddy(r), storage); err != nil {
		return err
	}
	_, err = validateCA(storage, r.RootHash)
	return err
}

// recoverManaged runs after scene recovery has tried exact-route removal against
// the surviving gateway. Failures remain visible while the daemon stays usable.
func (s *store) recoverManaged() error {
	r, err := s.managed()
	if err != nil {
		return err
	}
	if r.Identity == nil && !r.Pending {
		return nil
	}
	if err := s.stopManaged(&r, nil); err != nil {
		r.Status = "uncertain"
		r.Failure = err.Error()
	} else {
		r.Status = "interrupted"
		r.Failure = "daemon interruption; explicit gateway start required"
	}
	return s.saveManaged(r)
}

func (s *store) stopManaged(r *managedRecord, group *native.Group) error {
	if r.Status == "unconfigured" && r.Identity == nil && !r.Pending {
		return nil
	}
	if r.Pending {
		previous, err := native.PreviousBoot(r.PendingBoot)
		if err != nil || !previous {
			return problem("gateway_ownership", "managed guardian launch effect is missing; preserve uncertain resources")
		}
		// The intent predates this boot; no inactive helper can have survived.
		r.Pending, r.PendingBoot = false, ""
	}
	if r.Identity != nil {
		if (r.Identity.Protocol != 0 && (r.Identity.Purpose != "strict" || r.Identity.Protocol != 1)) || group != nil && group.Identity != *r.Identity {
			return problem("gateway_ownership", "managed guardian strict policy binding mismatch; preserve uncertain resources")
		}
		var err error
		if group != nil {
			err = group.Stop(time.Second)
		} else {
			err = native.Stop(*r.Identity, time.Second)
		}
		if err != nil {
			return problem("gateway_ownership", "managed gateway cleanup could not be verified: "+err.Error())
		}
		r.Identity = nil
	}
	r.Status, r.Failure = "stopped", ""
	return s.saveManaged(*r)
}

func (s *service) gatewayControl(ctx context.Context, action string, request v1.GatewayRequest) (v1.GatewayResponse, error) {
	if request.Force && action != "stop" {
		return v1.GatewayResponse{}, problem("invalid_request", "force is supported only by gateway stop")
	}
	s.mu.Lock() // Serializes gateway lifecycle with scene launch acceptance.
	defer s.mu.Unlock()
	r, err := s.store.managed()
	if err != nil {
		return v1.GatewayResponse{}, err
	}
	if request.Force && request.ExpectedClaimRevision != r.ClaimRevision {
		return v1.GatewayResponse{}, problem("conflict", "selected gateway claim changed; inspect before issuing a new force stop")
	}
	if action == "status" {
		return s.gatewayStatus(ctx, r), nil
	}
	if s.closing {
		return v1.GatewayResponse{}, problem("shutting_down", "daemon is shutting down")
	}
	if request.ConfigPath != "" {
		if !filepath.IsAbs(request.ConfigPath) {
			return v1.GatewayResponse{}, problem("invalid_request", "gateway configuration path must be absolute")
		}
		data, err := os.ReadFile(request.ConfigPath)
		if err != nil {
			return v1.GatewayResponse{}, problem("gateway_config", "cannot read machine configuration")
		}
		var config v1.MachineConfig
		if err := manifest.Decode(data, filepath.Ext(request.ConfigPath), &config); err != nil {
			return v1.GatewayResponse{}, err
		}
		if err := manifest.ValidateMachine(config); err != nil {
			return v1.GatewayResponse{}, err
		}
		if config.Caddy == nil || config.Caddy.Mode != "managed" {
			return v1.GatewayResponse{}, problem("gateway_external", "gateway commands require explicit managed mode; external gateway lifecycle belongs to its operator")
		}
		c := manifest.CaddyDefaults(*config.Caddy)
		if r.Status != "unconfigured" && !reflect.DeepEqual(c, r.Config) {
			return v1.GatewayResponse{}, problem("gateway_config", "managed gateway configuration differs from retained state; original configuration preserved")
		}
		r.Config = c
	} else if r.Status == "unconfigured" && action != "stop" {
		return v1.GatewayResponse{}, problem("gateway_config", "first gateway start requires --config with explicit managed mode and native executable")
	}
	if action == "stop" || action == "restart" {
		if err := s.store.publishedActive(); err != nil {
			return v1.GatewayResponse{}, err
		}
		expected := r
		if err := s.store.stopManaged(&r, s.gatewayGroup); err != nil {
			if !request.Force {
				return v1.GatewayResponse{}, err
			}
			nativeResult := (*native.Exit)(nil)
			if s.gatewayGroup != nil {
				nativeResult = s.gatewayGroup.Result()
			}
			if err := s.store.abandonManaged(ctx, &r, expected, nativeResult, err); err != nil {
				return v1.GatewayResponse{}, err
			}
			if s.gatewayGroup != nil {
				s.gatewayGroup.Relinquish()
			}
		}
		s.gatewayGroup = nil
		if action == "stop" {
			return s.gatewayStatus(ctx, r), nil
		}
	}
	if r.Pending || r.Status == "uncertain" {
		return v1.GatewayResponse{}, problem("gateway_ownership", "managed gateway ownership is uncertain; use gateway stop to verify cleanup, then gateway start")
	}
	if r.Identity != nil {
		if s.gatewayStatus(ctx, r).Status == "running" {
			return s.gatewayStatus(ctx, r), nil
		}
		return v1.GatewayResponse{}, problem("gateway_unavailable", "managed gateway lost readiness; use explicit gateway restart after stopping published scenes")
	}
	if err := s.startManaged(ctx, &r); err != nil {
		return v1.GatewayResponse{}, err
	}
	return s.gatewayStatus(ctx, r), nil
}

func (s *store) publishedActive() error {
	return s.db.View(func(tx *bolt.Tx) error {
		return tx.Bucket([]byte("instances")).ForEach(func(key, data []byte) error {
			var r record
			if err := decodeRecord(data, string(key), &r); err != nil {
				return err
			}
			i := r.Instance
			if i.Plan.Publish != nil && i.Execution != nil && (active(i.Status) || i.Execution.Kept) {
				return problem("gateway_busy", "stop all active published scenes before gateway stop/restart")
			}
			return nil
		})
	})
}

func (s *service) gatewayStatus(ctx context.Context, r managedRecord) v1.GatewayResponse {
	response := v1.GatewayResponse{ClaimRevision: r.ClaimRevision, NativeOwnershipWarnings: abandonmentWarnings(r.NativeAbandonments), APIVersion: v1.Version, Mode: "managed", Status: r.Status, Endpoint: r.Config.Endpoint, Message: r.Failure}
	if r.Config.HTTPSPort != nil {
		response.HTTPSPort = *r.Config.HTTPSPort
	}
	if r.Status == "running" {
		if s.gatewayGroup == nil || s.gatewayGroup.Result() != nil || !s.gatewayGroup.Alive() || s.managedListeners(ctx, r.Config) != nil {
			response.Status, response.Message = "unavailable", "managed gateway readiness lost; explicit restart required"
		}
	}
	return response
}

func (s *service) managedListeners(ctx context.Context, c v1.CaddyConfig) error {
	if s.gatewayGroup == nil {
		return problem("gateway_unavailable", "gateway is not started")
	}
	u, _ := url.Parse(c.Endpoint)
	admin, _ := strconv.Atoi(u.Port())
	https := gateway.ManagedHTTPSBindings(c)
	addresses := make([]string, len(https))
	for i, binding := range https {
		addresses[i] = binding.Address
	}
	for _, listener := range []struct {
		port      int
		addresses []string
	}{{admin, []string{u.Host}}, {*c.HTTPSPort, addresses}} {
		owned, err := native.OwnsListenerAddresses(ctx, s.gatewayGroup.PGID(), listener.port, listener.addresses)
		if err != nil || !owned {
			return problem("gateway_unavailable", "configured managed listener ownership/family/exposure is unavailable; no fallback performed")
		}
	}
	return nil
}

func (s *service) startManaged(ctx context.Context, r *managedRecord) (result error) {
	if _, err := exec.LookPath(r.Config.Executable); err != nil {
		return problem("gateway_config", "configured native Caddy executable is unavailable or not executable")
	}
	root, saved, storage := managedPaths(s.directory)
	u, _ := url.Parse(r.Config.Endpoint)
	// Bind checks are observations, never reservations. Subsequent guardian
	// listener proof rejects races; no retry, fallback, or unrelated actuation.
	for _, binding := range append([]gateway.ManagedListener{{Network: "tcp", Address: u.Host}}, gateway.ManagedHTTPSBindings(r.Config)...) {
		listener, err := net.Listen(binding.Network, binding.Address)
		if err != nil {
			return problem("gateway_port_conflict", "configured managed listener is unavailable; no fallback or takeover")
		}
		if err := listener.Close(); err != nil {
			return err
		}
	}
	if r.Scope == "" {
		r.Scope = newID()
		if err := s.store.saveManaged(*r); err != nil {
			return err
		}
	}
	if r.Initialized {
		if err := validateManagedFiles(s.directory, *r); err != nil {
			return err
		}
	} else {
		if _, err := os.Lstat(root); !os.IsNotExist(err) {
			return problem("gateway_state", "unjournaled managed state exists; preserved")
		}
		for _, dir := range []string{root, filepath.Join(root, "config"), storage} {
			if err := localipc.Directory(dir, true); err != nil {
				return err
			}
		}
		data, err := gateway.ManagedConfig(managedCaddy(*r), storage)
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(root, "bootstrap.json"), data, 0600); err != nil {
			return err
		}
	}
	logPath := filepath.Join(root, "gateway.log")
	if _, err := os.Lstat(logPath); err == nil {
		if err := localipc.File(logPath, false); err != nil {
			return err
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	log, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return err
	}
	defer func() { result = errors.Join(result, log.Close()) }()
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	boot, err := native.CurrentBoot()
	if err != nil {
		return err
	}
	r.ClaimRevision = newID()
	r.Pending, r.PendingBoot = true, boot
	if err := s.store.saveManaged(*r); err != nil {
		return err
	}
	group, startErr := native.Start(executable, log, log)
	if group == nil {
		r.Pending, r.PendingBoot = false, ""
		return errors.Join(startErr, s.store.saveManaged(*r))
	}
	if group.Identity.Boot != boot {
		return errors.New("native launch boot provenance mismatch; preserve intent")
	}
	r.Identity = &group.Identity
	r.Pending, r.PendingBoot = false, ""
	s.gatewayGroup = group
	if err := s.store.saveManaged(*r); err != nil {
		return err
	}
	defer func() {
		if result != nil {
			cleanup := s.store.stopManaged(r, group)
			if cleanup != nil {
				r.Status = "uncertain"
				r.Failure = cleanup.Error()
				result = errors.Join(result, cleanup, s.store.saveManaged(*r))
			} else {
				s.gatewayGroup = nil
			}
		}
	}()
	if startErr != nil {
		return startErr
	}
	args := []string{"run", "--config", filepath.Join(root, "bootstrap.json")}
	if r.Initialized {
		args = []string{"run", "--resume"}
	}
	r.Initialized = true // From this point never fall back to bootstrap, even on failure.
	if err := s.store.saveManaged(*r); err != nil {
		return err
	}
	if err := group.Activate(native.Spec{Executable: r.Config.Executable, Args: args, Directory: root, Environment: []string{"HOME=" + root, "XDG_CONFIG_HOME=" + filepath.Join(root, "config"), "XDG_DATA_HOME=" + storage}, InstanceID: "managed-gateway", Component: "caddy", Attempt: newID()}); err != nil {
		return err
	}
	adminReady := false
	deadline := time.Now().Add(10 * time.Second)
	client := &http.Client{Timeout: time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	defer client.CloseIdleConnections()
	for time.Now().Before(deadline) {
		if group.Result() != nil {
			return problem("gateway_start_failed", "native Caddy exited; inspect private gateway/gateway.log")
		}
		if s.managedListeners(ctx, r.Config) == nil {
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, r.Config.Endpoint+"/config/", nil)
			if err != nil {
				return err
			}
			response, err := client.Do(req)
			if err == nil {
				_ = response.Body.Close()
				if response.StatusCode == http.StatusOK {
					adminReady = true
					break
				}
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
	if !adminReady {
		return problem("gateway_unavailable", "managed admin API did not become ready")
	}
	if err := s.managedListeners(ctx, r.Config); err != nil {
		return err
	}
	if _, err := os.Stat(saved); err != nil {
		return problem("gateway_state", "Caddy did not persist effective configuration; explicit repair required")
	}
	hash, err := validateCA(storage, r.RootHash)
	if err != nil {
		return err
	}
	r.RootHash = hash
	if err := validateManagedFiles(s.directory, *r); err != nil {
		return err
	}
	r.Status, r.Failure = "running", ""
	return s.store.saveManaged(*r)
}

// requireManaged fences launch acceptance while gateway lifecycle holds s.mu.
func (s *service) requireManaged(ctx context.Context, c v1.CaddyConfig) error {
	if c.Mode != "managed" {
		return nil
	}
	r, err := s.store.managed()
	if err != nil {
		return err
	}
	if r.Status == "unconfigured" {
		return problem("gateway_unavailable", "start the managed gateway explicitly before running published scenes")
	}
	if !reflect.DeepEqual(c, r.Config) {
		return problem("gateway_config", "scene settings differ from the daemon-owned gateway")
	}
	if s.gatewayStatus(ctx, r).Status != "running" {
		return problem("gateway_unavailable", "start the managed gateway explicitly before running published scenes")
	}
	return nil
}

// consumerCACertPath resolves only explicitly mapped trust, before workload
// allocation. It returns Caddy's existing public certificate, never an export
// copy or key path. The private state and pinned CA must still be usable.
func (s *store) consumerCACertPath(snapshot plan.Snapshot) (string, error) {
	needed := false
	for _, component := range snapshot.Plan.Components {
		for _, mount := range component.Mounts {
			if mount.OriginCACert != "" {
				if component.Runtime != v1.Container || !mount.ReadOnly || snapshot.Plan.Publish == nil || snapshot.Plan.Publish.Resource != mount.OriginCACert || snapshot.Plan.Resources[mount.OriginCACert].Kind != "origin" {
					return "", problem("invalid_contract", "origin_ca_cert requires a read-only container mount of the published origin")
				}
				needed = true
			}
		}
		for key, value := range component.Environment {
			if value.Redacted {
				value = snapshot.Secrets[component.Name+"/environment/"+key]
			}
			if r := value.Symbolic; r != nil && r.Kind == "resource" && r.Field == "ca_cert_path" {
				if snapshot.Plan.Publish == nil || snapshot.Plan.Publish.Resource != r.Name || snapshot.Plan.Resources[r.Name].Kind != "origin" {
					return "", problem("invalid_contract", "ca_cert_path requires a selected consumer and published origin")
				}
				if component.Runtime == v1.Container {
					found := false
					for _, mount := range component.Mounts {
						if mount.OriginCACert == r.Name && mount.ReadOnly {
							found = true
						}
					}
					if !found {
						return "", problem("invalid_contract", "container ca_cert_path requires an explicit read-only origin_ca_cert mount")
					}
				}
				needed = true
			}
		}
	}
	if !needed {
		return "", nil
	}
	if snapshot.Caddy == nil || snapshot.Caddy.Mode != "managed" {
		return "", problem("invalid_contract", "ca_cert_path requires explicit managed Caddy configuration")
	}
	r, err := s.managed()
	if err != nil {
		return "", err
	}
	if !r.Initialized || r.RootHash == "" || !reflect.DeepEqual(*snapshot.Caddy, r.Config) {
		return "", problem("gateway_ca", "managed CA has no matching pinned identity; start the configured gateway explicitly")
	}
	if err := validateManagedFiles(s.directory, r); err != nil {
		return "", err
	}
	_, _, storage := managedPaths(s.directory)
	path := filepath.Join(storage, "pki", "authorities", "local", "root.crt")
	data, err := os.ReadFile(path)
	if err != nil {
		return "", problem("gateway_ca", "managed public root certificate is unavailable")
	}
	block, rest := pem.Decode(data)
	if block == nil || block.Type != "CERTIFICATE" || len(block.Headers) != 0 || len(bytes.TrimSpace(rest)) != 0 || !bytes.Equal(bytes.TrimSpace(data), bytes.TrimSpace(pem.EncodeToMemory(block))) {
		return "", problem("gateway_ca", "managed public root must contain only its PEM certificate; preserve original state")
	}
	return path, nil
}
