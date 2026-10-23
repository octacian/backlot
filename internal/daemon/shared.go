package daemon

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/containerd/errdefs"
	"github.com/go-sql-driver/mysql"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/mount"
	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/client"
	v1 "github.com/octacian/backlot/api/v1"
	"github.com/octacian/backlot/internal/manager"
	"github.com/octacian/backlot/internal/plan"
	bolt "go.etcd.io/bbolt"
)

type sharedServiceRecord struct {
	ID            string                `json:"id"`
	Project       sharedProjectIdentity `json:"project"`
	Name          string                `json:"name"`
	Declaration   v1.SharedService      `json:"declaration"`
	Endpoint      string                `json:"endpoint"`
	Configuration string                `json:"configuration"`
	EffectiveArgs []string              `json:"effective_args"`
	ImageID       string                `json:"image_id"`
	AdminPassword string                `json:"admin_password"`
	Volume        ownedResource         `json:"volume"`
	Network       ownedResource         `json:"network"`
	Server        ownedResource         `json:"server"`
	Port          int                   `json:"port"`
	Status        string                `json:"status"`
	Destroying    bool                  `json:"destroying"`
	Failure       string                `json:"failure,omitempty"`
}
type sharedAllocation struct {
	ServiceID string            `json:"service_id"`
	Request   v1.ManagerRequest `json:"request"`
	Result    v1.ManagerResult  `json:"result"`
	Released  bool              `json:"released"`
}
type sharedConnection struct {
	Request v1.ManagerRequest
	Network ownedResource
	Alias   string
	Port    int
}

// lockShared serializes effects and durable updates per service. Callers that
// mutate allocation maps also hold the existing instance lifecycle ownership.
// Ordered acquisition avoids cycles when one instance uses multiple services.
func (s *store) lockShared(ctx context.Context, ids ...string) (func(), error) {
	ids = slices.Clone(ids)
	slices.Sort(ids)
	ids = slices.Compact(ids)
	var acquired []chan struct{}
	release := func() {
		for _, lock := range acquired {
			<-lock
		}
	}
	for _, id := range ids {
		candidate := make(chan struct{}, 1)
		value, _ := s.sharedLocks.LoadOrStore(id, candidate)
		lock := value.(chan struct{})
		select {
		case lock <- struct{}{}:
			acquired = append(acquired, lock)
		case <-ctx.Done():
			release()
			return nil, ctx.Err()
		}
	}
	// A ready lock and cancellation can both win select; never begin effects
	// after observing cancellation, even when every lock was immediately free.
	if err := ctx.Err(); err != nil {
		release()
		return nil, err
	}
	return release, nil
}

func sharedDigest(declaration v1.SharedService, endpoint string) string {
	data, _ := json.Marshal(struct {
		Declaration v1.SharedService
		Endpoint    string
	}{declaration, endpoint})
	return hash(string(data))
}
func (s *store) sharedService(id string) (sharedServiceRecord, error) {
	var record sharedServiceRecord
	err := s.db.View(func(tx *bolt.Tx) error {
		data := tx.Bucket([]byte("shared_services")).Get([]byte(id))
		if data == nil {
			return problem("not_found", "shared service not found; use shared list")
		}
		var err error
		record, err = decodeSharedService(data, id)
		return err
	})
	return record, err
}

// decodeSharedService is shared by ordinary reads and transaction-local preflight.
func decodeSharedService(data []byte, id string) (sharedServiceRecord, error) {
	var record sharedServiceRecord
	if err := json.Unmarshal(data, &record); err != nil {
		return record, problem("state_corrupt", "shared service cannot be decoded; preserved")
	}
	if record.ID != id || !validID(id) || !validID(record.Project.ID) || !validID(record.AdminPassword) || !validID(record.Server.Token) || !validID(record.Network.Token) || !validID(record.Volume.Token) || record.Configuration != sharedDigest(record.Declaration, record.Endpoint) {
		return record, problem("state_corrupt", "shared service contract incomplete; preserved")
	}
	for _, resource := range []ownedResource{record.Server, record.Network, record.Volume} {
		if err := validateSharedResourceHandle(resource); err != nil {
			return record, err
		}
	}
	return record, nil
}

