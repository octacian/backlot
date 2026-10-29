package v1

import (
	"encoding/base64"
	"encoding/json"
	"path/filepath"
	"strings"
	"time"
)

// SceneSummary describes declared work, without satisfying prerequisites.
type SceneSummary struct {
	Name           string   `json:"name"`
	Lifetime       Lifetime `json:"lifetime"`
	ComponentCount int      `json:"component_count"`
	TerminalJob    string   `json:"terminal_job,omitempty"`
	Publication    bool     `json:"publication"`
}

// SceneListResponse contains strictly validated declarations from one manifest.
type SceneListResponse struct {
	APIVersion   string         `json:"api_version"`
	Project      string         `json:"project"`
	ManifestPath string         `json:"manifest_path"`
	CheckoutPath string         `json:"checkout_path"`
	Scenes       []SceneSummary `json:"scenes"`
}

// DeclaredComponent omits commands, environment values and fixture values.
type DeclaredComponent struct {
	Name         string                    `json:"name"`
	Kind         ComponentKind             `json:"kind"`
	Runtime      Runtime                   `json:"runtime"`
	Policy       JobPolicy                 `json:"policy,omitempty"`
	DependsOn    []Dependency              `json:"depends_on,omitempty"`
	Resources    []string                  `json:"resources,omitempty"`
	Outputs      []string                  `json:"outputs,omitempty"`
	Ports        map[string]ServicePort    `json:"ports,omitempty"`
	Mounts       []Mount                   `json:"mounts,omitempty"`
	HostMappings []HostMapping             `json:"host_mappings,omitempty"`
	Artifacts    map[string]ArtifactSource `json:"artifacts,omitempty"`
	Readiness    string                    `json:"readiness,omitempty"`
}

// DeclaredPublication describes routing without resolving the origin or probe.
type DeclaredPublication struct {
	Resource  string  `json:"resource"`
	Routes    []Route `json:"routes"`
	ProbeKind string  `json:"probe_kind"`
}

// DeclaredSharedService suppresses provider argument vectors.
type DeclaredSharedService struct {
	Manager string `json:"manager"`
	Storage string `json:"storage"`
	Admin   string `json:"admin"`
	Port    int    `json:"port"`
}

// DeclaredInput describes a required input without inspecting its configuration.
type DeclaredInput struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Secret      bool   `json:"secret"`
	Generate    string `json:"generate,omitempty"`
}

// Prerequisites names selected requirements, never their values or availability.
type Prerequisites struct {
	Tools       []string        `json:"tools"`
	Inputs      []DeclaredInput `json:"inputs"`
	Seeds       []string        `json:"seeds"`
	Required    []string        `json:"required"`
	PassThrough []string        `json:"pass_through"`
	Docker      bool            `json:"docker"`
	Caddy       bool            `json:"caddy"`
}

// SceneDescriptionResponse describes a selected graph in dependency-first order.
type SceneDescriptionResponse struct {
	APIVersion     string                           `json:"api_version"`
	Project        string                           `json:"project"`
	ManifestPath   string                           `json:"manifest_path"`
	CheckoutPath   string                           `json:"checkout_path"`
	Scene          SceneSummary                     `json:"scene"`
	Components     []DeclaredComponent              `json:"components"`
	Resources      map[string]Resource              `json:"resources"`
	Outputs        map[string]Output                `json:"outputs"`
	BuildImages    map[string]BuildImage            `json:"build_images"`
	SharedServices map[string]DeclaredSharedService `json:"shared_services"`
	Publication    *DeclaredPublication             `json:"publication,omitempty"`
	Prerequisites  Prerequisites                    `json:"prerequisites"`
}

