package v1

import (
	"encoding/hex"
	"path/filepath"
	"strings"
)

// DoctorStatus classifies an observed check without treating missing evidence as success.
type DoctorStatus string

// Diagnostic outcomes are explicit across all execution contexts.
const (
	DoctorOK          DoctorStatus = "ok"
	DoctorFailed      DoctorStatus = "failed"
	DoctorUnsupported DoctorStatus = "unsupported"
	DoctorDeferred    DoctorStatus = "deferred"
)

// DoctorRequest selects exactly one scene or recorded instance; empty selects daemon access.
// ContainerProbeImage explicitly authorizes a bounded temporary diagnostic container.
type DoctorRequest struct {
	APIVersion          string `json:"api_version"`
	ProjectPath         string `json:"project_path,omitempty"`
	Scene               string `json:"scene,omitempty"`
	ConfigPath          string `json:"config_path,omitempty"`
	InstanceID          string `json:"instance_id,omitempty"`
	ContainerProbeImage string `json:"container_probe_image,omitempty"`
	ProbePublicCA       bool   `json:"probe_public_ca,omitempty"`
}

// Validate rejects ambiguous targets before any filesystem/provider work.
func (r DoctorRequest) Validate() error {
	invalid := func() error {
		return &PlanError{Code: "invalid_request", Message: "doctor requires --scene with an absolute project path, or --instance alone; container probe requires --instance and an explicit image"}
	}
	if r.APIVersion != Version {
		return &PlanError{Code: "api_version", Message: "doctor requires a v1 request"}
	}
	if r.InstanceID != "" {
		decoded, err := hex.DecodeString(r.InstanceID)
		if err != nil || len(decoded) != 32 || strings.ToLower(r.InstanceID) != r.InstanceID || r.ProjectPath != "" || r.Scene != "" || r.ConfigPath != "" {
			return invalid()
		}
	} else if r.ProjectPath != "" || r.Scene != "" || r.ConfigPath != "" {
		if !filepath.IsAbs(r.ProjectPath) || r.Scene == "" || len(r.Scene) > 63 || (r.ConfigPath != "" && !filepath.IsAbs(r.ConfigPath)) {
			return invalid()
		}
	}
	if (r.ContainerProbeImage != "" && r.InstanceID == "") || (r.ProbePublicCA && r.ContainerProbeImage == "") || len(r.ContainerProbeImage) > 512 || strings.ContainsAny(r.ContainerProbeImage, "\x00\r\n\t ") {
		return invalid()
	}
	return nil
}

// DoctorCheck is one bounded observation with an explicit execution/trust context.
type DoctorCheck struct {
	Name    string       `json:"name"`
	Status  DoctorStatus `json:"status"`
	Context string       `json:"context"`
	Message string       `json:"message"`
}

// DoctorPath locates an input or retained state without exposing its contents.
// State is present, absent, unrecorded, changed, or unavailable; Digest is launch provenance.
type DoctorPath struct {
	Name   string `json:"name"`
	Path   string `json:"path,omitempty"`
	State  string `json:"state"`
	Digest string `json:"digest,omitempty"`
}

// DoctorResponse reports observations only, never application/browser trust or aggregate readiness.
// Healthy means no observed failed check; deferred/unsupported checks remain incomplete evidence.
type DoctorResponse struct {
	APIVersion string        `json:"api_version"`
	Healthy    bool          `json:"healthy"`
	Scope      string        `json:"scope"`
	InstanceID string        `json:"instance_id,omitempty"`
	Paths      []DoctorPath  `json:"paths"`
	Checks     []DoctorCheck `json:"checks"`
}

// Validate checks decoded diagnostic enums and failure consistency.
func (r DoctorResponse) Validate() error {
	bad := func() error { return &PlanError{Code: "invalid_response", Message: "invalid doctor response contract"} }
	if r.APIVersion != Version || (r.Scope != "daemon" && r.Scope != "scene" && r.Scope != "instance") || r.Checks == nil || r.Paths == nil {
		return bad()
	}
	if r.Scope == "instance" {
		decoded, err := hex.DecodeString(r.InstanceID)
		if err != nil || len(decoded) != 32 || strings.ToLower(r.InstanceID) != r.InstanceID {
			return bad()
		}
	} else if r.InstanceID != "" {
		return bad()
	}
	failed := false
	for _, c := range r.Checks {
		if c.Name == "" || c.Context == "" || c.Message == "" {
			return bad()
		}
		switch c.Status {
		case DoctorOK, DoctorDeferred, DoctorUnsupported:
		case DoctorFailed:
			failed = true
		default:
			return bad()
		}
	}
	for _, p := range r.Paths {
		if p.Name == "" || (p.Path != "" && !filepath.IsAbs(p.Path)) {
			return bad()
		}
		switch p.State {
		case "present", "absent", "unrecorded", "changed", "unavailable":
		default:
			return bad()
		}
	}
	if r.Healthy == failed {
		return bad()
	}
	return nil
}