// Empty handles are pending intents; acknowledged Docker IDs cannot be aliases.
func validateSharedResourceHandle(r ownedResource) error {
	if r.ID == "" {
		return nil
	}
	switch r.Kind {
	case "container", "network":
		if validID(r.ID) {
			return nil
		}
	case "volume":
		if r.ID == r.Name {
			return nil
		}
	}
	return problem("state_corrupt", "invalid acknowledged shared resource handle; preserved")
}
func (s *store) saveShared(record sharedServiceRecord) error {
	return s.db.Update(func(tx *bolt.Tx) error { return put(tx, "shared_services", record.ID, record) })
}
func decodeSharedAllocations(data []byte) (map[string]sharedAllocation, error) {
	out := map[string]sharedAllocation{}
	if data == nil {
		return out, nil
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, problem("state_corrupt", "shared allocations cannot be decoded; preserved")
	}
	if out == nil {
		return nil, problem("state_corrupt", "shared allocations are null; preserved")
	}
	for _, allocation := range out {
		if !validID(allocation.ServiceID) || manager.ValidateRequest(allocation.Request) != nil || (allocation.Request.Operation != "allocate" && allocation.Request.Operation != "release") {
			return nil, problem("state_corrupt", "shared allocation identity incomplete; preserved")
		}
		if allocation.Request.Force {
			return nil, problem("state_corrupt", "stored force authority is invalid; preserved")
		}
		if allocation.Result != (v1.ManagerResult{}) && manager.ValidateResult(allocation.Request, allocation.Result) != nil {
			return nil, problem("state_corrupt", "shared allocation observation invalid; preserved")
		}
		if allocation.Released && (allocation.Request.Operation != "release" || allocation.Result.Ownership != "absent" || allocation.Result.Effect != "released") {
			return nil, problem("state_corrupt", "shared release acknowledgment invalid; preserved")
		}
	}
	return out, nil
}
func (s *store) sharedAllocations(id string) (map[string]sharedAllocation, error) {
	var out map[string]sharedAllocation
	err := s.db.View(func(tx *bolt.Tx) error {
		var err error
		out, err = decodeSharedAllocations(tx.Bucket([]byte("shared_allocations")).Get([]byte(id)))
		return err
	})
	return out, err
}
func (s *store) saveSharedAllocations(id string, allocations map[string]sharedAllocation) error {
	for _, a := range allocations {
		if a.Request.Force {
			return problem("state_corrupt", "force authority cannot be persisted")
		}
	}
	return s.db.Update(func(tx *bolt.Tx) error { return put(tx, "shared_allocations", id, allocations) })
}

// ensureSharedResource reconciles an intent by exact capability before creating.
// An acknowledged storage effect that disappeared is never silently replaced.
func (s *store) ensureSharedResource(ctx context.Context, d *dockerEngine, record *sharedServiceRecord, r *ownedResource) error {
	err := verifyDockerResource(ctx, d, *r)
	if err == nil {
		if r.ID == "" {
			r.ID, err = recoverResourceEffect(ctx, d, *r)
			if err == nil {
				err = s.saveShared(*record)
			}
		}
		return err
	}
	if !errdefs.IsNotFound(err) {
		return providerFailure("verify shared resource", err)
	}
	if r.ID != "" {
		return problem("shared_missing", "retained shared storage/network disappeared; resources preserved; explicit maintenance required")
	}
	r.ID, err = createDockerResource(ctx, d, *r)
	if err != nil {
		return providerFailure("create shared resource", err)
	}
	if err = s.sharedPoint("create-effect:" + r.Kind); err != nil {
		return err
	}
	return s.saveShared(*record)
}