// FilteredListRequest intersects recorded selectors before bounded pagination.
type FilteredListRequest struct {
	APIVersion   string         `json:"api_version"`
	AfterID      string         `json:"after_id,omitempty"`
	Limit        int            `json:"limit,omitempty"`
	ProjectPath  string         `json:"project_path,omitempty"`
	CheckoutPath string         `json:"checkout_path,omitempty"`
	ProjectID    string         `json:"project_id,omitempty"`
	Scene        string         `json:"scene,omitempty"`
	Lifetime     Lifetime       `json:"lifetime,omitempty"`
	State        InstanceStatus `json:"state,omitempty"`
}

// Validate rejects mixed locations, invalid selectors and unbounded pages.
func (r FilteredListRequest) Validate() error {
	invalid := func() error {
		return &PlanError{Code: "invalid_request", Message: "use one absolute project/checkout location, declared selectors and limit 0 through 100"}
	}
	if r.APIVersion != Version || r.Limit < 0 || r.Limit > 100 || r.AfterID != "" && !buildImageHex(r.AfterID) || r.ProjectPath != "" && r.CheckoutPath != "" {
		return invalid()
	}
	for _, p := range []string{r.ProjectPath, r.CheckoutPath} {
		if p != "" && (!filepath.IsAbs(p) || strings.ContainsRune(p, 0)) {
			return invalid()
		}
	}
	for _, n := range []string{r.ProjectID, r.Scene} {
		if n != "" && !sceneIdentifier.MatchString(n) {
			return invalid()
		}
	}
	if r.Lifetime != "" && r.Lifetime != Persistent && r.Lifetime != Disposable {
		return invalid()
	}
	switch r.State {
	case "", Preparing, Prepared, Cancelled, Interrupted, Starting, RuntimeReady, Stopping, Stopped, Destroyed, Succeeded, Failed:
	default:
		return invalid()
	}
	return nil
}

// ToolCandidate reports a current caller-side candidate, without executable lookup.
type ToolCandidate struct {
	Name        string `json:"name"`
	Candidate   string `json:"candidate"`
	SourceLayer string `json:"source_layer"`
	SourcePath  string `json:"source_path"`
}

// ConfigurationResponse is an allowlisted local composition, never raw configuration.
type ConfigurationResponse struct {
	APIVersion         string            `json:"api_version"`
	Context            string            `json:"context"`
	ManifestPath       string            `json:"manifest_path"`
	MachinePath        string            `json:"machine_path"`
	MachinePresent     bool              `json:"machine_present"`
	DefaultMachinePath string            `json:"default_machine_path"`
	PrivatePath        string            `json:"private_path,omitempty"`
	PrivatePresent     bool              `json:"private_present"`
	Association        string            `json:"association,omitempty"`
	Inputs             []SetupInput      `json:"inputs"`
	Tools              []ToolCandidate   `json:"tools"`
	Docker             *DockerConfig     `json:"docker,omitempty"`
	Caddy              *CaddyConfig      `json:"caddy,omitempty"`
	DeclaredStorage    *StorageConfig    `json:"declared_storage_intent,omitempty"`
	DaemonDirectory    string            `json:"prospective_daemon_directory"`
	DaemonSocket       string            `json:"prospective_daemon_socket"`
	Defaults           ExecutionDefaults `json:"defaults"`
}

// ExecutionDefaults describes prospective CLI/daemon defaults, not running policy.
type ExecutionDefaults struct {
	Execution         ExecutionOptions `json:"execution"`
	LeaseDuration     string           `json:"lease_duration"`
	RetentionAge      string           `json:"retention_age"`
	RetentionBytes    int64            `json:"retention_bytes"`
	RetentionAttempts int              `json:"retention_attempts"`
}

// DaemonConfigurationResponse reports policy captured by the active daemon.
type DaemonConfigurationResponse struct {
	APIVersion        string `json:"api_version"`
	StateVersion      int    `json:"state_version"`
	DaemonGeneration  string `json:"daemon_generation"`
	Context           string `json:"context"`
	Directory         string `json:"directory"`
	Socket            string `json:"socket"`
	LeaseDuration     string `json:"lease_duration"`
	RetentionAge      string `json:"retention_age"`
	RetentionAttempts int    `json:"retention_attempts"`
	RetentionBytes    int64  `json:"retention_bytes"`
}

