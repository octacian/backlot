package daemon

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"maps"
	"net"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/containerd/errdefs"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/mount"
	moby "github.com/moby/moby/client"
	v1 "github.com/octacian/backlot/api/v1"
	"github.com/octacian/backlot/internal/plan"
	bolt "go.etcd.io/bbolt"
)

// diagnosticOwner is joined before daemon storage/ownership can be released.
type diagnosticOwner struct {
	cancel context.CancelFunc
	done   chan struct{}
}
type diagnosticProbeRecord struct {
	Endpoint string            `json:"endpoint"`
	Resource ownedResource     `json:"resource"`
	Network  ownedResource     `json:"network"`
	CA       *diagnosticCACopy `json:"ca,omitempty"`
}

func (s *store) saveDiagnosticProbe(key string, r diagnosticProbeRecord) error {
	return s.db.Update(func(tx *bolt.Tx) error { return put(tx, "meta", "doctor_probe/"+key, r) })
}
func (s *store) forgetDiagnosticProbe(key string) error {
	return s.db.Update(func(tx *bolt.Tx) error { return tx.Bucket([]byte("meta")).Delete([]byte("doctor_probe/" + key)) })
}
func decodeDiagnosticProbe(id string, data []byte) (r diagnosticProbeRecord, err error) {
	if !jsonObject(data) || json.Unmarshal(data, &r) != nil || !validDiagnosticCACopy(r) || !validID(id) || !validID(r.Resource.Token) || r.Resource.Kind != "container" || r.Resource.Name != "backlot-doctor-"+id || (r.Network.Kind != "" && (r.Network.Kind != "network" || !validID(r.Network.Token) || r.Network.Name != "backlot-doctor-network-"+id)) {
		return r, problem("state_corrupt", "invalid diagnostic ownership journal; preserved")
	}
	return r, nil
}

func (s *store) recoverDiagnosticProbes() error {
	records := map[string]diagnosticProbeRecord{}
	err := s.db.View(func(tx *bolt.Tx) error {
		return tx.Bucket([]byte("meta")).ForEach(func(k, data []byte) error {
			key := string(k)
			if !strings.HasPrefix(key, "doctor_probe/") {
				return nil
			}
			id := strings.TrimPrefix(key, "doctor_probe/")
			r, err := decodeDiagnosticProbe(id, data)
			if err != nil {
				return err
			}
			records[id] = r
			return nil
		})
	})
	if err != nil {
		return err
	}
	for _, key := range slices.Sorted(maps.Keys(records)) {
		r := records[key]
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		d, e := newDocker(r.Endpoint)
		if e == nil {
			e = s.cleanupDiagnosticProbe(ctx, d, key, r)
			_ = d.Close()
		}
		cancel()
		if e == nil {
			e = s.forgetDiagnosticProbe(key)
			if e != nil {
				e = errors.Join(diagnosticRemovalFailed, e)
			}
		}
		if e != nil {
			err = diagnosticRecoveryFailureFor(err, key, r, e)
		}
	}
	return err
}
func (s *service) stopDiagnostics() error {
	s.mu.Lock()
	owners := make([]*diagnosticOwner, 0, len(s.diagnosticOwners))
	for _, owner := range s.diagnosticOwners {
		owner.cancel()
		owners = append(owners, owner)
	}
	s.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()
	for _, owner := range owners {
		select {
		case <-owner.done:
		case <-ctx.Done():
			return problem("cleanup_failed", "diagnostic owner remains unjoined; preserve daemon state")
		}
	}
	return s.store.recoverDiagnosticProbes()
}