func (s *store) verifySharedServer(ctx context.Context, d *dockerEngine, record sharedServiceRecord) error {
	return s.verifySharedServerMode(ctx, d, record, false)
}
func (s *store) verifySharedServerMode(ctx context.Context, d *dockerEngine, record sharedServiceRecord, force bool) error {
	if err := verifySharedResource(ctx, d, record.Server, force); err != nil {
		return sharedProviderError(err)
	}
	out, err := d.ContainerInspect(ctx, resourceHandle(record.Server), client.ContainerInspectOptions{})
	if err != nil {
		return sharedProviderError(err)
	}
	if out.Container.Config == nil || !slices.Equal(out.Container.Config.Cmd, record.EffectiveArgs) || out.Container.Image != record.ImageID || out.Container.HostConfig == nil || out.Container.HostConfig.RestartPolicy.Name != container.RestartPolicyDisabled {
		return problem("shared_uncertain", "shared server configuration changed; preserved")
	}
	expectedEnv := "MARIADB_ROOT_PASSWORD=" + record.AdminPassword
	if !contains(out.Container.Config.Env, expectedEnv) {
		return problem("shared_uncertain", "shared administration changed; preserved")
	}
	mounted := false
	if len(out.Container.Mounts) != 1 {
		return problem("shared_uncertain", "shared mounts changed; preserved")
	}
	for _, m := range out.Container.Mounts {
		if m.Destination == "/var/lib/mysql" && m.Name == record.Volume.Name && m.RW {
			mounted = true
		}
	}
	if !mounted || out.Container.NetworkSettings == nil || len(out.Container.NetworkSettings.Networks) != 1 || out.Container.NetworkSettings.Networks[record.Network.Name] == nil || out.Container.NetworkSettings.Networks[record.Network.Name].NetworkID != record.Network.ID {
		return problem("shared_uncertain", "shared storage/network configuration changed; preserved")
	}
	return nil
}

func (s *store) startShared(ctx context.Context, record *sharedServiceRecord) error {
	return s.startSharedMode(ctx, record, false)
}