// ReadContract identifies a supported finite or record-oriented interface.
type ReadContract struct {
	ID   string `json:"id"`
	Mode string `json:"mode"`
}

// ContractsResponse identifies focused supported reads, not provider readiness.
type ContractsResponse struct {
	APIVersion       string           `json:"api_version"`
	StateVersion     int              `json:"state_version"`
	Context          string           `json:"context"`
	Executable       *VersionResponse `json:"executable,omitempty"`
	DaemonGeneration string           `json:"daemon_generation,omitempty"`
	Schemas          []string         `json:"schemas"`
	Contracts        []ReadContract   `json:"contracts"`
}

// SupportedReadContracts returns the installed discovery and existing evidence modes.
func SupportedReadContracts() []ReadContract {
	return []ReadContract{{ID: "scene-declarations/v1", Mode: "finite"}, {ID: "instance-filter/v1", Mode: "finite"}, {ID: "log-page/v1", Mode: "finite"}, {ID: "attempt-history/v1", Mode: "finite"}, {ID: "configuration/v1", Mode: "finite"}, {ID: "logs/v1", Mode: "ndjson"}, {ID: "backlot.export/v1", Mode: "finite"}}
}

// SupportedDaemonReadContracts lists finite daemon reads, excluding local declarations.
func SupportedDaemonReadContracts() []ReadContract {
	return []ReadContract{{ID: "instance-filter/v1", Mode: "finite"}, {ID: "log-page/v1", Mode: "finite"}, {ID: "attempt-history/v1", Mode: "finite"}, {ID: "configuration/v1", Mode: "finite"}, {ID: "logs/v1", Mode: "finite"}, {ID: "backlot.export/v1", Mode: "finite"}}
}

// LogPageRequest selects a bounded scan of retained output with transient continuation.
type LogPageRequest struct {
	APIVersion string `json:"api_version"`
	InstanceID string `json:"instance_id"`
	Component  string `json:"component,omitempty"`
	Attempt    string `json:"attempt,omitempty"`
	Stream     string `json:"stream,omitempty"`
	After      string `json:"after,omitempty"`
	Limit      int    `json:"limit,omitempty"`
}

// Validate bounds filters and untrusted cursor input before reading evidence.
func (r LogPageRequest) Validate() error {
	if r.APIVersion != Version || !buildImageHex(r.InstanceID) || r.Attempt != "" && !buildImageHex(r.Attempt) || r.Component != "" && !sceneIdentifier.MatchString(r.Component) || r.Stream != "" && r.Stream != "stdout" && r.Stream != "stderr" || r.Limit < 0 || r.Limit > 1000 || len(r.After) > 2048 {
		return &PlanError{Code: "invalid_request", Message: "log page requires recorded IDs, valid component/stream, bounded cursor and limit 0 through 1000"}
	}
	return nil
}

// EvidenceGap describes known missing or invalidated evidence independently of records.
type EvidenceGap struct {
	Attempt string `json:"attempt,omitempty"`
	Code    string `json:"code"`
	Detail  string `json:"detail"`
}

// LogPageResponse is one finite live page; absence never proves complete capture.
type LogPageResponse struct {
	APIVersion              string                   `json:"api_version"`
	InstanceID              string                   `json:"instance_id"`
	Records                 []LogRecord              `json:"records"`
	Continuation            string                   `json:"continuation,omitempty"`
	More                    bool                     `json:"more"`
	Active                  bool                     `json:"active"`
	HistoricalCollection    string                   `json:"historical_collection"`
	Gaps                    []EvidenceGap            `json:"gaps"`
	NativeOwnershipWarnings []NativeOwnershipWarning `json:"native_ownership_warnings,omitempty"`
}