// doctorContainer executes only this documented diagnostic script; image commands,
// entrypoints and application environment are never inherited from the scene.
func (s *service) doctorContainer(parent context.Context, r *v1.DoctorResponse, request v1.DoctorRequest, snap plan.Snapshot, instance v1.Instance) {
	ctx, cancel := context.WithTimeout(parent, 10*time.Second)
	defer cancel()
	owner := &diagnosticOwner{cancel: cancel, done: make(chan struct{})}
	key := newID()
	s.mu.Lock()
	if s.closing {
		s.mu.Unlock()
		diagnosticAdd(r, "container_probe", "container", v1.DoctorDeferred, "daemon is shutting down; no probe accepted")
		return
	}
	if s.diagnosticOwners == nil {
		s.diagnosticOwners = map[string]*diagnosticOwner{}
	}
	s.diagnosticOwners[key] = owner
	s.mu.Unlock()
	defer func() { s.mu.Lock(); delete(s.diagnosticOwners, key); close(owner.done); s.mu.Unlock() }()
	d, err := newDocker(snap.Docker.Endpoint)
	if err != nil {
		diagnosticAdd(r, "container_probe", "container", v1.DoctorFailed, "cannot access selected Docker endpoint; no probe created")
		return
	}
	defer func() { _ = d.Close() }()
	image, err := d.ImageInspect(ctx, request.ContainerProbeImage)
	if err != nil {
		diagnosticAdd(r, "container_probe_image", "container", v1.DoctorFailed, "probe image is not accessible locally; choose an already-present image with the documented shell/getent/timeout/OpenSSL contract; doctor never pulls")
		return
	}
	if image.Config != nil && len(image.Config.Volumes) > 0 {
		diagnosticAdd(r, "container_probe_image", "container", v1.DoctorUnsupported, "probe images declaring volumes are unsupported; choose a diagnostic image without implicit storage")
		return
	}
	origin, _ := url.Parse(instance.Execution.Origin)
	host := &container.HostConfig{ReadonlyRootfs: true, CapDrop: []string{"ALL"}, SecurityOpt: []string{"no-new-privileges"}, NetworkMode: "bridge", Resources: container.Resources{Memory: 128 << 20, PidsLimit: ptrInt64(64)}}
	if snap.Caddy != nil && snap.Caddy.Mode == "managed" {
		for _, c := range snap.Plan.Components {
			for _, mapping := range c.HostMappings {
				if mapping.Origin == snap.Plan.Publish.Resource {
					host.ExtraHosts = []string{origin.Hostname() + ":host-gateway"}
				}
			}
		}
	}
	ca := ""
	execution := "container image system trust"
	if request.ProbePublicCA {
		if snap.Caddy == nil || snap.Caddy.Mode != "managed" {
			diagnosticAdd(r, "container_probe_public_ca", "container explicit public CA", v1.DoctorUnsupported, "explicit public CA probe requires a managed gateway public certificate")
			return
		}

		ca = "/backlot-public-ca.crt"
		execution = "container explicit public CA"
	}
	resource := ownedResource{Kind: "container", Name: "backlot-doctor-" + key, Token: newID()}
	journal := diagnosticProbeRecord{Endpoint: snap.Docker.Endpoint, Resource: resource, Network: ownedResource{Kind: "network", Name: "backlot-doctor-network-" + key, Token: newID()}}
	var publicCA []byte
	if request.ProbePublicCA {
		publicCA, journal.CA, err = s.store.planDiagnosticCACopy(snap)
		if err != nil {
			diagnosticAdd(r, "container_probe_public_ca", execution, v1.DoctorFailed, "matching pinned public CA certificate is unavailable or invalid; no probe created")
			return
		}
	}
	if err := s.store.saveDiagnosticProbe(key, journal); err != nil {
		diagnosticAdd(r, "container_probe_journal", execution, v1.DoctorFailed, "cannot persist ownership intent; no probe created")
		return
	}
	// Register independent cleanup before the first external effect, including an
	// ambiguous create response. Name is never deletion authority without its token.
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		journal.Resource = resource
		err := s.store.cleanupDiagnosticProbe(cleanup, d, key, journal)
		if err == nil {
			err = s.store.forgetDiagnosticProbe(key)
			if err != nil {
				err = errors.Join(diagnosticRemovalFailed, err)
			}
		}
		diagnosticCleanupResult(r, key, journal, err)
		if err != nil {
			s.mu.Lock()
			s.diagnosticRecoveryErr = diagnosticRecoveryFailureFor(s.diagnosticRecoveryErr, key, journal, err)
			s.mu.Unlock()
		}
	}()
	if journal.CA != nil {
		if err := s.store.allocateDiagnosticCACopy(key, &journal, publicCA); err != nil {
			diagnosticAdd(r, "container_probe_public_ca", execution, v1.DoctorFailed, "cannot allocate verified public CA copy; owned cleanup follows")
			return
		}
		host.Mounts = []mount.Mount{{Type: mount.TypeBind, Source: diagnosticCACopyPath(s.directory, key), Target: ca, ReadOnly: true}}
	}
	networkID, err := createDockerResource(ctx, d, journal.Network)
	if err != nil {
		diagnosticAdd(r, "container_probe_network", execution, v1.DoctorFailed, "owned probe bridge creation failed; cleanup reconciles uncertain effects")
		return
	}
	journal.Network.ID = networkID
	if err := s.store.saveDiagnosticProbe(key, journal); err != nil {
		diagnosticAdd(r, "container_probe_journal", execution, v1.DoctorFailed, "cannot persist owned bridge effect; verified cleanup follows")
		return
	}
	host.NetworkMode = container.NetworkMode(networkID)
	script := `for tool in getent timeout openssl; do command -v "$tool" >/dev/null || exit 20; done
[ -x /bin/bash ] || exit 20
getent hosts "$1" >/dev/null 2>&1 || exit 10
timeout 3 /bin/bash -c 'exec 3<>/dev/tcp/"$1"/"$2"' backlot-doctor "$1" "$4" >/dev/null 2>&1 || exit 11
if [ -n "$3" ]; then
 timeout 5 openssl s_client -connect "$5" -servername "$1" -verify_hostname "$1" -verify_return_error -CAfile "$3" </dev/null >/dev/null 2>&1
else
 timeout 5 openssl s_client -connect "$5" -servername "$1" -verify_hostname "$1" -verify_return_error </dev/null >/dev/null 2>&1
fi
code=$?
case "$code" in 0) exit 0;; 124|137) exit 14;; *) exit 12;; esac`
	port := origin.Port()
	if port == "" {
		port = "443"
	}

	config := &container.Config{Image: image.ID, Healthcheck: &container.HealthConfig{Test: []string{"NONE"}}, Entrypoint: []string{"/bin/sh", "-c", script, "backlot-doctor"}, Cmd: []string{origin.Hostname(), instance.Execution.Origin, ca, port, net.JoinHostPort(origin.Hostname(), port)}, Labels: resourceLabels(resource), Env: []string{"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin", "HOME=/tmp"}, WorkingDir: "/", User: "0:0"}
	if err := s.store.verifyDiagnosticCACopy(key, journal.CA); err != nil {
		diagnosticAdd(r, "container_probe_public_ca", execution, v1.DoctorFailed, "public CA copy changed before probe creation; preserve and inspect")
		return
	}
	created, err := d.ContainerCreate(ctx, moby.ContainerCreateOptions{Name: resource.Name, Config: config, HostConfig: host})
	if err != nil {
		diagnosticAdd(r, "container_probe", execution, v1.DoctorFailed, "owned probe creation failed; inspect image contract and selected endpoint, cleanup reconciles uncertain effect")
		return
	}
	resource.ID = created.ID
	journal.Resource = resource
	if err := s.store.saveDiagnosticProbe(key, journal); err != nil {
		diagnosticAdd(r, "container_probe_journal", execution, v1.DoctorFailed, "cannot persist probe effect; verified cleanup follows")
		return
	}
	if s.checkpoint != nil {
		if err := s.checkpoint("doctor-effect"); err != nil {
			diagnosticAdd(r, "container_probe", execution, v1.DoctorFailed, "probe interrupted after journaled creation; verified cleanup follows")
			return
		}
	}
	if err := verifyDockerResource(ctx, d, resource); err != nil {
		diagnosticAdd(r, "container_probe", execution, v1.DoctorFailed, "probe ownership could not be verified; resource preserved if uncertain")
		return
	}
	if err := s.store.verifyDiagnosticCACopy(key, journal.CA); err != nil {
		diagnosticAdd(r, "container_probe_public_ca", execution, v1.DoctorFailed, "public CA copy changed before probe start; preserve and inspect")
		return
	}
	if _, err = d.ContainerStart(ctx, resource.ID, moby.ContainerStartOptions{}); err != nil {
		diagnosticAdd(r, "container_probe", execution, v1.DoctorFailed, "probe start failed; verify image diagnostic contract")
		return
	}
	if s.checkpoint != nil {
		if err := s.checkpoint("doctor-started"); err != nil {
			diagnosticAdd(r, "container_probe", execution, v1.DoctorFailed, "running probe cancelled; verified cleanup follows")
			return
		}
	}
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		observed, err := d.ContainerInspect(ctx, resource.ID, moby.ContainerInspectOptions{})
		if err != nil || observed.Container.State == nil {
			diagnosticAdd(r, "container_probe", execution, v1.DoctorFailed, "probe state unavailable; verified cleanup follows")
			return
		}
		if !observed.Container.State.Running {
			code := observed.Container.State.ExitCode
			if code == 20 || code == 126 || code == 127 {
				diagnosticAdd(r, "container_probe_contract", execution, v1.DoctorUnsupported, "image must provide /bin/sh, /bin/bash, getent, timeout and OpenSSL s_client hostname/chain verification")
				return
			}
			if code != 0 && code != 10 && code != 11 && code != 12 && code != 14 {
				diagnosticAdd(r, "container_probe", execution, v1.DoctorFailed, "diagnostic script failed or was terminated; inspect the image contract and execution resources")
				return
			}
			dns := v1.DoctorOK
			if code == 10 {
				dns = v1.DoctorFailed
			}
			diagnosticAdd(r, "container_dns", "container resolver", dns, map[bool]string{true: "canonical hostname resolves in the diagnostic container", false: "canonical hostname fails container resolution; inspect declared host mappings and container DNS"}[dns == v1.DoctorOK])
			tcpStatus, tcpMessage := v1.DoctorOK, "canonical HTTPS TCP listener is reachable from the diagnostic container"
			if code == 10 {
				tcpStatus, tcpMessage = v1.DoctorDeferred, "TCP deferred because container DNS failed"
			}
			if code == 11 {
				tcpStatus, tcpMessage = v1.DoctorFailed, "container TCP connection failed/timed out; inspect HTTPS bind address and declared host-gateway mapping"
			}
			diagnosticAdd(r, "container_tcp", "container network", tcpStatus, tcpMessage)
			status, message := v1.DoctorOK, "normal hostname TLS and certificate dates verified; this establishes only this diagnostic image's context"
			if code != 0 {
				status = v1.DoctorFailed
				message = "container HTTPS failed; inspect listener/bind reachability and image trust/public CA inputs"
			}
			if code == 10 {
				status = v1.DoctorDeferred
				message = "TLS deferred because container DNS failed"
			}
			if code == 11 {
				status = v1.DoctorDeferred
				message = "TLS deferred because container TCP reachability failed"
			}
			if code == 14 {
				message = "container TLS verification timed out; inspect listener/TLS availability in this context"
			}
			if code == 12 {
				message = "container TLS validation failed; verify hostname, dates and image trust or explicitly opt in to --probe-public-ca"
			}
			diagnosticAdd(r, "container_https", execution, status, message)
			return
		}
		select {
		case <-ctx.Done():
			diagnosticAdd(r, "container_probe", execution, v1.DoctorFailed, "bounded probe cancelled or timed out; independent verified cleanup follows")
			return
		case <-ticker.C:
		}
	}
}
func ptrInt64(value int64) *int64 { return &value }