// startSharedMode may waive shared capability labels only for existing targets.
// Configuration and current binding verification still precede SQL connection.
func (s *store) startSharedMode(ctx context.Context, record *sharedServiceRecord, force bool) (resultErr error) {
	defer func() { resultErr = s.sharedOperationResult(record, resultErr) }()
	if record.Destroying || record.Server.Removed || record.Volume.Removed || record.Network.Removed {
		return problem("shared_cleanup_pending", "shared service destruction is incomplete; retry shared destroy")
	}
	duration, err := time.ParseDuration(record.Declaration.ReadinessTimeout)
	if err != nil {
		return problem("state_corrupt", "shared readiness deadline invalid")
	}
	ctx, cancel := context.WithTimeout(ctx, duration)
	defer cancel()
	d, err := newDocker(record.Endpoint)
	if err != nil {
		return err
	}
	defer func() { _ = d.Close() }()
	if force && record.ImageID == "" {
		return problem("shared_uncertain", "recorded image unavailable; restore original server or retire exact storage manually")
	}
	if record.ImageID == "" {
		image, err := d.ImageInspect(ctx, record.Declaration.Image)
		if err != nil {
			return providerFailure("inspect shared image (pre-pull explicitly)", err)
		}
		record.ImageID = image.ID
		record.EffectiveArgs = slices.Clone(record.Declaration.Args)
		if len(record.EffectiveArgs) == 0 && image.Config != nil {
			record.EffectiveArgs = slices.Clone(image.Config.Cmd)
		}
		if err = s.saveShared(*record); err != nil {
			return err
		}
	}
	if force {
		for _, r := range []*ownedResource{&record.Volume, &record.Network} {
			if err = verifySharedResource(ctx, d, *r, true); err != nil {
				return sharedProviderError(err)
			}
			if r.ID == "" {
				r.ID, err = recoverResourceEffect(ctx, d, *r)
				if err != nil {
					return sharedProviderError(err)
				}
				if err = s.saveShared(*record); err != nil {
					return err
				}
			}
		}
	} else {
		if err = s.ensureSharedResource(ctx, d, record, &record.Volume); err != nil {
			return err
		}
		if err = s.ensureSharedResource(ctx, d, record, &record.Network); err != nil {
			return err
		}
	}
	out, err := d.ContainerInspect(ctx, resourceHandle(record.Server), client.ContainerInspectOptions{})
	if errdefs.IsNotFound(err) {
		if force {
			return problem("shared_missing", "recorded server missing; restore original server or retire exact storage manually")
		}
		if record.Server.ID != "" {
			return problem("shared_missing", "retained shared server disappeared; preserved; explicit maintenance required")
		}
		port, _ := network.ParsePort("3306/tcp")
		labels := resourceLabels(record.Server)
		labels["io.backlot.shared-service"] = record.ID
		cfg := &container.Config{Image: record.ImageID, Env: []string{"MARIADB_ROOT_PASSWORD=" + record.AdminPassword, "MARIADB_ROOT_HOST=%"}, Labels: labels, ExposedPorts: network.PortSet{port: {}}}
		if len(record.Declaration.Args) > 0 {
			cfg.Cmd = record.Declaration.Args
		}
		host := &container.HostConfig{RestartPolicy: container.RestartPolicy{Name: container.RestartPolicyDisabled}, Mounts: []mount.Mount{{Type: mount.TypeVolume, Source: record.Volume.Name, Target: "/var/lib/mysql"}}, PortBindings: network.PortMap{port: []network.PortBinding{{HostIP: publishAddress(nil), HostPort: ""}}}}
		networking := &network.NetworkingConfig{EndpointsConfig: map[string]*network.EndpointSettings{record.Network.Name: {NetworkID: record.Network.ID, Aliases: []string{"shared-" + record.ID[:32]}}}}
		created, createErr := d.ContainerCreate(ctx, client.ContainerCreateOptions{Name: record.Server.Name, Config: cfg, HostConfig: host, NetworkingConfig: networking})
		if createErr != nil {
			return providerFailure("create shared server", createErr)
		}
		record.Server.ID = created.ID
		if err = s.sharedPoint("create-effect:container"); err != nil {
			return err
		}
		if err = s.saveShared(*record); err != nil {
			return err
		}
	} else if err != nil {
		return providerFailure("inspect shared server", err)
	} else {
		if err = s.verifySharedServerMode(ctx, d, *record, force); err != nil {
			return err
		}
		if record.Server.ID == "" {
			record.Server.ID = out.Container.ID
			if err = s.saveShared(*record); err != nil {
				return err
			}
		}
	}
	if err = s.verifySharedServerMode(ctx, d, *record, force); err != nil {
		return err
	}
	if _, err = d.ContainerStart(ctx, record.Server.ID, client.ContainerStartOptions{}); err != nil {
		return providerFailure("start shared server", err)
	}
	out, err = d.ContainerInspect(ctx, record.Server.ID, client.ContainerInspectOptions{})
	if err != nil {
		return providerFailure("inspect shared binding", err)
	}
	port, _ := network.ParsePort("3306/tcp")
	if out.Container.NetworkSettings == nil || len(out.Container.NetworkSettings.Ports[port]) != 1 {
		return problem("shared_uncertain", "shared loopback binding unavailable")
	}
	binding := out.Container.NetworkSettings.Ports[port][0]
	if binding.HostIP != publishAddress(nil) {
		return problem("shared_uncertain", "shared binding is not loopback; preserved")
	}
	record.Port, err = strconv.Atoi(binding.HostPort)
	if err != nil || record.Port <= 0 {
		return problem("shared_uncertain", "shared binding invalid")
	}
	if err = s.saveShared(*record); err != nil {
		return err
	}
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		db, openErr := sharedDB(*record)
		if openErr == nil {
			openErr = db.PingContext(ctx)
			_ = db.Close()
		}
		if openErr == nil {
			break
		}
		select {
		case <-ctx.Done():
			return problem("shared_unavailable", "shared MariaDB readiness failed; retained intent permits explicit retry")
		case <-ticker.C:
		}
	}
	db, err := sharedDB(*record)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	var version string
	var skipGrants int
	if err = db.QueryRowContext(ctx, "SELECT VERSION(), @@skip_grant_tables").Scan(&version, &skipGrants); err != nil {
		return problem("shared_unavailable", "cannot verify MariaDB server version/security mode")
	}
	if !strings.Contains(version, "MariaDB") || skipGrants != 0 {
		return problem("shared_unsupported", "bundled manager requires MariaDB with privilege enforcement enabled")
	}
	record.Status = "running"
	record.Failure = ""
	return s.saveShared(*record)
}
func sharedDB(record sharedServiceRecord) (*sql.DB, error) {
	config := mysql.NewConfig()
	config.User = "root"
	config.Passwd = record.AdminPassword
	config.Net = "tcp"
	config.Addr = net.JoinHostPort("127.0.0.1", strconv.Itoa(record.Port))
	config.Timeout = 2 * time.Second
	config.ReadTimeout = 5 * time.Second
	config.WriteTimeout = 5 * time.Second
	connector, err := mysql.NewConnector(config)
	if err != nil {
		return nil, problem("shared_unavailable", "cannot configure private administration")
	}
	db := sql.OpenDB(connector)
	db.SetMaxOpenConns(1)
	return db, nil
}