// Validate checks original record identity and encoding without interpreting output.
func (r LogRecord) Validate() error {
	if !buildImageHex(r.InstanceID) || !buildImageHex(r.Attempt) || !sceneIdentifier.MatchString(r.Component) || r.Stream != "stdout" && r.Stream != "stderr" {
		return invalidDiscoveryResponse()
	}
	if _, err := time.Parse(time.RFC3339Nano, r.Time); err != nil {
		return invalidDiscoveryResponse()
	}
	if r.Data != "" {
		if r.Message != "" {
			return invalidDiscoveryResponse()
		}
		if _, err := base64.StdEncoding.DecodeString(r.Data); err != nil {
			return invalidDiscoveryResponse()
		}
	}
	return nil
}

func invalidDiscoveryResponse() error {
	return &PlanError{Code: "invalid_response", Message: "invalid discovery response contract"}
}

// Validate checks the active policy's API/state fence and finite settings.
func (r DaemonConfigurationResponse) Validate() error {
	lease, err := time.ParseDuration(r.LeaseDuration)
	age, ageErr := time.ParseDuration(r.RetentionAge)
	if r.APIVersion != Version || r.StateVersion != StateVersion {
		return &PlanError{Code: "api_version", Message: "incompatible daemon API or state format"}
	}
	if !buildImageHex(r.DaemonGeneration) || r.Context != "daemon-startup policy" || !scenePath(r.Directory) || r.Socket != filepath.Join(r.Directory, "daemon.sock") || err != nil || lease < 100*time.Millisecond || lease > 24*time.Hour || ageErr != nil || age <= 0 || r.RetentionBytes <= 0 || r.RetentionAttempts <= 0 {
		return invalidDiscoveryResponse()
	}
	return nil
}

// ValidateDaemon checks a daemon capability response without inventing build identity.
func (r ContractsResponse) ValidateDaemon() error {
	if r.APIVersion != Version || r.StateVersion != StateVersion {
		return &PlanError{Code: "api_version", Message: "incompatible daemon API or state format"}
	}
	if !buildImageHex(r.DaemonGeneration) || r.Context != "daemon supported contracts" || r.Executable != nil || r.Schemas == nil || r.Contracts == nil || len(r.Contracts) > 32 {
		return invalidDiscoveryResponse()
	}
	seen := map[string]bool{}
	for _, c := range r.Contracts {
		if c.ID == "" || len(c.ID) > 128 || seen[c.ID] || c.Mode != "finite" && c.Mode != "ndjson" {
			return invalidDiscoveryResponse()
		}
		seen[c.ID] = true
	}
	return nil
}

// Validate binds returned evidence to the requested identity, filters and bounds.
func (r LogPageResponse) Validate(q LogPageRequest) error {
	limit := q.Limit
	if limit == 0 {
		limit = 100
	}
	if r.APIVersion != Version || r.InstanceID != q.InstanceID || r.Records == nil || r.Gaps == nil || len(r.Records) > limit || len(r.Continuation) > 2048 || r.More && r.Continuation == "" || r.HistoricalCollection != "unknown" && r.HistoricalCollection != "known" {
		return invalidDiscoveryResponse()
	}
	size := 0
	for _, record := range r.Records {
		if record.Validate() != nil || record.InstanceID != q.InstanceID || q.Component != "" && q.Component != record.Component || q.Attempt != "" && q.Attempt != record.Attempt || q.Stream != "" && q.Stream != record.Stream {
			return invalidDiscoveryResponse()
		}
		data, _ := json.Marshal(record)
		size += len(data)
	}
	if size > 512<<10 || len(r.Gaps) > 3 {
		return invalidDiscoveryResponse()
	}
	for _, g := range r.Gaps {
		if g.Attempt != "" && !buildImageHex(g.Attempt) || g.Detail == "" || g.Code != "retention_expired" && g.Code != "collection_incomplete" && g.Code != "source_changed" {
			return invalidDiscoveryResponse()
		}
	}
	return nil
}