// cleanupDiagnosticProbe removes consumers before their exact owned bridge.
// An unresolved container effect prevents releasing network ownership.
func cleanupDiagnosticProbe(ctx context.Context, d *dockerEngine, r diagnosticProbeRecord) error {
	resource, err := resolveDiagnosticResource(ctx, d, r.Resource)
	if err != nil && !errdefs.IsNotFound(err) {
		return err
	}
	if err == nil {
		if err := removeContainer(ctx, d, resource); err != nil && !errdefs.IsNotFound(err) {
			return err
		}
	}
	if r.Network.Kind != "" {
		network, err := resolveDiagnosticResource(ctx, d, r.Network)
		if errdefs.IsNotFound(err) {
			return nil
		}
		if err != nil {
			return err
		}
		err = removeDockerResource(ctx, d, network)
		if errdefs.IsNotFound(err) {
			return nil
		}
		return err
	}
	return nil
}

// resolveDiagnosticResource treats a lost create's name only as an observation.
// Authenticate the returned full Engine ID before any subsequent request; never
// fall back to the name if that immutable identity disappears or becomes uncertain.
func resolveDiagnosticResource(ctx context.Context, d *dockerEngine, r ownedResource) (ownedResource, error) {
	if d == nil {
		return r, problem("missing_provider", "recorded Docker engine required for diagnostic cleanup")
	}
	var id string
	var labels map[string]string
	switch r.Kind {
	case "container":
		out, err := d.ContainerInspect(ctx, resourceHandle(r), moby.ContainerInspectOptions{})
		if err != nil {
			return r, err
		}
		if out.Container.Config == nil {
			return r, problem("cleanup_failed", "container configuration missing; preserved")
		}
		id, labels = out.Container.ID, out.Container.Config.Labels
	case "network":
		out, err := d.NetworkInspect(ctx, resourceHandle(r), moby.NetworkInspectOptions{})
		if err != nil {
			return r, err
		}
		id, labels = out.Network.ID, out.Network.Labels
	default:
		return r, problem("state_corrupt", "unknown diagnostic Docker allocation kind")
	}
	if err := owned(labels, r); err != nil {
		return r, err
	}
	decoded, err := hex.DecodeString(id)
	if err != nil || len(decoded) != 32 || (r.ID != "" && r.ID != id) {
		return r, problem("cleanup_failed", "Docker identity uncertain; resource preserved")
	}
	r.ID = id
	return r, nil
}

func (s *store) diagnosticPending(active map[string]bool) (bool, error) {
	pending := false
	err := s.db.View(func(tx *bolt.Tx) error {
		return tx.Bucket([]byte("meta")).ForEach(func(key, _ []byte) error {
			name := string(key)
			if strings.HasPrefix(name, "doctor_probe/") && !active[strings.TrimPrefix(name, "doctor_probe/")] {
				pending = true
			}
			return nil
		})
	})
	return pending, err
}