func (s *service) ensureSharedAllocations(ctx context.Context, id string, snap plan.Snapshot, entry *execution) error {
	entry.shared = map[string]sharedConnection{}
	if len(snap.Plan.SharedServices) == 0 {
		return nil
	}
	project, err := identifyShared(entry.source.source, snap.Plan.Project)
	if err != nil {
		return err
	}
	var ids []string
	for _, resource := range snap.Plan.Resources {
		if resource.Kind == "allocation" {
			ids = append(ids, hash(project.ID+"/"+resource.SharedService))
		}
	}
	release, err := s.store.lockShared(ctx, ids...)
	if err != nil {
		return err
	}
	defer release()
	if err = s.store.db.Update(func(tx *bolt.Tx) error { return registerSharedProject(tx, project) }); err != nil {
		return err
	}
	allocations, err := s.store.sharedAllocations(id)
	if err != nil {
		return err
	}
	// A partial multi-allocation cleanup must not provision any new effects.
	for _, allocation := range allocations {
		if allocation.Request.Operation == "release" || allocation.Released {
			return problem("allocation_conflict", "allocation release is pending; explicit reset/destroy required")
		}
	}
	for name, resource := range snap.Plan.Resources {
		if resource.Kind != "allocation" {
			continue
		}
		declaration := snap.Plan.SharedServices[resource.SharedService]
		if snap.Docker == nil {
			return problem("missing_provider", "shared allocations require configured Docker")
		}
		serviceID := hash(project.ID + "/" + resource.SharedService)
		record, err := s.store.sharedService(serviceID)
		var detail *v1.PlanError
		if errors.As(err, &detail) && detail.Code == "not_found" {
			newResource := func(kind string) ownedResource {
				token := newID()
				return ownedResource{Kind: kind, Token: token, Name: "backlot-" + token}
			}
			record = sharedServiceRecord{ID: serviceID, Project: project, Name: resource.SharedService, Declaration: declaration, Endpoint: snap.Docker.Endpoint, Configuration: sharedDigest(declaration, snap.Docker.Endpoint), AdminPassword: newID(), Volume: newResource("volume"), Network: newResource("network"), Server: newResource("container"), Status: "intent"}
			err = s.store.saveShared(record)
		}
		if err != nil {
			return err
		}
		if record.Status == "intent" && record.ImageID == "" {
			if err = s.store.sharedPoint("server-intent"); err != nil {
				return err
			}
		}
		if record.Project != project || record.Configuration != sharedDigest(declaration, snap.Docker.Endpoint) {
			return problem("shared_conflict", "retained shared server configuration differs; peer server preserved; use a distinct shared service name")
		}
		if err = s.store.startShared(ctx, &record); err != nil {
			return err
		}
		allocation, exists := allocations[name]
		if !exists {
			allocation = sharedAllocation{ServiceID: serviceID, Request: v1.ManagerRequest{Version: v1.ManagerProtocolVersion, Operation: "allocate", AllocationID: newID(), Ownership: newID(), Database: "bl" + newID()[:32], Username: "bl" + newID()[:32], Password: newID()}}
			allocations[name] = allocation
			if err = s.store.saveSharedAllocations(id, allocations); err != nil {
				return err
			}
		}
		if !exists {
			if err = s.store.sharedPoint("allocation-intent"); err != nil {
				return err
			}
		}
		if allocation.Released || allocation.Request.Operation == "release" || allocation.ServiceID != serviceID {
			return problem("allocation_conflict", "retained allocation differs or was partially released; explicit reset/destroy required")
		}
		db, err := sharedDB(record)
		if err != nil {
			return err
		}
		request := allocation.Request
		if allocation.Result.Effect == "ready" {
			request.Operation = "inspect"
			observed, inspectErr := manager.MariaDB(ctx, db, request, s.store.sharedPoint)
			if inspectErr != nil || observed.Ownership != "owned" || observed.Effect != "ready" {
				_ = db.Close()
				return problem("allocation_missing", "retained allocation is missing or unverifiable; no automatic recreation; reset/destroy explicitly")
			}
		}
		request.Operation = "allocate"
		result, effectErr := manager.MariaDB(ctx, db, request, s.store.sharedPoint)
		_ = db.Close()
		if err = s.store.sharedPoint("allocation-effect"); err != nil {
			return err
		}
		allocation.Result = result
		allocations[name] = allocation
		if err = s.store.saveSharedAllocations(id, allocations); err != nil {
			return err
		}
		if effectErr != nil {
			return effectErr
		}
		if err = manager.ValidateResult(request, result); err != nil || result.Ownership != "owned" || result.Effect != "ready" {
			return problem("allocation_uncertain", "manager did not verify a ready allocation; preserved")
		}
		entry.shared[name] = sharedConnection{Request: request, Network: record.Network, Alias: "shared-" + serviceID[:32], Port: record.Port}
	}
	return nil
}

func (s *store) releaseSharedAllocations(ctx context.Context, id string) error {
	return s.releaseSharedAllocationsWithForce(ctx, id, false)
}
func (s *store) releaseSharedAllocationsWithForce(ctx context.Context, id string, force bool) error {
	allocations, err := s.sharedAllocations(id)
	if err != nil {
		return err
	}
	if len(allocations) == 0 {
		return nil
	}
	var ids []string
	for _, allocation := range allocations {
		ids = append(ids, allocation.ServiceID)
	}
	release, err := s.lockShared(ctx, ids...)
	if err != nil {
		return err
	}
	defer release()
	allocations, err = s.sharedAllocations(id)
	if err != nil {
		return err
	}
	for name, allocation := range allocations {
		allocation.Request.Operation = "release"
		allocations[name] = allocation
	}
	if err = s.saveSharedAllocations(id, allocations); err != nil {
		return err
	}
	for name, allocation := range allocations {
		if allocation.Released {
			continue
		}
		// Destructive intent survives every SQL/Engine failure and acknowledgment gap.
		if err = s.sharedPoint("release-intent"); err != nil {
			return err
		}
		record, err := s.sharedService(allocation.ServiceID)
		if err != nil {
			return err
		}
		if force {
			if err = s.checkSharedReleaseCollision(id, name, allocation); err != nil {
				return err
			}
		}
		result, effectErr := s.releaseSharedAllocation(ctx, &record, allocation, force)
		request := allocation.Request
		request.Operation = "release"
		if effectErr == nil && manager.ValidateResult(request, result) == nil && result.Ownership == "absent" && result.Effect == "released" {
			if err = s.sharedPoint("release-effect"); err != nil {
				return err
			}
		}
		allocation.Result = result
		if effectErr == nil && manager.ValidateResult(request, result) == nil && result.Ownership == "absent" && result.Effect == "released" {
			allocation.Released = true
		}
		allocations[name] = allocation
		if err = s.saveSharedAllocations(id, allocations); err != nil {
			return err
		}
		if effectErr != nil {
			return effectErr
		}
		if !allocation.Released {
			return problem("cleanup_failed", "shared allocation absence unverified; obligation retained")
		}
	}
	return s.db.Update(func(tx *bolt.Tx) error { return tx.Bucket([]byte("shared_allocations")).Delete([]byte(id)) })
}

func (s *store) sharedIDs() ([]string, error) {
	var ids []string
	err := s.db.View(func(tx *bolt.Tx) error {
		return tx.Bucket([]byte("shared_services")).ForEach(func(key, _ []byte) error { ids = append(ids, string(key)); return nil })
	})
	return ids, err
}
func (s *store) sharedDependents(serviceID string, all bool) (int, error) {
	count := 0
	err := s.db.View(func(tx *bolt.Tx) error {
		return tx.Bucket([]byte("shared_allocations")).ForEach(func(key, data []byte) error {
			allocations, err := decodeSharedAllocations(data)
			if err != nil {
				return err
			}
			for _, allocation := range allocations {
				if allocation.ServiceID == serviceID && !allocation.Released {
					if all {
						count++
						continue
					}
					r, err := load(tx, string(key))
					if err != nil {
						return err
					}
					if active(r.Instance.Status) || r.Instance.Execution != nil && (r.Instance.Execution.Kept || r.Instance.Execution.CleanupFailure != "") {
						count++
					}
				}
			}
			return nil
		})
	})
	return count, err
}
func (s *store) stopShared(ctx context.Context, record *sharedServiceRecord) (resultErr error) {
	defer func() { resultErr = s.sharedOperationResult(record, resultErr) }()
	if record.Server.Removed {
		return nil
	}
	d, err := newDocker(record.Endpoint)
	if err != nil {
		return err
	}
	defer func() { _ = d.Close() }()
	if err = s.verifySharedServer(ctx, d, *record); err != nil {
		if errdefs.IsNotFound(err) && (record.Server.ID == "" || record.Destroying) {
			return nil
		}
		return err
	}
	if err = s.sharedPoint("stop-server"); err != nil {
		return err
	}
	seconds := 10
	if _, err = d.ContainerStop(ctx, resourceHandle(record.Server), client.ContainerStopOptions{Timeout: &seconds}); err != nil {
		return providerFailure("stop shared server", err)
	}
	out, err := d.ContainerInspect(ctx, resourceHandle(record.Server), client.ContainerInspectOptions{})
	if err != nil {
		return sharedProviderError(err)
	}
	if out.Container.State == nil || out.Container.State.Running {
		return problem("cleanup_failed", "shared server remains live; preserved")
	}
	record.Status = "stopped"
	record.Failure = ""
	if record.Destroying {
		record.Status = "destroying"
	}
	return s.saveShared(*record)
}
func (s *service) sharedControl(ctx context.Context, action string, request v1.SharedRequest) (v1.SharedResponse, error) {
	return s.sharedControlWithForce(ctx, action, request, false)
}
func (s *service) sharedDestroy(ctx context.Context, request v1.SharedDestroyRequest) (v1.SharedResponse, error) {
	return s.sharedControlWithForce(ctx, "destroy", v1.SharedRequest{APIVersion: request.APIVersion, ServiceID: request.ServiceID}, request.Force)
}
func (s *service) sharedControlWithForce(ctx context.Context, action string, request v1.SharedRequest, force bool) (v1.SharedResponse, error) {
	response := v1.SharedResponse{APIVersion: v1.Version, Services: []v1.SharedStatus{}}
	if force && action != "destroy" {
		return response, problem("invalid_request", "force is supported only for shared destroy")
	}
	ctx, finish, err := s.beginMutation(ctx)
	if err != nil {
		return response, err
	}
	defer finish()
	if action != "list" && !validID(request.ServiceID) || action == "list" && request.ServiceID != "" {
		return response, problem("invalid_request", "shared operation requires the recorded service ID; list takes no ID")
	}
	ids := []string{request.ServiceID}
	if action == "list" {
		var err error
		ids, err = s.store.sharedIDs()
		if err != nil {
			return response, err
		}
	}
	release, err := s.store.lockShared(ctx, ids...)
	if err != nil {
		return response, err
	}
	defer release()
	for _, id := range ids {
		record, err := s.store.sharedService(id)
		if err != nil {
			return response, err
		}
		if action == "stop" || action == "restart" || action == "destroy" {
			count, err := s.store.sharedDependents(id, action == "destroy")
			if err != nil {
				return response, err
			}
			if count > 0 {
				return response, problem("shared_in_use", "shared service has dependent consumers/retained allocations; stop or destroy those instances explicitly")
			}
			if action == "destroy" {
				record.Destroying = true
				record.Status = "destroying"
				if err = s.store.saveShared(record); err != nil {
					return response, err
				}
			}
			if force {
				err = s.store.stopForcedShared(ctx, &record)
			} else {
				err = s.store.stopShared(ctx, &record)
			}
			if err != nil {
				return response, err
			}
		}
		switch action {
		case "start", "restart":
			if err = s.store.startShared(ctx, &record); err != nil {
				return response, err
			}
		case "destroy":
			d, err := newDocker(record.Endpoint)
			if err != nil {
				return response, err
			}
			record.Status = "destroying"
			err = s.store.saveShared(record)
			for _, resource := range []*ownedResource{&record.Server, &record.Volume, &record.Network} {
				if err != nil || resource.Removed {
					continue
				}
				if force {
					err = removeForcedSharedResource(ctx, d, *resource)
				} else if resource.Kind == "container" {
					err = removeContainer(ctx, d, *resource)
				} else {
					err = removeDockerResource(ctx, d, *resource)
				}
				if err == nil {
					err = s.store.sharedPoint("remove-effect:" + resource.Kind)
				}
				if err == nil {
					observed := verifySharedResource(ctx, d, *resource, force)
					if !errdefs.IsNotFound(observed) {
						err = problem("cleanup_failed", "shared resource absence unverified; obligation retained")
						continue
					}
					resource.Removed = true
					err = s.store.saveShared(record)
				}
			}
			_ = d.Close()
			if err != nil {
				return response, sharedProviderError(err)
			}
			if err = s.store.db.Update(func(tx *bolt.Tx) error { return tx.Bucket([]byte("shared_services")).Delete([]byte(id)) }); err != nil {
				return response, err
			}
			record.Status = "destroyed"
			record.Failure = ""
		case "list", "inspect", "stop":
		default:
			return response, problem("invalid_request", "unknown shared action")
		}
		count, err := s.store.sharedDependents(id, true)
		if err != nil {
			return response, err
		}
		pending, err := s.store.sharedPendingReleases(id)
		if err != nil {
			return response, err
		}
		response.Services = append(response.Services, v1.SharedStatus{ID: id, ProjectID: record.Project.ID, Name: record.Name, Configuration: record.Configuration, Status: record.Status, Allocations: count, PendingReleases: pending, Failure: record.Failure})
	}
	return response, nil
}

// stopSharedServices follows consumer join during shutdown/recovery. It never
// releases allocations or storage, and never restarts application execution.
func (s *store) stopSharedServices(ctx context.Context) error {
	ids, err := s.sharedIDs()
	if err != nil {
		return err
	}
	var failures error
	for _, id := range ids {
		failures = errors.Join(failures, s.stopSharedService(ctx, id))
	}

	return failures
}

// The shutdown/recovery budget includes waiting for the current effect owner.
func (s *store) stopSharedService(ctx context.Context, id string) error {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	release, err := s.lockShared(ctx, id)
	if err != nil {
		return err
	}
	defer release()
	record, err := s.sharedService(id)
	if err != nil {
		return err
	}
	dependents, err := s.sharedDependents(id, false)
	if err == nil && dependents > 0 {
		err = problem("shared_in_use", "shared consumers are active or cleanup is uncertain; server preserved")
	}
	if err == nil {
		err = s.stopShared(ctx, &record)
	}
	if err != nil {
		record.Status = "uncertain"
		record.Failure = "shared shutdown/recovery could not verify stop; restore provider/ownership and retry explicit shared stop or destroy"
		err = errors.Join(err, s.saveShared(record))
	}
	return err
}

// Engine errors can contain private generated names/capabilities. Preserve
// classification internally while publishing only controlled diagnostics.
func sharedProviderError(err error) error {
	var detail *v1.PlanError
	if errors.As(err, &detail) {
		return err
	}
	return providerFailure("shared service operation", err)
}

// sharedPoint permits owned test daemons to pause exactly between an external
// effect and its durable acknowledgment. Production has no installed callback.
func (s *store) sharedPoint(point string) error {
	if s.sharedCheckpoint != nil {
		return s.sharedCheckpoint(point)
	}
	return nil
}

// Store only controlled diagnostics; engine/SQL errors can contain authority.
// Destruction intent is independent from observation and cannot be cleared here.
func (s *store) sharedOperationResult(record *sharedServiceRecord, err error) error {
	if err == nil {
		return nil
	}
	record.Status = "uncertain"
	if record.Destroying {
		record.Status = "destroying"
	}
	record.Failure = "shared operation incomplete; resources preserved; restore provider/ownership and retry explicit control or cleanup"
	return errors.Join(err, s.saveShared(*record))
}

// markSharedRelease records destructive intent before generic provider cleanup
// can fail. It performs no effects and preserves private cleanup's empty bypass.
func (s *store) markSharedRelease(ctx context.Context, id string) error {
	allocations, err := s.sharedAllocations(id)
	if err != nil || len(allocations) == 0 {
		return err
	}
	var ids []string
	for _, a := range allocations {
		ids = append(ids, a.ServiceID)
	}
	unlock, err := s.lockShared(ctx, ids...)
	if err != nil {
		return err
	}
	defer unlock()
	allocations, err = s.sharedAllocations(id)
	if err != nil {
		return err
	}
	for name, a := range allocations {
		a.Request.Operation = "release"
		allocations[name] = a
	}
	return s.saveSharedAllocations(id, allocations)
}

func (s *store) sharedPendingReleases(serviceID string) (int, error) {
	count := 0
	err := s.db.View(func(tx *bolt.Tx) error {
		return tx.Bucket([]byte("shared_allocations")).ForEach(func(_, data []byte) error {
			allocations, err := decodeSharedAllocations(data)
			if err != nil {
				return err
			}
			for _, a := range allocations {
				if a.ServiceID == serviceID && !a.Released && a.Request.Operation == "release" {
					count++
				}
			}
			return nil
		})
	})
	return count, err
}
